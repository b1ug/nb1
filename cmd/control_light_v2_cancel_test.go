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
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

type lightV2CancelService struct {
	lightV2CommandService
	mu                  sync.Mutex
	current             control.FrameJob
	finishAfterLookup   int32
	finishStatus        string
	skippedPosts        int32
	advanceDuringCancel bool
}

func newLightV2CancelService(t *testing.T, status string) *lightV2CancelService {
	t.Helper()
	queued, running := lightV2ProgressFixture(t)
	service := &lightV2CancelService{current: running}
	if status == "queued" {
		service.current = queued
	} else {
		service.current.Status = status
	}
	if status == "succeeded" || status == "failed" || status == "cancelled" {
		service.finishLocked(status)
	}
	return service
}

// The conditional HTTP handler reads Instance internally without performing
// the network /v1/instance prepare operation. Trace assertions still reject a
// client that tries to prepare a new ticket during cancellation.
func (*lightV2CancelService) Instance() control.InstanceInfo {
	return control.InstanceInfo{DaemonInstanceID: commandInstance, Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}}
}

func (s *lightV2CancelService) finishLocked(status string) {
	s.current.Status = status
	end := s.current.CreatedAt.Add(3 * time.Millisecond)
	s.current.FinishedAt = &end
	if status == "succeeded" {
		s.current.CompletedFrames = s.current.TotalFrames
		s.current.CompletedTargetWrites = s.current.PlannedTargetWrites
	}
	if status == "failed" {
		s.current.Error = "original execution failed"
	}
}

