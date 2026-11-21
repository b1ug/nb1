package control

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/sequence"
)

func sequenceTestInput() sequence.Sequence {
	return sequence.Sequence{Timing: sequence.TimingSerial, Steps: []sequence.Step{
		{Targets: []sequence.Target{{LED: 0, Color: "#123456"}}},
	}}
}

func prepareSequenceTest() (sequence.Sequence, error) { return sequenceTestInput(), nil }

func startSequenceTest(t *testing.T, options Options) (*Engine, *frameIntegrationBackend) {
	t.Helper()
	e, b, _ := startFrameIntegration(t, options, "set", "program", "raw-rgb", "mk2-basic", "hold")
	return e, b
}

func awaitSequenceTerminal(t *testing.T, e *Engine, id, status string) SequenceJob {
	t.Helper()
	var job SequenceJob
	awaitEngineCondition(t, func() bool {
		var err error
		job, err = e.SequenceJob(id)
		return err == nil && job.FinishedAt != nil
	})
	if job.Status != status {
		t.Fatalf("job status %q (%s), want %q", job.Status, job.Error, status)
	}
	return job
}

func TestRoutedSequenceAdmitsOnlyTheExplicitSerial(t *testing.T) {
	e, b := startSequenceTest(t, Options{})
	prepare := func() (sequence.Sequence, SequenceRoute, error) {
		return sequenceTestInput(), SequenceRoute{Serial: "a", Source: "default", UseSingleOnline: true}, nil
	}
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "not-managed", "request", [32]byte{}, prepare); !errors.Is(err, ErrSequenceTargetAbsent) {
		t.Fatalf("explicit unmanaged target must not use the prepared default: %v", err)
	}
	if _, err := e.SequenceJob("request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected request retained a job: %v", err)
	}
	if calls, _, _, _, _, _ := b.snapshot(); len(engineSetColors(calls)) != 0 {
		t.Fatalf("rejected request wrote to the prepared default: %+v", calls)
	}
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "", "empty-binding", [32]byte{}, func() (sequence.Sequence, SequenceRoute, error) {
		return sequenceTestInput(), SequenceRoute{Source: "trigger"}, nil
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty binding must not become an implicit selection: %v", err)
	}
	job, replayed, err := e.SubmitRoutedSequence(t.Context(), "a", "request", [32]byte{}, prepare)
	if err != nil || replayed || job.Serial != "a" || job.TargetSource != "request" {
		t.Fatalf("explicit target was not recorded: %+v, replayed=%t, err=%v", job, replayed, err)
	}
	awaitSequenceTerminal(t, e, job.ID, "succeeded")
}

type routedSequenceTestDriver func(string) (Backend, error)

func (d routedSequenceTestDriver) Open(serial string) (Backend, error) { return d(serial) }

