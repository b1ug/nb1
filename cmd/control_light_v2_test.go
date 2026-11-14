package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

const cliLightV2Input = `{"contract":"nb1-light-action/experimental-v2","action":{"kind":"orbit","colors":["red",{"space":"rgb8","r":0,"g":0,"b":255}],"leds":[2,1],"period_ms":600,"cycles":2,"segments":4,"on_finish":"off"}}`
const cliLightV2JobID = "framejob_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type lightV2CommandService struct {
	lightCommandService                                      // Legacy counters must stay zero for every v2 request.
	frameMu                                                  sync.Mutex
	frameSubmitted, frameLookups, frameHistory, frameCancels int32
	frameReceipt                                             control.ReceiptRequest
	frameCurrent                                             control.FrameJob
	frameRunning                                             bool
	frameLookupError                                         error
}

func (*lightV2CommandService) Device(serial string) (control.DeviceState, error) {
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial,
		Capabilities: []string{"set", "raw-rgb", "mk2-basic", "hold"}}, Connection: "online"}, nil
}

func cliLightV2QueuedJob(p control.FrameProgram) control.FrameJob {
	preview, err := control.PreviewFrameProgram(p)
	if err != nil {
		panic(err) // Test fixtures only.
	}
	return control.FrameJob{ID: cliLightV2JobID, Device: "demo", Status: "queued", Program: preview.Program,
		TotalFrames: preview.TotalFrames, PlannedTargetWrites: preview.PlannedTargetWrites,
		CreatedAt: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)}
}

func (s *lightV2CommandService) SubmitFrameWithReceipt(_ context.Context, serial string, p control.FrameProgram, policy string, ticket control.ReceiptRequest) (control.FrameReceiptResult, error) {
	atomic.AddInt32(&s.frameSubmitted, 1)
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	if policy != "reject" {
		return control.FrameReceiptResult{}, control.ErrInvalid
	}
	s.frameReceipt = ticket
	s.frameCurrent = cliLightV2QueuedJob(p)
	s.frameCurrent.Device = serial
	return control.FrameReceiptResult{Job: s.frameCurrent, DaemonInstanceID: ticket.ExpectedDaemonInstanceID}, nil
}

func (s *lightV2CommandService) FrameRequest(serial, id, instance string) (control.FrameJob, error) {
	atomic.AddInt32(&s.frameLookups, 1)
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	if s.frameLookupError != nil {
		return control.FrameJob{}, s.frameLookupError
	}
	if serial != s.frameCurrent.Device || id != s.frameReceipt.RequestID || instance != s.frameReceipt.ExpectedDaemonInstanceID {
		return control.FrameJob{}, control.ErrRequestNotFound
	}
	job := s.frameCurrent
	started := job.CreatedAt.Add(time.Millisecond)
	job.StartedAt = &started
	job.Status = "running"
	if !s.frameRunning {
		finished := started.Add(time.Millisecond)
		job.FinishedAt = &finished
		job.Status = "succeeded"
		job.CompletedFrames, job.CompletedTargetWrites = job.TotalFrames, job.PlannedTargetWrites
	}
	return job, nil
}

func (s *lightV2CommandService) FrameJob(string) (control.FrameJob, error) {
	atomic.AddInt32(&s.frameHistory, 1)
	return control.FrameJob{}, control.ErrNotFound
}

func (s *lightV2CommandService) CancelFrame(context.Context, string) (control.FrameJob, error) {
	atomic.AddInt32(&s.frameCancels, 1)
	return control.FrameJob{}, control.ErrNotFound
}

func (s *lightV2CommandService) seed(t *testing.T, data string) {
	t.Helper()
	compiled, err := lightactionv2.CompileRequest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	s.frameReceipt = control.ReceiptRequest{RequestID: commandRequestID, ExpectedDaemonInstanceID: commandInstance}
	s.frameCurrent = cliLightV2QueuedJob(compiled.Program)
}

type lightV2RequestTrace struct {
	method, path, query string
	body                []byte
}

type lightV2Trace struct {
	mu       sync.Mutex
	requests []lightV2RequestTrace
}

func (trace *lightV2Trace) snapshot() []lightV2RequestTrace {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]lightV2RequestTrace(nil), trace.requests...)
}

