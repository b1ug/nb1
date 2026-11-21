package control

import (
	"context"
	"fmt"

	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type idleOperation uint8

const (
	idleInspect idleOperation = iota
	idleResetFlash
)

type idleCall struct {
	started   bool // Protected by engine.mu.
	run       func()
	reject    func(error)
	stopWrite context.CancelFunc
}

type idleOutcome[T any] struct {
	result T
	err    error
}

// runIdle dispatches once, to the worker that already owns the serial. Until
// the call returns, jobs cannot replace it, another operation cannot begin,
// and shutdown cannot close its handle. No mutex is held during USB I/O.
func runIdle[T any](ctx context.Context, e *Engine, serial string, op idleOperation, perform func(context.Context, *worker) (T, error)) (T, error) {
	var zero T
	e.mu.Lock()
	w, err := e.idleWorkerLocked(serial, op)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		e.mu.Unlock()
		return zero, err
	}

	operationCtx := ctx
	var stopWrite context.CancelFunc
	if op == idleResetFlash {
		operationCtx, stopWrite = context.WithCancel(ctx)
		defer stopWrite()
	}
	done := make(chan idleOutcome[T], 1)
	call := &idleCall{
		stopWrite: stopWrite,
		reject:    func(err error) { done <- idleOutcome[T]{err: err} },
		run: func() {
			var result T
			err := operationCtx.Err()
			if err == nil {
				result, err = perform(operationCtx, w)
			}
			e.mu.Lock()
			w.idle = nil
			done <- idleOutcome[T]{result: result, err: err}
			e.mu.Unlock()
		},
	}
	w.idle = call
	select {
	case w.wake <- struct{}{}:
	default:
	}
	e.mu.Unlock()

	select {
	case outcome := <-done:
		return outcome.result, outcome.err
	case <-ctx.Done():
		e.mu.Lock()
		dispatched := call.started
		if w.idle == call {
			w.cancelIdleLocked(ctx.Err())
		}
		e.mu.Unlock()
		// Prefer the actual result, if the operation finished concurrently.
		select {
		case outcome := <-done:
			return outcome.result, outcome.err
		default:
			if op == idleResetFlash && dispatched {
				return zero, fmt.Errorf("flash reset was dispatched; persistence outcome is unknown: %w", ctx.Err())
			}
			return zero, ctx.Err()
		}
	}
}

func (e *Engine) idleWorkerLocked(serial string, op idleOperation) (*worker, error) {
	if !e.started || e.closed {
		return nil, ErrClosed
	}
	if op == idleResetFlash && e.options.ReadOnly {
		return nil, fmt.Errorf("%w: runtime is read-only", ErrInvalid)
	}
	w := e.workers[serial]
	if w == nil {
		return nil, ErrNotFound
	}
	if w.state.Connection == "busy" {
		return nil, ErrBusy
	}
	if w.state.Connection != "online" {
		return nil, ErrOffline
	}
	if w.active != nil || len(w.queue) != 0 || w.idle != nil {
		return nil, ErrBusy
	}
	switch op {
	case idleInspect:
		if !w.nativeInspectionSupported || w.deviceSessionID == "" {
			return nil, nativeinspection.ErrUnsupported
		}
	case idleResetFlash:
		if !w.flashResetSupported {
			return nil, flashreset.ErrUnsupported
		}
	}
	return w, nil
}

// An operation waiting behind a poll has not touched the handle and must
// never follow it into another device session. In-flight I/O remains owned.
func (w *worker) cancelIdleLocked(err error) {
	call := w.idle
	if call == nil {
		return
	}
	if call.started {
		// A write checks cancellation between native calls and before Flash
		// commit. Only its actual return may free the worker.
		if call.stopWrite != nil {
			call.stopWrite()
		}
		return
	}
	w.idle = nil
	call.reject(err)
}
