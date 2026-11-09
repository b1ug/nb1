package cmd

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/b1ug/nb1/internal/lightaction"
)

const cliLightInput = `{"contract":"nb1-light-action/v1","action":{"kind":"pulse","color":"lime","attack_ms":60,"decay_ms":240,"rest_ms":700,"cycles":2,"on_finish":"off"}}`

type lightCommandService struct {
	commandService
	mu                                         sync.Mutex
	prepares, keyed, lookups, history, cancels int32
	last                                       control.ReceiptRequest
	policy                                     string
	current                                    control.Job
	running                                    bool
}

func (s *lightCommandService) policyValue() string { s.mu.Lock(); defer s.mu.Unlock(); return s.policy }

func (s *lightCommandService) Instance() control.InstanceInfo {
	atomic.AddInt32(&s.prepares, 1)
	return control.InstanceInfo{DaemonInstanceID: commandInstance, Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}}
}
func (s *lightCommandService) Device(serial string) (control.DeviceState, error) {
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial, Capabilities: []string{"set", "program", "raw-rgb", "hold"}}, Connection: "online"}, nil
}
func (s *lightCommandService) Submit(ctx context.Context, serial string, program control.Program, policy string) (control.Job, error) {
	s.mu.Lock()
	s.policy = policy
	s.mu.Unlock()
	return s.commandService.Submit(ctx, serial, program, policy)
}
func (s *lightCommandService) SubmitWithReceipt(_ context.Context, serial string, program control.Program, policy string, receipt control.ReceiptRequest) (control.ReceiptResult, error) {
	atomic.AddInt32(&s.keyed, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last, s.policy = receipt, policy
	s.current = control.Job{ID: "job-light", Device: serial, Status: "queued", Program: program, TotalSteps: len(program.Steps) * program.Repeat, CreatedAt: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)}
	return control.ReceiptResult{Job: s.current, DaemonInstanceID: receipt.ExpectedDaemonInstanceID}, nil
}
func (s *lightCommandService) Request(serial, id, instance string) (control.Job, error) {
	atomic.AddInt32(&s.lookups, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt := s.last; id != receipt.RequestID || instance != receipt.ExpectedDaemonInstanceID {
		return control.Job{}, control.ErrRequestNotFound
	}
	job := s.current
	job.Status = "succeeded"
	job.CompletedSteps = job.TotalSteps
	if s.running {
		job.Status = "running"
		job.CompletedSteps = 0
	}
	return job, nil
}
func (s *lightCommandService) Job(string) (control.Job, error) {
	atomic.AddInt32(&s.history, 1)
	return control.Job{}, control.ErrNotFound
}
func (s *lightCommandService) Cancel(context.Context, string) (control.Job, error) {
	atomic.AddInt32(&s.cancels, 1)
	return control.Job{}, control.ErrNotFound
}

func TestControlLightDirectPreviewIsPureSharedCompilation(t *testing.T) {
	blocked := previewFile(t, "not a lock directory")
	path := previewFile(t, cliLightInput)
	text, err := executePreviewCommand(context.Background(), "--mode", "direct", "--endpoint", "not-a-valid-origin", "--lock-dir", blocked, "light", "preview", path)
	if err != nil {
		t.Fatal(err)
	}
	var result daemonclient.LightPreview
	if err = json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	expected, err := lightaction.CompileRequest([]byte(cliLightInput))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Result, expected) || !reflect.DeepEqual(result.Preview.Program, expected.Program) || result.Preview.Validation.Device != "not_requested" {
		t.Fatal(text)
	}
	data, err := os.ReadFile(blocked)
	if err != nil || string(data) != "not a lock directory" {
		t.Fatal("pure preview touched lock path", err)
	}
}

func TestControlLightGuardsBeforeFileNetworkAndOwnership(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	for _, args := range [][]string{
		{"light", "preview"}, {"light", "preview", "missing", "--policy", "reject"}, {"--mode", "direct", "light", "preview", "missing", "--serial", "demo"},
		{"light", "preview", "missing", "--request-id", "x", "--expected-daemon-instance-id", "y"},
		{"light", "run", "../bad", "missing"}, {"light", "run", "demo", "missing", "--policy", "replace"}, {"light", "run", "demo", "missing", "--policy", "queue"},
		{"light", "run", "demo", "missing", "--request-id", "x"}, {"light", "run", "demo", "missing", "--request-id=", "--expected-daemon-instance-id="},
		{"--mode", "direct", "light", "run", "demo", "missing", "--request-id", "x", "--expected-daemon-instance-id", "y"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lock := filepath.Join(t.TempDir(), "never-created")
			all := append([]string{"--endpoint", server.URL, "--lock-dir", lock}, args...)
			out, err := executePreviewCommand(context.Background(), all...)
			if err == nil || out != "" || strings.Contains(err.Error(), "no such file") {
				t.Fatal(out, err)
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Fatal("guard reached ownership")
			}
		})
	}
	for _, body := range []string{`{}`, `{"contract":"v2","action":{"kind":"off"}}`, `{"contract":"nb1-light-action/v1","action":{"kind":"off","led":-0}}`, `{"contract":"nb1-light-action/v1","action":{"kind":"pulse","color":"lime","cycles":0}}`} {
		path := previewFile(t, body)
		for _, mode := range []string{"daemon", "direct"} {
			lock := filepath.Join(t.TempDir(), "never-created")
			out, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "--mode", mode, "--lock-dir", lock, "light", "run", "demo", path)
			if err == nil || out != "" {
				t.Fatal(out, err)
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Fatal("invalid action reached ownership")
			}
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid commands made %d HTTP calls", atomic.LoadInt32(&calls))
	}
}

