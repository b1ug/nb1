package daemonclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/playback"
)

// Allow the daemon's full compact history with 10,000 rows and bounded
// serials, request IDs and Job IDs. Other responses retain their usual limit.
const maxPlaybackHistoryResponseBytes = 5 << 20

type PlaybackRequests struct {
	Requests []control.PlaybackRequest `json:"requests"`
	Limit    int                       `json:"limit"`
	Count    int                       `json:"count"`
}

func (c *Client) PlaybackStatus(ctx context.Context) (control.PlaybackState, error) {
	return c.playbackState(ctx, http.MethodGet, "/v1/playback", nil)
}

func (c *Client) PlaybackRequests(ctx context.Context, limit int) (PlaybackRequests, error) {
	path := "/v1/playback/requests"
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", path, limit)
	}
	var result PlaybackRequests
	err := c.sequenceV1Response(ctx, http.MethodGet, path, nil, nil, func(response *http.Response) error {
		return decodeBoundedResponse(response, &result, "Playback history", maxPlaybackHistoryResponseBytes)
	}, http.StatusOK)
	if err != nil {
		return PlaybackRequests{}, playbackRouteError(err)
	}
	if result.Requests == nil || result.Limit < 1 || result.Limit > playback.MaxHistoryLimit || (limit > 0 && result.Limit > limit) ||
		result.Count != len(result.Requests) || result.Count > result.Limit {
		return PlaybackRequests{}, &DecodeError{Status: http.StatusOK, Err: errors.New("invalid Playback history bounds")}
	}
	return result, nil
}

func (c *Client) SetPlaybackOverride(ctx context.Context, mode string, duration time.Duration) (control.PlaybackState, error) {
	if duration <= 0 || duration%time.Millisecond != 0 {
		return control.PlaybackState{}, fmt.Errorf("duration must be a positive whole number of milliseconds")
	}
	input := struct {
		Mode       string `json:"mode"`
		DurationMS int64  `json:"duration_ms"`
	}{Mode: mode, DurationMS: duration.Milliseconds()}
	result, err := c.playbackState(ctx, http.MethodPost, "/v1/playback/override", input)
	if err == nil && (result.Override == nil || result.Override.Mode != mode || result.Override.ExpiresAt.Sub(result.EvaluatedAt) > duration) {
		return control.PlaybackState{}, &DecodeError{Status: http.StatusOK, Err: errors.New("daemon did not confirm the requested Playback override")}
	}
	return result, err
}

func (c *Client) ClearPlaybackOverride(ctx context.Context) (control.PlaybackState, error) {
	result, err := c.playbackState(ctx, http.MethodDelete, "/v1/playback/override", nil)
	if err == nil && result.Override != nil {
		return control.PlaybackState{}, &DecodeError{Status: http.StatusOK, Err: errors.New("daemon did not confirm clearing the Playback override")}
	}
	return result, err
}

func (c *Client) playbackState(ctx context.Context, method, path string, input any) (control.PlaybackState, error) {
	var state control.PlaybackState
	if err := c.sequenceV1Request(ctx, method, path, input, &state, http.StatusOK); err != nil {
		return control.PlaybackState{}, playbackRouteError(err)
	}
	if state.Timezone == "" || state.EvaluatedAt.IsZero() || state.HistoryLimit < 1 || state.HistoryLimit > playback.MaxHistoryLimit ||
		!validPlaybackDecision(state) {
		return control.PlaybackState{}, &DecodeError{Status: http.StatusOK, Err: errors.New("invalid Playback state")}
	}
	return state, nil
}

func validPlaybackDecision(state control.PlaybackState) bool {
	if state.Mode != playback.ModeAlways && state.Mode != playback.ModeNever && state.Mode != playback.ModeWeekly {
		return false
	}
	if override := state.Override; override != nil {
		if !override.ExpiresAt.After(state.EvaluatedAt) || state.Reason != "runtime_override_"+override.Mode {
			return false
		}
		return (override.Mode == control.PlaybackOverrideSuppress && state.Status == "suppressed") ||
			(override.Mode == control.PlaybackOverridePermit && state.Status == "allowed")
	}
	switch state.Mode {
	case playback.ModeAlways:
		return state.Status == "allowed" && state.Reason == "mode_always"
	case playback.ModeNever:
		return state.Status == "suppressed" && state.Reason == "mode_never"
	default: // weekly
		return (state.Status == "allowed" && (state.Reason == "weekly_window" || state.Reason == "weekly_overnight_window")) ||
			(state.Status == "suppressed" && state.Reason == "outside_weekly_window")
	}
}

func playbackRouteError(err error) error {
	// None of these routes look up an ID. A structured not_found therefore
	// identifies a daemon without the Playback API.
	var failure *Error
	if errors.As(err, &failure) && ((failure.Status == http.StatusNotFound && failure.Code == "not_found") ||
		(failure.Status == http.StatusNotImplemented && failure.Code == "playback_unsupported")) {
		return &Error{Status: failure.Status, Code: "playback_unsupported", Message: "use a matching nb1 daemon with the sequence-v1 Playback API"}
	}
	return err
}
