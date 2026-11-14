package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type eventRecoveryDriver struct {
	inner  *recoveryDriver
	failed chan struct{}
}

func (d eventRecoveryDriver) Open(serial string) (Backend, error) {
	backend, err := d.inner.Open(serial)
	if err != nil {
		select {
		case d.failed <- struct{}{}:
		default:
		}
	}
	return backend, err
}

func nextConnectionEvent(t *testing.T, engine *Engine, sequence uint64, connection, path string) ConnectionEvent {
	t.Helper()
	select {
	case event, open := <-engine.Events():
		if !open || event.Sequence != sequence || event.Connection != connection || event.Path != path || event.Serial != "a" || event.Time.IsZero() {
			t.Fatalf("connection event = %+v open=%v; want sequence=%d connection=%s path=%s", event, open, sequence, connection, path)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("missing connection transition")
		return ConnectionEvent{}
	}
}

func TestConnectionEventsDescribeLossAndNewPathRecoveryWithoutSampleSpam(t *testing.T) {
	first := &recoveryBackend{newEngineBackend("a"), "path-before"}
	second := &recoveryBackend{newEngineBackend("a"), "path-after"}
	driver := eventRecoveryDriver{inner: &recoveryDriver{first: first, second: second}, failed: make(chan struct{}, 16)}
	e, err := New(driver, Options{Serials: []string{"a"}, PollInterval: 10 * time.Millisecond, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	nextConnectionEvent(t, e, 1, "connecting", "")
	if event := nextConnectionEvent(t, e, 2, "offline", ""); event.Error == "" {
		t.Fatal("offline event lost its error")
	}
	// Confirm repeated failed opens have actually happened, rather than relying
	// on a sleep and accidentally passing without exercising deduplication.
	for i := 0; i < 3; i++ {
		awaitEngineSignal(t, driver.failed)
	}
	select {
	case event := <-e.Events():
		t.Fatalf("unchanged offline state spammed an event: %+v", event)
	default:
	}
	driver.inner.mu.Lock()
	driver.inner.ready = true
	driver.inner.mu.Unlock()
	if event := nextConnectionEvent(t, e, 3, "online", "path-before"); event.Error != "" {
		t.Fatalf("recovery event retained stale error: %s", event.Error)
	}
	reads := make(chan struct{}, 16)
	var disconnected int32
	first.hooks(nil, nil, func() error {
		select {
		case reads <- struct{}{}:
		default:
		}
		if atomic.LoadInt32(&disconnected) != 0 {
			return errors.New("simulated USB removal")
		}
		return nil
	})
	for i := 0; i < 3; i++ {
		awaitEngineSignal(t, reads)
	}
	select {
	case event := <-e.Events():
		t.Fatalf("RGB sampling emitted a connection record: %+v", event)
	default:
	}
	atomic.StoreInt32(&disconnected, 1)
	if event := nextConnectionEvent(t, e, 4, "offline", "path-before"); event.Error != "simulated USB removal" {
		t.Fatalf("removal event error = %s", event.Error)
	}
	nextConnectionEvent(t, e, 5, "online", "path-after")
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextConnectionEvent(t, e, 6, "closing", "path-after")
	nextConnectionEvent(t, e, 7, "closed", "path-after")
	if event, open := <-e.Events(); open {
		t.Fatalf("stream did not close after workers finished: %+v", event)
	}
	if e.DroppedEvents() != 0 {
		t.Fatal("unexpected event loss")
	}
}

type cyclingEventDriver struct {
	mu      sync.Mutex
	opens   int
	cycling int32
	stable  chan struct{}
	once    sync.Once
}

func (d *cyclingEventDriver) Open(serial string) (Backend, error) {
	d.mu.Lock()
	d.opens++
	number := d.opens
	d.mu.Unlock()
	return &cyclingEventBackend{engineBackend: newEngineBackend(serial), driver: d, number: number}, nil
}

type cyclingEventBackend struct {
	*engineBackend
	driver *cyclingEventDriver
	number int
	reads  int
}

func (b *cyclingEventBackend) Info() DeviceInfo {
	info := b.engineBackend.Info()
	info.Path = fmt.Sprintf("path-%d", b.number)
	return info
}

func (b *cyclingEventBackend) Read() (Observation, error) {
	b.reads++
	if b.reads > 1 && atomic.LoadInt32(&b.driver.cycling) != 0 {
		return Observation{}, errors.New("simulated recurrent disconnect")
	}
	observation, err := b.engineBackend.Read()
	if b.reads > 1 {
		b.driver.once.Do(func() { close(b.driver.stable) })
	}
	return observation, err
}

func TestFullEventBufferCannotBlockDeviceExecutionOrShutdown(t *testing.T) {
	driver := &cyclingEventDriver{cycling: 1, stable: make(chan struct{})}
	e, err := New(driver, Options{Serials: []string{"a"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = e.Close(ctx)
	})
	// Nobody reads Events during these >64 real worker transitions.
	awaitEngineCondition(t, func() bool {
		driver.mu.Lock()
		defer driver.mu.Unlock()
		return driver.opens >= 40
	})
	atomic.StoreInt32(&driver.cycling, 0)
	awaitEngineSignal(t, driver.stable)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineJob(t, e, job.ID, "succeeded")
	if e.DroppedEvents() == 0 {
		t.Fatal("stalled consumer did not produce observable drops")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		t.Fatalf("full log buffer blocked shutdown: %v", err)
	}
	var events []ConnectionEvent
	for event := range e.Events() {
		events = append(events, event)
	}
	if len(events) != connectionEventBufferSize {
		t.Fatalf("event buffer was not bounded: %d", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event ordering changed: %+v", event)
		}
	}
}

func TestConnectionEventsCloseBeforeStartAndConcurrentClose(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(fmt.Sprintf("started=%v", start), func(t *testing.T) {
			e, err := New(engineDriver{"a": newEngineBackend("a")}, Options{Serials: []string{"a"}, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if start {
				if err := e.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			var done sync.WaitGroup
			for i := 0; i < 16; i++ {
				done.Add(1)
				go func() {
					defer done.Done()
					if err := e.Close(context.Background()); err != nil {
						t.Error(err)
					}
				}()
			}
			done.Wait()
			for range e.Events() {
			}
			if err := e.Start(context.Background()); !errors.Is(err, ErrClosed) {
				t.Fatalf("Start after closed event stream = %v", err)
			}
		})
	}
}
