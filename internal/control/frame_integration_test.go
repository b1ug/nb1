package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These are fake native handles. No test opens USB or performs discovery.
type frameIntegrationBackend struct {
	*engineBackend
	capabilities []string
}

func (b *frameIntegrationBackend) Info() DeviceInfo {
	return DeviceInfo{Serial: b.serial, Product: "fake mk2", Firmware: 204,
		Capabilities: append([]string(nil), b.capabilities...)}
}

type frameIntegrationDriver struct{ backend *frameIntegrationBackend }

func (d frameIntegrationDriver) Open(serial string) (Backend, error) {
	_, _, _, _, closed, _ := d.backend.snapshot()
	if serial != d.backend.serial || closed != 0 {
		return nil, ErrOffline
	}
	return d.backend, nil
}

func startFrameIntegration(t *testing.T, options Options, capabilities ...string) (*Engine, *frameIntegrationBackend, *receiptClock) {
	t.Helper()
	if capabilities == nil {
		capabilities = []string{"set", "raw-rgb", "mk2-basic", "hold"}
	}
	b := &frameIntegrationBackend{engineBackend: newEngineBackend("a"), capabilities: capabilities}
	options.Serials = []string{"a"}
	if options.PollInterval == 0 {
		options.PollInterval = time.Hour
	}
	e, err := New(frameIntegrationDriver{b}, options)
	if err != nil {
		t.Fatal(err)
	}
	clock := &receiptClock{ms: 100}
	e.receipts.nowMS = clock.now
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); errors.Is(err, context.DeadlineExceeded) {
			t.Error("frame integration fake remained blocked during cleanup")
		}
	})
	awaitEngineCondition(t, func() bool {
		state, _ := e.Device("a")
		return state.Connection == "online"
	})
	return e, b, clock
}

func frameIntegrationProgram() FrameProgram {
	p := frameTestProgram()
	p.Frames[0].FadeMS, p.Frames[0].IntervalMS = 50, 50
	return p
}

func submitFrameIntegration(t *testing.T, e *Engine, p FrameProgram, ticket ReceiptRequest) FrameReceiptResult {
	t.Helper()
	result, err := e.SubmitFrameWithReceipt(context.Background(), "a", p, "reject", ticket)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func awaitFrameIntegration(t *testing.T, e *Engine, id, status string) FrameJob {
	t.Helper()
	var job FrameJob
	awaitEngineCondition(t, func() bool {
		var err error
		job, err = e.FrameJob(id)
		return err == nil && job.FinishedAt != nil
	})
	if job.Status != status {
		t.Fatalf("frame job=%s (%s), want %s", job.Status, job.Error, status)
	}
	return job
}

func TestFrameIntegrationQuiesceRepeatNoReadsAndCleanupBeforeTerminal(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{PollInterval: 10 * time.Millisecond})
	readGate := newEngineGate()
	defer readGate.open()
	var cleaning int32
	b.hooks(nil, func(mode string) error {
		if mode == "off" {
			atomic.StoreInt32(&cleaning, 1)
		}
		return nil
	}, func() error {
		if atomic.LoadInt32(&cleaning) != 0 {
			return readGate.block()
		}
		return nil
	})
	p := frameIntegrationProgram()
	p.Repeat = 2
	accepted := submitFrameIntegration(t, e, p, receiptTicket(e, 1))
	awaitEngineSignal(t, readGate.entered)
	beforeRead, err := e.FrameJob(accepted.Job.ID)
	if err != nil || beforeRead.FinishedAt != nil || beforeRead.Status != "running" || beforeRead.CompletedFrames != 2 || beforeRead.CompletedTargetWrites != 6 {
		t.Fatalf("job completed before final readback: %#v err=%v", beforeRead, err)
	}
	calls, _, _, maxInFlight, _, unsafe := b.snapshot()
	var writes []Step
	quiesce, off := -1, -1
	for i, call := range calls {
		if call.kind == "stop" && call.mode == "hold" && quiesce < 0 {
			quiesce = i
		}
		if call.kind == "stop" && call.mode == "off" && quiesce >= 0 {
			off = i
		}
		if call.kind == "set" {
			writes = append(writes, call.step)
		}
	}
	if quiesce < 0 || off <= quiesce || maxInFlight != 1 || unsafe {
		t.Fatalf("missing ownership/quiesce/cleanup ordering: %#v", calls)
	}
	for _, call := range calls[quiesce+1 : off] {
		if call.kind == "read" {
			t.Fatal("periodic read interrupted the frame schedule")
		}
	}
	wantColors := []string{"#990000", "#000099", "#000099", "#990000", "#000099", "#990000"}
	var colors []string
	for i, step := range writes {
		colors = append(colors, step.Color)
		if (i < 2 && step.FadeMS != 0) || (i >= 2 && step.FadeMS != 50) {
			t.Fatalf("initial was repeated or target fade changed: %#v", writes)
		}
	}
	if !reflect.DeepEqual(colors, wantColors) {
		t.Fatalf("wrong repeat/init sequence: %v", colors)
	}
	readGate.open()
	job := awaitFrameIntegration(t, e, accepted.Job.ID, "succeeded")
	if job.TotalFrames != 2 || job.PlannedTargetWrites != 6 || job.CompletedFrames != 2 || job.CompletedTargetWrites != 6 || job.StartedAt == nil {
		t.Fatalf("incorrect final counters: %#v", job)
	}
	state, _ := e.Device("a")
	if state.ActiveJob != "" || state.Observed == nil || state.Observed.LED1 != "#000000" || state.Observed.LED2 != "#000000" {
		t.Fatalf("terminal job did not publish confirmed off: %#v", state)
	}
}

