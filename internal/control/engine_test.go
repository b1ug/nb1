package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type engineCall struct {
	kind string
	mode string
	step Step
}

type engineBackend struct {
	mu             sync.Mutex
	serial         string
	led1, led2     string
	calls          []engineCall
	inFlight       int
	maxInFlight    int
	closeWhileBusy bool
	closeCount     int
	afterClose     bool
	setHook        func(Step) error
	stopHook       func(string) error
	readHook       func() error
}

func newEngineBackend(serial string) *engineBackend {
	return &engineBackend{serial: serial, led1: "#000000", led2: "#000000"}
}

func (b *engineBackend) Info() DeviceInfo {
	return DeviceInfo{Serial: b.serial, Product: "fake blink", Capabilities: []string{"feature", "stop_hold"}}
}

func (b *engineBackend) begin(call engineCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call)
	b.inFlight++
	if b.inFlight > b.maxInFlight {
		b.maxInFlight = b.inFlight
	}
	if b.closeCount > 0 {
		b.afterClose = true
	}
}

func (b *engineBackend) end() {
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
}

func (b *engineBackend) Set(step Step) error {
	b.begin(engineCall{kind: "set", step: step})
	defer b.end()
	b.mu.Lock()
	hook := b.setHook
	b.mu.Unlock()
	if hook != nil {
		if err := hook(step); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if step.LED == 0 || step.LED == 1 {
		b.led1 = step.Color
	}
	if step.LED == 0 || step.LED == 2 {
		b.led2 = step.Color
	}
	return nil
}

func (b *engineBackend) Stop(mode string) error {
	b.begin(engineCall{kind: "stop", mode: mode})
	defer b.end()
	b.mu.Lock()
	hook := b.stopHook
	b.mu.Unlock()
	if hook != nil {
		if err := hook(mode); err != nil {
			return err
		}
	}
	if mode == "off" {
		b.mu.Lock()
		b.led1, b.led2 = "#000000", "#000000"
		b.mu.Unlock()
	}
	return nil
}

func (b *engineBackend) Read() (Observation, error) {
	b.begin(engineCall{kind: "read"})
	defer b.end()
	b.mu.Lock()
	hook := b.readHook
	b.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return Observation{}, err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now().UTC()}, nil
}

func (b *engineBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeWhileBusy = b.closeWhileBusy || b.inFlight != 0
	b.closeCount++
	b.calls = append(b.calls, engineCall{kind: "close"})
}

func (b *engineBackend) hooks(set func(Step) error, stop func(string) error, read func() error) {
	b.mu.Lock()
	b.setHook, b.stopHook, b.readHook = set, stop, read
	b.mu.Unlock()
}

func (b *engineBackend) snapshot() ([]engineCall, string, string, int, int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]engineCall(nil), b.calls...), b.led1, b.led2, b.maxInFlight, b.closeCount, b.closeWhileBusy || b.afterClose
}

type engineDriver map[string]*engineBackend

func (d engineDriver) Open(serial string) (Backend, error) {
	if backend := d[serial]; backend != nil {
		backend.mu.Lock()
		closed := backend.closeCount != 0
		backend.mu.Unlock()
		if closed {
			return nil, fmt.Errorf("fake device %s remains offline", serial)
		}
		return backend, nil
	}
	return nil, fmt.Errorf("missing fake device %s", serial)
}

type engineGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
}

func newEngineGate() *engineGate {
	return &engineGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *engineGate) block() error {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return nil
}

func (g *engineGate) open() { g.unblock.Do(func() { close(g.release) }) }

func awaitEngineSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fake native I/O")
	}
}

func awaitEngineCondition(t *testing.T, check func() bool) {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for engine state")
		}
	}
}

