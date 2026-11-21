package control

import (
	"context"
	"fmt"

	"github.com/b1ug/nb1/internal/flashreset"
)

// FlashResetBackend operates on the handle already held by its device worker.
type FlashResetBackend interface {
	ResetFlash(context.Context, string) (flashreset.Result, error)
}

// ResetFlash replaces the selected device's persistent Pattern rows with one
// built-in preset. This operation is never queued or replayed by the worker.
func (e *Engine) ResetFlash(ctx context.Context, serial, preset string) (flashreset.Result, error) {
	if err := ctx.Err(); err != nil {
		return flashreset.Result{}, err
	}
	if !flashreset.Valid(preset) {
		return flashreset.Result{}, fmt.Errorf("%w: unknown flash reset preset %q", ErrInvalid, preset)
	}
	return runIdle(ctx, e, serial, idleResetFlash, func(ctx context.Context, w *worker) (flashreset.Result, error) {
		result, err := w.backend.(FlashResetBackend).ResetFlash(ctx, preset)
		// The backend checks the serial on its handle before writing. Its receipt
		// must identify the same device; never relabel a mismatched receipt.
		if result.Status != "" {
			if invalid := result.Validate(serial, preset); invalid != nil {
				return flashreset.Result{}, fmt.Errorf("invalid Flash reset receipt; persistence outcome is unknown: %w", invalid)
			}
		}
		return result, err
	})
}
