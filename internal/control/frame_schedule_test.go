package control

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// The fake advances only when Set or a timer asks it to. All interval tests
// complete without sleeping or relying on wall-clock scheduling precision.
type scheduleTestClock struct {
	now     time.Time
	timers  []*scheduleTestTimer
	late    map[int]time.Duration
	onTimer func(int) bool
}

type scheduleTestTimer struct {
	channel chan time.Time
	delay   time.Duration
	stopped bool
}

func newScheduleTestClock() *scheduleTestClock {
	return &scheduleTestClock{now: time.Unix(0, 0)}
}
func (c *scheduleTestClock) Now() time.Time { return c.now }
func (c *scheduleTestClock) NewTimer(delay time.Duration) frameTimer {
	timer := &scheduleTestTimer{channel: make(chan time.Time, 1), delay: delay}
	c.timers = append(c.timers, timer)
	index := len(c.timers)
	if c.onTimer != nil && !c.onTimer(index) {
		return timer
	}
	c.now = c.now.Add(delay + c.late[index])
	timer.channel <- c.now
	return timer
}
func (t *scheduleTestTimer) Channel() <-chan time.Time { return t.channel }
func (t *scheduleTestTimer) Stop() bool {
	prior := t.stopped
	t.stopped = true
	return !prior
}
func (c *scheduleTestClock) milliseconds() int64 {
	return c.now.Sub(time.Unix(0, 0)).Milliseconds()
}

type scheduleTestWrite struct {
	step          Step
	start, finish int64
}
type scheduleTestSetter struct {
	clock  *scheduleTestClock
	writes []scheduleTestWrite
	delays map[int]time.Duration
	onSet  func(int) error
}

func (s *scheduleTestSetter) Set(step Step) error {
	write := scheduleTestWrite{step: step, start: s.clock.milliseconds()}
	index := len(s.writes) + 1
	s.clock.now = s.clock.now.Add(s.delays[index])
	var err error
	if s.onSet != nil {
		err = s.onSet(index)
	}
	write.finish = s.clock.milliseconds()
	s.writes = append(s.writes, write)
	return err
}

func scheduleTestProgram() FrameProgram {
	p := frameTestProgram()
	p.Frames[0].FadeMS = 100
	p.Frames[0].IntervalMS = 100
	return p
}

func requireStoppedScheduleTimers(t *testing.T, clock *scheduleTestClock) {
	t.Helper()
	for i, timer := range clock.timers {
		if timer.delay <= 0 || !timer.stopped {
			t.Fatalf("timer %d leaked or had invalid delay: %#v", i, timer)
		}
	}
}