func startEngineTest(t *testing.T, options Options, backends ...*engineBackend) *Engine {
	t.Helper()
	driver := make(engineDriver)
	for _, backend := range backends {
		driver[backend.serial] = backend
	}
	if len(options.Serials) == 0 {
		for _, backend := range backends {
			options.Serials = append(options.Serials, backend.serial)
		}
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Hour // Isolate explicit calls from idle polling.
	}
	e, err := New(driver, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); errors.Is(err, context.DeadlineExceeded) {
			t.Error("fake backend still blocked during test cleanup")
		}
	})
	for _, backend := range backends {
		serial := backend.serial
		awaitEngineCondition(t, func() bool {
			state, _ := e.Device(serial)
			return state.Connection == "online"
		})
	}
	return e
}

func engineProgram(color, finish string) Program {
	return Program{Steps: []Step{{Color: color}}, OnFinish: finish}
}

func submitEngineJob(t *testing.T, e *Engine, serial string, program Program, policy string) Job {
	t.Helper()
	job, err := e.Submit(context.Background(), serial, program, policy)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func awaitEngineJob(t *testing.T, e *Engine, id, status string) Job {
	t.Helper()
	var job Job
	awaitEngineCondition(t, func() bool {
		job, _ = e.Job(id)
		return job.FinishedAt != nil
	})
	if job.Status != status {
		t.Fatalf("job status = %s (%s), want %s", job.Status, job.Error, status)
	}
	return job
}

func engineSetColors(calls []engineCall) []string {
	var colors []string
	for _, call := range calls {
		if call.kind == "set" {
			colors = append(colors, call.step.Color)
		}
	}
	return colors
}

func TestEngineSerialDeviceExecutionAndParallelDevices(t *testing.T) {
	a, b := newEngineBackend("a"), newEngineBackend("b")
	e := startEngineTest(t, Options{}, a, b)
	gate := newEngineGate()
	defer gate.open()
	a.hooks(func(step Step) error {
		if step.Color == "#ff0000" {
			return gate.block()
		}
		return nil
	}, nil, nil)
	first := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	second := submitEngineJob(t, e, "a", engineProgram("#00ff00", "hold"), "queue")
	independent := submitEngineJob(t, e, "b", engineProgram("#0000ff", "hold"), "queue")
	awaitEngineJob(t, e, independent.ID, "succeeded")
	calls, _, _, _, _, _ := a.snapshot()
	if colors := engineSetColors(calls); len(colors) != 1 || colors[0] != "#ff0000" {
		t.Fatalf("second command reached busy device: %v", colors)
	}
	gate.open()
	awaitEngineJob(t, e, first.ID, "succeeded")
	awaitEngineJob(t, e, second.ID, "succeeded")
	for _, backend := range []*engineBackend{a, b} {
		_, _, _, max, _, unsafe := backend.snapshot()
		if max != 1 || unsafe {
			t.Fatalf("backend %s overlapped native operations: max=%d unsafe=%v", backend.serial, max, unsafe)
		}
	}
}

func TestEngineQueueRejectBoundAndReplace(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{QueueSize: 1}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(step Step) error {
		if step.Color == "#ff0000" {
			return gate.block()
		}
		return nil
	}, nil, nil)
	active := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "reject"); !errors.Is(err, ErrBusy) {
		t.Fatalf("reject policy = %v, want ErrBusy", err)
	}
	queued := submitEngineJob(t, e, "a", engineProgram("#00ff00", "hold"), "queue")
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "queue"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue = %v, want ErrQueueFull", err)
	}
	replacement := submitEngineJob(t, e, "a", engineProgram("#0000ff", "hold"), "replace")
	old, _ := e.Job(active.ID)
	if old.Status != "cancelling" || old.FinishedAt != nil {
		t.Fatalf("blocked active job acknowledged cancellation early: %+v", old)
	}
	removed := awaitEngineJob(t, e, queued.ID, "cancelled")
	if removed.StartedAt != nil {
		t.Fatal("replaced queued job was executed")
	}
	gate.open()
	awaitEngineJob(t, e, active.ID, "cancelled")
	awaitEngineJob(t, e, replacement.ID, "succeeded")
	calls, _, _, _, _, _ := b.snapshot()
	if got := strings.Join(engineSetColors(calls), ","); got != "#ff0000,#0000ff" {
		t.Fatalf("replacement execution = %s", got)
	}
	offBetween := false
	for _, call := range calls {
		if call.kind == "stop" && call.mode == "off" {
			offBetween = true
		}
		if call.kind == "set" && call.step.Color == "#0000ff" && !offBetween {
			t.Fatal("replacement started before old hardware cleanup")
		}
	}
}

