package mcpserver

import (
	"bytes"
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
	"github.com/b1ug/nb1/internal/remoteaccess"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type wireOutput struct {
	Endpoint string          `json:"endpoint"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *toolError      `json:"error,omitempty"`
}

const testVersion = "dev"
const publicTestEndpoint = "https://lamp.example/mcp"
const testToken = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("NB1_MCP_STDIO_HELPER") != "1" {
		return
	}
	args := []string{
		"--endpoint", os.Getenv("NB1_MCP_TEST_ENDPOINT"), "--timeout", os.Getenv("NB1_MCP_TEST_TIMEOUT"),
	}
	if tokenFile := os.Getenv("NB1_MCP_TEST_TOKEN_FILE"); tokenFile != "" {
		args = append(args, "--token-file", tokenFile)
	}
	os.Exit(RunStdioCommand("nb1 mcp", args, testVersion, os.Stderr))
}

func connectStdio(t *testing.T, endpoint string, timeout time.Duration) *mcp.ClientSession {
	t.Helper()
	return connectStdioWithToken(t, endpoint, timeout, "")
}

func connectStdioWithToken(t *testing.T, endpoint string, timeout time.Duration, tokenFile string) *mcp.ClientSession {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioHelper$")
	command.Env = append(os.Environ(), "NB1_MCP_STDIO_HELPER=1", "NB1_MCP_TEST_ENDPOINT="+endpoint, "NB1_MCP_TEST_TIMEOUT="+timeout.String(), "NB1_MCP_TEST_TOKEN_FILE="+tokenFile)
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

type routedFakeBackend struct {
	*fakeBackend
	serial string
}

func (b *routedFakeBackend) Info() control.DeviceInfo {
	info := b.fakeBackend.Info()
	info.Serial = b.serial
	return info
}

type routedFakeDriver map[string]*routedFakeBackend

func (d routedFakeDriver) Open(serial string) (control.Backend, error) {
	if backend := d[serial]; backend != nil {
		return backend, nil
	}
	return nil, fmt.Errorf("missing fake device %s", serial)
}

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
		if tool.Description != protocolText.ToolDescriptions[tool.Name] || strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q description differs from embedded text or is empty: %q", tool.Name, tool.Description)
		}
		if tool.Name == "run_light" {
			checkRunLightSchemaDescriptions(t, tool.InputSchema)
		}
	}
	sort.Strings(names)
	want := []string{"cancel_job", "clear_playback_override", "get_device_state", "get_job", "get_pattern", "get_playback_status", "list_colors", "list_devices", "list_patterns", "list_playback_requests", "preview_light", "resolve_color", "run_light", "save_color", "save_pattern", "set_playback_override"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tool set=%v", names)
	}
	if len(protocolText.ToolDescriptions) != len(names) {
		t.Errorf("embedded descriptions cover %d tools, server advertises %d", len(protocolText.ToolDescriptions), len(names))
	}
	var devices []control.DeviceState
	requireSuccess(t, call(t, session, "list_devices", `{}`), &devices)
	if len(devices) != 1 || devices[0].Serial != "20000001" {
		t.Fatalf("device list=%+v", devices)
	}
	var playbackStatus control.PlaybackState
	requireSuccess(t, call(t, session, "get_playback_status", `{}`), &playbackStatus)
	if playbackStatus.Mode != "always" || playbackStatus.Status != "allowed" {
		t.Fatalf("initial Playback status=%+v", playbackStatus)
	}
	requireSuccess(t, call(t, session, "set_playback_override", `{"mode":"suppress","duration_ms":60000}`), &playbackStatus)
	if playbackStatus.Status != "suppressed" || playbackStatus.Override == nil || playbackStatus.Override.Mode != "suppress" {
		t.Fatalf("suppressed Playback status=%+v", playbackStatus)
	}
	blocked := call(t, session, "run_light", `{"serial":"20000001","id":"suppressed-job","action":{"kind":"off"}}`)
	if blocked.Error == nil || blocked.Error.Code != "playback_suppressed" || blocked.Error.HTTPStatus != 423 {
		t.Fatalf("suppressed playback result=%+v", blocked)
	}
	if count := backend.setCount(); count != 0 {
		t.Fatalf("suppressed playback issued %d Set calls", count)
	}
	var history daemonclient.PlaybackRequests
	requireSuccess(t, call(t, session, "list_playback_requests", `{}`), &history)
	if len(history.Requests) != 1 || history.Requests[0].Decision != "suppressed" || history.Requests[0].JobID != "suppressed-job" {
		t.Fatalf("suppressed playback history=%+v", history)
	}
	playbackStatus = control.PlaybackState{}
	requireSuccess(t, call(t, session, "clear_playback_override", `{}`), &playbackStatus)
	if playbackStatus.Status != "allowed" || playbackStatus.Override != nil {
		t.Fatalf("cleared Playback status=%+v", playbackStatus)
	}
	requireSuccess(t, call(t, session, "get_device_state", `{"serial":"20000001"}`), nil)
	var saved daemonclient.SequenceColor
	requireSuccess(t, call(t, session, "save_color", `{"name":"status-ok","color":{"space":"rgb8","r":0,"g":255,"b":0}}`), &saved)
	if saved.Color != "#00ff00" {
		t.Fatalf("saved color=%+v", saved)
	}
	var resolved daemonclient.ResolvedSequenceColor
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

func TestOfficialStdioBuildIdentityAndRunWithoutPreview(t *testing.T) {
	server, backend := engineServer(t)
	session := connectStdio(t, server.URL, 3*time.Second)
	initialized := session.InitializeResult()
	if initialized.ServerInfo.Name != "nb1" || initialized.ServerInfo.Version != testVersion {
		t.Fatalf("initialize identity=%+v, want nb1/%s", initialized.ServerInfo, testVersion)
	}
	if !strings.Contains(initialized.Instructions, "Preview is optional.") ||
		!strings.Contains(initialized.Instructions, "without a preceding preview") ||
		!strings.Contains(initialized.Instructions, "This stdio adapter selects its local HTTP endpoint with --endpoint (default "+defaultEndpoint+")") ||
		!strings.Contains(initialized.Instructions, "Foreground CLI direct Jobs are separate") {
		t.Fatalf("initialize instructions still require preview: %s", initialized.Instructions)
	}
	var job control.SequenceJob
	requireSuccess(t, call(t, session, "run_light", `{"serial":"20000001","id":"direct-play","action":{"kind":"set","color":"red"}}`), &job)
	if job.ID != "direct-play" {
		t.Fatalf("run lost its Job identity: %+v", job)
	}
	completed := waitJob(t, session, job.ID, "succeeded")
	if completed.CompletedSteps != 1 || backend.setCount() != 1 {
		t.Fatalf("run without preview=%+v Set=%d", completed, backend.setCount())
	}
}

// run_light exposes all six shared schema descriptions. Check the field and a
// distinguishing term so swapping either the lookup keys or the copy fails.
func checkRunLightSchemaDescriptions(t *testing.T, inputSchema any) {
	t.Helper()
	type field struct {
		Properties  map[string]field
		OneOf       []field
		Type        string
		Const       string
		Description string
	}
	var schema field
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	actions := make(map[string]field)
	for _, action := range schema.Properties["action"].OneOf {
		actions[action.Properties["kind"].Const] = action
	}
	color := actions["set"].Properties["color"]
	var colorString field
	for _, variant := range color.OneOf {
		if variant.Type == "string" {
			colorString = variant
		}
	}
	bindings := []struct {
		key, term string
		field     field
	}{
		{"job_id", "Crockford Base32", schema.Properties["id"]},
		{"job_policy", "preempt", schema.Properties["policy"]},
		{"sequence", "interval_ms", schema.Properties["sequence"]},
		{"action_period_ms", "full cycle", actions["breathe"].Properties["period_ms"]},
		{"color_hsv", "HSV", color},
		{"color_string", "Hex6", colorString},
	}
	if len(protocolText.SchemaDescriptions) != len(bindings) {
		t.Errorf("embedded schema descriptions have %d entries, checked %d", len(protocolText.SchemaDescriptions), len(bindings))
	}
	for _, b := range bindings {
		if got := b.field.Description; got != protocolText.SchemaDescriptions[b.key] || !strings.Contains(got, b.term) {
			t.Errorf("run_light %s description = %q, want the catalog entry containing %q", b.key, got, b.term)
		}
	}
}

func testMCPTransport(t *testing.T, daemonURL string, timeout time.Duration, remote bool) (string, func() *mcp.ClientSession) {
	t.Helper()
	recoveryURL := daemonURL
	connect := func() *mcp.ClientSession { return connectStdio(t, daemonURL, timeout) }
	if remote {
		public := httptest.NewUnstartedServer(nil)
		t.Cleanup(public.Close)
		recoveryURL = "https://" + public.Listener.Addr().String() + "/mcp"
		handler, closeClient, err := NewHTTPHandler(daemonURL, recoveryURL, timeout, testVersion, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(closeClient)
		mux := http.NewServeMux()
		mux.Handle("/mcp", handler)
		public.Config.Handler = mux
		public.StartTLS()
		connect = func() *mcp.ClientSession {
			agent := mcp.NewClient(&mcp.Implementation{Name: "nb1-http-test", Version: "1"}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			session, err := agent.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: recoveryURL, HTTPClient: public.Client()}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			return session
		}
	}
	return recoveryURL, connect
}

func TestOfficialStdioTriggerRoutingPreservesJobTargetAndIdentity(t *testing.T) {
	testTriggerRoutingPreservesJobTargetAndIdentity(t, false)
}

func TestStreamableHTTPTriggerRoutingPreservesJobTargetAndIdentity(t *testing.T) {
	testTriggerRoutingPreservesJobTargetAndIdentity(t, true)
}

func testTriggerRoutingPreservesJobTargetAndIdentity(t *testing.T, remote bool) {
	t.Helper()
	a := &routedFakeBackend{fakeBackend: &fakeBackend{led1: "#000000", led2: "#000000"}, serial: "20000001"}
	b := &routedFakeBackend{fakeBackend: &fakeBackend{led1: "#000000", led2: "#000000"}, serial: "20000002"}
	engine, err := control.New(routedFakeDriver{a.serial: a, b.serial: b}, control.Options{Serials: []string{a.serial, b.serial}, PollInterval: 10 * time.Millisecond})
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
	for _, serial := range []string{a.serial, b.serial} {
		for {
			state, _ := engine.Device(serial)
			if state.Connection == "online" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fake devices failed to connect")
			}
			time.Sleep(time.Millisecond)
		}
	}
	library := filepath.Join(t.TempDir(), "library")
	if err := os.Mkdir(library, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTrigger := func(serial string) {
		t.Helper()
		data := fmt.Sprintf(`{"schema_version":1,"triggers":{"success":{"pattern_id":"gentle","serial":%q}}}`, serial)
		if err := os.WriteFile(filepath.Join(library, "triggers.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTrigger(b.serial)
	handler := httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(library), httpapi.WithSequenceRouting(a.serial, true, library))
	var submissions int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&submissions, 1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	endpoint, connect := testMCPTransport(t, server.URL, 3*time.Second, remote)
	session := connect()
	saved := call(t, session, "save_pattern", `{"id":"gentle","action":{"kind":"set","color":"red"}}`)
	if saved.Error != nil {
		t.Fatalf("save fixture Pattern: %+v", *saved.Error)
	}
	requireSuccess(t, saved, nil)
	run := func(args, id, serial string) control.SequenceJob {
		t.Helper()
		out := call(t, session, "run_light", args)
		var job control.SequenceJob
		requireSuccess(t, out, &job)
		if out.ID != id || out.Endpoint != endpoint || job.ID != id || job.Serial != serial {
			t.Fatalf("accepted Job identity/target changed: output=%+v Job=%+v", out, job)
		}
		finished := waitJob(t, session, id, "succeeded")
		if finished.ID != id || finished.Serial != serial {
			t.Fatalf("queried Job identity/target changed: %+v", finished)
		}
		return finished
	}
	run(`{"id":"trigger-original","trigger":"success"}`, "trigger-original", b.serial)
	if a.setCount() != 0 || b.setCount() != 1 {
		t.Fatalf("trigger without serial ignored its binding: A=%d B=%d", a.setCount(), b.setCount())
	}
	run(`{"id":"default-original","action":{"kind":"set","color":"blue"}}`, "default-original", a.serial)
	run(`{"serial":"20000001","id":"override-original","trigger":"success"}`, "override-original", a.serial)
	if a.setCount() != 2 || b.setCount() != 1 {
		t.Fatalf("default/explicit override reached wrong target: A=%d B=%d", a.setCount(), b.setCount())
	}
	before := atomic.LoadInt32(&submissions)
	for _, mixed := range []string{
		`{"trigger":"success","action":{"kind":"off"}}`,
		`{"trigger":"success","pattern_id":"gentle"}`,
		`{"trigger":"success","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red"}],"fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"hold"}}`,
	} {
		out := call(t, session, "run_light", mixed)
		if out.Error == nil || out.Error.Code != "invalid_request" || out.Error.OutcomeUnknown {
			t.Fatalf("mixed trigger selector accepted or reported unknown: %+v", out)
		}
	}
	if atomic.LoadInt32(&submissions) != before {
		t.Fatal("mixed trigger selectors reached HTTP submission")
	}
	// A new mapping must not retarget or repeat a Job retained under its ID.
	writeTrigger(a.serial)
	run(`{"id":"trigger-original","trigger":"success"}`, "trigger-original", b.serial)
	if err := os.WriteFile(filepath.Join(library, "triggers.json"), []byte(`{"schema_version":1,"triggers":`), 0600); err != nil {
		t.Fatal(err)
	}
	rejected := call(t, session, "run_light", `{"id":"bad-trigger-config","trigger":"success"}`)
	if rejected.Error == nil || rejected.Error.Code != "invalid_trigger_config" || rejected.Error.HTTPStatus != 503 || rejected.Error.OutcomeUnknown {
		t.Fatalf("invalid trigger configuration is a definite rejection: %+v", rejected)
	}
	if a.setCount() != 2 || b.setCount() != 1 {
		t.Fatalf("replaying a retained trigger Job wrote again: A=%d B=%d", a.setCount(), b.setCount())
	}

	const hold = `{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#123456"}],"hold_ms":10000}]}`
	requireSuccess(t, call(t, session, "run_light", fmt.Sprintf(`{"id":"guard-a","serial":%q,"sequence":%s}`, a.serial, hold)), nil)
	requireSuccess(t, call(t, session, "run_light", fmt.Sprintf(`{"id":"guard-b","serial":%q,"sequence":%s}`, b.serial, hold)), nil)
	waitJob(t, session, "guard-a", "running")
	waitJob(t, session, "guard-b", "running")
	rejected = call(t, session, "cancel_job", fmt.Sprintf(`{"id":"guard-b","serial":%q}`, a.serial))
	if rejected.Error == nil || rejected.Error.Code != "job_target_mismatch" || rejected.Error.HTTPStatus != 409 ||
		rejected.Error.OutcomeUnknown || rejected.ID != "guard-b" || rejected.Endpoint != endpoint {
		t.Fatalf("cross-device cancel guard rejection=%+v", rejected)
	}
	var running control.SequenceJob
	requireSuccess(t, call(t, session, "get_job", `{"id":"guard-b"}`), &running)
	if running.Status != "running" || running.Serial != b.serial {
		t.Fatalf("wrong guard changed the Job: %+v", running)
	}
	requireSuccess(t, call(t, session, "cancel_job", fmt.Sprintf(`{"id":"guard-b","serial":%q}`, b.serial)), nil)
	waitJob(t, session, "guard-b", "cancelled")
	requireSuccess(t, call(t, session, "get_job", `{"id":"guard-a"}`), &running)
	if running.Status != "running" || running.Serial != a.serial {
		t.Fatalf("guarded cancellation touched another device: %+v", running)
	}
	requireSuccess(t, call(t, session, "cancel_job", `{"id":"guard-a"}`), nil)
	waitJob(t, session, "guard-a", "cancelled")
}