func TestControlLightDaemonPreviewAndOnePreparedSubmission(t *testing.T) {
	service := &lightCommandService{}
	server := httptest.NewServer(httpapi.New(service))
	defer server.Close()
	path := previewFile(t, cliLightInput)
	text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "preview", path, "--serial", "demo")
	if err != nil || !strings.Contains(text, `"supported"`) || atomic.LoadInt32(&service.prepares) != 0 || atomic.LoadInt32(&service.keyed) != 0 {
		t.Fatal(text, err)
	}
	text, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", path)
	var result lightRunOutput
	if err != nil || json.Unmarshal([]byte(text), &result) != nil || !result.Accepted || result.OutcomeUnknown || result.RequestTicket == nil || !strings.HasPrefix(result.RequestTicket.RequestID, "req_60000_") || result.DaemonInstance != commandInstance || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.keyed) != 1 || service.policyValue() != "reject" || atomic.LoadInt32(&service.submitted) != 0 {
		t.Fatal(text, err, service)
	}
	text, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", path, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err != nil || json.Unmarshal([]byte(text), &result) != nil || result.RequestTicket.RequestID != commandRequestID || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.keyed) != 2 {
		t.Fatal(text, err, atomic.LoadInt32(&service.prepares), atomic.LoadInt32(&service.keyed))
	}
	// The legacy default is still replace; this new subtree did not mutate it.
	_, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "set", "demo", "#00ff00")
	if err != nil || service.policyValue() != "replace" || atomic.LoadInt32(&service.submitted) != 1 {
		t.Fatal("legacy default changed", err, service.policyValue())
	}
}

func TestControlLightWaitUsesExactReceiptAndDoesNotCancelWhenCallerStops(t *testing.T) {
	previous := waitComplete
	waitComplete = true
	defer func() { waitComplete = previous }()
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "caller_canceled"}[running], func(t *testing.T) {
			service := &lightCommandService{running: running}
			ctx, cancel := context.WithCancel(context.Background())
			var commandFinished chan struct{}
			lookupStarted, releaseLookup := make(chan struct{}), make(chan struct{})
			var lookupOnce sync.Once
			var traceMu sync.Mutex
			var trace [][3]string
			handler := httpapi.New(service)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				traceMu.Lock()
				trace = append(trace, [3]string{r.Method, r.URL.Path, r.URL.RawQuery})
				traceMu.Unlock()
				if running && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/devices/demo/requests/") {
					// Run the real handler first, so the exact original receipt has
					// reached service.Request after the POST was accepted. Buffer the
					// response to cancel during this read, before sending any headers.
					probe := httptest.NewRecorder()
					handler.ServeHTTP(probe, r)
					if probe.Code != http.StatusOK {
						w.WriteHeader(probe.Code)
						return
					}
					lookupOnce.Do(func() { close(lookupStarted) })
					select {
					case <-r.Context().Done():
					case <-releaseLookup: // Cleanup also releases the gate on failure.
					}
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer func() {
				cancel()
				close(releaseLookup)
				if commandFinished != nil {
					select {
					case <-commandFinished:
					case <-time.After(5 * time.Second):
						t.Error("CLI goroutine did not stop after cleanup cancellation")
					}
				}
				server.Close()
			}()
			path := previewFile(t, cliLightInput)
			args := []string{"--endpoint", server.URL, "light", "run", "demo", path}
			type commandResult struct {
				text string
				err  error
			}
			done := make(chan commandResult, 1)
			commandFinished = make(chan struct{})
			go func() {
				defer close(commandFinished)
				text, err := executePreviewCommand(ctx, args...)
				done <- commandResult{text, err}
			}()
			// This is a liveness guard only. The behavioral trigger is the
			// receipt handshake, not a wall-clock budget for prepare+POST+wait.
			watchdog := time.NewTimer(5 * time.Second)
			defer watchdog.Stop()
			if running {
				select {
				case <-lookupStarted:
					cancel()
				case early := <-done:
					t.Fatalf("command stopped before receipt wait: %s %v", early.text, early.err)
				case <-watchdog.C:
					t.Fatal("command did not reach the original receipt wait")
				}
			}
			var got commandResult
			select {
			case got = <-done:
			case <-watchdog.C:
				t.Fatal("command did not finish after receipt/caller synchronization")
			}
			var result lightRunOutput
			if json.Unmarshal([]byte(got.text), &result) != nil || result.RequestTicket == nil || !result.Accepted || result.Job == nil || atomic.LoadInt32(&service.keyed) != 1 || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.lookups) == 0 || atomic.LoadInt32(&service.history) != 0 || atomic.LoadInt32(&service.cancels) != 0 || atomic.LoadInt32(&service.submitted) != 0 {
				t.Fatal(got.text, got.err)
			}
			service.mu.Lock()
			originalTicket, accepted := service.last, service.current
			service.mu.Unlock()
			if *result.RequestTicket != originalTicket || result.DaemonInstance != originalTicket.ExpectedDaemonInstanceID || result.Job.ID != accepted.ID || result.Job.Device != accepted.Device || !reflect.DeepEqual(result.Job.Program, accepted.Program) || !result.Job.CreatedAt.Equal(accepted.CreatedAt) {
				t.Fatal("wait lost the original acceptance/ticket", got.text)
			}
			traceMu.Lock()
			requests := append([][3]string(nil), trace...)
			traceMu.Unlock()
			var prepares, posts, lookups int
			for _, request := range requests {
				switch {
				case request == [3]string{http.MethodGet, "/v1/instance", ""}:
					prepares++
				case request == [3]string{http.MethodPost, "/v1/devices/demo/light", ""}:
					posts++
				case request == [3]string{http.MethodGet, "/v1/devices/demo/requests/" + originalTicket.RequestID, "expected_daemon_instance_id=" + originalTicket.ExpectedDaemonInstanceID}:
					lookups++
				default:
					t.Fatal("wait retried, changed the receipt or used history/cancel", request)
				}
			}
			if prepares != 1 || posts != 1 || lookups < 1 {
				t.Fatal("wait repeated prepare/submission or did not read its receipt", requests)
			}
			if running {
				if !errors.Is(got.err, context.Canceled) || ctx.Err() != context.Canceled || !result.OutcomeUnknown {
					t.Fatal(got.text, got.err)
				}
			} else if got.err != nil || result.OutcomeUnknown || result.Job.Status != "succeeded" {
				t.Fatal(got.text, got.err)
			}
			// Actual DeadlineExceeded behavior remains covered separately by
			// TestWaitingTimeoutDoesNotCancelDaemonJob, without requiring that a
			// short whole-command deadline first completes admission over HTTP.
		})
	}
}

