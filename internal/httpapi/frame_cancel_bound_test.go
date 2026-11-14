package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

func TestFrameCancelConditionalBodyAndLegacyEmptyCompatibility(t *testing.T) {
	path := "/v1/frame-jobs/" + frameHTTPJob + "/cancel"
	for _, test := range []struct {
		name, body    string
		code, cancels int
		bound         bool
	}{
		{"empty", "", 202, 1, false}, {"empty-object", "{}", 202, 1, false},
		{"correct", `{"expected_daemon_instance_id":"` + frameHTTPInstance + `"}`, 202, 1, true},
		{"wrong", `{"expected_daemon_instance_id":"daemon_ffffffffffffffffffffffffffffffff"}`, 409, 0, false},
		{"null", `{"expected_daemon_instance_id":null}`, 400, 0, false},
		{"blank", `{"expected_daemon_instance_id":""}`, 400, 0, false},
		{"number", `{"expected_daemon_instance_id":1}`, 400, 0, false},
		{"array", `{"expected_daemon_instance_id":[]}`, 400, 0, false},
		{"malformed", `{"expected_daemon_instance_id":"daemon_a"}`, 400, 0, false},
		{"duplicate", `{"expected_daemon_instance_id":"` + frameHTTPInstance + `","expected_daemon_instance_id":"` + frameHTTPInstance + `"}`, 400, 0, false},
		{"unknown", `{"expected_daemon_instance_id":"` + frameHTTPInstance + `","allow":true}`, 400, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &frameHTTPSpy{program: frameHTTPProgram(t)}
			response := frameHTTPDo(s, "POST", path, test.body)
			if response.Code != test.code || s.cancelCalls != test.cancels {
				t.Fatalf("status=%d cancel=%d body=%s", response.Code, s.cancelCalls, response.Body.String())
			}
			values := response.Header().Values("X-NB1-Daemon-Instance-ID")
			if test.bound {
				if len(values) != 1 || values[0] != frameHTTPInstance {
					t.Fatal(values)
				}
			} else if len(values) != 0 {
				t.Fatal("legacy/rejected request claimed binding", values)
			}
		})
	}
}

type conditionalCancelBackend struct {
	entered, release chan struct{}
	once             sync.Once
}

func (b *conditionalCancelBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20005601", Capabilities: []string{"set", "raw-rgb", "mk2-basic", "hold"}}
}
func (b *conditionalCancelBackend) Set(control.Step) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}
func (b *conditionalCancelBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now()}, nil
}
func (b *conditionalCancelBackend) Stop(string) error { return nil }
func (b *conditionalCancelBackend) Close()            {}

type conditionalCancelDriver struct{ backend *conditionalCancelBackend }

func (d conditionalCancelDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type conditionalCancelEngine struct {
	*control.Engine
	calls int32
}

func (s *conditionalCancelEngine) CancelFrame(ctx context.Context, id string) (control.FrameJob, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.Engine.CancelFrame(ctx, id)
}

func TestFrameCancelWrongInstanceCannotMutateFakeEngine(t *testing.T) {
	b := &conditionalCancelBackend{entered: make(chan struct{}), release: make(chan struct{})}
	e, err := control.New(conditionalCancelDriver{b}, control.Options{Serials: []string{"20005601"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	defer func() {
		once.Do(func() { close(b.release) })
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := e.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	for {
		state, _ := e.Device("20005601")
		if state.Connection == "online" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	compiled, err := lightactionv2.CompileJSON([]byte(`{"kind":"orbit","period_ms":200}`))
	if err != nil {
		t.Fatal(err)
	}
	info := e.Instance()
	ticket := control.ReceiptRequest{RequestID: info.Idempotency.RequestIDPrefix + strings.Repeat("a", 32), ExpectedDaemonInstanceID: info.DaemonInstanceID}
	accepted, err := e.SubmitFrameWithReceipt(ctx, "20005601", compiled.Program, "reject", ticket)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	service := &conditionalCancelEngine{Engine: e}
	handler := httpapi.New(service)
	post := func(instance string) *httptest.ResponseRecorder {
		return frameCancelRequestTo(handler, accepted.Job.ID, instance)
	}
	wrong := post("daemon_ffffffffffffffffffffffffffffffff")
	job, _ := e.FrameJob(accepted.Job.ID)
	if wrong.Code != 409 || atomic.LoadInt32(&service.calls) != 0 || job.Status != "running" {
		t.Fatalf("wrong instance mutated engine: %d %d %#v", wrong.Code, service.calls, job)
	}
	correct := post(info.DaemonInstanceID)
	job, _ = e.FrameJob(accepted.Job.ID)
	if correct.Code != 202 || atomic.LoadInt32(&service.calls) != 1 || job.Status != "cancelling" || correct.Header().Get("X-NB1-Daemon-Instance-ID") != info.DaemonInstanceID {
		t.Fatalf("correct binding failed: %d %d %#v", correct.Code, service.calls, job)
	}
	once.Do(func() { close(b.release) })
}

func frameCancelRequestTo(handler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, id, instance string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8931/v1/frame-jobs/"+id+"/cancel", strings.NewReader(`{"expected_daemon_instance_id":"`+instance+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
