package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/sequence"
	"github.com/b1ug/nb1/internal/transporttrace"
)

func largeSequenceMemoryInput(timing string) sequence.Sequence {
	input := sequence.Sequence{Timing: timing, Repeat: 1, OnFinish: "hold", Steps: make([]sequence.Step, sequence.MaxExecutedSteps)}
	if timing == sequence.TimingInterval {
		input.Initial = []sequence.Target{{LED: 1, Color: "#123456"}, {LED: 2, Color: "#654321"}}
	}
	for i := range input.Steps {
		if timing == sequence.TimingSerial {
			input.Steps[i].Targets = []sequence.Target{{LED: 0, Color: "#123456"}}
		} else {
			interval := 50
			input.Steps[i] = sequence.Step{Targets: input.Initial, FadeMS: 50, IntervalMS: &interval}
		}
	}
	return input
}

func assertSequenceMemory(t *testing.T, e *Engine, id string, targets int, hasPlan bool) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	entry := e.sequenceJobs.entries[id]
	if entry == nil || e.jobs[id] != entry.record {
		t.Fatal("job identity reservation was lost")
	}
	r := entry.record
	if entry.retainedTargets != targets || e.sequenceJobs.retainedTargets != targets {
		t.Fatalf("retained targets entry=%d store=%d, want %d", entry.retainedTargets, e.sequenceJobs.retainedTargets, targets)
	}
	if (r.program != nil || r.frames != nil) != hasPlan {
		t.Fatalf("execution plan retention differs from %v", hasPlan)
	}
	if !hasPlan {
		scope := transportScope(r, transporttrace.Cleanup, transporttrace.NoPosition(), "off")
		if scope.JobID != id || scope.Frame != (r.kind == recordFrames) {
			t.Fatalf("terminal diagnostic identity: %+v", scope)
		}
	}
}

func TestSequenceTerminalReleasesLargePlansAfterCleanup(t *testing.T) {
	for _, timing := range []string{sequence.TimingSerial, sequence.TimingInterval} {
		t.Run(timing, func(t *testing.T) {
			e, backend := startScopeEngine(t, time.Hour)
			input := largeSequenceMemoryInput(timing)
			cost := sequence.MaxExecutedSteps
			if timing == sequence.TimingInterval {
				cost = 2 + 2*sequence.MaxExecutedSteps
			}
			clock := time.Now()
			e.mu.Lock()
			e.sequenceJobs.now = func() time.Time { return clock }
			e.sequenceJobs.maxTargets = cost
			e.mu.Unlock()
			advance := func(delta time.Duration) {
				e.mu.Lock()
				clock = clock.Add(delta)
				e.mu.Unlock()
			}
			setGate, cleanupGate := newEngineGate(), newEngineGate()
			defer setGate.open()
			defer cleanupGate.open()
			backend.hooks(func(Step) error { return setGate.block() }, func(mode string) error {
				if mode == "off" {
					return cleanupGate.block()
				}
				return nil
			}, nil)
			prepare := func() (sequence.Sequence, error) { return input, nil }
			ctx := context.Background()
			fingerprint := [32]byte{7}
			job, _, err := e.SubmitSequence(ctx, "a", "large-"+timing, fingerprint, prepare)
			if err != nil {
				t.Fatal(err)
			}
			awaitEngineSignal(t, setGate.entered)
			advance(time.Hour)
			assertSequenceMemory(t, e, job.ID, cost, true)
			if _, _, err := e.SubmitSequence(ctx, "a", "budget-before", [32]byte{}, prepareSequenceTest); !errors.Is(err, ErrSequenceJobCapacity) {
				t.Fatalf("active target budget: %v", err)
			}
			cancelled, err := e.CancelSequence(ctx, job.ID)
			if err != nil || cancelled.Status != "cancelling" {
				t.Fatalf("cancel: %+v, %v", cancelled, err)
			}
			assertSequenceMemory(t, e, job.ID, cost, true)
			setGate.open()
			awaitEngineSignal(t, cleanupGate.entered)
			assertSequenceMemory(t, e, job.ID, cost, true)
			cleanupGate.open()
			terminal := awaitSequenceTerminal(t, e, job.ID, "cancelled")
			if terminal.TotalSteps != sequence.MaxExecutedSteps {
				t.Fatalf("large plan lost its logical size: %+v", terminal)
			}
			assertSequenceMemory(t, e, job.ID, 0, false)
			phases := map[transporttrace.Phase]bool{}
			for _, call := range backend.traces() {
				if call.scope.JobID == job.ID {
					phases[call.scope.Phase] = true
				}
			}
			if !phases[transporttrace.Cleanup] || !phases[transporttrace.CleanupVerify] {
				t.Fatalf("cancellation diagnostic phases: %+v", phases)
			}

			// The first Job remains retained while a new plan uses its target budget.
			next, _, err := e.SubmitSequence(ctx, "a", "budget-after", [32]byte{}, prepareSequenceTest)
			if err != nil {
				t.Fatalf("terminal job retained target capacity: %v", err)
			}
			awaitSequenceTerminal(t, e, next.ID, "succeeded")
			assertSequenceMemory(t, e, next.ID, 0, false)

			advance(SequenceJobRetention - time.Nanosecond)
			replayed, duplicate, err := e.SubmitSequence(ctx, "a", job.ID, fingerprint, nil)
			if err != nil || !duplicate || !reflect.DeepEqual(replayed, terminal) {
				t.Fatalf("retained Job replay: %+v, %v, %v", replayed, duplicate, err)
			}
			cancelled, err = e.CancelSequence(ctx, job.ID)
			if err != nil || !reflect.DeepEqual(cancelled, terminal) {
				t.Fatalf("terminal cancel: %+v, %v", cancelled, err)
			}
			queried, err := e.SequenceJob(job.ID)
			if err != nil || !reflect.DeepEqual(queried, terminal) {
				t.Fatalf("terminal query: %+v, %v", queried, err)
			}
			advance(time.Nanosecond)
			if _, err := e.SequenceJob(job.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("terminal reads refreshed expiration: %v", err)
			}
			fresh, duplicate, err := e.SubmitSequence(ctx, "a", job.ID, [32]byte{8}, prepareSequenceTest)
			if err != nil || duplicate || fresh.Status != "queued" {
				t.Fatalf("reuse after expiration: %+v, %v, %v", fresh, duplicate, err)
			}
			awaitSequenceTerminal(t, e, fresh.ID, "succeeded")
			assertSequenceMemory(t, e, fresh.ID, 0, false)
		})
	}
}

func TestSequenceFailedJobReleasesPlanAndKeepsError(t *testing.T) {
	e, backend := startSequenceTest(t, Options{})
	backend.hooks(func(Step) error { return fmt.Errorf("fake device write failure") }, nil, nil)
	prepare := func() (sequence.Sequence, error) { return largeSequenceMemoryInput(sequence.TimingSerial), nil }
	job, _, err := e.SubmitSequence(context.Background(), "a", "failure", [32]byte{}, prepare)
	if err != nil {
		t.Fatal(err)
	}
	terminal := awaitSequenceTerminal(t, e, job.ID, "failed")
	if terminal.Error != "fake device write failure" {
		t.Fatalf("terminal error lost: %+v", terminal)
	}
	assertSequenceMemory(t, e, job.ID, 0, false)
}