func (s *lightV2CancelService) FrameRequest(serial, requestID, instance string) (control.FrameJob, error) {
	n := atomic.AddInt32(&s.frameLookups, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if serial != "demo" || requestID != commandRequestID || instance != commandInstance {
		return control.FrameJob{}, control.ErrRequestNotFound
	}
	if s.finishAfterLookup > 0 && n >= s.finishAfterLookup && (s.current.Status == "cancelling" || s.finishStatus == "succeeded") {
		status := s.finishStatus
		if status == "" {
			status = "cancelled"
		}
		s.finishLocked(status)
	}
	return s.current, nil
}

func (s *lightV2CancelService) CancelFrame(_ context.Context, id string) (control.FrameJob, error) {
	atomic.AddInt32(&s.frameCancels, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.current.ID {
		return control.FrameJob{}, control.ErrNotFound
	}
	if s.current.Status == "queued" {
		s.finishLocked("cancelled")
	} else if s.current.Status == "running" {
		s.current.Status = "cancelling"
		if s.advanceDuringCancel {
			s.current.CompletedTargetWrites++
		}
	}
	return s.current, nil
}

func (s *lightV2CancelService) snapshot() control.FrameJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

type lightV2CancelOutputView struct {
	Contract        string                  `json:"contract"`
	Mode            string                  `json:"mode"`
	RequestTicket   *control.ReceiptRequest `json:"request_ticket"`
	CancelAttempted bool                    `json:"cancel_attempted"`
	AlreadyTerminal bool                    `json:"already_terminal"`
	CancelConfirmed bool                    `json:"cancel_confirmed"`
	CancelError     string                  `json:"cancel_error"`
	OutcomeUnknown  bool                    `json:"outcome_unknown"`
	DaemonInstance  string                  `json:"daemon_instance_id"`
	Job             *control.FrameJob       `json:"job"`
	Error           string                  `json:"error"`
}

// Use the real HTTP handler for conditional cancellation. Most faults modify
// only a fake response after fake service I/O; the natural-completion case
// explicitly drops the POST before the service can observe it.
func lightV2CancelServer(t *testing.T, service *lightV2CancelService, fault string) (*httptest.Server, *lightV2Trace) {
	t.Helper()
	trace := &lightV2Trace{}
	application := httpapi.New(service)
	var reads int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		trace.mu.Lock()
		trace.requests = append(trace.requests, lightV2RequestTrace{r.Method, r.URL.Path, r.URL.RawQuery, body})
		trace.mu.Unlock()
		read := int32(0)
		if r.Method == http.MethodGet {
			read = atomic.AddInt32(&reads, 1)
		}
		response := httptest.NewRecorder()
		if r.Method == http.MethodPost && fault == "post-5xx-natural" {
			atomic.AddInt32(&service.skippedPosts, 1)
			response.Header().Set("Content-Type", "application/json")
			response.Code = http.StatusServiceUnavailable
			_, _ = response.Body.WriteString(`{"error":{"code":"unavailable","message":"POST not delivered"}}`)
		} else {
			application.ServeHTTP(response, r)
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel") && fault != "post-5xx-natural" {
			values := response.Header().Values("X-NB1-Daemon-Instance-ID")
			if len(values) != 1 || values[0] != commandInstance {
				t.Errorf("conditional cancellation did not attach one matching instance header: %v", values)
			}
		}
		if fault == "post-disconnect" && r.Method == http.MethodPost {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		if fault == "pre-missing" && read == 1 || fault == "confirm-missing" && read == 2 {
			response.Code = http.StatusNotFound
			response.Body.Reset()
			_, _ = response.Body.WriteString(`{"error":{"code":"request_not_found","message":"unknown original outcome"}}`)
		}
		if fault == "pre-instance" && read == 1 || fault == "confirm-instance" && read == 2 {
			response.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_ffffffffffffffffffffffffffffffff")
		}
		mutate := r.Method == http.MethodPost && (fault == "post-created" || fault == "post-program" || fault == "post-running") || read == 1 && fault == "pre-program" || read == 2 && (fault == "confirm-created" || fault == "confirm-running" || fault == "confirm-progress" || fault == "confirm-program" || fault == "confirm-id" || fault == "confirm-time") || read == 3 && fault == "confirm-503-regress"
		if mutate {
			var job control.FrameJob
			if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
				t.Error(err)
				return
			}
			switch fault {
			case "post-created", "confirm-created":
				job.CreatedAt = job.CreatedAt.Add(-time.Second)
			case "post-program", "pre-program", "confirm-program":
				// Deep-copy before touching a nested target, keeping the fake's
				// own immutable program intact for subsequent confirmation.
				job.Program, _ = control.NormalizeFrameProgram(job.Program)
				job.Program.Frames[0].Targets[0].Color = "#ffffff"
			case "post-running", "confirm-running":
				job.Status = "running"
			case "confirm-progress":
				job.CompletedTargetWrites--
			case "confirm-id":
				job.ID = "framejob_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			case "confirm-time":
				started := job.StartedAt.Add(time.Nanosecond)
				job.StartedAt = &started
			case "confirm-503-regress":
				job.CompletedFrames, job.CompletedTargetWrites = 0, 2
			}
			response.Body.Reset()
			_ = json.NewEncoder(response.Body).Encode(job)
		}
		if fault == "post-malformed" && r.Method == http.MethodPost {
			response.Body.Reset()
			_, _ = response.Body.WriteString(`{"id":`)
		}
		if fault == "post-5xx" && r.Method == http.MethodPost {
			response.Code = http.StatusServiceUnavailable
			response.Body.Reset()
			_, _ = response.Body.WriteString(`{"error":{"code":"unavailable","message":"cancel result unknown"}}`)
		}
		if read == 2 && strings.HasPrefix(fault, "confirm-503-") {
			response.Code = http.StatusServiceUnavailable
			response.Body.Reset()
			_, _ = response.Body.WriteString(`{"error":{"code":"unavailable","message":"receipt temporarily unavailable"}}`)
		}
		if r.Method == http.MethodPost {
			switch fault {
			case "post-header-missing":
				response.Header().Del("X-NB1-Daemon-Instance-ID")
			case "post-header-wrong":
				response.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_ffffffffffffffffffffffffffffffff")
			case "post-header-duplicate":
				response.Header().Add("X-NB1-Daemon-Instance-ID", commandInstance)
			}
		}
		for key, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	return server, trace
}

func lightV2CancelArgs(endpoint, path string) []string {
	return []string{"--endpoint", endpoint, "light", "cancel", "demo", path,
		"--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance}
}

func assertLightV2CancelTicket(t *testing.T, output lightV2CancelOutputView) {
	t.Helper()
	if output.Contract != lightactionv2.Contract || output.Mode != "daemon" || output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance {
		t.Fatalf("cancel lost original contract/ticket: %#v", output)
	}
}

func assertLightV2CancelRoutes(t *testing.T, service *lightV2CancelService, trace *lightV2Trace, wantGets, wantPosts int) {
	t.Helper()
	gets, posts := 0, 0
	for index, request := range trace.snapshot() {
		switch request.method {
		case http.MethodGet:
			gets++
			if request.path != "/v1/devices/demo/frame-requests/"+commandRequestID || request.query != "expected_daemon_instance_id="+commandInstance || len(request.body) != 0 {
				t.Fatalf("cancel queried a different receipt or history: %#v", request)
			}
		case http.MethodPost:
			posts++
			var body map[string]string
			bodyErr := json.Unmarshal(request.body, &body)
			if index != 1 || request.path != "/v1/frame-jobs/"+cliLightV2JobID+"/cancel" || bodyErr != nil || len(body) != 1 || body["expected_daemon_instance_id"] != commandInstance || request.query != "" {
				t.Fatalf("cancel posted a new action or different cancellation: %#v", request)
			}
		default:
			t.Fatalf("unexpected method: %#v", request)
		}
	}
	if gets != wantGets || posts != wantPosts || atomic.LoadInt32(&service.frameCancels)+atomic.LoadInt32(&service.skippedPosts) != int32(wantPosts) || atomic.LoadInt32(&service.prepares) != 0 || atomic.LoadInt32(&service.frameSubmitted) != 0 || atomic.LoadInt32(&service.submitted) != 0 || atomic.LoadInt32(&service.keyed) != 0 || atomic.LoadInt32(&service.frameHistory) != 0 || atomic.LoadInt32(&service.history) != 0 || atomic.LoadInt32(&service.cancels) != 0 {
		t.Fatalf("cancel made extra calls or submitted work: gets=%d posts=%d, want %d/%d", gets, posts, wantGets, wantPosts)
	}
}

func TestControlLightV2CancelConfirmsOriginalReceiptAfterOnePost(t *testing.T) {
	for _, withID := range []bool{false, true} {
		t.Run(map[bool]string{false: "resolve-id", true: "known-id"}[withID], func(t *testing.T) {
			service := newLightV2CancelService(t, "running")
			server, trace := lightV2CancelServer(t, service, "")
			defer server.Close()
			args := lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input))
			if withID {
				args = append(args, "--job-id", cliLightV2JobID)
			}
			text, err := executePreviewCommand(context.Background(), args...)
			var output lightV2CancelOutputView
			if err != nil || json.Unmarshal([]byte(text), &output) != nil || !output.CancelAttempted || output.AlreadyTerminal || output.CancelConfirmed || output.CancelError != "" || output.OutcomeUnknown || output.Job == nil || output.Job.Status != "cancelling" || output.DaemonInstance != commandInstance {
				t.Fatal(text, err)
			}
			assertLightV2CancelTicket(t, output)
			assertLightV2CancelRoutes(t, service, trace, 2, 1)
		})
	}
}

func TestControlLightV2CancelTerminalAndAlreadyCancellingDoNotPost(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled", "cancelling"} {
		t.Run(status, func(t *testing.T) {
			service := newLightV2CancelService(t, status)
			original := service.snapshot()
			server, trace := lightV2CancelServer(t, service, "")
			defer server.Close()
			text, err := executePreviewCommand(context.Background(), lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input))...)
			var output lightV2CancelOutputView
			if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.CancelAttempted || output.OutcomeUnknown || output.CancelConfirmed != (status == "cancelled") || output.AlreadyTerminal != (status != "cancelling") || output.Job == nil || !reflect.DeepEqual(*output.Job, original) {
				t.Fatal(text, err)
			}
			assertLightV2CancelTicket(t, output)
			assertLightV2CancelRoutes(t, service, trace, 1, 0)
		})
	}
}