func TestRoutedSequenceFallbackAndReplayAcrossConnectivityChanges(t *testing.T) {
	backends := sequenceCacheDriver{
		"a": {engineBackend: newEngineBackend("a"), capabilities: []string{"set", "program", "raw-rgb"}},
		"b": {engineBackend: newEngineBackend("b"), capabilities: []string{"set", "program", "raw-rgb"}},
	}
	defaultAvailable := make(chan struct{})
	driver := routedSequenceTestDriver(func(serial string) (Backend, error) {
		if serial == "a" {
			select {
			case <-defaultAvailable:
			default:
				return nil, ErrOffline
			}
			if _, _, _, _, closed, _ := backends[serial].snapshot(); closed != 0 {
				return nil, ErrOffline
			}
		}
		return backends.Open(serial)
	})
	e, err := New(driver, Options{Serials: []string{"a", "b"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	awaitEngineCondition(t, func() bool {
		a, _ := e.Device("a")
		b, _ := e.Device("b")
		return a.Connection == "offline" && b.Connection == "online"
	})
	alternate := newEngineGate()
	defer alternate.open()
	backends["b"].hooks(func(Step) error { return alternate.block() }, nil, nil)
	prepare := func() (sequence.Sequence, SequenceRoute, error) {
		return sequenceTestInput(), SequenceRoute{Serial: "a", Source: "default", UseSingleOnline: true}, nil
	}
	original, replayed, err := e.SubmitRoutedSequence(t.Context(), "", "original", [32]byte{1}, prepare)
	if err != nil || replayed || original.Serial != "b" || original.TargetSource != "fallback" {
		t.Fatalf("initial fallback: %+v, replayed=%t, err=%v", original, replayed, err)
	}
	awaitEngineSignal(t, alternate.entered)
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "", "busy-alternate", [32]byte{2}, prepare); !errors.Is(err, ErrBusy) {
		t.Fatalf("an active alternate retains its busy rejection: %v", err)
	}
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "", "bound-offline", [32]byte{3}, func() (sequence.Sequence, SequenceRoute, error) {
		return sequenceTestInput(), SequenceRoute{Serial: "a", Source: "trigger", UseSingleOnline: true}, nil
	}); !errors.Is(err, ErrSequenceTargetOffline) {
		t.Fatalf("an unavailable binding must not substitute the alternate: %v", err)
	}
	// Becoming idle must not make a handle with known cleanup failure
	// eligible to admit another submission.
	backends["a"].hooks(nil, func(mode string) error {
		if mode == "off" {
			return errors.New("test cleanup failure")
		}
		return nil
	}, nil)
	close(defaultAvailable)
	awaitEngineCondition(t, func() bool {
		a, _ := e.Device("a")
		return a.Connection == "online"
	})
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "", "ambiguous", [32]byte{6}, func() (sequence.Sequence, SequenceRoute, error) {
		return sequenceTestInput(), SequenceRoute{Serial: "missing", Source: "default", UseSingleOnline: true}, nil
	}); !errors.Is(err, ErrSequenceTargetOffline) {
		t.Fatalf("an active device still counts as online when testing uniqueness: %v", err)
	}
	retry, replayed, err := e.SubmitRoutedSequence(t.Context(), "", original.ID, [32]byte{1}, func() (sequence.Sequence, SequenceRoute, error) {
		t.Error("retained job resolved its device again")
		return sequence.Sequence{}, SequenceRoute{}, ErrInvalid
	})
	if err != nil || !replayed || retry.Serial != original.Serial || retry.TargetSource != original.TargetSource || !retry.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("fallback job replay changed its accepted identity: %+v, replayed=%t, err=%v", retry, replayed, err)
	}
	fresh, replayed, err := e.SubmitRoutedSequence(t.Context(), "", "fresh", [32]byte{4}, prepare)
	if err != nil || replayed || fresh.Serial != "a" || fresh.TargetSource != "default" {
		t.Fatalf("reconnected default: %+v, replayed=%t, err=%v", fresh, replayed, err)
	}
	awaitSequenceTerminal(t, e, fresh.ID, "failed")
	a, _ := e.Device("a")
	if a.Connection != "offline" || a.ActiveJob != "" || a.QueueDepth != 0 {
		t.Fatalf("failed cleanup must leave the handle unadmittable: %+v", a)
	}
	if _, _, err := e.SubmitRoutedSequence(t.Context(), "", "after-cleanup-failure", [32]byte{5}, prepare); !errors.Is(err, ErrBusy) {
		t.Fatalf("the only online alternate is still busy: %v", err)
	}

	alternate.open()
	awaitSequenceTerminal(t, e, original.ID, "succeeded")
}

