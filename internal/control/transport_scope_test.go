package control

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/transporttrace"
)

type scopedCall struct {
	kind  string
	scope transporttrace.Scope
}
type scopeTestBackend struct {
	*frameIntegrationBackend
	traceMu  sync.Mutex
	scope    transporttrace.Scope
	seen     []scopedCall
	recorder *transporttrace.Recorder
}

func (b *scopeTestBackend) SetTransportScope(s transporttrace.Scope) {
	b.traceMu.Lock()
	b.scope = s
	b.traceMu.Unlock()
}
func (b *scopeTestBackend) capture(kind string) transporttrace.Scope {
	b.traceMu.Lock()
	defer b.traceMu.Unlock()
	s := b.scope
	b.seen = append(b.seen, scopedCall{kind, s})
	return s
}
func (b *scopeTestBackend) Set(step Step) error {
	s := b.capture("set")
	var trace *transporttrace.OperationTrace
	if b.recorder != nil {
		trace = b.recorder.Operation("20005601", s, transporttrace.SetColor, transporttrace.Target{LED: uint8(step.LED)})
	}
	a := trace.Begin(1)
	err := b.frameIntegrationBackend.Set(step)
	a.End(err, false, false, 0)
	return err
}
func (b *scopeTestBackend) Read() (Observation, error) {
	b.capture("read")
	return b.frameIntegrationBackend.Read()
}
func (b *scopeTestBackend) Stop(mode string) error {
	b.capture("stop")
	return b.frameIntegrationBackend.Stop(mode)
}
func (b *scopeTestBackend) traces() []scopedCall {
	b.traceMu.Lock()
	defer b.traceMu.Unlock()
	return append([]scopedCall(nil), b.seen...)
}

type scopeTestDriver struct{ backend *scopeTestBackend }

func (d scopeTestDriver) Open(string) (Backend, error) { return d.backend, nil }
func startScopeEngine(t *testing.T, poll time.Duration) (*Engine, *scopeTestBackend) {
	t.Helper()
	b := &scopeTestBackend{frameIntegrationBackend: &frameIntegrationBackend{engineBackend: newEngineBackend("a"), capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold", "cancel", "observe"}}}
	e, err := New(scopeTestDriver{b}, Options{Serials: []string{"a"}, PollInterval: poll})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	awaitEngineCondition(t, func() bool { s, _ := e.Device("a"); return s.Connection == "online" })
	return e, b
}

func TestTransportProgramScopeRepeatedIdenticalTargetsAndObservations(t *testing.T) {
	e, b := startScopeEngine(t, 10*time.Millisecond)
	p := Program{Steps: []Step{{LED: 1, Color: "#112233", HoldMS: 10}, {LED: 1, Color: "#112233", HoldMS: 10}}, Repeat: 2, OnFinish: "hold"}
	j := submitEngineJob(t, e, "a", p, "reject")
	awaitEngineJob(t, e, j.ID, "succeeded")
	awaitEngineCondition(t, func() bool {
		for _, call := range b.traces() {
			if call.scope.Phase == transporttrace.IdleObserve {
				return true
			}
		}
		return false
	})
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sets []transporttrace.Position
	phases := map[transporttrace.Phase]bool{}
	for _, call := range b.traces() {
		s := call.scope
		phases[s.Phase] = true
		if call.kind == "set" {
			if s.JobID != j.ID || s.Pattern || s.Frame || s.Phase != transporttrace.ProgramStep {
				t.Fatal(s)
			}
			sets = append(sets, s.Position)
		}
		if s.Phase == transporttrace.ConnectRead || s.Phase == transporttrace.IdleObserve || s.Phase == transporttrace.Shutdown {
			if s.JobID != "" || s.PatternID != "" {
				t.Fatal("stale job scope", s)
			}
		}
		if s.Phase == transporttrace.ProgramObserve {
			if s.JobID != j.ID || s.Position.Step < 0 || s.Position.Repeat < 0 {
				t.Fatal("unbound step observation", s)
			}
		}
	}
	if len(sets) != 4 {
		t.Fatal(sets)
	}
	for i, p := range sets {
		if p.Repeat != int32(i/2) || p.Step != int32(i%2) || p.Frame != -1 {
			t.Fatal("identical color confused position", sets)
		}
	}
	for _, phase := range []transporttrace.Phase{transporttrace.ConnectRead, transporttrace.IdleObserve, transporttrace.PreflightHold, transporttrace.ProgramObserve, transporttrace.Cleanup, transporttrace.CleanupVerify, transporttrace.Shutdown} {
		if !phases[phase] {
			t.Fatalf("missing phase %v", phase)
		}
	}
	b.traceMu.Lock()
	defer b.traceMu.Unlock()
	if b.scope.Phase != transporttrace.PhaseNone || b.scope.JobID != "" {
		t.Fatal("scope not cleared")
	}
}

func TestTransportPatternScopeKeepsOriginalRefForBothKinds(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b := startScopeEngine(t, time.Hour)
			ref := patternRef(77)
			resolved := patternResolved(t, kind, ref, "#112233")
			accepted := mustPattern(t, e, resolved, "reject", receiptTicket(e, 1))
			awaitPattern(t, e, ref, receiptTicket(e, 1), "succeeded")
			id := patternEnvelopeID(accepted.Job)
			var sets int
			for _, c := range b.traces() {
				if c.scope.JobID == "" {
					continue
				}
				s := c.scope
				if s.JobID != id || !s.Pattern || s.PatternID != ref.ID || s.Revision != ref.Revision || s.Frame != (kind == "frame") {
					t.Fatal(s)
				}
				if c.kind == "set" {
					if kind == "frame" {
						if sets < 2 {
							if s.Phase != transporttrace.FrameInitial || s.Position.Initial != int32(sets) || s.Position.Target != int32(sets) {
								t.Fatal("initial position not carried to worker", s)
							}
						} else {
							if s.Phase != transporttrace.FrameTarget || s.Position.Frame != 0 || s.Position.ExecutedFrame != 0 || s.Position.Repeat != 0 || s.Position.Target != int32(sets-2) {
								t.Fatal("frame position not carried to worker", s)
							}
						}
					} else if s.Position.Repeat != 0 || s.Position.Step != 0 {
						t.Fatal("program position missing", s)
					}
					sets++
				}
			}
			if sets == 0 {
				t.Fatal("no scoped target")
			}
		})
	}
}

