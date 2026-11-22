package daemonclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/b1ug/nb1/internal/control"
)

type PlaybackRequests struct {
	Requests []control.PlaybackRequest `json:"requests"`
	Limit    int                       `json:"limit"`
	Count    int                       `json:"count"`
}

func (c *Client) PlaybackStatus(ctx context.Context) (control.PlaybackState, error) {
	var result control.PlaybackState
	err := c.do(ctx, http.MethodGet, "/v1/playback", nil, &result)
	return result, err
}

func (c *Client) PlaybackRequests(ctx context.Context, limit int) (PlaybackRequests, error) {
	path := "/v1/playback/requests"
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", path, limit)
	}
	var result PlaybackRequests
	err := c.do(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *Client) SetPlaybackOverride(ctx context.Context, mode string, duration time.Duration) (control.PlaybackState, error) {
	if duration <= 0 || duration%time.Millisecond != 0 {
		return control.PlaybackState{}, fmt.Errorf("duration must be a positive whole number of milliseconds")
	}
	input := struct {
		Mode       string `json:"mode"`
		DurationMS int64  `json:"duration_ms"`
	}{Mode: mode, DurationMS: duration.Milliseconds()}
	var result control.PlaybackState
	err := c.do(ctx, http.MethodPost, "/v1/playback/override", input, &result)
	return result, err
}

func (c *Client) ClearPlaybackOverride(ctx context.Context) (control.PlaybackState, error) {
	var result control.PlaybackState
	err := c.do(ctx, http.MethodDelete, "/v1/playback/override", nil, &result)
	return result, err
}