func TestFrameIntegrationConcurrentReplayAndCrossKindConflict(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	p, ticket := frameIntegrationProgram(), receiptTicket(e, 2)
	start := make(chan struct{})
	results := make(chan FrameReceiptResult, 12)
	errorsFound := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := e.SubmitFrameWithReceipt(context.Background(), "a", p, "reject", ticket)
			results <- result
			errorsFound <- err
		}()
	}
	close(start)
	allReturned := make(chan struct{})
	go func() { wg.Wait(); close(allReturned) }()
	awaitEngineSignal(t, allReturned)
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	fresh := 0
	for result := range results {
		if id == "" {
			id = result.Job.ID
		}
		if result.Job.ID != id || result.DaemonInstanceID != ticket.ExpectedDaemonInstanceID {
			t.Fatalf("replay changed identity: %#v", result)
		}
		if !result.Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("admitted %d distinct executions", fresh)
	}
	awaitEngineSignal(t, gate.entered)
	assertReceiptUsage(t, e, 1, 4)
	if _, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "reject", ticket); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("legacy payload reused a frame receipt: %v", err)
	}
	if _, err := e.Job(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("frame escaped through legacy Job: %v", err)
	}
	if _, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("frame escaped through legacy Request: %v", err)
	}
	mutated := cloneFrameProgram(p)
	mutated.Initial[0].Color = "#ABCDEF"
	if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", mutated, "reject", ticket); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("changed frame payload replayed: %v", err)
	}
	view, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	view.Program.Initial[0].Color = "#ffffff"
	view.Program.Frames[0].Targets[0].Color = "#ffffff"
	again, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
	if err != nil || again.Program.Initial[0].Color != "#990000" || again.Program.Frames[0].Targets[0].Color != "#000099" {
		t.Fatalf("frame receipt shared nested program memory: %#v, %v", again, err)
	}
	gate.open()
	awaitFrameIntegration(t, e, id, "succeeded")
	calls, _, _, _, _, _ := b.snapshot()
	if len(engineSetColors(calls)) != 4 {
		t.Fatal("receipt replay executed the frame twice")
	}
}