func TestSequenceReplayAfterConcurrentPreparationFailure(t *testing.T) {
	e, b := startSequenceTest(t, Options{})
	backendGate := newEngineGate()
	defer backendGate.open()
	b.hooks(func(Step) error { return backendGate.block() }, nil, nil)
	prepareGate := newEngineGate()
	defer prepareGate.open()
	ctx := context.Background()
	fingerprint := [32]byte{1}

	type result struct {
		job      SequenceJob
		replayed bool
		err      error
	}
	first := make(chan result, 1)
	go func() {
		job, replayed, err := e.SubmitSequence(ctx, "a", "turn_1", fingerprint, func() (sequence.Sequence, error) {
			prepareGate.block()
			return sequence.Sequence{}, errors.New("Pattern vanished during resolution")
		})
		first <- result{job, replayed, err}
	}()
	awaitEngineSignal(t, prepareGate.entered)
	job, replayed, err := e.SubmitSequence(ctx, "a", "turn_1", fingerprint, prepareSequenceTest)
	if err != nil || replayed || job.ID != "turn_1" || job.Status != "queued" {
		t.Fatalf("accepted job %+v, replayed %v, err %v", job, replayed, err)
	}
	awaitEngineSignal(t, backendGate.entered)
	prepareGate.open()
	select {
	case earlier := <-first:
		if earlier.err != nil || !earlier.replayed || earlier.job.ID != job.ID || earlier.job.Status != "running" {
			t.Fatalf("concurrent preparation failure did not return accepted job: %+v", earlier)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent submission did not finish")
	}

	unreachable := func() (sequence.Sequence, error) {
		t.Error("existing ID resolved its source again")
		return sequence.Sequence{}, ErrInvalid
	}
	if _, replayed, err := e.SubmitSequence(ctx, "a", job.ID, fingerprint, unreachable); err != nil || !replayed {
		t.Fatalf("matching replay while running: replayed %v, err %v", replayed, err)
	}
	if _, _, err := e.SubmitSequence(ctx, "a", job.ID, [32]byte{2}, unreachable); !errors.Is(err, ErrSequenceJobConflict) {
		t.Fatalf("different fingerprint: %v", err)
	}
	if _, _, err := e.SubmitSequence(ctx, "another-device", job.ID, fingerprint, unreachable); !errors.Is(err, ErrSequenceJobConflict) {
		t.Fatalf("different serial with identical fingerprint: %v", err)
	}
	if _, _, err := e.SubmitSequence(ctx, "a", "different-id", fingerprint, prepareSequenceTest); !errors.Is(err, ErrBusy) {
		t.Fatalf("single-owner admission: %v", err)
	}
	if _, err := e.Job(job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy Job exposed v1 record: %v", err)
	}
	cancelled, err := e.CancelSequence(ctx, job.ID)
	if err != nil || cancelled.Status != "cancelling" || cancelled.FinishedAt != nil {
		t.Fatalf("cancel before worker cleanup: %+v, %v", cancelled, err)
	}
	backendGate.open()
	awaitSequenceTerminal(t, e, job.ID, "cancelled")
}

func TestSequenceRetentionStartsAtTerminalAndSurvivesLegacyHistory(t *testing.T) {
	e, b := startSequenceTest(t, Options{MaxHistory: 1})
	clock := time.Now()
	e.mu.Lock()
	e.sequenceJobs.now = func() time.Time { return clock }
	e.mu.Unlock()
	advance := func(delta time.Duration) {
		e.mu.Lock()
		clock = clock.Add(delta)
		e.mu.Unlock()
	}
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	ctx := context.Background()
	fingerprint := [32]byte{3}
	job, _, err := e.SubmitSequence(ctx, "a", "retained", fingerprint, prepareSequenceTest)
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, gate.entered)
	advance(time.Hour)
	if active, err := e.SequenceJob(job.ID); err != nil || active.Status != "running" {
		t.Fatalf("active job expired: %+v, %v", active, err)
	}
	if _, _, err := e.SubmitSequence(ctx, "a", "new-active", fingerprint, prepareSequenceTest); !errors.Is(err, ErrBusy) {
		t.Fatalf("active device ownership: %v", err)
	}
	gate.open()
	terminal := awaitSequenceTerminal(t, e, job.ID, "succeeded")
	if terminal.TotalSteps != 1 || terminal.CompletedSteps != 1 {
		t.Fatalf("serial step progress: %+v", terminal)
	}

	legacy := submitEngineJob(t, e, "a", engineProgram("#010203", "off"), "reject")
	awaitEngineJob(t, e, legacy.ID, "succeeded")
	newer := submitEngineJob(t, e, "a", engineProgram("#040506", "off"), "reject")
	awaitEngineJob(t, e, newer.ID, "succeeded")
	if _, err := e.Job(legacy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy history did not evict its older entry: %v", err)
	}
	advance(SequenceJobRetention - time.Nanosecond)
	snapshot, replayed, err := e.SubmitSequence(ctx, "a", job.ID, fingerprint, nil)
	if err != nil || !replayed || snapshot.Status != "succeeded" || !snapshot.FinishedAt.Equal(*terminal.FinishedAt) {
		t.Fatalf("retained terminal replay: %+v, %v, %v", snapshot, replayed, err)
	}
	*snapshot.FinishedAt = time.Time{}
	cancelled, err := e.CancelSequence(ctx, job.ID)
	if err != nil || cancelled.Status != "succeeded" || !cancelled.FinishedAt.Equal(*terminal.FinishedAt) {
		t.Fatalf("terminal cancel changed the accepted receipt: %+v, %v", cancelled, err)
	}
	advance(time.Nanosecond)
	if _, err := e.SequenceJob(job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read/replay/cancel refreshed expiration: %v", err)
	}
	if _, _, err := e.SubmitSequence(ctx, "a", "new-terminal", fingerprint, prepareSequenceTest); err != nil {
		t.Fatalf("new Job admission failed: %v", err)
	}
}

