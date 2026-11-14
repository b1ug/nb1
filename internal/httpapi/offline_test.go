package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

type offlineGate struct {
	entered, release chan struct{}
	enter, unblock   sync.Once
}

func newOfflineGate() *offlineGate {
	return &offlineGate{entered: make(chan struct{}), release: make(chan struct{})}
}
func (g *offlineGate) block() {
	g.enter.Do(func() { close(g.entered) })
	<-g.release
}
func (g *offlineGate) open() { g.unblock.Do(func() { close(g.release) }) }

type offlineBackend struct {
	readGate, setGate, closeGate *offlineGate
	cleanupFailure               bool
	reads, holds, released       int32
}

func (*offlineBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "a", Path: "old-path", Capabilities: []string{"set"}}
}
func (b *offlineBackend) Set(control.Step) error {
	if b.setGate != nil {
		b.setGate.block()
	}
	return nil
}
func (b *offlineBackend) Read() (control.Observation, error) {
	if atomic.AddInt32(&b.reads, 1) > 1 && b.readGate != nil {
		b.readGate.block()
		return control.Observation{}, errors.New("simulated device removal")
	}
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *offlineBackend) Stop(mode string) error {
	if mode == "hold" && atomic.AddInt32(&b.holds, 1) > 1 && b.cleanupFailure {
		return errors.New("simulated finish cleanup failure")
	}
	return nil
}
func (b *offlineBackend) Close() {
	b.closeGate.block()
	// Represents native Close completing before its ownership lease is released.
	atomic.StoreInt32(&b.released, 1)
}

type offlineDriver struct {
	backend *offlineBackend
	opens   int32
}

func (d *offlineDriver) Open(string) (control.Backend, error) {
	atomic.AddInt32(&d.opens, 1)
	if atomic.LoadInt32(&d.backend.released) != 0 {
		return nil, control.ErrOffline
	}
	return d.backend, nil
}

func awaitOfflineSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("fake backend barrier did not arrive")
	}
}

func awaitOfflineEvent(t *testing.T, engine *control.Engine, connection string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-engine.Events():
			if !ok {
				t.Fatal("event stream closed before expected transition")
			}
			if event.Connection == connection {
				return
			}
		case <-deadline.C:
			t.Fatalf("no %s connection transition", connection)
		}
	}
}

func offlineSubmit(t *testing.T, handler http.Handler, want int) control.Job {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/devices/a/jobs", bytes.NewBufferString(`{"program":{"steps":[{"color":"#102030"}],"on_finish":"hold"},"policy":"queue"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != want {
		t.Errorf("submit status=%d body=%s; want %d", w.Code, w.Body.String(), want)
	}
	var job control.Job
	if w.Code == http.StatusAccepted {
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

func TestOfflineStatePublishedBeforeBlockedNativeClose(t *testing.T) {
	for _, scenario := range []string{"monitor_read_failure", "finish_cleanup_failure", "shutdown_during_monitor_read"} {
		t.Run(scenario, func(t *testing.T) {
			backend := &offlineBackend{closeGate: newOfflineGate()}
			if scenario == "finish_cleanup_failure" {
				backend.setGate = newOfflineGate()
				backend.cleanupFailure = true
			} else {
				backend.readGate = newOfflineGate()
			}
			driver := &offlineDriver{backend: backend}
			engine, err := control.New(driver, control.Options{Serials: []string{"a"}, PollInterval: 10 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if backend.readGate != nil {
					backend.readGate.open()
				}
				if backend.setGate != nil {
					backend.setGate.open()
				}
				backend.closeGate.open()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := engine.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			awaitOfflineEvent(t, engine, "online")
			handler := httpapi.New(engine)
			var active control.Job
			if backend.setGate != nil {
				active = offlineSubmit(t, handler, http.StatusAccepted)
				awaitOfflineSignal(t, backend.setGate.entered)
			} else {
				awaitOfflineSignal(t, backend.readGate.entered)
			}
			queued := offlineSubmit(t, handler, http.StatusAccepted)
			wantConnection, wantQueued := "offline", "failed"
			if scenario == "shutdown_during_monitor_read" {
				go func() { _ = engine.Close(context.Background()) }()
				awaitOfflineEvent(t, engine, "closing")
				wantConnection, wantQueued = "closing", "cancelled"
			}
			if backend.setGate != nil {
				backend.setGate.open()
			} else {
				backend.readGate.open()
			}
			awaitOfflineSignal(t, backend.closeGate.entered)
			state, _ := engine.Device("a")
			if state.Connection != wantConnection || state.LastError == "" || state.QueueDepth != 0 {
				t.Errorf("state while native Close is blocked: %+v; want %s with error and empty queue", state, wantConnection)
			}
			job, err := engine.Job(queued.ID)
			if err != nil || job.Status != wantQueued || job.FinishedAt == nil {
				t.Errorf("queued job while Close is blocked: %+v err=%v; want terminal %s", job, err, wantQueued)
			}
			if active.ID != "" {
				job, err := engine.Job(active.ID)
				if err != nil || job.Status != "failed" {
					t.Errorf("active cleanup failure: %+v err=%v", job, err)
				}
			}
			offlineSubmit(t, handler, http.StatusServiceUnavailable)
			if atomic.LoadInt32(&backend.released) != 0 || atomic.LoadInt32(&driver.opens) != 1 {
				t.Fatal("old handle/lease released or new backend opened before native Close completed")
			}
			select {
			case <-engine.Done():
				t.Fatal("runtime completed while native Close was still blocked")
			default:
			}
			if scenario == "shutdown_during_monitor_read" {
				backend.closeGate.open()
				awaitOfflineSignal(t, engine.Done())
				for event := range engine.Events() {
					if event.Connection != "closed" {
						t.Errorf("shutdown regressed from closing to %s", event.Connection)
					}
				}
			}
		})
	}
}
