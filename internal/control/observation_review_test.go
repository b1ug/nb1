package control

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func observationReviewReads(b *engineBackend) int {
	calls, _, _, _, _, _ := b.snapshot()
	reads := 0
	for _, call := range calls {
		if call.kind == "read" {
			reads++
		}
	}
	return reads
}

func TestObservationCadenceDoesNotCatchUpAfterSlowRead(t *testing.T) {
	for _, mode := range []string{"idle", "long_hold"} {
		t.Run(mode, func(t *testing.T) {
			b := newEngineBackend("a")
			const interval = 20 * time.Millisecond
			e := startEngineTest(t, Options{PollInterval: interval}, b)
			setGate, slowRead := newEngineGate(), newEngineGate()
			defer setGate.open()
			defer slowRead.open()
			if mode == "long_hold" {
				b.hooks(func(Step) error { return setGate.block() }, nil, nil)
				submitEngineJob(t, e, "a", Program{Steps: []Step{{Color: "#112233", HoldMS: 1000}}, OnFinish: "hold"}, "queue")
				awaitEngineSignal(t, setGate.entered)
			}
			starts := make(chan time.Time, 16)
			returned := make(chan time.Time, 1)
			var reads int32
			b.hooks(nil, nil, func() error {
				starts <- time.Now()
				if atomic.AddInt32(&reads, 1) == 1 {
					slowRead.block()
					returned <- time.Now()
				}
				return nil
			})
			setGate.open()
			awaitEngineSignal(t, slowRead.entered)
			<-starts
			// Hold one fake synchronous Read across several scheduled intervals.
			// This intentionally exercises missed ticks, rather than sleeping to
			// guess when the worker might have entered the Read.
			missed := time.NewTimer(3 * interval)
			<-missed.C
			slowRead.open()
			finished := <-returned
			select {
			case next := <-starts:
				if lag := next.Sub(finished); lag < interval {
					t.Fatalf("%s issued catch-up Read after %s; want at least %s", mode, lag, interval)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("observations did not resume after slow Read")
			}
			e.beginClose()
		})
	}
}

func TestObservationReadFailureStopsShortProgramAndQueuedWork(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{PollInterval: 20 * time.Millisecond}, b)
	setGate, readGate := newEngineGate(), newEngineGate()
	defer setGate.open()
	defer readGate.open()
	b.hooks(func(Step) error { return setGate.block() }, nil, nil)
	active := submitEngineJob(t, e, "a", Program{Steps: []Step{{Color: "#112233"}}, Repeat: 50, OnFinish: "hold"}, "queue")
	awaitEngineSignal(t, setGate.entered)
	queued := submitEngineJob(t, e, "a", engineProgram("#abcdef", "hold"), "queue")
	failure := errors.New("injected observation failure while Set still succeeds")
	b.hooks(nil, nil, func() error { _ = readGate.block(); return failure })
	setGate.open()
	awaitEngineSignal(t, readGate.entered)
	calls, _, _, _, _, _ := b.snapshot()
	setsBeforeFailure := len(engineSetColors(calls))
	readGate.open()
	job := awaitEngineJob(t, e, active.ID, "failed")
	awaitEngineJob(t, e, queued.ID, "failed")
	awaitEngineCondition(t, func() bool { state, _ := e.Device("a"); return state.Connection == "offline" })
	if !strings.Contains(job.Error, failure.Error()) {
		t.Fatalf("observation failure was hidden: %+v", job)
	}
	calls, _, _, maxInFlight, closed, unsafe := b.snapshot()
	if len(engineSetColors(calls)) != setsBeforeFailure || maxInFlight != 1 || closed != 1 || unsafe {
		t.Fatalf("work continued or Read overlapped cleanup: calls=%v max=%d closed=%d unsafe=%v", calls, maxInFlight, closed, unsafe)
	}
}

