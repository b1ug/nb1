package cmd

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

// Test only the helper receiving an owned fake Engine. Do not invoke the CLI's
// direct discovery path or construct a native devicebackend in these tests.
type lightV2DirectBackend struct {
	mu               sync.Mutex
	led1, led2       string
	writes, closed   int
	entered, release chan struct{}
	once             sync.Once
}

func (b *lightV2DirectBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "demo", Capabilities: []string{"set", "raw-rgb", "mk2-basic", "hold"}}
}
func (b *lightV2DirectBackend) Set(step control.Step) error {
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if step.LED == 1 || step.LED == 0 {
		b.led1 = step.Color
	}
	if step.LED == 2 || step.LED == 0 {
		b.led2 = step.Color
	}
	b.writes++
	return nil
}
func (b *lightV2DirectBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now()}, nil
}
func (b *lightV2DirectBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "off" {
		b.led1, b.led2 = "#000000", "#000000"
	}
	return nil
}
func (b *lightV2DirectBackend) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closed++ }

type lightV2DirectDriver struct{ backend *lightV2DirectBackend }

func (d lightV2DirectDriver) Open(string) (control.Backend, error) { return d.backend, nil }

func startLightV2DirectEngine(t *testing.T, b *lightV2DirectBackend) *control.Engine {
	t.Helper()
	e, err := control.New(lightV2DirectDriver{b}, control.Options{Serials: []string{"demo"}, PollInterval: time.Hour, ShutdownMode: "hold"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		state, _ := e.Device("demo")
		if state.Connection == "online" {
			return e
		}
		select {
		case <-deadline:
			t.Fatal("fake Engine did not start")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestLightV2DirectOwnedEngineUsesSameFrameExecutor(t *testing.T) {
	b := &lightV2DirectBackend{led1: "#000000", led2: "#000000"}
	engine := startLightV2DirectEngine(t, b)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	compiled, err := lightactionv2.CompileRequest([]byte(`{"contract":"nb1-light-action/experimental-v2","action":{"kind":"orbit","period_ms":200,"segments":4}}`))
	if err != nil {
		t.Fatal(err)
	}
	out := lightV2RunOutput{Contract: lightactionv2.Contract, Mode: "direct", Policy: "reject"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = runLightV2OnEngine(ctx, engine, "demo", compiled, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Job == nil || out.Job.Status != "succeeded" || !reflect.DeepEqual(out.Job.Program, compiled.Program) || out.RequestTicket != nil {
		t.Fatalf("unexpected direct result: %#v", out)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writes != 10 || b.led1 != "#000000" || b.led2 != "#000000" || b.closed != 0 {
		t.Fatalf("direct helper duplicated executor/ownership lifecycle: %#v", b)
	}
}

func TestLightV2DirectTimeoutRetainsHandleForCallerCleanup(t *testing.T) {
	b := &lightV2DirectBackend{led1: "#000000", led2: "#000000", entered: make(chan struct{}), release: make(chan struct{})}
	engine := startLightV2DirectEngine(t, b)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(b.release) })
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = engine.Close(ctx)
	}()
	compiled, err := lightactionv2.CompileRequest([]byte(`{"contract":"nb1-light-action/experimental-v2","action":{"kind":"orbit","period_ms":200,"segments":4}}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := lightV2RunOutput{Mode: "direct"}
	done := make(chan error, 1)
	go func() { done <- runLightV2OnEngine(ctx, engine, "demo", compiled, &out) }()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fake native Set not reached")
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct client wait did not stop")
	}
	if !errors.Is(err, context.Canceled) || !out.Accepted || out.Job == nil {
		t.Fatalf("lost direct outcome on timeout: %#v %v", out, err)
	}
	short, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if err = engine.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup pretended to interrupt Set: %v", err)
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed != 0 {
		t.Fatal("closed native handle before Set returned")
	}
	releaseOnce.Do(func() { close(b.release) })
	shutdown, finish := context.WithTimeout(context.Background(), time.Second)
	defer finish()
	if err = engine.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	final, err := engine.FrameJob(out.Job.ID)
	if err != nil || final.Status != "cancelled" {
		t.Fatalf("cleanup did not settle cancellation: %#v %v", final, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != 1 || b.led1 != "#000000" || b.led2 != "#000000" {
		t.Fatal("direct fake cleanup did not finish")
	}
}