func lightV2TestServer(service *lightV2CommandService) (*httptest.Server, *lightV2Trace) {
	trace := &lightV2Trace{}
	application := httpapi.New(service)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		trace.mu.Lock()
		trace.requests = append(trace.requests, lightV2RequestTrace{r.Method, r.URL.Path, r.URL.RawQuery, body})
		trace.mu.Unlock()
		application.ServeHTTP(w, r)
	}))
	return server, trace
}

// executePreviewCommand intentionally omits the application root flags. Use
// this local equivalent only when testing actual --wait flag parsing; restore
// the shared root variable so other command tests remain isolated.
func executeLightV2WaitCommand(ctx context.Context, args ...string) (string, error) {
	previous := waitComplete
	defer func() { waitComplete = previous }()
	command := newControlCommand()
	command.PersistentFlags().BoolVar(&waitComplete, "wait", false, "test root wait flag")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func assertLightV2NoFallback(t *testing.T, service *lightV2CommandService) {
	t.Helper()
	if atomic.LoadInt32(&service.submitted) != 0 || atomic.LoadInt32(&service.keyed) != 0 || atomic.LoadInt32(&service.lookups) != 0 || atomic.LoadInt32(&service.history) != 0 || atomic.LoadInt32(&service.cancels) != 0 || atomic.LoadInt32(&service.frameHistory) != 0 || atomic.LoadInt32(&service.frameCancels) != 0 {
		t.Fatal("v2 request used legacy submission/history/cancellation or frame history/cancel fallback")
	}
}

func TestControlLightV2DirectPreviewHasNoClientOrOwnership(t *testing.T) {
	blockedLock := previewFile(t, "regular file, not an ownership directory")
	input := previewFile(t, cliLightV2Input)
	text, err := executePreviewCommand(context.Background(), "--mode", "direct", "--endpoint", "not-a-valid-origin", "--lock-dir", blockedLock, "light", "preview", input)
	if err != nil {
		t.Fatal(err)
	}
	var result daemonclient.LightV2Preview
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	want, err := lightactionv2.CompileRequest([]byte(cliLightV2Input))
	if err != nil || !reflect.DeepEqual(result.Result, want) || !reflect.DeepEqual(result.Preview.Program, want.Program) || result.Preview.Execution.Available {
		t.Fatalf("pure v2 preview changed compilation or claimed execution: %s; %v", text, err)
	}
	unchanged, err := os.ReadFile(blockedLock)
	if err != nil || string(unchanged) != "regular file, not an ownership directory" {
		t.Fatal("v2 preview touched ownership path", err)
	}
}

func TestControlLightV2DaemonPreviewAndRunRetainOriginalAction(t *testing.T) {
	service := &lightV2CommandService{}
	server, trace := lightV2TestServer(service)
	defer server.Close()
	path := previewFile(t, cliLightV2Input)
	text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "preview", path, "--serial", "demo")
	var preview daemonclient.LightV2Preview
	if err != nil || json.Unmarshal([]byte(text), &preview) != nil || !preview.Preview.Execution.Available || atomic.LoadInt32(&service.prepares) != 0 || atomic.LoadInt32(&service.frameSubmitted) != 0 {
		t.Fatal(text, err)
	}
	text, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", path)
	var output lightV2RunOutput
	if err != nil || json.Unmarshal([]byte(text), &output) != nil || !output.Accepted || output.OutcomeUnknown || output.Job == nil || output.RequestTicket == nil || output.Contract != lightactionv2.Contract || output.Policy != "reject" || output.Mode != "daemon" || output.DaemonInstance != commandInstance || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.frameSubmitted) != 1 {
		t.Fatal(text, err)
	}
	if !strings.HasPrefix(output.RequestTicket.RequestID, "req_60000_") || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance {
		t.Fatalf("automatic original ticket was not preserved: %#v", output)
	}
	text, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", path, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.RequestTicket.RequestID != commandRequestID || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.frameSubmitted) != 2 {
		t.Fatal(text, err)
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cliLightV2Input), &original); err != nil {
		t.Fatal(err)
	}
	var originalAction interface{}
	_ = json.Unmarshal(original["action"], &originalAction)
	for _, request := range trace.snapshot() {
		if request.path == "/v1/instance" {
			continue
		}
		if request.method != http.MethodPost || (request.path != "/v2/light/preview" && request.path != "/v2/devices/demo/light") {
			t.Fatalf("v2 command took unexpected route: %#v", request)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(request.body, &envelope); err != nil {
			t.Fatal(err)
		}
		var action interface{}
		_ = json.Unmarshal(envelope["action"], &action)
		if !reflect.DeepEqual(action, originalAction) || envelope["program"] != nil {
			t.Fatalf("CLI replaced original color forms/action with local compilation: %s", request.body)
		}
	}
	assertLightV2NoFallback(t, service)
}

