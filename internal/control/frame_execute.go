package control

import (
	"context"
	"errors"

	"github.com/b1ug/nb1/internal/transporttrace"
)

// The wrapper runs only on the owning worker. State publication is short and
// locked; no second goroutine can operate the backend during frame execution.
type workerFrameSetter struct {
	worker *worker
	record *record
	ctx    context.Context
	cancel context.CancelFunc
}

func (s workerFrameSetter) cancelPending() {
	select {
	case <-s.record.cancel:
		s.cancel()
	case <-s.worker.engine.stop:
		s.cancel()
	default:
	}
}

func (s workerFrameSetter) Set(step Step) error {
	return s.SetFrameTarget(transporttrace.NoPosition(), step)
}

func (s workerFrameSetter) SetFrameTarget(position transporttrace.Position, step Step) error {
	// Check channels directly too: native completion need not wait for the
	// context bridge goroutine to be scheduled before recognizing cancellation.
	s.cancelPending()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	phase := transporttrace.FrameTarget
	if position.Initial >= 0 {
		phase = transporttrace.FrameInitial
	}
	err := s.worker.scopedSet(s.record, phase, position, step)
	if err == nil {
		e := s.worker.engine
		e.mu.Lock()
		desired := step
		s.worker.state.Desired = &desired
		s.record.completedTargetWrites++
		e.mu.Unlock()
	}
	s.cancelPending()
	return err
}

func (w *worker) executeFrames(r *record) {
	e := w.engine
	ctx, cancel := context.WithCancel(context.Background())
	bridgeStop, bridgeDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(bridgeDone)
		select {
		case <-r.cancel:
			cancel()
		case <-e.stop:
			cancel()
		case <-bridgeStop:
		}
	}()
	defer func() {
		cancel()
		close(bridgeStop)
		<-bridgeDone
	}()
	setter := workerFrameSetter{worker: w, record: r, ctx: ctx, cancel: cancel}
	setter.cancelPending()
	runErr := w.scopedStop(r, transporttrace.PreflightHold, "hold")
	cancelled := false
	if runErr == nil {
		_, runErr = runFrameSchedule(ctx, setter, wallFrameClock{}, *r.frames, func(value frameScheduleProgress) {
			e.mu.Lock()
			r.completedFrames = value.CompletedFrames
			e.mu.Unlock()
		})
		if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
			cancelled, runErr = true, nil
		}
	}
	// This shared path confirms Stop+Read before terminal status, retains the
	// lease during native calls, and handles cancellation during final hold.
	w.finishExecution(r, runErr, cancelled)
}
