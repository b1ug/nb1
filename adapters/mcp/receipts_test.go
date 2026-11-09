package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testInstanceID = "daemon_0123456789abcdef0123456789abcdef"
const testRequestID = "req_60000_0123456789abcdef0123456789abcdef"

func connectStdio(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestStdioHelperProcess$")
	command.Env = append(os.Environ(), "NB1_MCP_TEST_HELPER=1", "NB1_MCP_TEST_ENDPOINT="+endpoint)
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-receipts", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func withTicket(args map[string]any) map[string]any {
	args["request_id"] = testRequestID
	args["expected_daemon_instance_id"] = testInstanceID
	return args
}

func assertTicket(t *testing.T, out output, want requestTicket) {
	t.Helper()
	if out.RequestTicket == nil || *out.RequestTicket != want {
		t.Fatalf("original ticket lost or replaced: %+v", out)
	}
}

func TestStdioSixLegacyToolsAndOptionalReceipts(t *testing.T) {
	var mu sync.Mutex
	var posts []submission
	var paths []string
	var instanceRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/devices":
			fmt.Fprint(w, `[{"serial":"20005601","connection":"online"}]`)
		case r.URL.Path == "/v1/instance":
			instanceRequests++
			fmt.Fprintf(w, `{"daemon_instance_id":%q,"idempotency":{"supported":true,"request_id_prefix":"req_60000_","accept_until_uptime_ms":60000,"max_accept_window_ms":60000,"receipt_retention_ms":60000,"max_entries":4096,"max_retained_steps":65536}}`, testInstanceID)
		case r.URL.Path == "/v1/devices/20005601":
			fmt.Fprint(w, `{"serial":"20005601","observed":{"led1":"#010203","sampled_at":"2026-11-09T02:00:00Z"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/20005601/jobs":
			var body submission
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			posts = append(posts, body)
			if body.RequestID != "" && body.Program.Steps[0].Color != "#102030" {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"error":{"code":"idempotency_conflict","message":"different accepted payload"}}`)
				return
			}
			if body.RequestID != "" {
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "true")
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprint(w, `{"id":"job_receipt","device":"20005601","status":"succeeded"}`)
			} else {
				w.WriteHeader(http.StatusAccepted)
				fmt.Fprint(w, `{"id":"job_legacy","status":"queued"}`)
			}
		case r.URL.Path == "/v1/jobs/job_legacy":
			fmt.Fprint(w, `{"id":"job_legacy","status":"running"}`)
		case r.URL.Path == "/v1/jobs/job_legacy/cancel":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":"job_legacy","status":"cancelling"}`)
		case strings.HasPrefix(r.URL.Path, "/v1/devices/20005601/requests/"):
			if r.URL.Query().Get("expected_daemon_instance_id") != testInstanceID {
				t.Error("receipt lookup lost original instance query")
			}
			w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
			fmt.Fprint(w, `{"id":"job_receipt","device":"20005601","status":"succeeded"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 11 {
		t.Fatalf("six legacy tools plus preview and color expected: %+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if (tool.Name == "set_device_state" || tool.Name == "run_program") && tool.Annotations.IdempotentHint {
			t.Fatal("unkeyed writes must not advertise global idempotency")
		}
	}
	legacy := []struct {
		name string
		args map[string]any
	}{
		{"list_devices", map[string]any{}},
		{"get_device_state", map[string]any{"serial": "20005601"}},
		{"set_device_state", map[string]any{"serial": "20005601", "color": "#102030"}},
		{"run_program", map[string]any{"serial": "20005601", "program": map[string]any{"steps": []any{map[string]any{"color": "#102030"}}}}},
		{"get_job", map[string]any{"id": "job_legacy"}},
		{"cancel_job", map[string]any{"id": "job_legacy"}},
	}
	for _, tc := range legacy {
		res, out := call(t, session, tc.name, tc.args)
		if res.IsError || out.RequestTicket != nil || out.InstanceBindingVerified != nil {
			t.Fatalf("legacy %s changed: %+v %+v", tc.name, res, out)
		}
		if tc.name == "list_devices" {
			if _, ok := out.Data.([]any); !ok || out.Kind != "devices" {
				t.Fatalf("legacy Data array changed: %+v", out)
			}
			encoded, _ := json.Marshal(res.StructuredContent)
			if bytes.Contains(encoded, []byte("request_ticket")) {
				t.Fatalf("legacy output gained optional ticket: %s", encoded)
			}
		}
	}
	mu.Lock()
	if instanceRequests != 0 || len(posts) != 2 || posts[0].RequestID != "" || posts[1].ExpectedDaemonInstanceID != "" {
		t.Fatalf("legacy requests were changed: %+v %d", posts, instanceRequests)
	}
	mu.Unlock()
	var previous string
	for i := 0; i < 2; i++ {
		res, out := call(t, session, "list_devices", map[string]any{"prepare_request": true})
		if res.IsError || out.RequestTicket == nil || out.Accepted || out.Kind != "devices" {
			t.Fatalf("prepare: %+v %+v", res, out)
		}
		if _, ok := out.Data.([]any); !ok {
			t.Fatalf("prepared devices Data stopped being an array: %+v", out)
		}
		if err := validateTicket(*out.RequestTicket); err != nil || !strings.HasPrefix(out.RequestTicket.RequestID, "req_60000_") || out.RequestTicket.ExpectedDaemonInstanceID != testInstanceID || out.RequestTicket.RequestID == previous {
			t.Fatalf("bad/nonfresh ticket: %+v %v", out, err)
		}
		previous = out.RequestTicket.RequestID
	}
	want := requestTicket{testRequestID, testInstanceID}
	for i := 0; i < 2; i++ {
		res, out := call(t, session, "set_device_state", withTicket(map[string]any{"serial": "20005601", "color": "#102030"}))
		if res.IsError || !out.Accepted || out.Data.(map[string]any)["id"] != "job_receipt" || out.Data.(map[string]any)["status"] != "succeeded" {
			t.Fatalf("accepted terminal replay changed: %+v %+v", res, out)
		}
		assertTicket(t, out, want)
	}
	res, out := call(t, session, "run_program", withTicket(map[string]any{"serial": "20005601", "program": map[string]any{"steps": []any{map[string]any{"color": "#102030"}}, "on_finish": "hold"}}))
	if res.IsError || !out.Accepted {
		t.Fatalf("keyed run: %+v", out)
	}
	assertTicket(t, out, want)
	res, out = call(t, session, "get_job", withTicket(map[string]any{"serial": "20005601"}))
	if res.IsError || out.Accepted || out.Data.(map[string]any)["id"] != "job_receipt" {
		t.Fatalf("receipt lookup: %+v", out)
	}
	assertTicket(t, out, want)
	res, out = call(t, session, "set_device_state", withTicket(map[string]any{"serial": "20005601", "color": "#abcdef"}))
	if !res.IsError || out.Error == nil || out.Error.Code != "idempotency_conflict" || out.Error.OutcomeUnknown {
		t.Fatalf("conflict not preserved: %+v", out)
	}
	assertTicket(t, out, want)
	mu.Lock()
	defer mu.Unlock()
	if instanceRequests != 2 || len(posts) != 6 || len(paths) != 15 || !reflect.DeepEqual(posts[2], posts[3]) || !reflect.DeepEqual(posts[2], posts[4]) {
		t.Fatalf("retry, ticket regeneration or payload mutation: posts=%+v paths=%+v instances=%d", posts, paths, instanceRequests)
	}
}

func TestStdioReceiptDryRunAndInvalidArgumentsMakeNoHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	// A deadline of 1 is syntactically valid even though only the daemon can
	// decide if it has expired. Offline preview must not claim that decision.
	for _, name := range []string{"set_device_state", "run_program"} {
		args := withTicket(map[string]any{"serial": "20005601", "dry_run": true})
		args["request_id"] = "req_1_0123456789abcdef0123456789abcdef"
		if name == "set_device_state" {
			args["color"] = "#AABBCC"
		} else {
			args["program"] = map[string]any{"steps": []any{map[string]any{"color": "#AABBCC"}}}
		}
		res, out := call(t, session, name, args)
		if res.IsError || !out.DryRun || out.Accepted || out.InstanceBindingVerified == nil || *out.InstanceBindingVerified {
			t.Fatalf("dry-run falsely verified daemon: %+v", out)
		}
		assertTicket(t, out, requestTicket{args["request_id"].(string), testInstanceID})
		preview := out.Data.(map[string]any)["request"].(map[string]any)
		if preview["request_id"] != args["request_id"] || preview["expected_daemon_instance_id"] != testInstanceID {
			t.Fatalf("preview lost ticket: %+v", preview)
		}
	}
	cases := []struct{ name, args string }{
		{"list_devices", `{"prepare_request":null}`},
		{"list_devices", `{"prepare_request":true,"prepare_\u0072equest":false}`},
		{"list_devices", `{"Prepare_Request":true}`},
		{"set_device_state", `{"serial":"20005601","color":"#102030","request_id":"` + testRequestID + `"}`},
		{"run_program", `{"serial":"20005601","program":{"steps":[{"color":"#102030"}]},"expected_daemon_instance_id":"` + testInstanceID + `"}`},
		{"set_device_state", `{"serial":"20005601","color":"#102030","request_id":"","expected_daemon_instance_id":""}`},
		{"set_device_state", `{"serial":"20005601","color":"#102030","request_id":"` + testRequestID + `","expected_daemon_instance_id":null}`},
		{"set_device_state", `{"serial":"20005601","color":"#102030","request_id":"` + testRequestID + `","request_\u0069d":"` + testRequestID + `","expected_daemon_instance_id":"` + testInstanceID + `"}`},
		{"run_program", `{"serial":"20005601","program":{"steps":[{"color":"#102030"}]},"request_id":"` + testRequestID + `","Expected_Daemon_Instance_ID":"` + testInstanceID + `"}`},
		{"get_job", `{}`},
		{"get_job", `{"id":"job_a","serial":"20005601"}`},
		{"get_job", `{"id":"job_a","serial":"20005601","request_id":"` + testRequestID + `","expected_daemon_instance_id":"` + testInstanceID + `"}`},
		{"get_job", `{"serial":"20005601","request_id":"` + testRequestID + `"}`},
		{"get_job", `{"serial":"20005601","request_id":"req_9223372036854775808_0123456789abcdef0123456789abcdef","expected_daemon_instance_id":"` + testInstanceID + `"}`},
		{"get_job", `{"serial":"20005601","request_id":"req_01_0123456789abcdef0123456789abcdef","expected_daemon_instance_id":"` + testInstanceID + `"}`},
		{"get_device_state", `{"serial":"20005601","request_id":"` + testRequestID + `"}`},
		{"cancel_job", `{"id":"job_a","expected_daemon_instance_id":"` + testInstanceID + `"}`},
	}
	for i, tc := range cases {
		res, out := call(t, session, tc.name, json.RawMessage(tc.args))
		if !res.IsError {
			t.Fatalf("invalid case %d accepted: %+v", i, out)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid or preview requests made HTTP: %d", requests.Load())
	}
}

func TestStdioReceiptErrorsKeepOriginalTicketWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	var mode atomic.Value
	mode.Store("expired")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// Isolate each injected transport fault from Go's automatic safe GET
		// replay on a stale pooled connection. This test counts adapter-level
		// requests and verifies that writes never retry or acquire a new key.
		w.Header().Set("Connection", "close")
		switch mode.Load().(string) {
		case "expired":
			w.WriteHeader(http.StatusGone)
			fmt.Fprint(w, `{"error":{"code":"request_expired","message":"expired receipt"}}`)
		case "changed":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"daemon_instance_changed","message":"daemon restarted"}}`)
		case "notfound":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"request_not_found","message":"not found at this instant"}}`)
		case "oldroute":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":"not_found","message":"no receipt route"}}`)
		case "unsupported":
			w.WriteHeader(http.StatusNotImplemented)
			fmt.Fprint(w, `{"error":{"code":"idempotency_unsupported","message":"unsupported"}}`)
		case "invaliderror":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `not-json`)
		case "server":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"code":"internal","message":"server failed"}}`)
		case "malformed":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{not-json`)
		case "transport":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close() // response lost after the fake server saw the POST
		}
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	for _, tc := range []struct{ mode, name string }{
		{"expired", "set_device_state"}, {"changed", "set_device_state"}, {"server", "set_device_state"}, {"malformed", "set_device_state"}, {"transport", "set_device_state"}, {"expired", "get_job"}, {"changed", "get_job"}, {"notfound", "get_job"}, {"server", "get_job"}, {"malformed", "get_job"}, {"transport", "get_job"}, {"oldroute", "get_job"}, {"unsupported", "get_job"}, {"invaliderror", "get_job"},
	} {
		mode.Store(tc.mode)
		before := requests.Load()
		args := withTicket(map[string]any{"serial": "20005601"})
		if tc.name == "set_device_state" {
			args["color"] = "#102030"
		}
		res, out := call(t, session, tc.name, args)
		if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown || out.Accepted {
			t.Fatalf("%s/%s hides old outcome: %+v", tc.name, tc.mode, out)
		}
		assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		if requests.Load() != before+1 {
			t.Fatalf("%s/%s retried, prepared a new key or fell back: %d requests", tc.name, tc.mode, requests.Load()-before)
		}
	}
}