func TestControlLightUnknownPostPreservesTicketWithoutRetry(t *testing.T) {
	var gets, posts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/instance" {
			atomic.AddInt32(&gets, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(control.InstanceInfo{DaemonInstanceID: commandInstance, Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/devices/demo/light" {
			t.Errorf("unexpected fallback %s %s", r.Method, r.URL)
		}
		atomic.AddInt32(&posts, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightInput))
	var result lightRunOutput
	if err == nil || json.Unmarshal([]byte(text), &result) != nil || result.Accepted || !result.OutcomeUnknown || result.RequestTicket == nil || result.RequestTicket.ExpectedDaemonInstanceID != commandInstance || atomic.LoadInt32(&gets) != 1 || atomic.LoadInt32(&posts) != 1 {
		t.Fatal(text, err, atomic.LoadInt32(&gets), atomic.LoadInt32(&posts))
	}
}

func TestControlLightOldDaemonAndWireLimitNeverUseLegacySubmission(t *testing.T) {
	for _, status := range []int{404, 501} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			if r.URL.Path != "/v1/devices/demo/light" {
				t.Error("unexpected fallback", r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"old"}}`)
		}))
		path := previewFile(t, cliLightInput)
		out, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", path, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
		if err == nil || !strings.Contains(err.Error(), "light_run_unsupported") || !strings.Contains(out, commandRequestID) || atomic.LoadInt32(&calls) != 1 {
			t.Fatal(out, err, atomic.LoadInt32(&calls))
		}
		server.Close()
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1) }))
	defer server.Close()
	_, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightInput), "--request-id", strings.Repeat("x", lightaction.MaxRequestBytes), "--expected-daemon-instance-id", commandInstance)
	if err == nil || !strings.Contains(err.Error(), "complete light request exceeds") || atomic.LoadInt32(&calls) != 0 {
		t.Fatal(err, atomic.LoadInt32(&calls))
	}
}

func TestControlLightAmbiguousErrorPreservesTicket(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "POST" || r.URL.Path != "/v1/devices/demo/light" {
			t.Errorf("unexpected fallback or Prepare: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"daemon_instance_changed","code":"invalid_request","message":"fixture"}}`)
	}))
	defer server.Close()
	out, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightInput), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	var result lightRunOutput
	if err == nil || json.Unmarshal([]byte(out), &result) != nil || result.Accepted || !result.OutcomeUnknown || result.RequestTicket == nil || result.RequestTicket.RequestID != commandRequestID || result.RequestTicket.ExpectedDaemonInstanceID != commandInstance || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("ambiguous error lost ticket, retried or was trusted: %s; err=%v calls=%d", out, err, atomic.LoadInt32(&calls))
	}
}