func TestControlLightV2WaitQueriesOriginalReceiptAndNeverCancels(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "timeout"}[running], func(t *testing.T) {
			service := &lightV2CommandService{frameRunning: running}
			server, trace := lightV2TestServer(service)
			defer server.Close()
			args := []string{"--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightV2Input), "--wait"}
			if running {
				args = append(args, "--timeout", "25ms")
			}
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2RunOutput
			if json.Unmarshal([]byte(text), &output) != nil || !output.Accepted || output.Job == nil || output.RequestTicket == nil || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.frameSubmitted) != 1 || atomic.LoadInt32(&service.frameLookups) < 1 {
				t.Fatal(text, err)
			}
			if running && (err == nil || !output.OutcomeUnknown) || !running && (err != nil || output.OutcomeUnknown || output.Job.Status != "succeeded") {
				t.Fatal(text, err)
			}
			for _, request := range trace.snapshot() {
				if strings.Contains(request.path, "/frame-requests/") && (request.method != http.MethodGet || !strings.Contains(request.path, output.RequestTicket.RequestID) || request.query != "expected_daemon_instance_id="+commandInstance) {
					t.Fatalf("wait queried a replacement ticket: %#v", request)
				}
			}
			assertLightV2NoFallback(t, service)
		})
	}
}

func TestControlLightV2UnknownPostKeepsTicketWithoutRetry(t *testing.T) {
	for _, malformedError := range []bool{false, true} {
		t.Run(map[bool]string{false: "connection-drop", true: "ambiguous-error"}[malformedError], func(t *testing.T) {
			var gets, posts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/instance" {
					atomic.AddInt32(&gets, 1)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(control.InstanceInfo{DaemonInstanceID: commandInstance, Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}})
					return
				}
				atomic.AddInt32(&posts, 1)
				if r.Method != http.MethodPost || r.URL.Path != "/v2/devices/demo/light" {
					t.Errorf("unexpected retry/prepare/fallback: %s %s", r.Method, r.URL)
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if malformedError {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"code":"daemon_instance_changed","code":"invalid_request","message":"ambiguous"}}`)
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
			}))
			defer server.Close()
			text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightV2Input))
			var output lightV2RunOutput
			if err == nil || json.Unmarshal([]byte(text), &output) != nil || output.Accepted || !output.OutcomeUnknown || output.RequestTicket == nil || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance || atomic.LoadInt32(&gets) != 1 || atomic.LoadInt32(&posts) != 1 {
				t.Fatal("unknown result lost original ticket or retried", text, err)
			}
		})
	}
}

func TestControlLightV2OldDaemonNeverFallsBackToV1OrFrames(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if r.Method != http.MethodPost || r.URL.Path != "/v2/devices/demo/light" {
					t.Errorf("old daemon caused fallback or prepare: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"v2 route unavailable"}}`)
			}))
			defer server.Close()
			text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightV2Input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
			var output lightV2RunOutput
			if err == nil || !strings.Contains(err.Error(), "light_v2_run_unsupported") || json.Unmarshal([]byte(text), &output) != nil || output.Accepted || output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || atomic.LoadInt32(&calls) != 1 {
				t.Fatal("unsupported v2 did not retain one rejected request", text, err)
			}
		})
	}
}

func TestControlLightV2RequestRecoveryOnlyReadsOriginalTicket(t *testing.T) {
	for _, withJobID := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown-job-id", true: "known-job-id"}[withJobID], func(t *testing.T) {
			service := &lightV2CommandService{}
			service.seed(t, cliLightV2Input)
			server, trace := lightV2TestServer(service)
			defer server.Close()
			args := []string{"--endpoint", server.URL, "light", "request", "demo", previewFile(t, cliLightV2Input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance}
			if withJobID {
				args = append(args, "--job-id", cliLightV2JobID)
			}
			text, err := executePreviewCommand(context.Background(), args...)
			var output lightV2RunOutput
			if err != nil || json.Unmarshal([]byte(text), &output) != nil || !output.Accepted || output.OutcomeUnknown || output.Job == nil || output.Job.ID != cliLightV2JobID || output.Job.Status != "succeeded" || output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance {
				t.Fatal(text, err)
			}
			requests := trace.snapshot()
			if len(requests) != 1 || requests[0].method != http.MethodGet || requests[0].path != "/v1/devices/demo/frame-requests/"+commandRequestID || requests[0].query != "expected_daemon_instance_id="+commandInstance || len(requests[0].body) != 0 || atomic.LoadInt32(&service.prepares) != 0 || atomic.LoadInt32(&service.frameSubmitted) != 0 {
				t.Fatalf("recovery performed more than original receipt GET: %#v", requests)
			}
			assertLightV2NoFallback(t, service)
		})
	}
}