func TestSequenceIntervalAdmissionAndProgress(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{}, "set", "raw-rgb", "mk2-basic")
	// This wall-clock test checks admission, logical progress and write order.
	// Leave room for hosted Windows/race timer latency; exact interval deadlines
	// are checked separately with the deterministic clock in frame_schedule_test.
	interval := 500
	input := sequence.Sequence{Timing: sequence.TimingInterval, Repeat: 2, OnFinish: "hold",
		Initial: []sequence.Target{{LED: 1, Color: "#112233"}, {LED: 2, Color: "#445566"}},
		Steps: []sequence.Step{{FadeMS: 50, IntervalMS: &interval, Targets: []sequence.Target{
			{LED: 1, Color: "#778899"}, {LED: 2, Color: "#aabbcc"},
		}}}}
	prepare := func() (sequence.Sequence, error) { return input, nil }
	if _, _, err := e.SubmitSequence(context.Background(), "a", "interval", [32]byte{}, prepare); !errors.Is(err, ErrSequenceCapability) {
		t.Fatalf("hold without cached capability: %v", err)
	}
	input.OnFinish = "off"
	job, replayed, err := e.SubmitSequence(context.Background(), "a", "", [32]byte{}, prepare)
	if err != nil || replayed || !regexp.MustCompile(`^j_[0-9A-HJKMNP-TV-Z]{10}$`).MatchString(job.ID) {
		t.Fatalf("generated job ID %q, replayed %v, err %v", job.ID, replayed, err)
	}
	terminal := awaitSequenceTerminal(t, e, job.ID, "succeeded")
	if terminal.CompletedSteps != 2 || terminal.TotalSteps != 2 {
		t.Fatalf("expected logical steps independent of initial/target writes: %+v", terminal)
	}
	calls, _, _, _, _, _ := b.snapshot()
	var writes []Step
	for _, call := range calls {
		if call.kind == "set" {
			writes = append(writes, call.step)
		}
	}
	want := []Step{{LED: 1, Color: "#112233"}, {LED: 2, Color: "#445566"},
		{LED: 1, Color: "#778899", FadeMS: 50}, {LED: 2, Color: "#aabbcc", FadeMS: 50},
		{LED: 1, Color: "#778899", FadeMS: 50}, {LED: 2, Color: "#aabbcc", FadeMS: 50}}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("interval writes %+v, want %+v", writes, want)
	}
	if _, err := e.FrameJob(job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy FrameJob exposed v1 record: %v", err)
	}
}

func TestSequenceJobIDCollisionProtection(t *testing.T) {
	e, _ := startSequenceTest(t, Options{})
	ctx := context.Background()

	const zeroID = "j_0000000000"
	if _, _, err := e.SubmitSequence(ctx, "a", zeroID, [32]byte{}, prepareSequenceTest); err != nil {
		t.Fatal(err)
	}
	awaitSequenceTerminal(t, e, zeroID, "succeeded")
	random := make([]byte, 14)
	random[len(random)-1] = 1
	e.mu.Lock()
	e.sequenceJobs.random = bytes.NewReader(random)
	e.mu.Unlock()
	generated, _, err := e.SubmitSequence(ctx, "a", "", [32]byte{}, prepareSequenceTest)
	if err != nil || generated.ID != "j_0000000001" {
		t.Fatalf("random collision did not try an unused ID: %+v, %v", generated, err)
	}
}

func submitScheduledColor(t *testing.T, e *Engine, serial, id, policy, color string) {
	t.Helper()
	job, replay, err := e.SubmitSequenceWithPolicy(t.Context(), serial, id, [32]byte{}, policy, func() (sequence.Sequence, error) {
		input := sequenceTestInput()
		input.Steps[0].Targets[0].Color = color
		return input, nil
	})
	if err != nil || replay || job.ID != id || job.Serial != serial || job.Status != "queued" {
		t.Fatalf("submit %s on %s with %s: %+v, replay=%t err=%v", id, serial, policy, job, replay, err)
	}
}

