package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/b1ug/nb1/internal/control"
)

// LightV2CancelResult retains the last receipt-verified Job even when the
// cancellation response is lost or inconsistent. Attempted does not mean the
// request reached the daemon, or that cancellation/cleanup has completed.
type LightV2CancelResult struct {
	Job              control.FrameJob
	DaemonInstanceID string
	CancelAttempted  bool
	AlreadyTerminal  bool
	// Only transport/server uncertainty permits further receipt-only polling.
	// Integrity errors must not be hidden by a subsequent plausible response.
	QueryRecoveryAllowed bool
	// A validated POST snapshot also constrains recovery if its confirming GET
	// failed. Job remains the last fully receipt-confirmed display snapshot.
	RecoveryAnchor *control.FrameJob
}

// CancelLightV2 binds cancellation to the original high-level action and
// receipt before selecting a Job ID. It never submits a light action or retries
// a cancellation POST. The POST includes an instance precondition and requires
// the same instance in its response, then rechecks the original receipt.
func (c *Client) CancelLightV2(ctx context.Context, serial string, data []byte, receipt control.ReceiptRequest, jobID string) (LightV2CancelResult, error) {
	var result LightV2CancelResult
	_, expected, err := decodeLightV2Input(data)
	if err != nil {
		return result, err
	}
	before, err := c.LookupLightV2Request(ctx, serial, data, receipt, jobID)
	if err != nil {
		return result, err
	}
	result.Job, result.DaemonInstanceID = before, receipt.ExpectedDaemonInstanceID
	switch before.Status {
	case "succeeded", "failed", "cancelled":
		result.AlreadyTerminal = true
		return result, nil
	case "cancelling":
		return result, nil
	}
	uncertain := func(err error) (LightV2CancelResult, error) {
		return result, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	if err := ctx.Err(); err != nil {
		return uncertain(err)
	}
	body, err := json.Marshal(struct {
		ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id"`
	}{receipt.ExpectedDaemonInstanceID})
	if err != nil {
		return uncertain(err)
	}
	result.CancelAttempted = true
	var cancelled control.FrameJob
	// A nonempty body with GetBody disabled also prevents the transport from
	// replaying an empty-body POST after a reused connection fails before write.
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v1/frame-jobs/"+url.PathEscape(before.ID)+"/cancel", body, &cancelled, http.StatusAccepted)
	if err != nil {
		result.QueryRecoveryAllowed = cancellationQueryRecovery(err)
		return uncertain(lightRouteError(err, "light_v2_cancel"))
	}
	instances := headers.Values("X-NB1-Daemon-Instance-ID")
	if len(instances) != 1 || instances[0] != receipt.ExpectedDaemonInstanceID || len(headers.Values("X-NB1-Replayed")) != 0 || len(headers.Values("Location")) != 0 {
		return uncertain(lightDecodeError(http.StatusAccepted, "unexpected or inconsistent cancellation response binding"))
	}
	if err := validateLightV2Job(cancelled, serial, expected.Result, before.ID); err != nil {
		return uncertain(lightDecodeError(http.StatusAccepted, err.Error()))
	}
	if err := ValidateLightV2Progress(before, cancelled); err != nil {
		return uncertain(lightDecodeError(http.StatusAccepted, err.Error()))
	}
	switch cancelled.Status {
	case "cancelling", "cancelled", "succeeded", "failed":
	default:
		return uncertain(lightDecodeError(http.StatusAccepted, "cancellation response did not acknowledge cancellation or a terminal race"))
	}
	confirmed, err := c.LookupLightV2Request(ctx, serial, data, receipt, before.ID)
	if err != nil {
		result.QueryRecoveryAllowed = cancellationQueryRecovery(err)
		if result.QueryRecoveryAllowed {
			result.RecoveryAnchor = &cancelled
		}
		return uncertain(err)
	}
	if err := ValidateLightV2Progress(cancelled, confirmed); err != nil {
		return uncertain(fmt.Errorf("original receipt did not confirm cancellation response: %w", err))
	}
	result.Job = confirmed
	return result, nil
}

// A later receipt may settle a transport/server failure. An integrity failure
// must remain visible even if another GET happens to look plausible afterward.
func cancellationQueryRecovery(err error) bool {
	var malformed *DecodeError
	if errors.As(err, &malformed) {
		return false
	}
	var api *Error
	if errors.As(err, &api) {
		return api.Status >= 500 && api.Status != http.StatusNotImplemented
	}
	var transport net.Error
	return errors.As(err, &transport) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
