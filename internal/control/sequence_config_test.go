package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfiguredTerminalCacheEvictsAndExpiresWithoutBlockingNewWork(t *testing.T) {
	e, _ := startSequenceTest(t, Options{TerminalJobLimit: 2, TerminalJobTTL: 5 * time.Second})
	if count, ttl := e.SequenceJobLimits(); count != 2 || ttl != 5*time.Second {
		t.Fatalf("startup limits = %d %s", count, ttl)
	}
	clock := time.Now()
	e.mu.Lock()
	e.sequenceJobs.now = func() time.Time { return clock }
	e.mu.Unlock()
	advance := func(d time.Duration) { e.mu.Lock(); clock = clock.Add(d); e.mu.Unlock() }
	submit := func(id string) {
		t.Helper()
		if _, _, err := e.SubmitSequence(context.Background(), "a", id, [32]byte{}, prepareSequenceTest); err != nil {
			t.Fatal(err)
		}
		awaitSequenceTerminal(t, e, id, "succeeded")
	}
	submit("one")
	advance(time.Second)
	submit("two")
	if _, err := e.SequenceJob("one"); err != nil {
		t.Fatal(err)
	}
	submit("three")
	if _, err := e.SequenceJob("two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LRU did not evict two: %v", err)
	}
	advance(4 * time.Second)
	if _, err := e.SequenceJob("one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("custom TTL did not expire one: %v", err)
	}
	// Eviction is not a refusal window: a new identity may now execute.
	submit("four")
}

func TestInvalidTerminalCacheOptionsAreRejected(t *testing.T) {
	for _, options := range []Options{{TerminalJobLimit: -1}, {TerminalJobTTL: -time.Second}} {
		options.Serials = []string{"a"}
		if _, err := New(sequenceCacheDriver{}, options); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid cache accepted: %v", err)
		}
	}
}