func TestOfficialStdioGuardedCancelDoesNotRetryAfterLegacyRejection(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/jobs/guarded/cancel?serial=20000001" || r.ContentLength != 0 {
			t.Errorf("expected one bodyless guarded POST, got %s %s (%d bytes)", r.Method, r.URL.RequestURI(), r.ContentLength)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_request","message":"unexpected query"}}`)
	}))
	defer server.Close()
	session := connectStdio(t, server.URL, time.Second)
	out := call(t, session, "cancel_job", `{"id":"guarded","serial":"20000001"}`)
	if out.Error == nil || out.Error.Code != "invalid_request" || out.Error.HTTPStatus != 400 || out.Error.OutcomeUnknown ||
		out.ID != "guarded" || out.Endpoint != server.URL || attempts.Load() != 1 {
		t.Fatalf("legacy rejection=%+v attempts=%d", out, attempts.Load())
	}
}

func TestStreamableHTTPUsesSameTools(t *testing.T) {
	daemonServer, _ := engineServer(t)
	protectedServer := httptest.NewServer(remoteaccess.ProtectLocal(testToken, daemonServer.Config.Handler))
	t.Cleanup(protectedServer.Close)
	handler, closeClient, err := NewHTTPHandler(protectedServer.URL, publicTestEndpoint, 3*time.Second, testVersion, testToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "nb1-http-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	initialized := session.InitializeResult()
	if initialized.ServerInfo.Name != "nb1" || initialized.ServerInfo.Version != testVersion {
		t.Fatalf("initialize identity=%+v, want nb1/%s", initialized.ServerInfo, testVersion)
	}
	if !strings.Contains(initialized.Instructions, "This remote service uses Streamable HTTP.") {
		t.Fatalf("HTTP transport instructions missing: %s", initialized.Instructions)
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != len(toolSpecifications()) || len(listed.Tools) != 16 {
		t.Fatalf("tool count=%d, want 16", len(listed.Tools))
	}
	var devices []control.DeviceState
	result := call(t, session, "list_devices", `{}`)
	if result.Endpoint != publicTestEndpoint {
		t.Fatalf("remote tools advertised endpoint %q, want %q", result.Endpoint, publicTestEndpoint)
	}
	requireSuccess(t, result, &devices)
	if len(devices) != 1 || devices[0].Serial != "20000001" {
		t.Fatalf("device list=%+v", devices)
	}
	var resolved daemonclient.ResolvedSequenceColor
	requireSuccess(t, call(t, session, "resolve_color", `{"color":"#abc"}`), &resolved)
	if resolved.Color != "#aabbcc" {
		t.Fatalf("authenticated color resolution=%+v", resolved)
	}
}

func TestStreamableHTTPRejectsOversizedBodyDuringRead(t *testing.T) {
	handler, closeClient, err := NewHTTPHandler(defaultEndpoint, publicTestEndpoint, time.Second, testVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat(" ", (1<<20)+1)))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("stateless endpoint returned a session ID")
	}
}