type positionedTestSetter struct {
	*scheduleTestSetter
	positions []transporttrace.Position
}

func (s *positionedTestSetter) SetFrameTarget(p transporttrace.Position, step Step) error {
	s.positions = append(s.positions, p)
	return s.Set(step)
}
func TestTransportFramePositionsDoNotChangeSchedulingOrDeadlines(t *testing.T) {
	for _, delay := range []time.Duration{5 * time.Millisecond, 100 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			p := scheduleTestProgram()
			p.Repeat = 2
			plain := &scheduleTestSetter{clock: newScheduleTestClock(), delays: map[int]time.Duration{3: delay}}
			positioned := &positionedTestSetter{scheduleTestSetter: &scheduleTestSetter{clock: newScheduleTestClock(), delays: map[int]time.Duration{3: delay}}}
			a, errA := runFrameSchedule(context.Background(), plain, plain.clock, p, nil)
			b, errB := runFrameSchedule(context.Background(), positioned, positioned.clock, p, nil)
			if a != b || !reflect.DeepEqual(plain.writes, positioned.writes) || (errA == nil) != (errB == nil) || errors.Is(errA, errFrameDeadlineExceeded) != errors.Is(errB, errFrameDeadlineExceeded) {
				t.Fatalf("diagnostics changed schedule: %v/%v %v/%v", a, b, errA, errB)
			}
			if delay == 100*time.Millisecond && !errors.Is(errB, errFrameDeadlineExceeded) {
				t.Fatal("overrun swallowed")
			}
			for i, pos := range positioned.positions {
				if i < 2 {
					if pos.Initial != int32(i) || pos.Frame != -1 {
						t.Fatal(pos)
					}
				} else {
					executed := (i - 2) / 2
					if pos.Initial != -1 || pos.Repeat != int32(executed) || pos.Frame != 0 || pos.ExecutedFrame != int32(executed) || pos.Target != int32((i-2)%2) {
						t.Fatal(pos)
					}
				}
			}
		})
	}
}

func TestTransportInFlightScopeDoesNotReleaseOwnerOnContextEnd(t *testing.T) {
	e, b := startScopeEngine(t, time.Hour)
	r := transporttrace.New()
	b.recorder = r
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	j := submitEngineJob(t, e, "a", engineProgram("#123456", "hold"), "reject")
	awaitEngineSignal(t, gate.entered)
	s := r.Snapshot()
	if s.Stats.InFlight != 1 || len(s.Events) != 1 {
		t.Fatal("missing unresolved native attempt", s.Stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, _, _, _, closed, busy := b.snapshot()
	if closed != 0 || busy {
		t.Fatal("context released in-flight backend")
	}
	if r.Stats().InFlight != 1 {
		t.Fatal("context fabricated native completion")
	}
	gate.open()
	awaitEngineSignal(t, e.Done())
	r.Close()
	_, _, _, _, closed, busy = b.snapshot()
	if closed != 1 || busy || r.Stats().InFlight != 0 {
		t.Fatal("native completion/ownership ordering lost")
	}
	for _, c := range b.traces() {
		if c.kind == "set" && c.scope.JobID != j.ID {
			t.Fatal(c)
		}
	}
}

type reconnectScopeDriver struct {
	mu       sync.Mutex
	backends []*scopeTestBackend
	opens    int
}

func (d *reconnectScopeDriver) Open(string) (Backend, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.opens >= len(d.backends) {
		return nil, ErrOffline
	}
	b := d.backends[d.opens]
	d.opens++
	return b, nil
}
func TestTransportReconnectDoesNotCarryLastJobScope(t *testing.T) {
	newBackend := func() *scopeTestBackend {
		return &scopeTestBackend{frameIntegrationBackend: &frameIntegrationBackend{engineBackend: newEngineBackend("a"), capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold"}}}
	}
	one, two := newBackend(), newBackend()
	driver := &reconnectScopeDriver{backends: []*scopeTestBackend{one, two}}
	e, err := New(driver, Options{Serials: []string{"a"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	awaitEngineCondition(t, func() bool { s, _ := e.Device("a"); return s.Connection == "online" })
	j := submitEngineJob(t, e, "a", engineProgram("#112233", "hold"), "reject")
	awaitEngineJob(t, e, j.ID, "succeeded")
	one.hooks(nil, nil, func() error { return ErrOffline })
	awaitEngineCondition(t, func() bool {
		for _, c := range two.traces() {
			if c.scope.Phase == transporttrace.ConnectRead {
				return true
			}
		}
		return false
	})
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range two.traces() {
		if c.scope.JobID != "" || c.scope.Pattern || c.scope.PatternID != "" {
			t.Fatal("reopen inherited old job", c)
		}
	}
	for _, b := range []*scopeTestBackend{one, two} {
		b.traceMu.Lock()
		s := b.scope
		b.traceMu.Unlock()
		if s.Phase != transporttrace.PhaseNone || s.JobID != "" {
			t.Fatal("scope survived return", s)
		}
	}
}