func TestEngineCancelWaitsForHardwareCleanup(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	setGate, cleanupGate := newEngineGate(), newEngineGate()
	defer setGate.open()
	defer cleanupGate.open()
	b.hooks(func(Step) error { return setGate.block() }, func(mode string) error {
		if mode == "off" {
			return cleanupGate.block()
		}
		return nil
	}, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, setGate.entered)
	cancelled, err := e.Cancel(context.Background(), job.ID)
	if err != nil || cancelled.Status != "cancelling" || cancelled.FinishedAt != nil {
		t.Fatalf("Cancel accepted = %+v, %v", cancelled, err)
	}
	setGate.open()
	awaitEngineSignal(t, cleanupGate.entered)
	still, _ := e.Job(job.ID)
	if still.Status != "cancelling" || still.FinishedAt != nil {
		t.Fatalf("cleanup still blocked but cancellation terminal: %+v", still)
	}
	cleanupGate.open()
	awaitEngineJob(t, e, job.ID, "cancelled")
	_, led1, led2, _, closed, _ := b.snapshot()
	if led1 != "#000000" || led2 != "#000000" || closed != 0 {
		t.Fatalf("cancel cleanup = %s/%s closed=%d", led1, led2, closed)
	}
}

func TestEngineCleanupFailureCannotAcknowledgeCancellation(t *testing.T) {
	for _, failRead := range []bool{false, true} {
		t.Run(fmt.Sprintf("readback=%v", failRead), func(t *testing.T) {
			b := newEngineBackend("a")
			e := startEngineTest(t, Options{}, b)
			gate := newEngineGate()
			defer gate.open()
			failure := errors.New("fake cleanup failure")
			b.hooks(func(Step) error { return gate.block() }, func(mode string) error {
				if !failRead && mode == "off" {
					return failure
				}
				return nil
			}, func() error {
				if failRead {
					return failure
				}
				return nil
			})
			job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
			awaitEngineSignal(t, gate.entered)
			if _, err := e.Cancel(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			gate.open()
			failed := awaitEngineJob(t, e, job.ID, "failed")
			if !strings.Contains(failed.Error, failure.Error()) {
				t.Fatalf("cleanup error lost: %s", failed.Error)
			}
			awaitEngineCondition(t, func() bool {
				state, _ := e.Device("a")
				return state.Connection == "offline"
			})
		})
	}
}

func TestEngineCancelDuringFinishHoldForcesOff(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	holds := 0
	b.hooks(nil, func(mode string) error {
		if mode == "hold" {
			holds++
			if holds == 2 { // First hold is execution preflight.
				return gate.block()
			}
		}
		return nil
	}, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	if result, err := e.Cancel(context.Background(), job.ID); err != nil || result.Status != "cancelling" {
		t.Fatalf("finish-racing Cancel = %+v, %v", result, err)
	}
	gate.open()
	awaitEngineJob(t, e, job.ID, "cancelled")
	calls, led1, led2, _, _, _ := b.snapshot()
	lastStop := ""
	for _, call := range calls {
		if call.kind == "stop" {
			lastStop = call.mode
		}
	}
	if lastStop != "off" || led1 != "#000000" || led2 != "#000000" {
		t.Fatalf("cancelled hold was not followed by off: stop=%s rgb=%s/%s", lastStop, led1, led2)
	}
}

func TestEngineCancelDuringPreflightNeverSetsColor(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(nil, func(mode string) error {
		if mode == "hold" {
			return gate.block()
		}
		return nil
	}, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	if _, err := e.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	gate.open()
	awaitEngineJob(t, e, job.ID, "cancelled")
	calls, _, _, _, _, _ := b.snapshot()
	if colors := engineSetColors(calls); len(colors) != 0 {
		t.Fatalf("cancelled preflight still sent color: %v", colors)
	}
}

func TestEngineCloseDeadlineKeepsBackendUntilNativeReturn(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{ShutdownMode: "hold"}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked native Close = %v", err)
	}
	select {
	case <-e.Done():
		t.Fatal("runtime reported done before native operation returned")
	default:
	}
	_, _, _, _, closeCount, unsafe := b.snapshot()
	if closeCount != 0 || unsafe {
		t.Fatal("native handle was released while its I/O was blocked")
	}
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "queue"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after shutdown = %v", err)
	}
	gate.open()
	awaitEngineSignal(t, e.Done())
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitEngineJob(t, e, job.ID, "cancelled")
	_, led1, led2, _, closeCount, unsafe := b.snapshot()
	if closeCount != 1 || unsafe || led1 != "#000000" || led2 != "#000000" {
		t.Fatalf("interrupted job held output or unsafe close: rgb=%s/%s count=%d unsafe=%v", led1, led2, closeCount, unsafe)
	}
}

func TestEngineClosePropagatesActiveCleanupFailure(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	failure := errors.New("device stop failed during shutdown")
	b.hooks(func(Step) error { return gate.block() }, func(mode string) error {
		if mode == "off" {
			return failure
		}
		return nil
	}, nil)
	job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	closed := make(chan error, 1)
	go func() { closed <- e.Close(context.Background()) }()
	awaitEngineCondition(t, func() bool {
		state, _ := e.Job(job.ID)
		return state.Status == "cancelling"
	})
	gate.open()
	select {
	case err := <-closed:
		if !errors.Is(err, failure) {
			t.Fatalf("Close lost active cleanup failure: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after fake native return")
	}
	awaitEngineJob(t, e, job.ID, "failed")
}

func TestEngineFinishedHoldAndShutdownModes(t *testing.T) {
	for _, mode := range []string{"off", "hold"} {
		t.Run(mode, func(t *testing.T) {
			b := newEngineBackend("a")
			e := startEngineTest(t, Options{ShutdownMode: mode}, b)
			job := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
			awaitEngineJob(t, e, job.ID, "succeeded")
			if err := e.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, led1, led2, _, _, _ := b.snapshot()
			want := "#000000"
			if mode == "hold" {
				want = "#ff0000"
			}
			if led1 != want || led2 != want {
				t.Fatalf("shutdown %s left %s/%s, want %s", mode, led1, led2, want)
			}
		})
	}
}

func TestEngineReadOnlyDoesNotMutateDevice(t *testing.T) {
	b := newEngineBackend("a")
	b.led1, b.led2 = "#123456", "#654321"
	e := startEngineTest(t, Options{ReadOnly: true}, b)
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ff0000", "off"), "queue"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("read-only Submit = %v", err)
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, led1, led2, _, closed, _ := b.snapshot()
	for _, call := range calls {
		if call.kind == "stop" || call.kind == "set" {
			t.Fatalf("read-only runtime mutated hardware: %+v", call)
		}
	}
	if led1 != "#123456" || led2 != "#654321" || closed != 1 {
		t.Fatalf("read-only output changed: %s/%s close=%d", led1, led2, closed)
	}
}

func TestEngineUntouchedDirectHoldOnlyReadsAndCloses(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{ShutdownMode: "hold"}, b)
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, _, _, _, _, _ := b.snapshot()
	for _, call := range calls {
		if call.kind == "set" || call.kind == "stop" {
			t.Fatalf("untouched direct hold mutated device: %+v", call)
		}
	}
}

func TestEngineCancelledQueueDoesNotTouchHardware(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	active := submitEngineJob(t, e, "a", engineProgram("#ff0000", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	queued := submitEngineJob(t, e, "a", engineProgram("#00ff00", "hold"), "queue")
	if result, err := e.Cancel(context.Background(), queued.ID); err != nil || result.Status != "cancelled" || result.StartedAt != nil {
		t.Fatalf("queued cancellation = %+v, %v", result, err)
	}
	gate.open()
	awaitEngineJob(t, e, active.ID, "succeeded")
	calls, led1, _, _, _, _ := b.snapshot()
	if colors := engineSetColors(calls); len(colors) != 1 || led1 != "#ff0000" {
		t.Fatalf("queued cancellation changed output: colors=%v led1=%s", colors, led1)
	}
}

func TestEngineHistoryIsBoundedAndSnapshotsAreDetached(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{MaxHistory: 2}, b)
	var jobs []Job
	for i := 0; i < 3; i++ {
		program := engineProgram("#aabbcc", "hold")
		job := submitEngineJob(t, e, "a", program, "queue")
		program.Steps[0].Color = "#ffffff"
		job.Program.Steps[0].Color = "#ffffff"
		jobs = append(jobs, awaitEngineJob(t, e, job.ID, "succeeded"))
	}
	if _, err := e.Job(jobs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("history did not evict oldest terminal job: %v", err)
	}
	for _, job := range jobs[1:] {
		if _, err := e.Job(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	last := jobs[2]
	last.Program.Steps[0].Color = "#ffffff"
	*last.StartedAt = time.Time{}
	*last.FinishedAt = time.Time{}
	fresh, _ := e.Job(last.ID)
	if fresh.Program.Steps[0].Color != "#aabbcc" || fresh.StartedAt.IsZero() || fresh.FinishedAt.IsZero() {
		t.Fatalf("job snapshot aliases engine storage: %+v", fresh)
	}
	state, _ := e.Device("a")
	state.Capabilities[0] = "mutated"
	state.Desired.Color = "#ffffff"
	state.Observed.LED1 = "#ffffff"
	freshState, _ := e.Device("a")
	if freshState.Capabilities[0] == "mutated" || freshState.Desired.Color != "#aabbcc" || freshState.Observed.LED1 != "#aabbcc" {
		t.Fatalf("device snapshot aliases engine storage: %+v", freshState)
	}
	calls, _, _, _, _, _ := b.snapshot()
	for _, color := range engineSetColors(calls) {
		if color != "#aabbcc" {
			t.Fatalf("caller mutated submitted program: %v", engineSetColors(calls))
		}
	}
}

func TestEngineProgramRepeatsAndReportsCompletedSteps(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	job := submitEngineJob(t, e, "a", Program{
		Steps:  []Step{{LED: 1, Color: "#FF0000", HoldMS: 1}, {LED: 2, Color: "#0000FF", HoldMS: 1}},
		Repeat: 2, OnFinish: "hold",
	}, "queue")
	finished := awaitEngineJob(t, e, job.ID, "succeeded")
	if finished.CompletedSteps != 4 || finished.TotalSteps != 4 {
		t.Fatalf("program progress = %d/%d, want 4/4", finished.CompletedSteps, finished.TotalSteps)
	}
	calls, led1, led2, _, _, _ := b.snapshot()
	if got := strings.Join(engineSetColors(calls), ","); got != "#ff0000,#0000ff,#ff0000,#0000ff" || led1 != "#ff0000" || led2 != "#0000ff" {
		t.Fatalf("repeat sequence = %s, rgb=%s/%s", got, led1, led2)
	}
}

func TestEngineInvalidProgramsDoNotReachHardware(t *testing.T) {
	b := newEngineBackend("a")
	e := startEngineTest(t, Options{}, b)
	for _, step := range []Step{
		{LED: 256, Color: "#ff0000"},
		{LED: -1, Color: "#ff0000"},
		{Color: "red"},
		{Color: "#gg0000"},
		{Color: "#ff0000", FadeMS: 1},
		{Color: "#ff0000", HoldMS: -1},
	} {
		if _, err := e.Submit(context.Background(), "a", Program{Steps: []Step{step}}, "queue"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid step accepted: %+v, %v", step, err)
		}
	}
	calls, _, _, _, _, _ := b.snapshot()
	for _, call := range calls {
		if call.kind == "set" || call.kind == "stop" {
			t.Fatalf("invalid request touched device: %+v", call)
		}
	}
}
