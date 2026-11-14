package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type wireOutput struct {
	Endpoint string          `json:"endpoint"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *toolError      `json:"error,omitempty"`
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("NB1_MCP_STDIO_HELPER") != "1" {
		return
	}
	timeout, err := time.ParseDuration(os.Getenv("NB1_MCP_TEST_TIMEOUT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	d, err := newDaemon(os.Getenv("NB1_MCP_TEST_ENDPOINT"), timeout)
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

func connectStdio(t *testing.T, endpoint string, timeout time.Duration) *mcp.ClientSession {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioHelper$")
	command.Env = append(os.Environ(), "NB1_MCP_STDIO_HELPER=1", "NB1_MCP_TEST_ENDPOINT="+endpoint, "NB1_MCP_TEST_TIMEOUT="+timeout.String())
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "nb1-v1-test", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if command.ProcessState == nil || !command.ProcessState.Success() {
			t.Errorf("stdio helper did not exit cleanly on EOF: %v", command.ProcessState)
		}
	})
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name, raw string) wireOutput {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(raw)})
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out wireOutput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("%s output %s: %v", name, encoded, err)
	}
	if result.IsError != (out.Error != nil) || len(result.Content) != 1 {
		t.Fatalf("inconsistent MCP result: %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("missing text fallback: %+v", result.Content)
	}
	var fallback any
	if err := json.Unmarshal([]byte(text.Text), &fallback); err != nil || !reflect.DeepEqual(fallback, result.StructuredContent) {
		t.Fatalf("structured/text outputs differ: %s, %v", text.Text, err)
	}
	return out
}

func requireSuccess(t *testing.T, out wireOutput, dst any) {
	t.Helper()
	if out.Error != nil {
		t.Fatalf("tool failed: %+v", out)
	}
	if dst != nil {
		if err := json.Unmarshal(out.Data, dst); err != nil {
			t.Fatalf("invalid tool data %s: %v", out.Data, err)
		}
	}
}

type fakeDriver struct{ backend *fakeBackend }

func (d fakeDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type fakeBackend struct {
	mu   sync.Mutex
	sets int
	led1 string
	led2 string
}

func (*fakeBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20000001", Product: "synthetic blink", Firmware: 204, Capabilities: []string{"set", "program", "raw-rgb", "hold", "mk2-basic"}}
}

func (b *fakeBackend) Set(step control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sets++
	if step.LED == 0 || step.LED == 1 {
		b.led1 = step.Color
	}
	if step.LED == 0 || step.LED == 2 {
		b.led2 = step.Color
	}
	return nil
}

func (b *fakeBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now().UTC()}, nil
}

func (b *fakeBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "off" {
		b.led1, b.led2 = "#000000", "#000000"
	}
	return nil
}

func (*fakeBackend) Close() {}

func (b *fakeBackend) setCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sets
}

func engineServer(t *testing.T) (*httptest.Server, *fakeBackend) {
	t.Helper()
	backend := &fakeBackend{led1: "#000000", led2: "#000000"}
	engine, err := control.New(fakeDriver{backend}, control.Options{Serials: []string{"20000001"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := engine.Device("20000001")
		if state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake device failed to connect")
		}
		time.Sleep(time.Millisecond)
	}
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(filepath.Join(t.TempDir(), "library"))))
	t.Cleanup(server.Close)
	return server, backend
}

func waitJob(t *testing.T, session *mcp.ClientSession, id, status string) control.SequenceJob {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		var job control.SequenceJob
		requireSuccess(t, call(t, session, "get_job", fmt.Sprintf(`{"id":%q}`, id)), &job)
		if job.Status == status {
			return job
		}
		if job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("Job did not reach %s: %+v", status, job)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOfficialStdioCanonicalToolsAndLocalLibrary(t *testing.T) {
	server, backend := engineServer(t)
	session := connectStdio(t, server.URL, 3*time.Second)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"cancel_job", "get_device_state", "get_job", "get_pattern", "list_colors", "list_devices", "list_patterns", "preview_light", "resolve_color", "run_light", "save_color", "save_pattern"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tool set=%v", names)
	}
	var devices []control.DeviceState
	requireSuccess(t, call(t, session, "list_devices", `{}`), &devices)
	if len(devices) != 1 || devices[0].Serial != "20000001" {
		t.Fatalf("device list=%+v", devices)
	}
	requireSuccess(t, call(t, session, "get_device_state", `{"serial":"20000001"}`), nil)
	var saved daemonclient.SequenceColor
	requireSuccess(t, call(t, session, "save_color", `{"name":"status-ok","color":{"space":"rgb8","r":0,"g":255,"b":0}}`), &saved)
	if saved.Color != "#00ff00" {
		t.Fatalf("saved color=%+v", saved)
	}
	var resolved resolvedColor
	requireSuccess(t, call(t, session, "resolve_color", `{"color":"@status-ok"}`), &resolved)
	if resolved.Color != saved.Color {
		t.Fatalf("resolved local color=%+v", resolved)
	}
	var colors []daemonclient.SequenceColor
	requireSuccess(t, call(t, session, "list_colors", `{}`), &colors)
	if len(colors) != 1 || colors[0] != saved {
		t.Fatalf("colors=%+v", colors)
	}
	action := `{"kind":"blink","color":"@status-ok","on_ms":20,"off_ms":20}`
	var preview daemonclient.SequencePreview
	requireSuccess(t, call(t, session, "preview_light", `{"serial":"20000001","action":`+action+`}`), &preview)
	if preview.TotalSteps != 2 || preview.DeviceCheck.Status != "supported" {
		t.Fatalf("preview=%+v", preview)
	}
	var pattern daemonclient.SequencePattern
	requireSuccess(t, call(t, session, "save_pattern", `{"id":"gentle","title":"Gentle blink","action":`+action+`}`), &pattern)
	var patterns []daemonclient.SequencePatternSummary
	requireSuccess(t, call(t, session, "list_patterns", `{}`), &patterns)
	if len(patterns) != 1 || patterns[0].ID != "gentle" {
		t.Fatalf("patterns=%+v", patterns)
	}
	var loaded daemonclient.SequencePattern
	requireSuccess(t, call(t, session, "get_pattern", `{"id":"gentle"}`), &loaded)
	if !reflect.DeepEqual(pattern, loaded) {
		t.Fatal("saved Pattern changed on read")
	}
	requireSuccess(t, call(t, session, "preview_light", `{"pattern_id":"gentle"}`), nil)
	if count := backend.setCount(); count != 0 {
		t.Fatalf("read/preview/save operations issued %d Set calls", count)
	}
	run := call(t, session, "run_light", `{"serial":"20000001","id":"natural-one","pattern_id":"gentle"}`)
	requireSuccess(t, run, nil)
	if run.ID != "natural-one" || run.Endpoint != server.URL {
		t.Fatalf("Job metadata lost: %+v", run)
	}
	completed := waitJob(t, session, run.ID, "succeeded")
	if completed.CompletedSteps != 2 || backend.setCount() != 2 {
		t.Fatalf("execution=%+v Set=%d", completed, backend.setCount())
	}
	requireSuccess(t, call(t, session, "run_light", `{"serial":"20000001","id":"natural-one","pattern_id":"gentle"}`), nil)
	requireSuccess(t, call(t, session, "cancel_job", `{"id":"natural-one"}`), nil)
	if backend.setCount() != 2 {
		t.Fatal("replayed or terminal-cancelled Job executed again")
	}
}

func TestOfficialStdioBusyRejectionAndActiveCancellation(t *testing.T) {
	server, backend := engineServer(t)
	session := connectStdio(t, server.URL, 3*time.Second)
	args := `{"serial":"20000001","id":"active","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#123456"}],"hold_ms":5000}]}}`
	requireSuccess(t, call(t, session, "run_light", args), nil)
	waitJob(t, session, "active", "running")
	out := call(t, session, "run_light", `{"serial":"20000001","id":"other","action":{"kind":"off"}}`)
	if out.Error == nil || out.Error.Code != "device_busy" || out.Error.HTTPStatus != 409 || out.Error.OutcomeUnknown {
		t.Fatalf("busy rejection=%+v", out)
	}
	requireSuccess(t, call(t, session, "cancel_job", `{"id":"active"}`), nil)
	waitJob(t, session, "active", "cancelled")
	var state control.DeviceState
	requireSuccess(t, call(t, session, "get_device_state", `{"serial":"20000001"}`), &state)
	if backend.setCount() != 1 || state.Observed == nil || state.Observed.LED1 != "#000000" || state.Observed.LED2 != "#000000" {
		t.Fatalf("cancel cleanup=%+v Set=%d", state, backend.setCount())
	}
}

func TestOfficialStdioInvalidArgumentsStayOffHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, time.Second)
	for _, tc := range []struct{ name, args string }{
		{"list_devices", `{"include_capabilities":true}`},
		{"run_light", `{"serial":"20000001","id":"a","id":"b","action":{"kind":"off"}}`},
		{"run_light", `{"serial":"20000001","action":{"kind":"set","color":null}}`},
		{"run_light", `{"serial":"20000001","action":{"kind":"off"},"pattern_id":"a"}`},
		{"run_light", `{"serial":"20000001","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#123456"}],"interval_ms":50}]}}`},
		{"get_job", `{"id":"bad/id"}`},
		{"save_pattern", `{"id":"a","pattern_id":"b"}`},
		{"save_color", `{"name":"BadName","color":"red"}`},
	} {
		out := call(t, session, tc.name, tc.args)
		if out.Error == nil || out.Error.Code != "invalid_request" || out.Error.OutcomeUnknown {
			t.Errorf("invalid input accepted: %s %+v", tc.args, out)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid arguments made %d HTTP calls", calls)
	}
}

func fixtureJob(id string) control.SequenceJob {
	return control.SequenceJob{ID: id, Serial: "20000001", Status: "succeeded", TotalSteps: 1, CompletedSteps: 1, CreatedAt: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
}

func TestOfficialStdioLostResponseKeepsIDAndQueriesOriginalJob(t *testing.T) {
	var posts, gets int32
	var id atomic.Value
	id.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&posts, 1)
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			id.Store(body.ID)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/"+id.Load().(string) {
			atomic.AddInt32(&gets, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fixtureJob(id.Load().(string)))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, time.Second)
	out := call(t, session, "run_light", `{"serial":"20000001","action":{"kind":"off"}}`)
	if out.Error == nil || !out.Error.OutcomeUnknown || out.ID != id.Load().(string) || out.Endpoint != server.URL || !regexp.MustCompile(`^j_[0-9A-HJKMNP-TV-Z]{10}$`).MatchString(out.ID) {
		t.Fatalf("lost-response result=%+v", out)
	}
	var job control.SequenceJob
	requireSuccess(t, call(t, session, "get_job", fmt.Sprintf(`{"id":%q}`, out.ID)), &job)
	if job.ID != out.ID || atomic.LoadInt32(&posts) != 1 || atomic.LoadInt32(&gets) != 1 {
		t.Fatalf("recovery changed intent: job=%+v POST=%d GET=%d", job, posts, gets)
	}
}

func TestOfficialStdioMalformedAcceptanceKeepsCallerID(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(fixtureJob("different-job"))
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, time.Second)
	out := call(t, session, "run_light", `{"serial":"20000001","id":"original-job","action":{"kind":"off"}}`)
	if out.Error == nil || !out.Error.OutcomeUnknown || out.Error.Code != "invalid_daemon_response" || out.ID != "original-job" || out.Endpoint != server.URL || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("malformed acceptance=%+v calls=%d", out, calls)
	}
}

func TestOfficialStdioPreservesColorNumberLexemes(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		data, err := io.ReadAll(r.Body)
		if err != nil || !strings.Contains(string(data), "1e2") {
			t.Errorf("color number lexeme changed: %s %v", data, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"exponents are not accepted"}}`))
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, time.Second)
	out := call(t, session, "resolve_color", `{"color":{"space":"hsv","h_deg":1e2,"s_pct":100,"v_pct":100}}`)
	if out.Error == nil || out.Error.Code != "invalid_request" || out.Error.OutcomeUnknown || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("color rejection=%+v calls=%d", out, calls)
	}
}