func TestFrameIntegrationLegacyReceiptCannotBecomeFrame(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	ticket := receiptTicket(e, 3)
	accepted, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "reject", ticket)
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineSignal(t, gate.entered)
	if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", ticket); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("frame payload reused a legacy receipt: %v", err)
	}
	if _, err := e.FrameJob(accepted.Job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy escaped through FrameJob: %v", err)
	}
	if _, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy escaped through FrameRequest: %v", err)
	}
	if _, err := e.CancelFrame(context.Background(), accepted.Job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Frame cancellation accepted a legacy job: %v", err)
	}
	if job, err := e.Job(accepted.Job.ID); err != nil || job.Status != "running" {
		t.Fatalf("wrong-kind cancellation changed legacy work: %#v, %v", job, err)
	}
	assertReceiptUsage(t, e, 1, 1)
	gate.open()
	awaitEngineJob(t, e, accepted.Job.ID, "succeeded")
}

func TestFrameIntegrationSharedCapacityAndRetention(t *testing.T) {
	for _, firstKind := range []string{"frame", "legacy"} {
		for _, bound := range []string{"entries", "targets"} {
			t.Run(firstKind+"-"+bound, func(t *testing.T) {
				e, _, clock := startFrameIntegration(t, Options{})
				e.mu.Lock()
				if bound == "entries" {
					e.receipts.maxEntries = 1
				} else {
					e.receipts.maxSteps = 4
				}
				e.mu.Unlock()
				ticket := receiptTicket(e, 10)
				p := frameIntegrationProgram()
				p.Repeat = 2 // Charge four stored targets, not six executed writes.
				if firstKind == "frame" {
					first := submitFrameIntegration(t, e, p, ticket)
					awaitFrameIntegration(t, e, first.Job.ID, "succeeded")
					if _, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "reject", receiptTicket(e, 11)); !errors.Is(err, ErrReceiptCapacity) {
						t.Fatalf("legacy ignored frame capacity: %v", err)
					}
					if replay := submitFrameIntegration(t, e, p, ticket); !replay.Replayed || replay.Job.ID != first.Job.ID {
						t.Fatal("full capacity rejected frame replay")
					}
					assertReceiptUsage(t, e, 1, 4)
				} else {
					first, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "reject", ticket)
					if err != nil {
						t.Fatal(err)
					}
					awaitEngineJob(t, e, first.Job.ID, "succeeded")
					if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", p, "reject", receiptTicket(e, 11)); !errors.Is(err, ErrReceiptCapacity) {
						t.Fatalf("frame ignored legacy capacity: %v", err)
					}
					assertReceiptUsage(t, e, 1, 1)
				}
				clock.advance(60000)
				if firstKind == "frame" {
					if _, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
						t.Fatalf("frame receipt did not expire: %v", err)
					}
				} else if _, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
					t.Fatalf("legacy receipt did not expire: %v", err)
				}
				assertReceiptUsage(t, e, 0, 0)
				fresh := submitFrameIntegration(t, e, frameIntegrationProgram(), receiptTicket(e, 12))
				awaitFrameIntegration(t, e, fresh.Job.ID, "succeeded")
				assertReceiptUsage(t, e, 1, 4)
			})
		}
	}
}

func TestFrameIntegrationHistoryEvictionAndClosedReplay(t *testing.T) {
	e, _, _ := startFrameIntegration(t, Options{MaxHistory: 1})
	ticket := receiptTicket(e, 20)
	p := frameIntegrationProgram()
	first := submitFrameIntegration(t, e, p, ticket)
	awaitFrameIntegration(t, e, first.Job.ID, "succeeded")
	second := submitEngineJob(t, e, "a", engineProgram("#112233", "off"), "queue")
	awaitEngineJob(t, e, second.ID, "succeeded")
	if _, err := e.FrameJob(first.Job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("normal history did not evict frame: %v", err)
	}
	view, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
	if err != nil || view.ID != first.Job.ID || view.Status != "succeeded" {
		t.Fatalf("history eviction lost frame receipt: %#v, %v", view, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if replay := submitFrameIntegration(t, e, p, ticket); !replay.Replayed || replay.Job.ID != first.Job.ID {
		t.Fatal("closed engine lost frame receipt")
	}
	if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", p, "reject", receiptTicket(e, 21)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed engine accepted a new frame: %v", err)
	}
}

