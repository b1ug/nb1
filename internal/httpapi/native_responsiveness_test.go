package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

// This backend is entirely in memory. The gate ignores context cancellation
// until the test explicitly releases it, modelling synchronous native I/O.
type responsiveFakeBackend struct {
	mu                               sync.Mutex
	serial                           string
	entered, release                 chan struct{}
	once                             sync.Once
	inspections, reads, closes       int
	active, unsafeClose, ctxDeadline bool
}

func (b *responsiveFakeBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: b.serial, Firmware: 204, Capabilities: []string{"set", "program", "raw-rgb", "hold"}}
}
func (b *responsiveFakeBackend) Set(control.Step) error { return nil }
func (b *responsiveFakeBackend) Stop(string) error      { return nil }
func (b *responsiveFakeBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	b.reads++
	b.mu.Unlock()
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *responsiveFakeBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unsafeClose = b.unsafeClose || b.active
	b.closes++
}
func (b *responsiveFakeBackend) InspectNative(ctx context.Context, _ nativeinspection.Request) (nativeinspection.Result, error) {
	b.mu.Lock()
	b.inspections++
	b.active = true
	_, b.ctxDeadline = ctx.Deadline()
	b.mu.Unlock()
	b.once.Do(func() { close(b.entered) })
	<-b.release
	b.mu.Lock()
	b.active = false
	b.mu.Unlock()
	return nativeinspection.Result{}, context.Canceled
}

type responsiveFakeDriver struct {
	mu      sync.Mutex
	devices map[string]*responsiveFakeBackend
	opens   map[string]int
}

func (d *responsiveFakeDriver) Open(serial string) (control.Backend, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opens[serial]++
	if d.opens[serial] != 1 || d.devices[serial] == nil {
		return nil, control.ErrOffline
	}
	return d.devices[serial], nil
}
func (d *responsiveFakeDriver) Metrics() []control.TransportMetric {
	d.mu.Lock()
	defer d.mu.Unlock()
	var rows []control.TransportMetric
	for serial, b := range d.devices {
		b.mu.Lock()
		rows = append(rows, control.TransportMetric{Serial: serial, Operation: "fake_native_inspection", Calls: uint64(b.inspections), Attempts: uint64(b.inspections)})
		b.mu.Unlock()
	}
	return rows
}

type responsiveFixture struct {
	engine *control.Engine
	driver *responsiveFakeDriver
	server *httptest.Server
	client *http.Client
	a      *responsiveFakeBackend
	once   sync.Once
}

