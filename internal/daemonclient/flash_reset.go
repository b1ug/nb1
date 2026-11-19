package daemonclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/httpapi"
)

// ResetFlash obtains one short-lived ticket, then posts it once. A returned
// result records the transaction stage even if the Flash save was uncertain.
func (c *Client) ResetFlash(ctx context.Context, serial, preset string) (flashreset.Result, error) {
	var result flashreset.Result
	if _, err := hex.DecodeString(serial); err != nil || len(serial) != 8 {
		return result, fmt.Errorf("Flash reset requires an explicit 8-digit hexadecimal device serial")
	}
	if !flashreset.Valid(preset) {
		return result, fmt.Errorf("unknown Flash reset preset %q", preset)
	}
	path := "/v1/devices/" + serial + "/flash/reset"
	var ticket httpapi.FlashResetTicket
	if err := c.sequenceV1Request(ctx, http.MethodGet, path+"-ticket", nil, &ticket, http.StatusOK); err != nil {
		return result, fmt.Errorf("obtain Flash reset ticket before writing: %w", err)
	}
	if len(ticket.Ticket) != 32 || !lowerHex(ticket.Ticket) {
		return result, fmt.Errorf("daemon returned an invalid Flash reset ticket; no reset was sent")
	}
	var failure *Error
	err := c.sequenceV1Response(ctx, http.MethodPost, path, map[string]string{"preset": preset}, map[string]string{httpapi.FlashTicketHeader: ticket.Ticket}, func(response *http.Response) error {
		if response.StatusCode == http.StatusOK {
			if err := decodeCompilerResponse(response, &result); err != nil {
				return err
			}
			if result.Status != flashreset.Acknowledged {
				return fmt.Errorf("unexpected Flash success status %q", result.Status)
			}
		} else {
			var body httpapi.FlashResetFailure
			if err := decodeCompilerResponse(response, &body); err != nil {
				return err
			}
			result = body.Result
			if (result.Status != flashreset.StagingFailed && result.Status != flashreset.Unconfirmed) || body.Error.Code != "flash_"+result.Status || body.Error.Message == "" {
				return fmt.Errorf("invalid Flash failure response")
			}
			failure = &Error{Status: response.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
		}
		return result.Validate(serial, preset)
	}, http.StatusOK, http.StatusBadGateway)
	if err != nil {
		var rejection *Error
		if errors.As(err, &rejection) && rejection.Code != "flash_ticket_used_or_expired" && ((rejection.Status >= 400 && rejection.Status < 500) || rejection.Code == "device_offline" || rejection.Code == "flash_reset_unsupported") {
			return flashreset.Result{}, err
		}
		return flashreset.Result{}, fmt.Errorf("Flash reset outcome unknown; do not automatically retry: %w", err)
	}
	if failure != nil {
		return result, failure
	}
	return result, nil
}
