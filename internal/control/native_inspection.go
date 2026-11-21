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

// InspectNative reads on the existing device worker and returns the identity
// and session of the handle that collected the observation.
func (e *Engine) InspectNative(ctx context.Context, serial string, request nativeinspection.Request) (nativeinspection.Result, error) {
	if err := ctx.Err(); err != nil {
		return nativeinspection.Result{}, err
	}
	if err := request.Validate(); err != nil {
		return nativeinspection.Result{}, err
	}
	request = request.Clone()
	return runIdle(ctx, e, serial, idleInspect, func(ctx context.Context, w *worker) (nativeinspection.Result, error) {
		started := time.Now().UTC()
		result, err := w.backend.(NativeInspectionBackend).InspectNative(ctx, request)
		finished := time.Now().UTC()
		if result.Contract == "" {
			if err == nil {
				err = fmt.Errorf("native inspection backend returned no observation")
			}
			return nativeinspection.Result{}, err
		}
		result = result.Clone()
		result.DaemonInstanceID, result.DeviceSessionID = e.receipts.instanceID, w.deviceSessionID
		result.Device = nativeinspection.Identity{Serial: w.serial, Product: w.state.Product,
			Firmware: w.state.Firmware, FirmwareSource: "cached-on-open"}
		if result.StartedAt.IsZero() {
			result.StartedAt = started
		}
		if result.FinishedAt.IsZero() {
			result.FinishedAt = finished
		}
		return result, err
	})
}

var _ NativeInspectionService = (*Engine)(nil)