func newResponsiveFixture(t *testing.T) *responsiveFixture {
	t.Helper()
	f := &responsiveFixture{}
	f.driver = &responsiveFakeDriver{devices: map[string]*responsiveFakeBackend{}, opens: map[string]int{}}
	for _, serial := range []string{"20000001", "20000002"} {
		f.driver.devices[serial] = &responsiveFakeBackend{serial: serial, entered: make(chan struct{}), release: make(chan struct{})}
	}
	f.a = f.driver.devices["20000001"]
	var err error
	f.engine, err = control.New(f.driver, control.Options{Serials: []string{"20000001", "20000002"}, PollInterval: time.Hour, ShutdownMode: "hold"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(New(f.engine))
	f.client = f.server.Client()
	if strings.HasSuffix(f.server.URL, ":8931") {
		t.Fatal("test listener must never use the workspace daemon port")
	}
	t.Cleanup(func() {
		f.release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := f.engine.Close(ctx); err != nil {
			t.Error(err)
		}
		f.server.Close()
	})
	deadline := time.Now().Add(time.Second)
	for {
		a, _ := f.engine.Device("20000001")
		b, _ := f.engine.Device("20000002")
		if a.Connection == "online" && b.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("in-memory workers did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	return f
}
func (f *responsiveFixture) release() { f.once.Do(func() { close(f.a.release) }) }

type responsiveHTTPResult struct {
	status  int
	body    []byte
	err     error
	elapsed time.Duration
}

func (f *responsiveFixture) inspect(ctx context.Context) <-chan responsiveHTTPResult {
	out := make(chan responsiveHTTPResult, 1)
	go func() {
		started := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/devices/20000001/native-sequence/playback", nil)
		response, err := f.client.Do(req)
		value := responsiveHTTPResult{err: err}
		if response != nil {
			value.status = response.StatusCode
			value.body, value.err = io.ReadAll(io.LimitReader(response.Body, 65537))
			response.Body.Close()
		}
		value.elapsed = time.Since(started)
		out <- value
	}()
	return out
}
func responsiveEntered(t *testing.T, f *responsiveFixture) {
	t.Helper()
	select {
	case <-f.a.entered:
	case <-time.After(time.Second):
		t.Fatal("fake native call was not entered")
	}
}
func (f *responsiveFixture) request(t *testing.T, method, path string, body []byte, want int) ([]byte, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	r, err := f.client.Do(req)
	if err != nil {
		t.Fatal(path, err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || r.StatusCode != want {
		t.Fatalf("%s status=%d expected=%d error=%v body=%s", path, r.StatusCode, want, err, data)
	}
	return data, time.Since(started)
}
func (f *responsiveFixture) assertOwnerHeld(t *testing.T) {
	t.Helper()
	f.a.mu.Lock()
	active, inspections, closes, unsafe := f.a.active, f.a.inspections, f.a.closes, f.a.unsafeClose
	f.a.mu.Unlock()
	f.driver.mu.Lock()
	opens := f.driver.opens["20000001"]
	f.driver.mu.Unlock()
	if !active || inspections != 1 || closes != 0 || unsafe || opens != 1 {
		t.Fatalf("native ownership changed: active=%t inspections=%d closes=%d unsafe=%t opens=%d", active, inspections, closes, unsafe, opens)
	}
	if _, err := f.engine.Submit(context.Background(), "20000001", control.Program{Steps: []control.Step{{Color: "#ffffff"}}, OnFinish: "hold"}, "replace"); !errors.Is(err, control.ErrBusy) {
		t.Fatal("same-device admission did not remain busy", err)
	}
}

func TestNativeInspectionServerWaitBudgetKeepsOwnerAndOtherDevicesResponsive(t *testing.T) {
	f := newResponsiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background()) // Deliberately no caller deadline.
	defer cancel()
	if _, ok := ctx.Deadline(); ok || f.client.Timeout != 0 {
		t.Fatal("probe must use an HTTP request without a caller/client timeout")
	}
	result := f.inspect(ctx)
	responsiveEntered(t, f)
	latencies := map[string][]int64{}
	for index := 0; index < 5; index++ {
		for _, path := range []string{"/healthz", "/v1/devices", "/v1/devices/20000001", "/v1/transport-metrics"} {
			_, elapsed := f.request(t, http.MethodGet, path, nil, http.StatusOK)
			latencies[path] = append(latencies[path], elapsed.Microseconds())
		}
	}
	program := control.Program{Steps: []control.Step{{LED: 0, Color: "#00ff00"}}, Repeat: 1, OnFinish: "hold"}
	request, _ := json.Marshal(SubmitRequest{Program: &program, Policy: "reject"})
	data, _ := f.request(t, http.MethodPost, "/v1/devices/20000002/jobs", request, http.StatusAccepted)
	var job control.Job
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	jobStarted := time.Now()
	for job.Status != "succeeded" {
		data, _ = f.request(t, http.MethodGet, "/v1/jobs/"+job.ID, nil, http.StatusOK)
		if err := json.Unmarshal(data, &job); err != nil || job.Status == "failed" || time.Since(jobStarted) > time.Second {
			t.Fatalf("other device failed to progress: %+v %v", job, err)
		}
		time.Sleep(time.Millisecond)
	}
	jobElapsed := time.Since(jobStarted)
	auxiliary, _ := json.Marshal(map[string]interface{}{"other_device_job_succeeded": true, "other_device_job_us": jobElapsed.Microseconds(), "auxiliary_route_latency_us": latencies})
	t.Log("AUXILIARY_MEASUREMENT " + string(auxiliary))
	f.assertOwnerHeld(t)
	var value responsiveHTTPResult
	select {
	case value = <-result:
	case <-time.After(6 * time.Second):
		t.Fatal("bare HTTP native request did not receive a bounded server response while native call stayed blocked")
	}
	var failure ErrorResponse
	if value.err != nil || value.status != http.StatusServiceUnavailable || json.Unmarshal(value.body, &failure) != nil || failure.Error.Code != "unavailable" {
		t.Fatalf("unexpected bounded wait response: %+v %s", value, value.body)
	}
	if !strings.Contains(failure.Error.Message, "may still be") || strings.Contains(failure.Error.Message, "did not enter") {
		t.Fatal("response falsely asserted that native collection never started", failure.Error.Message)
	}
	if value.elapsed < 4500*time.Millisecond || value.elapsed > 6*time.Second {
		t.Fatal("server wait did not follow its five-second budget", value.elapsed)
	}
	f.assertOwnerHeld(t)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer closeCancel()
	if err := f.engine.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Close released a still-blocked native owner", err)
	}
	select {
	case <-f.engine.Done():
		t.Fatal("engine reported Done before native return")
	default:
	}
	f.a.mu.Lock()
	held := f.a.closes == 0 && f.a.active && !f.a.unsafeClose
	f.a.mu.Unlock()
	if !held {
		t.Fatal("native owner was closed early")
	}
	f.release()
	doneCtx, doneCancel := context.WithTimeout(context.Background(), time.Second)
	defer doneCancel()
	if err := f.engine.Close(doneCtx); err != nil {
		t.Fatal(err)
	}
	measurement := map[string]interface{}{"server_wait_ms": value.elapsed.Milliseconds(), "response_status": value.status, "response_code": failure.Error.Code, "native_remained_inflight_after_response": true, "other_device_job_succeeded": true, "other_device_job_us": jobElapsed.Microseconds(), "auxiliary_route_latency_us": latencies, "actual_USB_or_workspace_daemon_used": false}
	encoded, _ := json.Marshal(measurement)
	t.Log("RESPONSIVENESS_MEASUREMENT " + string(encoded))
}

func TestNativeInspectionCanceledWaitMessageIsNeutralForStartedCalls(t *testing.T) {
	f := newResponsiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18777/v1/devices/20000001/native-sequence/playback", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { New(f.engine).ServeHTTP(recorder, request); close(done) }()
	responsiveEntered(t, f)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler ignored earlier caller context")
	}
	var failure ErrorResponse
	if recorder.Code != http.StatusServiceUnavailable || json.Unmarshal(recorder.Body.Bytes(), &failure) != nil || failure.Error.Code != "unavailable" {
		t.Fatal("canceled wait changed status/code/shape", recorder.Code, recorder.Body.String())
	}
	f.assertOwnerHeld(t)
	t.Log("STARTED_NATIVE_WAIT_MESSAGE " + failure.Error.Message)
	if !strings.Contains(failure.Error.Message, "may still be") || strings.Contains(failure.Error.Message, "did not enter") {
		t.Fatal("context cancellation falsely implied native call never entered")
	}
}

func TestNativeInspectionEarlierCallerDeadlineKeepsNativeOwner(t *testing.T) {
	f := newResponsiveFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	result := f.inspect(ctx)
	responsiveEntered(t, f)
	select {
	case value := <-result:
		if !errors.Is(value.err, context.DeadlineExceeded) || value.elapsed > time.Second {
			t.Fatalf("earlier caller deadline was not respected: elapsed=%s err=%v", value.elapsed, value.err)
		}
		t.Log(fmt.Sprintf("CALLER_DEADLINE_MEASUREMENT elapsed_us=%d error=context_deadline_exceeded", value.elapsed.Microseconds()))
	case <-time.After(time.Second):
		t.Fatal("caller stayed blocked behind native I/O")
	}
	f.assertOwnerHeld(t)
}