func TestControlLightV2RequestUnknownMismatchAndWaitPreserveTicket(t *testing.T) {
	cases := []struct {
		name      string
		lookupErr error
		wrongJob  bool
		changed   bool
		wait      bool
	}{
		{"missing", control.ErrRequestNotFound, false, false, false},
		{"expired", control.ErrRequestExpired, false, false, false},
		{"instance-changed", control.ErrInstanceChanged, false, false, false},
		{"wrong-known-job", nil, true, false, false},
		{"changed-action", nil, false, true, false},
		{"wait-timeout", nil, false, false, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := &lightV2CommandService{frameLookupError: test.lookupErr, frameRunning: test.wait}
			service.seed(t, cliLightV2Input)
			server, trace := lightV2TestServer(service)
			defer server.Close()
			input := cliLightV2Input
			if test.changed {
				input = strings.Replace(input, `"period_ms":600`, `"period_ms":800`, 1)
			}
			args := []string{"--endpoint", server.URL, "light", "request", "demo", previewFile(t, input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance}
			if test.wrongJob {
				args = append(args, "--job-id", "framejob_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			}
			if test.wait {
				args = append(args, "--wait", "--timeout", "25ms")
			}
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2RunOutput
			if err == nil || json.Unmarshal([]byte(text), &output) != nil || !output.OutcomeUnknown || output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance {
				t.Fatal("uncertain recovery lost the original ticket", text, err)
			}
			for _, request := range trace.snapshot() {
				if request.method != http.MethodGet || request.path != "/v1/devices/demo/frame-requests/"+commandRequestID || len(request.body) != 0 {
					t.Fatalf("recovery caused a side effect or fallback: %#v", request)
				}
			}
			if atomic.LoadInt32(&service.prepares) != 0 || atomic.LoadInt32(&service.frameSubmitted) != 0 {
				t.Fatal("recovery prepared or resubmitted a request")
			}
			assertLightV2NoFallback(t, service)
		})
	}
}

func TestControlLightV2RequestAndPreviewStrictFlagGuards(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"light", "request", "demo", "missing"},
		{"light", "request", "demo", "missing", "--request-id", commandRequestID},
		{"light", "request", "demo", "missing", "--expected-daemon-instance-id", commandInstance},
		{"light", "request", "demo", "missing", "--request-id=", "--expected-daemon-instance-id="},
		{"light", "request", "../bad", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"--mode", "direct", "light", "request", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"light", "request", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--policy", "reject"},
		{"light", "request", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--job-id", "../bad"},
		{"light", "request", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--job-id="},
		{"light", "preview", "missing", "--wait"},
		{"light", "preview", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"light", "run", "demo", "missing", "--policy", "replace"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lock := filepath.Join(t.TempDir(), "untouched")
			all := append([]string{"--endpoint", server.URL, "--lock-dir", lock}, args...)
			out, err := executeLightV2WaitCommand(context.Background(), all...)
			if err == nil || out != "" || strings.Contains(err.Error(), "no such file") {
				t.Fatal("flag guard occurred after file access", out, err)
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Fatal("invalid command reached ownership", err)
			}
		})
	}
	for _, input := range []string{cliLightInput, strings.Replace(cliLightV2Input, `"segments":4`, `"segments":32`, 1)} {
		out, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "request", "demo", previewFile(t, input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
		if err == nil || out != "" {
			t.Fatal("recovery accepted a non-v2 or invalid action", out, err)
		}
	}
	if atomic.LoadInt32(&requests) != 0 {
		t.Fatalf("rejected command made %d HTTP requests", requests)
	}
}

var _ control.FrameService = (*lightV2CommandService)(nil)
var _ control.ReceiptService = (*lightV2CommandService)(nil)