func TestControlLightV2CancelQueuedTaskCanFinishWithoutStartedAt(t *testing.T) {
	service := newLightV2CancelService(t, "queued")
	server, trace := lightV2CancelServer(t, service, "")
	defer server.Close()
	text, err := executePreviewCommand(context.Background(), lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input))...)
	var output lightV2CancelOutputView
	if err != nil || json.Unmarshal([]byte(text), &output) != nil || !output.CancelAttempted || !output.CancelConfirmed || output.Job == nil || output.Job.Status != "cancelled" || output.Job.StartedAt != nil || output.Job.CompletedTargetWrites != 0 || output.OutcomeUnknown {
		t.Fatal(text, err)
	}
	assertLightV2CancelRoutes(t, service, trace, 2, 1)
}

func TestControlLightV2CancelUncertaintyKeepsLastInstanceBoundJob(t *testing.T) {
	cases := []struct {
		fault      string
		wantGets   int
		wantPosts  int
		trustedJob bool
	}{
		{"pre-missing", 1, 0, false}, {"pre-instance", 1, 0, false}, {"pre-program", 1, 0, false},
		{"post-disconnect", 1, 1, true}, {"post-5xx", 1, 1, true}, {"post-malformed", 1, 1, true}, {"post-created", 1, 1, true}, {"post-program", 1, 1, true}, {"post-running", 1, 1, true},
		{"post-header-missing", 1, 1, true}, {"post-header-wrong", 1, 1, true}, {"post-header-duplicate", 1, 1, true},
		{"confirm-missing", 2, 1, true}, {"confirm-instance", 2, 1, true}, {"confirm-created", 2, 1, true}, {"confirm-running", 2, 1, true}, {"confirm-progress", 2, 1, true},
		{"confirm-program", 2, 1, true}, {"confirm-id", 2, 1, true}, {"confirm-time", 2, 1, true},
	}
	for _, test := range cases {
		t.Run(test.fault, func(t *testing.T) {
			service := newLightV2CancelService(t, "running")
			service.advanceDuringCancel = test.fault == "confirm-progress"
			original := service.snapshot()
			server, trace := lightV2CancelServer(t, service, test.fault)
			defer server.Close()
			args := lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input))
			// A plausible later GET must not hide integrity failures, even
			// when the operator explicitly requested waiting.
			if test.wantPosts != 0 && test.fault != "post-disconnect" && test.fault != "post-5xx" {
				args = append(args, "--wait", "--timeout", "2s")
			}
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2CancelOutputView
			if err == nil || json.Unmarshal([]byte(text), &output) != nil || !output.OutcomeUnknown || output.CancelConfirmed || output.CancelAttempted != (test.wantPosts != 0) || test.wantPosts != 0 && output.CancelError == "" {
				t.Fatal(text, err)
			}
			if test.trustedJob {
				if output.Job == nil || !reflect.DeepEqual(*output.Job, original) {
					t.Fatalf("unbound cancellation response replaced original GET snapshot: %s", text)
				}
			} else if output.Job != nil {
				t.Fatalf("unverified initial lookup exposed a trusted Job: %s", text)
			}
			assertLightV2CancelTicket(t, output)
			assertLightV2CancelRoutes(t, service, trace, test.wantGets, test.wantPosts)
		})
	}
}