func TestObservationCancellationAndShutdownWaitForInflightRead(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{PollInterval: 20 * time.Millisecond}, b)
	setGate, readGate := newEngineGate(), newEngineGate()
	defer setGate.open()
	defer readGate.open()
	b.hooks(func(Step) error { return setGate.block() }, nil, nil)
	job := submitEngineJob(t, e, "a", Program{Steps: []Step{{Color: "#112233", HoldMS: 1000}}, OnFinish: "hold"}, "queue")
	awaitEngineSignal(t, setGate.entered)
	var reads int32
	b.hooks(nil, nil, func() error { atomic.AddInt32(&reads, 1); return readGate.block() })
	setGate.open()
	awaitEngineSignal(t, readGate.entered)
	if cancelled, err := e.Cancel(context.Background(), job.ID); err != nil || cancelled.Status != "cancelling" {
		t.Fatalf("cancel while Read in flight: %+v err=%v", cancelled, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close while Read blocked=%v", err)
	}
	_, _, _, _, closed, unsafe := b.snapshot()
	if closed != 0 || unsafe {
		t.Fatal("backend was released while Read was in flight")
	}
	readGate.open()
	awaitEngineSignal(t, e.Done())
	awaitEngineJob(t, e, job.ID, "cancelled")
	_, led1, led2, maxInFlight, closed, unsafe := b.snapshot()
	if led1 != "#000000" || led2 != "#000000" || maxInFlight != 1 || closed != 1 || unsafe || atomic.LoadInt32(&reads) != 2 {
		t.Fatalf("cancel/read cleanup mismatch: rgb=%s/%s max=%d closed=%d unsafe=%v reads=%d", led1, led2, maxInFlight, closed, unsafe, reads)
	}
}

func TestObservationCancellationSkipsAlreadyDueRead(t *testing.T) {
	b := newEngineBackend("a")
	const interval = 20 * time.Millisecond
	e := startEngineTest(t, Options{PollInterval: interval}, b)
	setGate, offGate := newEngineGate(), newEngineGate()
	defer setGate.open()
	defer offGate.open()
	b.hooks(func(Step) error { return setGate.block() }, func(mode string) error {
		if mode == "off" {
			return offGate.block()
		}
		return nil
	}, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#112233", "hold"), "queue")
	awaitEngineSignal(t, setGate.entered)
	baseline := observationReviewReads(b)
	due := time.NewTimer(3 * interval)
	<-due.C
	if _, err := e.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	setGate.open()
	awaitEngineSignal(t, offGate.entered)
	if observationReviewReads(b) != baseline {
		t.Fatal("known cancellation issued a due observation before off cleanup")
	}
	offGate.open()
	awaitEngineJob(t, e, job.ID, "cancelled")
}

func TestReviewShortStepsDoNotStarveObservation(t *testing.T) {
	b := newEngineBackend("a")
	const pollInterval = 50 * time.Millisecond
	e := startEngineTest(t, Options{PollInterval: pollInterval}, b)
	first, twentieth := newEngineGate(), newEngineGate()
	defer first.open()
	defer twentieth.open()
	var sets int32
	b.hooks(func(Step) error {
		switch atomic.AddInt32(&sets, 1) {
		case 1:
			return first.block()
		case 20:
			return twentieth.block()
		}
		return nil
	}, nil, nil)
	job := submitEngineJob(t, e, "a", Program{Steps: []Step{{Color: "#112233"}}, Repeat: 30, OnFinish: "hold"}, "queue")
	awaitEngineSignal(t, first.entered)
	before, _ := e.Device("a")
	beforeReads := observationReviewReads(b)
	started := time.Now()
	first.open()
	awaitEngineSignal(t, twentieth.entered)
	elapsed := time.Since(started)
	after, _ := e.Device("a")
	afterReads := observationReviewReads(b)
	current, _ := e.Job(job.ID)
	t.Logf("fake backend: elapsed=%s poll_interval=%s completed_steps=%d job_status=%s active_read_delta=%d observed_timestamp_changed=%v", elapsed, pollInterval, current.CompletedSteps, current.Status, afterReads-beforeReads, !after.Observed.SampledAt.Equal(before.Observed.SampledAt))
	if elapsed < 2*pollInterval || current.Status != "running" || current.CompletedSteps < 19 {
		t.Fatalf("barrier did not cover enough active short-step execution: elapsed=%s job=%+v", elapsed, current)
	}
	if after.Connection != "online" {
		t.Fatalf("unexpected device state: %+v", after)
	}
	if afterReads <= beforeReads || !after.Observed.SampledAt.After(before.Observed.SampledAt) {
		t.Error("periodic observations were starved across many active steps shorter than PollInterval")
	}
	// Cleanup still belongs to the worker; this test never calls a backend from
	// another goroutine or assumes a client deadline interrupts native I/O.
	e.beginClose()
}