func TestSequencePreemptionPreservesFIFOAndDeviceIsolation(t *testing.T) {
	driver := sequenceCacheDriver{
		"a": {engineBackend: newEngineBackend("a"), capabilities: []string{"set", "program", "raw-rgb"}},
		"b": {engineBackend: newEngineBackend("b"), capabilities: []string{"set", "program", "raw-rgb"}},
	}
	e, err := New(driver, Options{Serials: []string{"a", "b"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	awaitEngineCondition(t, func() bool {
		a, _ := e.Device("a")
		b, _ := e.Device("b")
		return a.Connection == "online" && b.Connection == "online"
	})
	write, off := newEngineGate(), newEngineGate()
	defer write.open()
	defer off.open()
	driver["a"].hooks(func(Step) error { return write.block() }, func(mode string) error {
		if mode == "off" {
			return off.block()
		}
		return nil
	}, nil)

	submitScheduledColor(t, e, "a", "old", "reject", "#110000")
	awaitEngineSignal(t, write.entered)
	submitScheduledColor(t, e, "a", "first", "queue", "#220000")
	submitScheduledColor(t, e, "a", "second", "queue", "#330000")
	// Resolve an implicit trigger binding only once, even if it changes while
	// hardware cleanup is pending and the original request is retried.
	route := func() (sequence.Sequence, SequenceRoute, error) {
		input := sequenceTestInput()
		input.Steps[0].Targets[0].Color = "#440000"
		return input, SequenceRoute{Serial: "a", Source: "trigger"}, nil
	}
	urgent, replay, err := e.SubmitRoutedSequenceWithPolicy(t.Context(), "", "urgent", [32]byte{}, "preempt", route)
	if err != nil || replay || urgent.Serial != "a" || urgent.TargetSource != "trigger" {
		t.Fatalf("admit urgent: %+v, replay=%t err=%v", urgent, replay, err)
	}
	urgent, replay, err = e.SubmitRoutedSequenceWithPolicy(t.Context(), "", "urgent", [32]byte{}, "preempt", nil)
	if err != nil || !replay || urgent.Serial != "a" || urgent.Status != "queued" {
		t.Fatalf("replay pending urgent: %+v, replay=%t err=%v", urgent, replay, err)
	}
	if _, _, err := e.SubmitRoutedSequenceWithPolicy(t.Context(), "", "urgent", [32]byte{}, "queue", nil); !errors.Is(err, ErrSequenceJobConflict) {
		t.Fatalf("reuse same ID with a different scheduling policy: %v", err)
	}
	write.open()
	awaitEngineSignal(t, off.entered)
	old, err := e.SequenceJob("old")
	if err != nil || old.Status != "cancelling" || old.FinishedAt != nil {
		t.Fatalf("old job while native off is blocked: %+v %v", old, err)
	}
	submitScheduledColor(t, e, "a", "third", "queue", "#550000")
	submitScheduledColor(t, e, "b", "independent", "reject", "#0000bb")
	awaitSequenceTerminal(t, e, "independent", "succeeded")
	if calls, _, _, _, _, _ := driver["a"].snapshot(); !reflect.DeepEqual(engineSetColors(calls), []string{"#110000"}) {
		t.Fatalf("A wrote while its cleanup was blocked: %+v", calls)
	}

	off.open()
	old = awaitSequenceTerminal(t, e, "old", "cancelled")
	for _, id := range []string{"urgent", "first", "second", "third"} {
		next := awaitSequenceTerminal(t, e, id, "succeeded")
		if next.StartedAt.Before(*old.FinishedAt) {
			t.Fatalf("%s started before %s completed", next.ID, old.ID)
		}
		old = next
	}
	calls, _, _, simultaneous, _, _ := driver["a"].snapshot()
	want := []string{"#110000", "#440000", "#220000", "#330000", "#550000"}
	if got := engineSetColors(calls); !reflect.DeepEqual(got, want) || simultaneous != 1 {
		t.Fatalf("serialized device A writes = %v, want %v (max in flight %d)", got, want, simultaneous)
	}
}

func TestSequencePreemptionRefusalLeavesRunningJobIntact(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{QueueSize: 1}, "set", "program", "raw-rgb")
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	submitScheduledColor(t, e, "a", "running", "reject", "#110000")
	awaitEngineSignal(t, gate.entered)
	ctx := t.Context()
	checkRefused := func(id, policy string, prepare func() (sequence.Sequence, error), want error) {
		t.Helper()
		if _, _, err := e.SubmitSequenceWithPolicy(ctx, "a", id, [32]byte{}, policy, prepare); !errors.Is(err, want) {
			t.Fatalf("submit %q: %v, want %v", policy, err, want)
		}
		if _, err := e.SequenceJob(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("rejected ID %s was registered", id)
		}
	}
	checkRefused("legacy", "replace", prepareSequenceTest, ErrInvalid)
	checkRefused("normal", "reject", prepareSequenceTest, ErrBusy)
	checkRefused("invalid", "preempt", func() (sequence.Sequence, error) { return sequence.Sequence{}, nil }, ErrInvalid)
	checkRefused("missing-hold", "preempt", func() (sequence.Sequence, error) {
		input := sequenceTestInput()
		input.OnFinish = "hold"
		return input, nil
	}, ErrSequenceCapability)
	submitScheduledColor(t, e, "a", "waiting", "queue", "#220000")
	checkRefused("full-queue", "queue", prepareSequenceTest, ErrQueueFull)
	checkRefused("full-urgent", "preempt", prepareSequenceTest, ErrQueueFull)
	old, err := e.SequenceJob("running")
	if err != nil || old.Status != "running" {
		t.Fatalf("refusal disrupted active Job: %+v %v", old, err)
	}
	gate.open()
	awaitSequenceTerminal(t, e, "running", "succeeded")
	awaitSequenceTerminal(t, e, "waiting", "succeeded")
}

func TestSequenceConcurrentPreemptionAndCancellationDuringCleanup(t *testing.T) {
	e, b := startSequenceTest(t, Options{QueueSize: 2})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	submitScheduledColor(t, e, "a", "old", "reject", "#110000")
	awaitEngineSignal(t, gate.entered)
	submitScheduledColor(t, e, "a", "waiting", "queue", "#220000")
	type result struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, id := range []string{"urgent1", "urgent2"} {
		go func() {
			<-start
			_, _, err := e.SubmitSequenceWithPolicy(t.Context(), "a", id, [32]byte{}, "preempt", prepareSequenceTest)
			results <- result{id, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, ErrPreemptionPending) {
		t.Fatalf("competing preemptions: %+v, %+v", first, second)
	}
	cancelled, err := e.CancelSequence(t.Context(), first.id)
	if err != nil || cancelled.Status != "cancelled" || cancelled.StartedAt != nil {
		t.Fatalf("cancel waiting urgent: %+v, %v", cancelled, err)
	}
	if _, _, err := e.SubmitSequenceWithPolicy(t.Context(), "a", second.id, [32]byte{}, "preempt", prepareSequenceTest); !errors.Is(err, ErrPreemptionPending) {
		t.Fatalf("preempted Job still owns the device: %v", err)
	}
	gate.open()
	awaitSequenceTerminal(t, e, "old", "cancelled")
	awaitSequenceTerminal(t, e, "waiting", "succeeded")
	submitScheduledColor(t, e, "a", second.id, "preempt", "#330000")
	awaitSequenceTerminal(t, e, second.id, "succeeded")
	calls, _, _, _, _, _ := b.snapshot()
	if got := engineSetColors(calls); !reflect.DeepEqual(got, []string{"#110000", "#220000", "#330000"}) {
		t.Fatalf("cancelled urgent job changed the output: %v", got)
	}
}

func TestSequencePreemptionCleanupFailureAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "cleanup readback fails"
		if shutdown {
			name = "shutdown during preemption"
		}
		t.Run(name, func(t *testing.T) {
			e, b := startSequenceTest(t, Options{ShutdownMode: "hold"})
			gate := newEngineGate()
			defer gate.open()
			b.hooks(func(Step) error { return gate.block() }, nil, nil)
			submitScheduledColor(t, e, "a", "old", "reject", "#110000")
			awaitEngineSignal(t, gate.entered)
			submitScheduledColor(t, e, "a", "waiting", "queue", "#220000")
			submitScheduledColor(t, e, "a", "urgent", "preempt", "#330000")
			wantActive, wantPending := "failed", "failed"
			if shutdown {
				e.beginClose()
				wantActive, wantPending = "cancelled", "cancelled"
			} else {
				b.hooks(nil, nil, func() error { return errors.New("lost during cleanup") })
			}
			gate.open()
			awaitSequenceTerminal(t, e, "old", wantActive)
			for _, id := range []string{"urgent", "waiting"} {
				if job := awaitSequenceTerminal(t, e, id, wantPending); job.StartedAt != nil {
					t.Fatalf("%s began after %s", id, name)
				}
			}
			calls, _, _, simultaneous, _, _ := b.snapshot()
			if colors := engineSetColors(calls); !reflect.DeepEqual(colors, []string{"#110000"}) || simultaneous != 1 {
				t.Fatalf("unexpected hardware writes %v, max in flight %d", colors, simultaneous)
			}
		})
	}
}