func TestControlLightV2CancelWaitRecoversTransportFailureUsingOnlyReads(t *testing.T) {
	for _, fault := range []string{"post-disconnect", "post-5xx", "post-5xx-natural"} {
		t.Run(fault, func(t *testing.T) {
			service := newLightV2CancelService(t, "running")
			service.finishAfterLookup = 3
			if fault == "post-5xx-natural" {
				service.finishStatus = "succeeded"
			}
			server, trace := lightV2CancelServer(t, service, fault)
			defer server.Close()
			args := append(lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input)), "--wait", "--timeout", "2s")
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2CancelOutputView
			if json.Unmarshal([]byte(text), &output) != nil || output.Job == nil || !output.CancelAttempted || output.CancelError == "" {
				t.Fatal("recovery lost the original cancellation failure", text, err)
			}
			if fault == "post-5xx" && !strings.Contains(output.CancelError, "cancel result unknown") || fault == "post-5xx-natural" && !strings.Contains(output.CancelError, "POST not delivered") {
				t.Fatal("recovery replaced the first cancellation error", text)
			}
			if fault == "post-5xx-natural" {
				if err == nil || output.CancelConfirmed || output.Job.Status != "succeeded" || atomic.LoadInt32(&service.frameCancels) != 0 {
					t.Fatal("natural completion was reported as confirmed cancellation", text, err)
				}
			} else if err != nil || output.OutcomeUnknown || !output.CancelConfirmed || output.Job.Status != "cancelled" {
				t.Fatal("original receipt did not settle the cancellation", text, err)
			}
			assertLightV2CancelTicket(t, output)
			assertLightV2CancelRoutes(t, service, trace, 3, 1)
		})
	}
}

func TestControlLightV2CancelRecoveryRetainsVerifiedPostAnchor(t *testing.T) {
	for _, ending := range []string{"cancelled", "succeeded", "regress"} {
		t.Run(ending, func(t *testing.T) {
			service := newLightV2CancelService(t, "running")
			service.advanceDuringCancel = true
			original := service.snapshot()
			if ending != "regress" {
				service.finishAfterLookup, service.finishStatus = 3, ending
			}
			server, trace := lightV2CancelServer(t, service, "confirm-503-"+ending)
			defer server.Close()
			args := append(lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input)), "--wait", "--timeout", "2s")
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2CancelOutputView
			if json.Unmarshal([]byte(text), &output) != nil || !output.CancelAttempted || !strings.Contains(output.CancelError, "receipt temporarily unavailable") || output.Job == nil {
				t.Fatal("recovery discarded the confirmation error or original job", text, err)
			}
			if ending == "cancelled" {
				if err != nil || output.OutcomeUnknown || !output.CancelConfirmed || output.Job.Status != "cancelled" || output.Job.CompletedTargetWrites != 5 {
					t.Fatal("valid anchored cancellation could not recover", text, err)
				}
			} else if err == nil || !output.OutcomeUnknown || output.CancelConfirmed || !reflect.DeepEqual(*output.Job, original) {
				t.Fatal("later GET erased verified POST progress/lifecycle", text, err)
			}
			assertLightV2CancelTicket(t, output)
			assertLightV2CancelRoutes(t, service, trace, 3, 1)
		})
	}
}

