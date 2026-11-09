package control

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errFrameDeadlineExceeded = errors.New("frame deadline exceeded")

// frameSetter deliberately exposes no Read, Stop, Close or discovery method.
// The caller must already own and quiesce the backend, and remains responsible
// for cleanup, observation and retaining its lease until native I/O returns.
type frameSetter interface {
	Set(Step) error
}

type frameTimer interface {
	Channel() <-chan time.Time
	Stop() bool
}

type frameClock interface {
	Now() time.Time
	NewTimer(time.Duration) frameTimer
}

// These adapters remain private and unused by the existing device worker.
type wallFrameClock struct{}
type wallFrameTimer struct{ timer *time.Timer }

func (wallFrameClock) Now() time.Time { return time.Now() }
func (wallFrameClock) NewTimer(d time.Duration) frameTimer {
	return wallFrameTimer{timer: time.NewTimer(d)}
}
func (t wallFrameTimer) Channel() <-chan time.Time { return t.timer.C }
func (t wallFrameTimer) Stop() bool                { return t.timer.Stop() }

type frameScheduleProgress struct {
	CompletedFrames       int
	CompletedTargetWrites int
}

// runFrameSchedule is an unintegrated scheduling primitive, not a Service or
// device owner. It validates the entire program before any Set. Progress means
// a host frame interval elapsed, not that optical settling/cleanup completed.
// No timer or context can interrupt a synchronous native Set already in flight.
// The progress callback runs synchronously on this scheduling goroutine: keep
// it brief, with no device operations, file/network I/O or blocking work.
func runFrameSchedule(ctx context.Context, setter frameSetter, clock frameClock, input FrameProgram, progress func(frameScheduleProgress)) (frameScheduleProgress, error) {
	var result frameScheduleProgress
	if ctx == nil || setter == nil || clock == nil {
		return result, invalidFrame("schedule requires context, setter and clock")
	}
	p, err := NormalizeFrameProgram(input)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	// Initial applies once, never at each repetition boundary. Waiting after
	// every zero-time target includes the last target's device update allowance.
	for i, target := range p.Initial {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		err := setter.Set(Step{LED: target.LED, Color: target.Color})
		if err != nil {
			return result, fmt.Errorf("frame initial target %d: %w", i, err)
		}
		result.CompletedTargetWrites++
		if err := ctx.Err(); err != nil {
			return result, err
		}
		ready := clock.Now().Add(time.Duration(FrameUpdateAllowanceMS) * time.Millisecond)
		if err := waitFrameUntil(ctx, clock, ready); err != nil {
			return result, err
		}
	}

	var settleAt time.Time
	for repeat := 0; repeat < p.Repeat; repeat++ {
		for _, frame := range p.Frames {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			// Start from actual current time. A late wakeup must not shorten the
			// next interval or trigger a burst to catch up an old global clock.
			deadline := clock.Now().Add(time.Duration(frame.IntervalMS) * time.Millisecond)
			var lastReturnedAt time.Time
			for i, target := range frame.Targets {
				if i > 0 {
					gapEnd := lastReturnedAt.Add(time.Duration(FrameWriteSpacingMS) * time.Millisecond)
					if gapEnd.After(deadline) {
						gapEnd = deadline
					}
					if err := waitFrameUntil(ctx, clock, gapEnd); err != nil {
						return result, err
					}
				}
				if err := frameWriteAllowed(ctx, clock, deadline); err != nil {
					return result, fmt.Errorf("frame %d target %d before Set: %w", result.CompletedFrames, i, err)
				}
				err := setter.Set(Step{LED: target.LED, Color: target.Color, FadeMS: frame.FadeMS})
				lastReturnedAt = clock.Now()
				postErr := frameWriteAllowed(ctx, clock, deadline)
				if err != nil {
					return result, fmt.Errorf("frame %d target %d Set: %w", result.CompletedFrames, i, err)
				}
				result.CompletedTargetWrites++
				if postErr != nil {
					return result, fmt.Errorf("frame %d target %d after Set: %w", result.CompletedFrames, i, postErr)
				}
			}
			// Targets share FadeMS and ordered Set returns are monotonic; the
			// final target return therefore supplies this frame's latest settle.
			settleAt = lastReturnedAt.Add(time.Duration(frame.FadeMS+FrameUpdateAllowanceMS) * time.Millisecond)
			if err := waitFrameUntil(ctx, clock, deadline); err != nil {
				return result, err
			}
			result.CompletedFrames++
			if progress != nil {
				progress(result)
			}
		}
	}
	// This is based on actual native Set completion, not the nominal tail in
	// preview. The caller still performs its own Stop/Read before job success.
	if err := waitFrameUntil(ctx, clock, settleAt); err != nil {
		return result, err
	}
	return result, nil
}

func frameWriteAllowed(ctx context.Context, clock frameClock, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !clock.Now().Before(deadline) {
		return errFrameDeadlineExceeded
	}
	return nil
}

func waitFrameUntil(ctx context.Context, clock frameClock, deadline time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := deadline.Sub(clock.Now())
		if remaining <= 0 {
			return nil
		}
		timer := clock.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.Channel():
			timer.Stop()
		}
		// Check cancellation and the monotonic clock again after each wakeup.
	}
}