func TestPrepareTicketValidationAndRandomFailure(t *testing.T) {
	info := instanceInfo{DaemonInstanceID: testInstanceID}
	info.Idempotency.Supported = true
	info.Idempotency.AcceptUntilUptimeMS = 60000
	info.Idempotency.RequestIDPrefix = "req_60000_"
	if ticket, err := ticketFromInstance(info, bytes.NewReader(make([]byte, 15))); err == nil || ticket != nil {
		t.Fatalf("short randomness created a ticket: %+v %v", ticket, err)
	}
	for _, change := range []func(*instanceInfo){
		func(v *instanceInfo) { v.Idempotency.Supported = false },
		func(v *instanceInfo) { v.Idempotency.RequestIDPrefix = "req_1_" },
		func(v *instanceInfo) { v.Idempotency.AcceptUntilUptimeMS = -1 },
		func(v *instanceInfo) { v.DaemonInstanceID = strings.ToUpper(testInstanceID) },
	} {
		bad := info
		change(&bad)
		if ticket, err := ticketFromInstance(bad, bytes.NewReader(make([]byte, 16))); err == nil || ticket != nil {
			t.Fatalf("malformed instance created a ticket: %+v %v", ticket, err)
		}
	}
}

func TestStdioPrepareFailureNeverSubmitsOrDowngrades(t *testing.T) {
	var instanceRequests, deviceRequests atomic.Int32
	var mode atomic.Value
	mode.Store("old")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/devices":
			deviceRequests.Add(1)
			fmt.Fprint(w, `[]`)
		case "/v1/instance":
			instanceRequests.Add(1)
			switch mode.Load().(string) {
			case "old":
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error":{"code":"not_found","message":"old daemon"}}`)
			case "unsupported":
				w.WriteHeader(http.StatusNotImplemented)
				fmt.Fprint(w, `{"error":{"code":"idempotency_unsupported","message":"service has no receipts"}}`)
			case "disabled":
				fmt.Fprintf(w, `{"daemon_instance_id":%q,"idempotency":{"supported":false}}`, testInstanceID)
			case "badprefix":
				fmt.Fprintf(w, `{"daemon_instance_id":%q,"idempotency":{"supported":true,"request_id_prefix":"req_123_","accept_until_uptime_ms":60000}}`, testInstanceID)
			}
		default:
			t.Errorf("preparation submitted or fell back: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	for _, value := range []string{"old", "unsupported", "disabled", "badprefix"} {
		mode.Store(value)
		res, out := call(t, session, "list_devices", map[string]any{"prepare_request": true})
		if !res.IsError || out.RequestTicket != nil || out.Accepted || out.Error == nil {
			t.Fatalf("%s silently prepared/downgraded: %+v", value, out)
		}
	}
	res, out := call(t, session, "list_devices", map[string]any{"prepare_request": false})
	if res.IsError || out.RequestTicket != nil || out.Kind != "devices" || deviceRequests.Load() != 5 || instanceRequests.Load() != 4 {
		t.Fatalf("optional prepare changed ordinary list: %+v devices=%d instance=%d", out, deviceRequests.Load(), instanceRequests.Load())
	}
}

func TestReceiptClassificationDoesNotChangeUnkeyedTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		fmt.Fprint(w, `{"error":{"code":"request_expired","message":"gone"}}`)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_device_state", map[string]any{"serial": "20005601"}},
		{"get_job", map[string]any{"id": "job_example"}},
		{"cancel_job", map[string]any{"id": "job_example"}},
		{"set_device_state", map[string]any{"serial": "20005601", "color": "#102030"}},
	} {
		res, out := call(t, session, tc.tool, tc.args)
		if !res.IsError || out.Error == nil || out.Error.OutcomeUnknown || out.RequestTicket != nil {
			t.Fatalf("unkeyed %s semantics changed: %+v", tc.tool, out)
		}
	}
}