func TestStreamableHTTPAcceptsTheAuthorityValidatedByItsMount(t *testing.T) {
	handler, closeClient, err := NewHTTPHandler(defaultEndpoint, publicTestEndpoint, time.Second, testVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeClient)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "lamp.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Mcp-Session-Id") != "" {
		t.Fatalf("status=%s headers=%v", response.Status, response.Header)
	}
}

func TestOfficialStdioUsesTokenFileForLoopbackAPI(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := remoteaccess.GenerateTokenFile(file); err != nil {
		t.Fatal(err)
	}
	token, err := remoteaccess.LoadToken(file)
	if err != nil {
		t.Fatal(err)
	}
	daemonServer, _ := engineServer(t)
	protectedServer := httptest.NewServer(remoteaccess.ProtectLocal(token, daemonServer.Config.Handler))
	t.Cleanup(protectedServer.Close)
	withoutToken := connectStdio(t, protectedServer.URL, 3*time.Second)
	denied := call(t, withoutToken, "resolve_color", `{"color":"#abc"}`)
	if denied.Error == nil || denied.Error.Code != "unauthorized" || denied.Error.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("uncredentialed result=%+v", denied)
	}
	session := connectStdioWithToken(t, protectedServer.URL, 3*time.Second, file)
	requireSuccess(t, call(t, session, "list_devices", `{}`), nil)
	var resolved daemonclient.ResolvedSequenceColor
	requireSuccess(t, call(t, session, "resolve_color", `{"color":"#abc"}`), &resolved)
	if resolved.Color != "#aabbcc" {
		t.Fatalf("authenticated color resolution=%+v", resolved)
	}
}