func TestFrameIntegrationLegacyReplaceWaitsForFrameOff(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(step Step) error {
		if step.Color == "#990000" && step.FadeMS == 0 {
			return gate.block()
		}
		return nil
	}, nil, nil)
	first := submitFrameIntegration(t, e, frameIntegrationProgram(), receiptTicket(e, 30))
	awaitEngineSignal(t, gate.entered)
	legacy := submitEngineJob(t, e, "a", engineProgram("#123456", "hold"), "replace")
	view, _ := e.FrameJob(first.Job.ID)
	if view.Status != "cancelling" || view.FinishedAt != nil {
		t.Fatalf("replace skipped in-flight frame cancellation: %#v", view)
	}
	calls, _, _, _, closed, _ := b.snapshot()
	if closed != 0 || len(engineSetColors(calls)) != 1 {
		t.Fatal("replacement used or closed the busy native handle")
	}
	gate.open()
	awaitFrameIntegration(t, e, first.Job.ID, "cancelled")
	awaitEngineJob(t, e, legacy.ID, "succeeded")
	calls, _, _, max, _, unsafe := b.snapshot()
	off, replacement := -1, -1
	for i, call := range calls {
		if call.kind == "stop" && call.mode == "off" && off < 0 {
			off = i
		}
		if call.kind == "set" && call.step.Color == "#123456" {
			replacement = i
		}
	}
	if off < 0 || replacement <= off || max != 1 || unsafe {
		t.Fatalf("legacy started before frame off cleanup: %#v", calls)
	}
}

func TestFrameIntegrationCancelPreservesReceiptAndWaitsForNativeReturn(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	p, ticket := frameIntegrationProgram(), receiptTicket(e, 35)
	first := submitFrameIntegration(t, e, p, ticket)
	awaitEngineSignal(t, gate.entered)
	for i := 0; i < 2; i++ {
		job, err := e.CancelFrame(context.Background(), first.Job.ID)
		if err != nil || job.Status != "cancelling" || job.FinishedAt != nil {
			t.Fatalf("cancel acknowledged terminal before native return: %#v, %v", job, err)
		}
	}
	calls, _, _, _, closed, unsafe := b.snapshot()
	if len(engineSetColors(calls)) != 1 || closed != 0 || unsafe {
		t.Fatal("cancel closed or advanced the blocked native handle")
	}
	gate.open()
	finished := awaitFrameIntegration(t, e, first.Job.ID, "cancelled")
	if finished.CompletedTargetWrites != 1 || finished.CompletedFrames != 0 {
		t.Fatalf("cancellation lost the successful in-flight initial write: %#v", finished)
	}
	replay := submitFrameIntegration(t, e, p, ticket)
	if !replay.Replayed || replay.Job.Status != "cancelled" || replay.Job.ID != first.Job.ID {
		t.Fatalf("cancelled receipt became new work: %#v", replay)
	}
	calls, led1, led2, _, _, _ := b.snapshot()
	if len(engineSetColors(calls)) != 1 || led1 != "#000000" || led2 != "#000000" {
		t.Fatalf("cancel failed off cleanup or resumed targets: %#v", calls)
	}
}

