package control

import (
	"fmt"
	"slices"
	"time"

	"github.com/b1ug/nb1/internal/playback"
)

const (
	PlaybackOverrideSuppress = "suppress"
	PlaybackOverridePermit   = "permit"
	maxPlaybackOverride      = 30 * 24 * time.Hour
)

// PlaybackOverride lives only in the running daemon. A daemon restart clears it.
type PlaybackOverride struct {
	Mode      string    `json:"mode"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PlaybackState describes the policy and effective admission result at the
// time it was read. It does not describe a Job or the physical LED output.
type PlaybackState struct {
	Mode         string            `json:"mode"`
	Timezone     string            `json:"timezone"`
	Status       string            `json:"status"`
	Reason       string            `json:"reason"`
	EvaluatedAt  time.Time         `json:"evaluated_at"`
	Override     *PlaybackOverride `json:"override,omitempty"`
	HistoryLimit int               `json:"history_limit"`
}

// PlaybackRequest is one policy decision. It records no request body or user
// content, only enough metadata to diagnose whether the policy admitted it.
type PlaybackRequest struct {
	ID         uint64    `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	Kind       string    `json:"kind"`
	Target     string    `json:"target,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	JobID      string    `json:"job_id,omitempty"`
}

// PlaybackService is optional so existing embedders and test doubles that
// implement Service remain source-compatible.
type PlaybackService interface {
	PlaybackStatus() PlaybackState
	PlaybackRequests(limit int) ([]PlaybackRequest, error)
	SetPlaybackOverride(mode string, duration time.Duration) (PlaybackState, error)
	ClearPlaybackOverride() (PlaybackState, error)
}

func (e *Engine) playbackStateLocked(at time.Time) PlaybackState {
	allowed, reason := e.options.PlaybackPolicy.Allows(at)
	state := PlaybackState{
		Mode: e.options.PlaybackPolicy.Mode, Timezone: e.options.PlaybackPolicy.Timezone,
		Status: "suppressed", Reason: reason, EvaluatedAt: at.UTC(),
		HistoryLimit: e.options.PlaybackPolicy.HistoryLimit,
	}
	if state.Mode == "" {
		state.Mode = playback.ModeAlways
	}
	if state.Timezone == "" {
		state.Timezone = "local"
	}
	if state.HistoryLimit == 0 {
		state.HistoryLimit = playback.DefaultHistoryLimit
	}
	if e.playbackOverride != nil {
		if !at.Before(e.playbackOverride.ExpiresAt) {
			e.playbackOverride = nil
		} else {
			override := *e.playbackOverride
			state.Override = &override
			allowed = override.Mode == PlaybackOverridePermit
			if allowed {
				reason = "runtime_override_permit"
			} else {
				reason = "runtime_override_suppress"
			}
			state.Reason = reason
		}
	}
	if allowed {
		state.Status = "allowed"
	}
	return state
}

func (e *Engine) PlaybackStatus() PlaybackState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.playbackStateLocked(e.now())
}

func (e *Engine) SetPlaybackOverride(mode string, duration time.Duration) (PlaybackState, error) {
	if mode != PlaybackOverrideSuppress && mode != PlaybackOverridePermit {
		return PlaybackState{}, fmt.Errorf("%w: playback override mode must be suppress or permit", ErrInvalid)
	}
	if duration <= 0 || duration > maxPlaybackOverride {
		return PlaybackState{}, fmt.Errorf("%w: playback override duration must be greater than zero and no longer than 720h", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return PlaybackState{}, ErrClosed
	}
	now := e.now()
	e.playbackOverride = &PlaybackOverride{Mode: mode, ExpiresAt: now.Add(duration).UTC()}
	return e.playbackStateLocked(now), nil
}

func (e *Engine) ClearPlaybackOverride() (PlaybackState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return PlaybackState{}, ErrClosed
	}
	e.playbackOverride = nil
	return e.playbackStateLocked(e.now()), nil
}

func (e *Engine) PlaybackRequests(limit int) ([]PlaybackRequest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit <= 0 {
		limit = e.options.PlaybackPolicy.HistoryLimit
	}
	if limit <= 0 {
		limit = playback.DefaultHistoryLimit
	}
	if limit > e.options.PlaybackPolicy.HistoryLimit && e.options.PlaybackPolicy.HistoryLimit > 0 {
		limit = e.options.PlaybackPolicy.HistoryLimit
	}
	if limit > playback.MaxHistoryLimit {
		return nil, fmt.Errorf("%w: limit must not exceed %d", ErrInvalid, playback.MaxHistoryLimit)
	}
	start := len(e.playbackHistory) - limit
	if start < 0 {
		start = 0
	}
	result := make([]PlaybackRequest, len(e.playbackHistory[start:]))
	copy(result, e.playbackHistory[start:])
	// Return newest first for convenient terminal/API inspection.
	slices.Reverse(result)
	return result, nil
}

func (e *Engine) requirePlaybackLocked(kind, target, requestID, jobID string, at time.Time) error {
	state := e.playbackStateLocked(at)
	if state.Status == "allowed" {
		return nil
	}
	e.appendPlaybackRequestLocked(PlaybackRequest{ReceivedAt: at.UTC(), Decision: "suppressed", Reason: state.Reason, Kind: kind, Target: target, RequestID: requestID, JobID: jobID})
	return fmt.Errorf("%w: %s", ErrPlaybackSuppressed, state.Reason)
}

func (e *Engine) recordPlaybackAllowedLocked(kind, target, requestID, jobID string, at time.Time) {
	state := e.playbackStateLocked(at)
	e.appendPlaybackRequestLocked(PlaybackRequest{ReceivedAt: at.UTC(), Decision: "allowed", Reason: state.Reason, Kind: kind, Target: target, RequestID: requestID, JobID: jobID})
}

func (e *Engine) appendPlaybackRequestLocked(entry PlaybackRequest) {
	e.playbackNextID++
	entry.ID = e.playbackNextID
	e.playbackHistory = append(e.playbackHistory, entry)
	limit := e.options.PlaybackPolicy.HistoryLimit
	if limit <= 0 {
		limit = playback.DefaultHistoryLimit
	}
	if len(e.playbackHistory) > limit {
		copy(e.playbackHistory, e.playbackHistory[len(e.playbackHistory)-limit:])
		e.playbackHistory = e.playbackHistory[:limit]
	}
}