func TestFrameScheduleInitialOnceSharedDeadlineAndProgress(t *testing.T) {
	clock := newScheduleTestClock()
	setter := &scheduleTestSetter{clock: clock}
	p := scheduleTestProgram()
	p.Frames = append(p.Frames, Frame{Targets: []FrameTarget{{LED: 1, Color: "#110000"}, {LED: 2, Color: "#000022"}}, FadeMS: 50, IntervalMS: 50})
	p.Repeat = 2
	var progressTimes []int64
	var progress []frameScheduleProgress
	got, err := runFrameSchedule(context.Background(), setter, clock, p, func(value frameScheduleProgress) {
		progress = append(progress, value)
		progressTimes = append(progressTimes, clock.milliseconds())
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStart := []int64{0, 10, 20, 30, 120, 130, 170, 180, 270, 280}
	var starts []int64
	for _, write := range setter.writes {
		starts = append(starts, write.start)
	}
	if !reflect.DeepEqual(starts, wantStart) || clock.milliseconds() != 340 {
		t.Fatalf("serial fade waits or repeated initialization: %v, end %d", starts, clock.milliseconds())
	}
	if !reflect.DeepEqual(progressTimes, []int64{120, 170, 270, 320}) {
		t.Fatalf("progress happened before frame completion: %v", progressTimes)
	}
	if got != (frameScheduleProgress{CompletedFrames: 4, CompletedTargetWrites: 10}) || len(progress) != 4 {
		t.Fatalf("incorrect frame/write accounting: %#v %v", got, progress)
	}
	for i, value := range progress {
		if value.CompletedFrames != i+1 || value.CompletedTargetWrites != 2+2*(i+1) {
			t.Fatalf("progress %d: %#v", i, value)
		}
	}
	for i, write := range setter.writes {
		if i < 2 && write.step.FadeMS != 0 || i >= 2 && write.step.FadeMS == 0 || write.step.HoldMS != 0 {
			t.Fatalf("incorrect exact device target: %#v", write)
		}
	}
	requireStoppedScheduleTimers(t, clock)
}

func TestFrameScheduleTailUsesActualLastNativeReturn(t *testing.T) {
	clock := newScheduleTestClock()
	setter := &scheduleTestSetter{clock: clock, delays: map[int]time.Duration{1: 3 * time.Millisecond, 2: 3 * time.Millisecond, 3: 3 * time.Millisecond, 4: 3 * time.Millisecond}}
	p := frameTestProgram() // 375ms interval, 380ms fade
	var frameDoneAt int64
	got, err := runFrameSchedule(context.Background(), setter, clock, p, func(frameScheduleProgress) { frameDoneAt = clock.milliseconds() })
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedFrames != 1 || setter.writes[2].start != 26 || setter.writes[3].start != 39 || setter.writes[3].finish != 42 || frameDoneAt != 401 || clock.milliseconds() != 432 {
		t.Fatalf("native time was omitted from spacing/tail: %#v writes=%#v progress=%d end=%d", got, setter.writes, frameDoneAt, clock.milliseconds())
	}
	// Preview's 25ms nominal tail is not the required actual 31ms here.
	if clock.milliseconds()-frameDoneAt != 31 {
		t.Fatal("scheduler reused approximate preview tail")
	}
	requireStoppedScheduleTimers(t, clock)
}

func TestFrameScheduleNoChasingLateFrameTimer(t *testing.T) {
	clock := newScheduleTestClock()
	clock.late = map[int]time.Duration{2: time.Second}
	setter := &scheduleTestSetter{clock: clock}
	p := scheduleTestProgram()
	p.Initial = p.Initial[:1]
	p.Frames[0].Targets = p.Frames[0].Targets[:1]
	p.Repeat = 3
	var progressTimes []int64
	_, err := runFrameSchedule(context.Background(), setter, clock, p, func(frameScheduleProgress) {
		progressTimes = append(progressTimes, clock.milliseconds())
	})
	if err != nil {
		t.Fatal(err)
	}
	var starts []int64
	for _, write := range setter.writes {
		starts = append(starts, write.start)
	}
	if !reflect.DeepEqual(starts, []int64{0, 10, 1110, 1210}) || !reflect.DeepEqual(progressTimes, []int64{1110, 1210, 1310}) || clock.milliseconds() != 1320 {
		t.Fatalf("late timer led to catch-up writes: starts=%v progress=%v end=%d", starts, progressTimes, clock.milliseconds())
	}
	requireStoppedScheduleTimers(t, clock)
}

func TestFrameScheduleDeadlineStopsBeforeFollowingTarget(t *testing.T) {
	cases := []struct {
		name       string
		delays     map[int]time.Duration
		late       map[int]time.Duration
		wantWrites int
	}{
		{"first-at-deadline", map[int]time.Duration{3: 100 * time.Millisecond}, nil, 3},
		{"first-past-deadline", map[int]time.Duration{3: 101 * time.Millisecond}, nil, 3},
		{"second-at-deadline", map[int]time.Duration{4: 90 * time.Millisecond}, nil, 4},
		{"gap-past-deadline", nil, map[int]time.Duration{3: 100 * time.Millisecond}, 3},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clock := newScheduleTestClock()
			clock.late = test.late
			setter := &scheduleTestSetter{clock: clock, delays: test.delays}
			progressCalls := 0
			p := scheduleTestProgram()
			p.Repeat = 3
			got, err := runFrameSchedule(context.Background(), setter, clock, p, func(frameScheduleProgress) { progressCalls++ })
			if !errors.Is(err, errFrameDeadlineExceeded) || len(setter.writes) != test.wantWrites || got.CompletedFrames != 0 || progressCalls != 0 {
				t.Fatalf("expired frame sent more targets or completed: %#v writes=%d progress=%d err=%v", got, len(setter.writes), progressCalls, err)
			}
			requireStoppedScheduleTimers(t, clock)
		})
	}
}