func TestOfficialStdioHTTPTimeoutRetainsIDWithoutCancel(t *testing.T) {
	var posts, gets, cancels int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&posts, 1)
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/timeout-job" {
			atomic.AddInt32(&gets, 1)
			w.Header().Set("Content-Type", "application/json")
			job := fixtureJob("timeout-job")
			job.Status, job.CompletedSteps = "running", 0
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		atomic.AddInt32(&cancels, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, 100*time.Millisecond)
	out := call(t, session, "run_light", `{"serial":"20000001","id":"timeout-job","action":{"kind":"off"}}`)
	if out.Error == nil || out.Error.Code != "request_timeout" || !out.Error.OutcomeUnknown || out.ID != "timeout-job" || out.Endpoint != server.URL {
		t.Fatalf("timeout result=%+v", out)
	}
	requireSuccess(t, call(t, session, "get_job", `{"id":"timeout-job"}`), nil)
	if atomic.LoadInt32(&posts) != 1 || atomic.LoadInt32(&gets) != 1 || atomic.LoadInt32(&cancels) != 0 {
		t.Fatalf("timeout replayed or cancelled work: POST=%d GET=%d other=%d", posts, gets, cancels)
	}
}

func TestEndpointAndTimeoutValidation(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:8931", "http://example.com:8931", "http://127.0.0.1:8931/path", "http://user@127.0.0.1:8931"} {
		if _, err := newDaemon(endpoint, time.Second); err == nil {
			t.Errorf("accepted invalid endpoint %s", endpoint)
		}
	}
	if _, err := newDaemon(defaultEndpoint, 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
}
