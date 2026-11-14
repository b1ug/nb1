package control

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryBackend struct {
	*engineBackend
	path string
}

func (b *recoveryBackend) Info() DeviceInfo { i := b.engineBackend.Info(); i.Path = b.path; return i }

type recoveryDriver struct {
	mu            sync.Mutex
	ready         bool
	first, second *recoveryBackend
}

func (d *recoveryDriver) Open(string) (Backend, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.ready {
		return nil, ErrOffline
	}
	_, _, _, _, closed, _ := d.first.snapshot()
	if closed == 0 {
		return d.first, nil
	}
	return d.second, nil
}

func awaitRecovery(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("simulated recovery timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReopenBySerialAfterSimulatedDisconnectWithoutReplayingWork(t *testing.T) {
	first := &recoveryBackend{newEngineBackend("a"), "port-before"}
	second := &recoveryBackend{newEngineBackend("a"), "port-after"}
	driver := &recoveryDriver{first: first, second: second}
	e, err := New(driver, Options{Serials: []string{"a"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	awaitRecovery(t, func() bool { s, _ := e.Device("a"); return s.Connection == "offline" })
	if _, err = e.Submit(context.Background(), "a", engineProgram("#ff0000", "hold"), "queue"); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline submit %v", err)
	}
	driver.mu.Lock()
	driver.ready = true
	driver.mu.Unlock()
	awaitRecovery(t, func() bool { s, _ := e.Device("a"); return s.Connection == "online" && s.Path == "port-before" })
	var disconnected int32
	set := make(chan struct{})
	var once sync.Once
	first.hooks(func(Step) error { once.Do(func() { close(set) }); return nil }, nil, func() error {
		if atomic.LoadInt32(&disconnected) != 0 {
			return errors.New("simulated USB removal")
		}
		return nil
	})
	program := engineProgram("#ff0000", "hold")
	program.Steps[0].HoldMS = 10000
	active, err := e.Submit(context.Background(), "a", program, "queue")
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, set)
	queued, err := e.Submit(context.Background(), "a", engineProgram("#00ff00", "hold"), "queue")
	if err != nil {
		t.Fatal(err)
	}
	atomic.StoreInt32(&disconnected, 1)
	awaitRecovery(t, func() bool { s, _ := e.Device("a"); return s.Connection == "online" && s.Path == "port-after" })
	awaitEngineJob(t, e, active.ID, "failed")
	awaitEngineJob(t, e, queued.ID, "failed")
	calls, _, _, _, _, _ := second.snapshot()
	for _, call := range calls {
		if call.kind == "set" {
			t.Fatal("old program was replayed on reconnected backend")
		}
	}
	fresh, err := e.Submit(context.Background(), "a", engineProgram("#0000ff", "hold"), "queue")
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineJob(t, e, fresh.ID, "succeeded")
	state, _ := e.Device("a")
	if state.Observed == nil || state.Observed.LED1 != "#0000ff" || state.LastError != "" {
		t.Fatalf("fresh state not restored: %+v", state)
	}
	_, _, _, _, closed, unsafe := first.snapshot()
	if closed != 1 || unsafe {
		t.Fatalf("old handle cleanup: count=%d unsafe=%v", closed, unsafe)
	}
}
