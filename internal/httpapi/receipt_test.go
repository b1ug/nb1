package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
)

const receiptInstance = "daemon_0123456789abcdef0123456789abcdef"
const receiptID = "req_60000_0123456789abcdef0123456789abcdef"
const receiptBody = `{"program":{"steps":[{"color":"#102030"}]},"request_id":"` + receiptID + `","expected_daemon_instance_id":"` + receiptInstance + `"}`

type receiptService struct {
	fakeService
	mu      sync.Mutex
	calls   int
	created int
	lookups int
	failure error
	job     control.Job
	binding control.ReceiptRequest
}

func (s *receiptService) Instance() control.InstanceInfo {
	return control.InstanceInfo{DaemonInstanceID: receiptInstance, Idempotency: control.IdempotencyInfo{
		Supported: true, RequestIDPrefix: "req_60000_", AcceptUntilUptimeMS: 60000, MaxAcceptWindowMS: 60000,
		ReceiptRetentionMS: 60000, MaxEntries: 4096, MaxRetainedSteps: 65536,
	}}
}
func (s *receiptService) SubmitWithReceipt(_ context.Context, serial string, p control.Program, policy string, binding control.ReceiptRequest) (control.ReceiptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.binding = binding
	if s.failure != nil {
		return control.ReceiptResult{}, s.failure
	}
	replayed := s.created != 0
	if !replayed {
		s.created++
		s.job = control.Job{ID: "job-receipt", Device: serial, Status: "succeeded", Program: p}
	}
	return control.ReceiptResult{Job: s.job, Replayed: replayed, DaemonInstanceID: receiptInstance}, nil
}
func (s *receiptService) Request(serial, requestID, instance string) (control.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++
	if s.failure != nil {
		return control.Job{}, s.failure
	}
	if serial != "20005601" || requestID != receiptID || instance != receiptInstance {
		return control.Job{}, control.ErrRequestNotFound
	}
	return s.job, nil
}

func receiptHTTP(service control.Service, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	httpapi.New(service).ServeHTTP(w, r)
	return w
}

func TestOptionalReceiptRoutesAndLegacySubmit(t *testing.T) {
	s := &receiptService{}
	w := receiptHTTP(s, "GET", "/v1/instance", "")
	var info control.InstanceInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || w.Code != 200 || !info.Idempotency.Supported || w.Header().Get("X-NB1-Daemon-Instance-ID") != receiptInstance {
		t.Fatalf("instance: %d %s, %v", w.Code, w.Body, err)
	}
	for _, replayed := range []string{"false", "true"} {
		w = receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", receiptBody)
		if w.Code != 202 || w.Header().Get("X-NB1-Replayed") != replayed || w.Header().Get("X-NB1-Daemon-Instance-ID") != receiptInstance || !strings.Contains(w.Body.String(), `"status":"succeeded"`) {
			t.Fatalf("accepted replay=%s: %d %s %v", replayed, w.Code, w.Body, w.Header())
		}
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil || location.Path != "/v1/devices/20005601/requests/"+receiptID || location.Query().Get("expected_daemon_instance_id") != receiptInstance {
			t.Fatalf("receipt Location: %s %v", w.Header().Get("Location"), err)
		}
		lookup := receiptHTTP(s, "GET", location.String(), "")
		if lookup.Code != 200 || lookup.Header().Get("X-NB1-Daemon-Instance-ID") != receiptInstance || lookup.Body.String() != w.Body.String() {
			t.Fatalf("lookup: %d %s", lookup.Code, lookup.Body)
		}
	}
	if s.binding.RequestID != receiptID || s.binding.ExpectedDaemonInstanceID != receiptInstance || s.created != 1 {
		t.Fatalf("binding lost: %+v, created=%d", s.binding, s.created)
	}
	w = receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", `{"program":{"steps":[{"color":"#102030"}]}}`)
	if w.Code != 202 || s.submitCount != 1 || s.calls != 2 || w.Header().Get("Location") != "/v1/jobs/job-1" || w.Header().Get("X-NB1-Replayed") != "" {
		t.Fatalf("legacy Submit changed: %d %s", w.Code, w.Body)
	}
}

