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
	e.sequenceJobs.maxEntries = 1
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
	if _, _, err := e.SubmitSequence(ctx, "a", "new-active", fingerprint, prepareSequenceTest); !errors.Is(err, ErrSequenceJobCapacity) {
		t.Fatalf("active record must consume capacity: %v", err)
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
	if _, _, err := e.SubmitSequence(ctx, "a", "new-terminal", fingerprint, prepareSequenceTest); !errors.Is(err, ErrSequenceJobCapacity) {
		t.Fatalf("terminal record must retain capacity: %v", err)
	}
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
		t.Fatalf("expired entry retained capacity: %v", err)
	}
}

func TestSequenceIntervalAdmissionAndProgress(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{}, "set", "raw-rgb", "mk2-basic")
	interval := 50
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