func TestStdioInvalidTokenFileDiagnosticsArePrivate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "missing-private-token")
	var diagnostics bytes.Buffer
	if status := RunStdioCommand("nb1 mcp", []string{"--token-file", file}, testVersion, &diagnostics); status != 2 {
		t.Fatalf("status=%d, want 2", status)
	}
	if out := diagnostics.String(); !strings.Contains(out, "load local API token") || strings.Contains(out, file) {
		t.Fatal("token file diagnostics omitted context or exposed its path")
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

func TestOfficialStdioPreemptsActiveAndRetainsQueuedJob(t *testing.T) {
	server, _ := engineServer(t)
	session := connectStdio(t, server.URL, 3*time.Second)
	requireSuccess(t, call(t, session, "run_light", `{"id":"active","serial":"20000001","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red"}],"hold_ms":10000}]}}`), nil)
	waitJob(t, session, "active", "running")
	requireSuccess(t, call(t, session, "run_light", `{"id":"queued","serial":"20000001","policy":"queue","action":{"kind":"off"}}`), nil)
	requireSuccess(t, call(t, session, "run_light", `{"id":"urgent","serial":"20000001","policy":"preempt","action":{"kind":"set","color":"blue"}}`), nil)
	waitJob(t, session, "active", "cancelled")
	waitJob(t, session, "urgent", "succeeded")
	waitJob(t, session, "queued", "succeeded")
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
		{"run_light", `{"action":{"kind":"off"},"policy":"replace"}`},
		{"run_light", `{"action":{"kind":"off"},"policy":null}`},
		{"preview_light", `{"action":{"kind":"off"},"policy":"preempt"}`},
		{"run_light", `{"serial":"20000001","id":"a","id":"b","action":{"kind":"off"}}`},
		{"run_light", `{"serial":"20000001","action":{"kind":"set","color":null}}`},
		{"run_light", `{"serial":"20000001","action":{"kind":"off"},"pattern_id":"a"}`},
		{"run_light", `{"serial":"20000001","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#123456"}],"interval_ms":50}]}}`},
		{"get_job", `{"id":"bad/id"}`},
		{"cancel_job", `{"id":"valid","serial":""}`},
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
	testLostResponseRecovery(t, false)
}

func TestRemoteMCPReconnectsToAdvertisedEndpointForOriginalJob(t *testing.T) {
	testLostResponseRecovery(t, true)
}

func testLostResponseRecovery(t *testing.T, remote bool) {
	t.Helper()
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
	recoveryURL, connect := testMCPTransport(t, server.URL, time.Second, remote)
	session := connect()
	out := call(t, session, "run_light", `{"serial":"20000001","action":{"kind":"off"}}`)
	if out.Error == nil || !out.Error.OutcomeUnknown || out.ID != id.Load().(string) || out.Endpoint != recoveryURL || !regexp.MustCompile(`^j_[0-9A-HJKMNP-TV-Z]{10}$`).MatchString(out.ID) {
		t.Fatalf("lost-response result=%+v", out)
	}
	if remote {
		if strings.Contains(out.Error.Message, server.URL) || !strings.Contains(out.Error.Message, recoveryURL) {
			t.Fatalf("remote recovery diagnostic advertises the wrong URL: %q", out.Error.Message)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		session = connect()
	}
	var job control.SequenceJob
	result := call(t, session, "get_job", fmt.Sprintf(`{"id":%q}`, out.ID))
	if result.Endpoint != recoveryURL {
		t.Fatalf("recovered through %q, want %q", result.Endpoint, recoveryURL)
	}
	requireSuccess(t, result, &job)
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
	for _, endpoint := range []string{"https://127.0.0.1:5885", "http://example.com:5885", "http://127.0.0.1:5885/path", "http://user@127.0.0.1:5885"} {
		if _, err := newDaemon(endpoint, time.Second, ""); err == nil {
			t.Errorf("accepted invalid endpoint %s", endpoint)
		}
	}
	if _, err := newDaemon(defaultEndpoint, 0, ""); err == nil {
		t.Fatal("accepted zero timeout")
	}
}