func TestReceiptPairStrictBoundary(t *testing.T) {
	for _, path := range []string{"/v1/instance", "/v1/devices/20005601/requests/" + receiptID + "?expected_daemon_instance_id=" + receiptInstance} {
		s := &receiptService{}
		w := receiptHTTP(s, "POST", path, "")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" || s.calls != 0 || s.lookups != 0 {
			t.Fatalf("read-only receipt route accepted POST: %d %s", w.Code, w.Body)
		}
	}
	for _, body := range []string{
		`{"program":{},"request_id":"x"}`,
		`{"program":{},"expected_daemon_instance_id":"x"}`,
		`{"program":{},"request_id":"","expected_daemon_instance_id":""}`,
		`{"program":{},"request_id":"x","expected_daemon_instance_id":""}`,
		`{"program":{},"request_id":null,"expected_daemon_instance_id":"x"}`,
		`{"program":{},"request_id":"x","expected_daemon_instance_id":null}`,
		`{"program":{},"Request_id":"x","expected_daemon_instance_id":"y"}`,
		`{"program":{},"request_id":"x","request_\u0069d":"x","expected_daemon_instance_id":"y"}`,
		`{"program":{},"request_id":[],"expected_daemon_instance_id":"y"}`,
	} {
		s := &receiptService{}
		w := receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", body)
		if w.Code != 400 || s.calls != 0 || s.submitCount != 0 {
			t.Fatalf("invalid pair reached Submit: %d %s body=%s", w.Code, w.Body, body)
		}
	}
	for _, query := range []string{"", "?expected_daemon_instance_id=", "?expected_daemon_instance_id=x&expected_daemon_instance_id=x", "?Expected_daemon_instance_id=x", "?expected_daemon_instance_id=x&other=y", "?expected_daemon_instance_id=%zz"} {
		s := &receiptService{}
		w := receiptHTTP(s, "GET", "/v1/devices/20005601/requests/"+receiptID+query, "")
		if w.Code != 400 || s.lookups != 0 {
			t.Fatalf("invalid lookup reached service: %s status=%d calls=%d", query, w.Code, s.lookups)
		}
	}
}

func TestReceiptErrorsAndUnsupportedServiceNeverDowngrade(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{control.ErrRequestConflict, 409, "idempotency_conflict"},
		{control.ErrInstanceChanged, 409, "daemon_instance_changed"},
		{control.ErrRequestExpired, 410, "request_expired"},
		{control.ErrReceiptCapacity, 429, "idempotency_capacity"},
		{control.ErrRequestNotFound, 404, "request_not_found"},
	} {
		for _, method := range []string{"POST", "GET"} {
			s := &receiptService{failure: tc.err}
			path, body := "/v1/devices/20005601/jobs", receiptBody
			if method == "GET" {
				path, body = "/v1/devices/20005601/requests/"+receiptID+"?expected_daemon_instance_id="+receiptInstance, ""
			}
			w := receiptHTTP(s, method, path, body)
			var failure httpapi.ErrorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &failure)
			if w.Code != tc.status || failure.Error.Code != tc.code || s.submitCount != 0 {
				t.Fatalf("%s %v: %d %s", method, tc.err, w.Code, w.Body)
			}
		}
	}
	s := &fakeService{}
	for _, path := range []string{"/v1/instance", "/v1/devices/20005601/requests/" + receiptID + "?expected_daemon_instance_id=" + receiptInstance} {
		if w := receiptHTTP(s, "GET", path, ""); w.Code != 501 {
			t.Fatalf("unsupported GET: %d %s", w.Code, w.Body)
		}
	}
	if w := receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", receiptBody); w.Code != 501 || s.submitCount != 0 {
		t.Fatalf("unsupported submit downgraded: %d calls=%d", w.Code, s.submitCount)
	}
}

func TestLostReceiptSubmissionResponseCanBeQueriedWithoutAutomaticRetry(t *testing.T) {
	s := &receiptService{}
	handler := httpapi.New(s)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			lost := false
			once.Do(func() { lost = true })
			if lost {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r) // Accepted before the response is lost.
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	binding := control.ReceiptRequest{RequestID: receiptID, ExpectedDaemonInstanceID: receiptInstance}
	program := control.Program{Steps: []control.Step{{Color: "#102030"}}}
	_, err = client.SubmitWithReceipt(context.Background(), "20005601", program, "replace", binding)
	var unknown *daemonclient.ReceiptError
	if !errors.As(err, &unknown) || !unknown.OutcomeUnknown || unknown.Receipt != binding {
		t.Fatalf("lost response did not preserve unknown binding: %v", err)
	}
	s.mu.Lock()
	calls := s.calls
	s.mu.Unlock()
	if calls != 1 {
		t.Fatalf("client automatically retried POST %d times", calls)
	}
	job, err := client.Request(context.Background(), "20005601", binding)
	if err != nil || job.ID != "job-receipt" {
		t.Fatalf("lookup after response loss: %+v %v", job, err)
	}
	result, err := client.SubmitWithReceipt(context.Background(), "20005601", program, "replace", binding)
	if err != nil || !result.Replayed || result.Job.ID != job.ID {
		t.Fatalf("explicit same-binding replay: %+v %v", result, err)
	}
	s.mu.Lock()
	created := s.created
	s.mu.Unlock()
	if created != 1 {
		t.Fatalf("fake service created %d jobs", created)
	}
}
