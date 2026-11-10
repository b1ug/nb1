package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	d, err := newDaemon(endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	s := newServer(d)
	server, client := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ss, err := s.Connect(ctx, server, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "adapter-test", Version: "1"}, nil).Connect(ctx, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, session *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, output) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out output
	if res.StructuredContent != nil {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
	}
	return res, out
}

func TestOfficialSDKToolsForwardAndPreserveJobSemantics(t *testing.T) {
	var mu sync.Mutex
	var submitted submission
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/devices":
			fmt.Fprint(w, `[{"serial":"20005601","connection":"online"}]`)
		case "/v1/devices/20005601":
			fmt.Fprint(w, `{"serial":"20005601","observed":{"led1":"#800000","led2":"#000000","sampled_at":"2026-11-09T02:00:00+08:00"}}`)
		case "/v1/devices/20005601/jobs":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Error(err)
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing JSON media type")
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"job_example","status":"queued"}`)
		case "/v1/jobs/job_example/cancel":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"job_example","status":"cancelling"}`)
		case "/v1/jobs/job_example":
			fmt.Fprint(w, `{"id":"job_example","status":"failed","error":"hardware cleanup failed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Errorf("missing schema: %s", tool.Name)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "cancel_job,cancel_pattern_request,get_device_state,get_job,get_pattern,get_pattern_request,get_pattern_v2,list_devices,list_patterns,list_patterns_v2,preview_light,preview_pattern,preview_pattern_draft_v2,preview_program,resolve_color,run_light,run_pattern,run_program,save_pattern_v2,set_device_state" {
		t.Fatal(names)
	}
	res, out := call(t, session, "list_devices", map[string]any{})
	if res.IsError || out.Kind != "devices" {
		t.Fatalf("list: %+v %+v", res, out)
	}
	res, out = call(t, session, "get_device_state", map[string]any{"serial": "20005601"})
	if res.IsError || !strings.Contains(fmt.Sprint(out.Data), "sampled_at") {
		t.Fatalf("state lost observation: %+v", out)
	}
	res, out = call(t, session, "set_device_state", map[string]any{"serial": "20005601", "color": "#AAFF00", "led": 2, "fade_ms": 20, "hold_ms": 5})
	if res.IsError || !out.Accepted || out.PollAfterMS != 200 || out.Data.(map[string]any)["status"] != "queued" {
		t.Fatalf("submit: %+v %+v", res, out)
	}
	mu.Lock()
	captured := submitted
	mu.Unlock()
	if captured.Program.OnFinish != "hold" || captured.Program.Repeat != 1 || captured.Policy != "replace" || captured.Program.Steps[0].Color != "#aaff00" || captured.Program.Steps[0].LED != 2 {
		t.Fatalf("compiled state %+v", captured)
	}
	res, out = call(t, session, "cancel_job", map[string]any{"id": "job_example"})
	if res.IsError || out.Data.(map[string]any)["status"] != "cancelling" || out.PollAfterMS != 200 {
		t.Fatalf("cancel was falsely completed: %+v", out)
	}
	res, out = call(t, session, "get_job", map[string]any{"id": "job_example"})
	if res.IsError || out.Data.(map[string]any)["status"] != "failed" || out.PollAfterMS != 0 {
		t.Fatalf("job status changed: %+v", out)
	}
	mu.Lock()
	count := len(paths)
	mu.Unlock()
	if count != 5 {
		t.Fatalf("unexpected requests (retry/fallback?): %v", paths)
	}
}

func TestSDKSchemaAndDryRunNeverReachDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request: %s", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	for name, args := range map[string]map[string]any{
		"overflow LED":   {"serial": "20005601", "color": "#ff0000", "led": 256},
		"unknown field":  {"serial": "20005601", "color": "#ff0000", "speed": 1},
		"fade precision": {"serial": "20005601", "color": "#ff0000", "fade_ms": 1},
		"path traversal": {"serial": "../other", "color": "#ff0000"},
	} {
		t.Run(name, func(t *testing.T) {
			res, _ := call(t, session, "set_device_state", args)
			if !res.IsError {
				t.Fatal("invalid input accepted")
			}
		})
	}
	res, out := call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "program": map[string]any{"steps": []any{map[string]any{"color": "#800000"}}}})
	if res.IsError || !out.DryRun || out.Accepted || out.Kind != "preview" {
		t.Fatalf("preview: %+v %+v", res, out)
	}
	request := out.Data.(map[string]any)["request"].(map[string]any)
	if request["policy"] != "replace" || request["program"].(map[string]any)["on_finish"] != "off" {
		t.Fatalf("defaults not normalized: %+v", request)
	}
	res, out = call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "program": map[string]any{"repeat": 10000, "steps": []any{map[string]any{"color": "#800000", "hold_ms": 3600000}}}})
	if !res.IsError || out.Error == nil || out.Error.Code != "invalid_request" {
		t.Fatalf("duration limit not enforced: %+v", out)
	}
	steps := make([]any, 1024)
	for i := range steps {
		steps[i] = map[string]any{"color": "#000000"}
	}
	res, out = call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "program": map[string]any{"repeat": 10000, "steps": steps}})
	if !res.IsError || out.Error == nil || out.Error.Code != "invalid_request" {
		t.Fatalf("zero-duration firmware ticks escaped limit: %+v", out)
	}
	res, out = call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "program": map[string]any{"repeat": 0, "steps": []any{map[string]any{"color": "#000000"}}}})
	if res.IsError || !out.DryRun {
		t.Fatalf("repeat zero must normalize to one: %+v %+v", res, out)
	}
}

func TestHTTPFailuresBecomeStructuredToolErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"queue_full","message":"device queue full"}}`)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	res, out := call(t, session, "set_device_state", map[string]any{"serial": "20005601", "color": "#ff0000", "policy": "queue"})
	if !res.IsError || out.Error == nil || out.Error.Code != "queue_full" || out.Error.HTTPStatus != 409 || out.Error.OutcomeUnknown || out.Accepted {
		t.Fatalf("HTTP failure lost: %+v %+v", res, out)
	}
}

func TestOfficialSDKOverStdio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"serial":"20005601","connection":"online"}]`)
	}))
	defer srv.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestStdioHelperProcess$")
	command.Env = append(os.Environ(), "NB1_MCP_TEST_HELPER=1", "NB1_MCP_TEST_ENDPOINT="+srv.URL)
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-integration", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 20 {
		t.Fatalf("list: %v %v", listed, err)
	}
	res, out := call(t, session, "list_devices", map[string]any{})
	if res.IsError || out.Kind != "devices" {
		t.Fatalf("stdio call: %+v", out)
	}
}

func TestStdioHelperProcess(t *testing.T) {
	if os.Getenv("NB1_MCP_TEST_HELPER") != "1" {
		return
	}
	d, err := newDaemon(os.Getenv("NB1_MCP_TEST_ENDPOINT"), time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = newServer(d).Run(context.Background(), &mcp.StdioTransport{MaxLineLength: 1 << 20})
	d.close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