func TestControlLightV2CancelWaitOnlyPollsOriginalReceipt(t *testing.T) {
	for _, alreadyCancelling := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-cancel", true: "already-cancelling"}[alreadyCancelling], func(t *testing.T) {
			status := "running"
			if alreadyCancelling {
				status = "cancelling"
			}
			service := newLightV2CancelService(t, status)
			service.finishAfterLookup = 3
			server, trace := lightV2CancelServer(t, service, "")
			defer server.Close()
			args := append(lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input)), "--wait", "--timeout", "2s")
			text, err := executeLightV2WaitCommand(context.Background(), args...)
			var output lightV2CancelOutputView
			if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.OutcomeUnknown || !output.CancelConfirmed || output.Job == nil || output.Job.Status != "cancelled" || output.CancelAttempted == alreadyCancelling {
				t.Fatal(text, err)
			}
			wantPosts := 1
			if alreadyCancelling {
				wantPosts = 0
			}
			assertLightV2CancelRoutes(t, service, trace, 3, wantPosts)
		})
	}
}

func TestControlLightV2CancelWaitTimeoutDoesNotRepeatPost(t *testing.T) {
	service := newLightV2CancelService(t, "running")
	server, trace := lightV2CancelServer(t, service, "")
	defer server.Close()
	args := append(lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input)), "--wait", "--timeout", "100ms")
	text, err := executeLightV2WaitCommand(context.Background(), args...)
	var output lightV2CancelOutputView
	if err == nil || json.Unmarshal([]byte(text), &output) != nil || !output.OutcomeUnknown || !output.CancelAttempted || output.Job == nil || output.Job.Status != "cancelling" {
		t.Fatal(text, err)
	}
	requests := trace.snapshot()
	posts, gets := 0, 0
	for _, request := range requests {
		if request.method == http.MethodPost {
			posts++
		} else if request.method == http.MethodGet {
			gets++
		}
	}
	if posts != 1 || gets < 2 {
		t.Fatalf("timeout repeated POST or skipped original confirmation: %#v", requests)
	}
	assertLightV2CancelTicket(t, output)
	assertLightV2CancelRoutes(t, service, trace, gets, 1)
}

func TestControlLightV2CancelRejectsKnownJobMismatchBeforePost(t *testing.T) {
	service := newLightV2CancelService(t, "running")
	server, trace := lightV2CancelServer(t, service, "")
	defer server.Close()
	args := append(lightV2CancelArgs(server.URL, previewFile(t, cliLightV2Input)), "--job-id", "framejob_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	text, err := executePreviewCommand(context.Background(), args...)
	var output lightV2CancelOutputView
	if err == nil || json.Unmarshal([]byte(text), &output) != nil || output.CancelAttempted || !output.OutcomeUnknown || output.Job != nil {
		t.Fatal(text, err)
	}
	assertLightV2CancelRoutes(t, service, trace, 1, 0)
}

func TestControlLightV2CancelStrictGuardsPrecedeFileAndNetwork(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&requests, 1) }))
	defer server.Close()
	for _, args := range [][]string{
		{"light", "cancel", "demo", "missing"},
		{"light", "cancel", "demo", "missing", "--request-id", commandRequestID},
		{"light", "cancel", "demo", "missing", "--expected-daemon-instance-id", commandInstance},
		{"light", "cancel", "demo", "missing", "--request-id=", "--expected-daemon-instance-id="},
		{"--mode", "direct", "light", "cancel", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"light", "cancel", "../bad", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"light", "cancel", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--policy", "reject"},
		{"light", "cancel", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--job-id="},
		{"light", "cancel", "demo", "missing", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--job-id", "../bad"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lock := filepath.Join(t.TempDir(), "must-not-create")
			all := append([]string{"--endpoint", server.URL, "--lock-dir", lock}, args...)
			out, err := executePreviewCommand(context.Background(), all...)
			if err == nil || out != "" || strings.Contains(err.Error(), "no such file") {
				t.Fatal("cancel guard accessed input file", out, err)
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Fatal("cancel guard touched ownership", err)
			}
		})
	}
	out, err := executePreviewCommand(context.Background(), lightV2CancelArgs(server.URL, previewFile(t, cliLightInput))...)
	if err == nil || out != "" || atomic.LoadInt32(&requests) != 0 {
		t.Fatal("cancel accepted v1 input or made a request", out, err)
	}
}

var _ control.FrameService = (*lightV2CancelService)(nil)
