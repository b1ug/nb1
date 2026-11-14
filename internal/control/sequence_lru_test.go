package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSequenceTerminalLRUTouchesKeepFixedExpiration(t *testing.T) {
	for _, access := range []string{"get", "replay", "cancel"} {
		t.Run(access, func(t *testing.T) {
			e, _ := startSequenceTest(t, Options{})
			clock := time.Now()
			e.mu.Lock()
			e.sequenceJobs.maxTerminalEntries = 2
			e.sequenceJobs.now = func() time.Time { return clock }
			e.mu.Unlock()
			advance := func(delta time.Duration) {
				e.mu.Lock()
				clock = clock.Add(delta)
				e.mu.Unlock()
			}
			ctx := context.Background()
			fingerprint := [32]byte{1}
			submit := func(id string) {
				t.Helper()
				if _, _, err := e.SubmitSequence(ctx, "a", id, fingerprint, prepareSequenceTest); err != nil {
					t.Fatal(err)
				}
				awaitSequenceTerminal(t, e, id, "succeeded")
			}
			submit("first")
			e.mu.Lock()
			expiresAt := e.sequenceJobs.entries["first"].expiresAt
			e.mu.Unlock()
			advance(time.Minute)
			submit("second")
			switch access {
			case "get":
				if _, err := e.SequenceJob("first"); err != nil {
					t.Fatal(err)
				}
			case "replay":
				if _, replayed, err := e.SubmitSequence(ctx, "a", "first", fingerprint, nil); err != nil || !replayed {
					t.Fatalf("replay: %v, %v", replayed, err)
				}
			case "cancel":
				if _, err := e.CancelSequence(ctx, "first"); err != nil {
					t.Fatal(err)
				}
			}
			advance(time.Minute)
			submit("third")
			if _, err := e.SequenceJob("second"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("least recently used terminal survived: %v", err)
			}
			if _, err := e.SequenceJob("first"); err != nil {
				t.Fatalf("recently used terminal was evicted: %v", err)
			}
			e.mu.Lock()
			if e.sequenceJobs.terminals.Len() != 2 || len(e.sequenceJobs.entries) != 2 || !e.sequenceJobs.entries["first"].expiresAt.Equal(expiresAt) {
				t.Error("LRU access changed retention or exceeded terminal capacity")
			}
			clock = expiresAt.Add(-time.Nanosecond)
			e.mu.Unlock()
			if _, err := e.SequenceJob("first"); err != nil {
				t.Fatalf("terminal expired early: %v", err)
			}
			advance(time.Nanosecond)
			if _, err := e.SequenceJob("first"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("LRU access extended the TTL: %v", err)
			}
			if _, replayed, err := e.SubmitSequence(ctx, "a", "first", [32]byte{2}, prepareSequenceTest); err != nil || replayed {
				t.Fatalf("ID reuse after expiry: replayed=%v err=%v", replayed, err)
			}
			awaitSequenceTerminal(t, e, "first", "succeeded")
			if _, replayed, err := e.SubmitSequence(ctx, "a", "second", [32]byte{2}, prepareSequenceTest); err != nil || replayed {
				t.Fatalf("ID reuse after LRU eviction: replayed=%v err=%v", replayed, err)
			}
		})
	}
}

type sequenceCacheDriver map[string]*frameIntegrationBackend

func (d sequenceCacheDriver) Open(serial string) (Backend, error) {
	if backend := d[serial]; backend != nil {
		return backend, nil
	}
	return nil, ErrNotFound
}

func TestSequenceActiveJobsPinOutsideSmallTerminalCache(t *testing.T) {
	driver := sequenceCacheDriver{}
	serials := []string{"a", "b", "c"}
	for _, serial := range serials {
		driver[serial] = &frameIntegrationBackend{engineBackend: newEngineBackend(serial),
			capabilities: []string{"set", "program", "raw-rgb", "hold"}}
	}
	e, err := New(driver, Options{Serials: serials, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	e.sequenceJobs.maxTerminalEntries = 1
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for _, serial := range serials {
		serial := serial
		awaitEngineCondition(t, func() bool {
			state, _ := e.Device(serial)
			return state.Connection == "online"
		})
	}
	ctx := context.Background()
	if _, _, err := e.SubmitSequence(ctx, "c", "old-terminal", [32]byte{}, prepareSequenceTest); err != nil {
		t.Fatal(err)
	}
	awaitSequenceTerminal(t, e, "old-terminal", "succeeded")
	gates := []*engineGate{newEngineGate(), newEngineGate(), newEngineGate()}
	for i, serial := range serials {
		gate := gates[i]
		defer gate.open()
		driver[serial].hooks(func(Step) error { return gate.block() }, nil, nil)
		if _, _, err := e.SubmitSequence(ctx, serial, "active-"+serial, [32]byte{}, prepareSequenceTest); err != nil {
			t.Fatalf("terminal cache blocked an idle device %s: %v", serial, err)
		}
		awaitEngineSignal(t, gate.entered)
	}
	e.mu.Lock()
	if len(e.sequenceJobs.entries) != 4 || e.sequenceJobs.terminals.Len() != 1 {
		t.Error("active jobs consumed terminal cache capacity")
	}
	e.mu.Unlock()
	if _, _, err := e.SubmitSequence(ctx, "a", "actually-busy", [32]byte{}, prepareSequenceTest); !errors.Is(err, ErrBusy) {
		t.Fatalf("device ownership was bypassed: %v", err)
	}
	gates[2].open()
	awaitSequenceTerminal(t, e, "active-c", "succeeded")
	if _, err := e.SequenceJob("old-terminal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminal overflow did not evict the oldest: %v", err)
	}
	for _, serial := range []string{"a", "b"} {
		job, err := e.SequenceJob("active-" + serial)
		if err != nil || job.Status != "running" {
			t.Fatalf("active job evicted: %+v %v", job, err)
		}
	}
	e.mu.Lock()
	if len(e.sequenceJobs.entries) != 3 || e.sequenceJobs.terminals.Len() != 1 {
		t.Error("terminal cache overflow changed active retention")
	}
	e.mu.Unlock()
}
