package control

import (
	"context"
	"fmt"
	"time"

	"github.com/b1ug/nb1/internal/nativeinspection"
)

// NativeInspectionBackend is optional. The existing Backend stays compatible
// with non-native implementations and test doubles. Only the worker invokes it.
type NativeInspectionBackend interface {
	InspectNative(context.Context, nativeinspection.Request) (nativeinspection.Result, error)
}

// NativeInspectionService adds live native reads without changing Service or
// creating a control Job, receipt, device handle or persistent library record.
type NativeInspectionService interface {
	InspectNative(context.Context, string, nativeinspection.Request) (nativeinspection.Result, error)
}

type nativeInspectionOutcome struct {
	result nativeinspection.Result
	err    error
}

type nativeInspectionCall struct {
	ctx      context.Context
	request  nativeinspection.Request
	result   chan nativeInspectionOutcome // One result; worker never waits for its caller.
	started  bool                         // Protected by engine.mu.
	instance string
	session  string
	identity nativeinspection.Identity
}

// InspectNative reserves an idle existing worker for one bounded native read.
// Busy rejection never replaces, queues or cancels a control job. A canceled
// caller cannot interrupt an in-flight native operation: admission remains
// reserved until the worker really returns. Available results retain their
// original error, including partial reads and typed native causes.
func (e *Engine) InspectNative(ctx context.Context, serial string, request nativeinspection.Request) (nativeinspection.Result, error) {
	if err := ctx.Err(); err != nil {
		return nativeinspection.Result{}, err
	}
	if err := request.Validate(); err != nil {
		return nativeinspection.Result{}, err
	}
	request = request.Clone()
	e.mu.Lock()
	if !e.started || e.closed {
		e.mu.Unlock()
		return nativeinspection.Result{}, ErrClosed
	}
	w, ok := e.workers[serial]
	if !ok {
		e.mu.Unlock()
		return nativeinspection.Result{}, ErrNotFound
	}
	if w.state.Connection != "online" {
		err := error(ErrOffline)
		if w.state.Connection == "busy" {
			err = ErrBusy
		}
		e.mu.Unlock()
		return nativeinspection.Result{}, err
	}
	if w.active != nil || len(w.queue) != 0 || w.inspection != nil {
		e.mu.Unlock()
		return nativeinspection.Result{}, ErrBusy
	}
	if !w.nativeInspectionSupported || w.deviceSessionID == "" {
		e.mu.Unlock()
		return nativeinspection.Result{}, nativeinspection.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nativeinspection.Result{}, err
	}
	call := &nativeInspectionCall{ctx: ctx, request: request, result: make(chan nativeInspectionOutcome, 1),
		instance: e.receipts.instanceID, session: w.deviceSessionID,
		identity: nativeinspection.Identity{Serial: w.serial, Product: w.state.Product,
			Firmware: w.state.Firmware, FirmwareSource: "cached-on-open"}}
	w.inspection = call
	select {
	case w.wake <- struct{}{}:
	default:
	}
	e.mu.Unlock()

	// Prefer an already available observation to losing it to simultaneous
	// cancellation. No synthetic result is made for a request not yet collected.
	select {
	case outcome := <-call.result:
		return outcome.result.Clone(), outcome.err
	default:
	}
	select {
	case outcome := <-call.result:
		return outcome.result.Clone(), outcome.err
	case <-ctx.Done():
		e.mu.Lock()
		if w.inspection == call && !call.started {
			w.cancelPendingInspectionLocked(ctx.Err())
		}
		e.mu.Unlock()
		select {
		case outcome := <-call.result:
			return outcome.result.Clone(), outcome.err
		default:
			return nativeinspection.Result{}, ctx.Err()
		}
	}
}

// cancelPendingInspectionLocked is used before close/reconnect can invalidate
// a reserved handle. An in-flight read keeps its gate until its actual return.
func (w *worker) cancelPendingInspectionLocked(err error) {
	call := w.inspection
	if call == nil || call.started {
		return
	}
	w.inspection = nil
	call.result <- nativeInspectionOutcome{err: err}
}

func (w *worker) inspectNative(call *nativeInspectionCall) {
	e := w.engine
	var result nativeinspection.Result
	var err error
	if contextErr := call.ctx.Err(); contextErr != nil {
		err = contextErr
	} else if backend, ok := w.backend.(NativeInspectionBackend); !ok {
		err = nativeinspection.ErrUnsupported
	} else {
		started := time.Now().UTC()
		result, err = backend.InspectNative(call.ctx, call.request.Clone())
		finished := time.Now().UTC()
		// Empty Contract means no observation was collected. Keep hard
		// rejections empty so HTTP does not advertise them as partial data.
		if result.Contract != "" {
			result = result.Clone()
			result.DaemonInstanceID, result.DeviceSessionID = call.instance, call.session
			result.Device = call.identity
			if result.StartedAt.IsZero() {
				result.StartedAt = started
			}
			if result.FinishedAt.IsZero() {
				result.FinishedAt = finished
			}
		} else {
			result = nativeinspection.Result{}
			if err == nil {
				err = fmt.Errorf("native inspection backend returned no observation")
			}
		}
	}
	e.mu.Lock()
	if w.inspection == call {
		w.inspection = nil
	}
	call.result <- nativeInspectionOutcome{result: result, err: err}
	e.mu.Unlock()
}

var _ NativeInspectionService = (*Engine)(nil)