func TestFrameIntegrationCloseRetainsNativeHandleUntilReturn(t *testing.T) {
	e, b, _ := startFrameIntegration(t, Options{})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	first := submitFrameIntegration(t, e, frameIntegrationProgram(), receiptTicket(e, 40))
	awaitEngineSignal(t, gate.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close pretended to interrupt native Set: %v", err)
	}
	_, _, _, max, closed, unsafe := b.snapshot()
	if max != 1 || closed != 0 || unsafe {
		t.Fatalf("closed a busy native handle: max=%d closed=%d unsafe=%v", max, closed, unsafe)
	}
	view, _ := e.FrameJob(first.Job.ID)
	if view.Status != "cancelling" || view.FinishedAt != nil {
		t.Fatalf("in-flight frame reported terminal shutdown: %#v", view)
	}
	gate.open()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if err := e.Close(ctx2); err != nil {
		t.Fatal(err)
	}
	finished := awaitFrameIntegration(t, e, first.Job.ID, "cancelled")
	if finished.CompletedTargetWrites != 1 || finished.CompletedFrames != 0 {
		t.Fatalf("shutdown lost the successful in-flight initial write: %#v", finished)
	}
	calls, _, _, _, closed, unsafe := b.snapshot()
	if closed != 1 || unsafe || len(engineSetColors(calls)) != 1 {
		t.Fatalf("shutdown resumed frame writes or leaked the handle: %#v", calls)
	}
}

func TestFrameIntegrationFailuresNeverReportSuccess(t *testing.T) {
	for _, operation := range []string{"set", "stop", "read"} {
		t.Run(operation, func(t *testing.T) {
			e, b, _ := startFrameIntegration(t, Options{})
			failure := fmt.Errorf("synthetic frame %s failure", operation)
			var cleanup int32
			b.hooks(func(Step) error {
				if operation == "set" {
					return failure
				}
				return nil
			}, func(mode string) error {
				if mode == "off" {
					atomic.StoreInt32(&cleanup, 1)
					if operation == "stop" {
						return failure
					}
				}
				return nil
			}, func() error {
				if operation == "read" && atomic.LoadInt32(&cleanup) != 0 {
					return failure
				}
				return nil
			})
			accepted := submitFrameIntegration(t, e, frameIntegrationProgram(), receiptTicket(e, 50))
			job := awaitFrameIntegration(t, e, accepted.Job.ID, "failed")
			if !strings.Contains(job.Error, failure.Error()) {
				t.Fatalf("lost execution/cleanup error: %#v", job)
			}
			awaitEngineCondition(t, func() bool {
				state, _ := e.Device("a")
				_, _, _, _, closed, _ := b.snapshot()
				return state.Connection == "offline" && closed == 1
			})
			if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", receiptTicket(e, 51)); !errors.Is(err, ErrOffline) {
				t.Fatalf("known offline handle admitted another frame: %v", err)
			}
			_, _, _, max, closed, unsafe := b.snapshot()
			if max != 1 || closed != 1 || unsafe {
				t.Fatalf("failed frame handle ownership: max=%d closed=%d unsafe=%v", max, closed, unsafe)
			}
		})
	}
}

func TestFrameIntegrationRejectsUnsupportedAdmissionWithoutWrites(t *testing.T) {
	t.Run("capabilities", func(t *testing.T) {
		e, b, _ := startFrameIntegration(t, Options{}, "set", "raw-rgb")
		if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", receiptTicket(e, 60)); err == nil {
			t.Fatal("backend without mk2-basic capability admitted a frame")
		}
		calls, _, _, _, _, _ := b.snapshot()
		if len(engineSetColors(calls)) != 0 {
			t.Fatal("capability rejection wrote a device target")
		}
		assertReceiptUsage(t, e, 0, 0)
	})
	t.Run("policy-and-context", func(t *testing.T) {
		e, b, _ := startFrameIntegration(t, Options{})
		for i, policy := range []string{"", "queue", "replace", "unknown"} {
			if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), policy, receiptTicket(e, 70+i)); err == nil {
				t.Fatalf("unsupported frame policy %q admitted", policy)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.SubmitFrameWithReceipt(ctx, "a", frameIntegrationProgram(), "reject", receiptTicket(e, 80)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled admission=%v", err)
		}
		calls, _, _, _, _, _ := b.snapshot()
		if len(engineSetColors(calls)) != 0 {
			t.Fatal("invalid admission wrote a device target")
		}
		assertReceiptUsage(t, e, 0, 0)
	})
}