func TestFrameScheduleCancellationAcrossAllWaitsAndNativeReturns(t *testing.T) {
	cases := []struct {
		name                   string
		cancelSet, cancelTimer int
		cancelProgress         bool
		wantWrites, wantFrames int
	}{
		{"first-initial-return", 1, 0, false, 1, 0},
		{"initial-wait", 0, 1, false, 1, 0},
		{"second-initial-return", 2, 0, false, 2, 0},
		{"first-frame-return", 3, 0, false, 3, 0},
		{"frame-gap", 0, 3, false, 3, 0},
		{"second-frame-return", 4, 0, false, 4, 0},
		{"frame-interval", 0, 4, false, 4, 0},
		{"tail-wait", 0, 5, false, 4, 1},
		{"completion-callback", 0, 0, true, 4, 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := newScheduleTestClock()
			clock.onTimer = func(index int) bool {
				if index == test.cancelTimer {
					cancel()
					return false
				}
				return true
			}
			setter := &scheduleTestSetter{clock: clock, onSet: func(index int) error {
				if index == test.cancelSet {
					cancel()
				}
				return nil
			}}
			got, err := runFrameSchedule(ctx, setter, clock, scheduleTestProgram(), func(frameScheduleProgress) {
				if test.cancelProgress {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) || len(setter.writes) != test.wantWrites || got.CompletedTargetWrites != test.wantWrites || got.CompletedFrames != test.wantFrames {
				t.Fatalf("cancellation wrote more targets or completed early: %#v writes=%d err=%v", got, len(setter.writes), err)
			}
			requireStoppedScheduleTimers(t, clock)
		})
	}
}

type scheduleSetFunction func(Step) error

func (fn scheduleSetFunction) Set(step Step) error { return fn(step) }

func TestFrameScheduleCancellationDoesNotPretendToInterruptNativeSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := newScheduleTestClock()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	calls := 0
	setter := scheduleSetFunction(func(Step) error {
		calls++
		if calls == 3 {
			close(entered)
			<-release
		}
		return nil
	})
	type outcome struct {
		progress frameScheduleProgress
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		progress, err := runFrameSchedule(ctx, setter, clock, scheduleTestProgram(), nil)
		done <- outcome{progress, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not reach the synthetic native Set")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("returned while synchronous native Set was still blocked")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	var got outcome
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not return after synthetic native Set was released")
	}
	if !errors.Is(got.err, context.Canceled) || calls != 3 || got.progress.CompletedFrames != 0 || got.progress.CompletedTargetWrites != 3 {
		t.Fatalf("did not stop immediately after native return: %#v calls=%d", got, calls)
	}
}

func TestFrameScheduleValidationAndSetFailuresNeverContinue(t *testing.T) {
	clock := newScheduleTestClock()
	setter := &scheduleTestSetter{clock: clock}
	p := scheduleTestProgram()
	p.Frames = append(p.Frames, Frame{Targets: []FrameTarget{{LED: 1, Color: "bad"}, {LED: 2, Color: "#000000"}}, IntervalMS: 100})
	if _, err := runFrameSchedule(context.Background(), setter, clock, p, nil); !errors.Is(err, ErrInvalid) || len(setter.writes) != 0 || len(clock.timers) != 0 {
		t.Fatalf("partial execution before full input validation: writes=%v err=%v", setter.writes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runFrameSchedule(ctx, setter, clock, scheduleTestProgram(), nil); !errors.Is(err, context.Canceled) || len(setter.writes) != 0 {
		t.Fatalf("already-cancelled schedule wrote a target: %v", err)
	}
	for failAt := 1; failAt <= 4; failAt++ {
		clock := newScheduleTestClock()
		failure := errors.New("synthetic Set failure")
		setter := &scheduleTestSetter{clock: clock, onSet: func(index int) error {
			if index == failAt {
				return failure
			}
			return nil
		}}
		got, err := runFrameSchedule(context.Background(), setter, clock, scheduleTestProgram(), nil)
		if !errors.Is(err, failure) || len(setter.writes) != failAt || got.CompletedTargetWrites != failAt-1 || got.CompletedFrames != 0 {
			t.Fatalf("Set failure was retried or ignored: failAt=%d result=%#v calls=%d err=%v", failAt, got, len(setter.writes), err)
		}
		requireStoppedScheduleTimers(t, clock)
	}
}

func TestFrameScheduleFreezesEntireInputBeforeInitialWrite(t *testing.T) {
	clock := newScheduleTestClock()
	p := scheduleTestProgram()
	setter := &scheduleTestSetter{clock: clock, onSet: func(index int) error {
		if index == 1 {
			p.Initial[1].Color = "invalid"
			p.Frames[0].Targets[1].Color = "invalid"
		}
		return nil
	}}
	_, err := runFrameSchedule(context.Background(), setter, clock, p, nil)
	if err != nil || setter.writes[1].step.Color != "#000099" || setter.writes[3].step.Color != "#990000" {
		t.Fatalf("in-flight program shares caller slices: writes=%#v err=%v", setter.writes, err)
	}
}

func TestFrameScheduleRejectsAbsentDependencies(t *testing.T) {
	clock := newScheduleTestClock()
	setter := &scheduleTestSetter{clock: clock}
	for _, test := range []struct {
		ctx    context.Context
		setter frameSetter
		clock  frameClock
	}{{nil, setter, clock}, {context.Background(), nil, clock}, {context.Background(), setter, nil}} {
		if _, err := runFrameSchedule(test.ctx, test.setter, test.clock, scheduleTestProgram(), nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing dependency accepted: %v", err)
		}
	}
	if len(setter.writes) != 0 {
		t.Fatal("dependency failure touched setter")
	}
}
