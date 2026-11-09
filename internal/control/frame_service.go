package control

import (
	"context"
	"time"
)

type FrameJob struct {
	ID                    string       `json:"id"`
	Device                string       `json:"device"`
	Status                string       `json:"status"`
	Program               FrameProgram `json:"program"`
	CompletedFrames       int          `json:"completed_frames"`
	TotalFrames           int          `json:"total_frames"`
	CompletedTargetWrites int          `json:"completed_target_writes"`
	PlannedTargetWrites   int          `json:"planned_target_writes"`
	CreatedAt             time.Time    `json:"created_at"`
	StartedAt             *time.Time   `json:"started_at,omitempty"`
	FinishedAt            *time.Time   `json:"finished_at,omitempty"`
	Error                 string       `json:"error,omitempty"`
}

type FrameReceiptResult struct {
	Job              FrameJob `json:"job"`
	Replayed         bool     `json:"replayed"`
	DaemonInstanceID string   `json:"daemon_instance_id"`
}

// FrameService is opt-in. Existing Service and ReceiptService stay unchanged.
type FrameService interface {
	Instance() InstanceInfo
	SubmitFrameWithReceipt(context.Context, string, FrameProgram, string, ReceiptRequest) (FrameReceiptResult, error)
	FrameJob(string) (FrameJob, error)
	CancelFrame(context.Context, string) (FrameJob, error)
	FrameRequest(string, string, string) (FrameJob, error)
}

// FrameExecutionForDevice checks cached facts only. It neither probes hardware
// nor proves optical performance, admission, or a particular daemon's support.
func FrameExecutionForDevice(state *DeviceState) FrameExecutionAvailability {
	if state == nil {
		return FrameExecutionAvailability{Reason: "not_selected"}
	}
	if state.Connection != "online" {
		return FrameExecutionAvailability{Reason: "device_not_online"}
	}
	caps := make(map[string]bool)
	for _, value := range state.Capabilities {
		caps[value] = true
	}
	if !caps["set"] || !caps["raw-rgb"] || !caps["mk2-basic"] {
		return FrameExecutionAvailability{Reason: "missing_capabilities"}
	}
	return FrameExecutionAvailability{Available: true, Reason: "cached_profile_supported"}
}

func (e *Engine) FrameJob(id string) (FrameJob, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.jobs[id]
	if !ok || r.origin != originLegacy || r.kind != recordFrames {
		return FrameJob{}, ErrNotFound
	}
	return r.frameSnapshot(), nil
}

func (e *Engine) CancelFrame(ctx context.Context, id string) (FrameJob, error) {
	if err := ctx.Err(); err != nil {
		return FrameJob{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.jobs[id]
	if !ok || r.origin != originLegacy || r.kind != recordFrames {
		return FrameJob{}, ErrNotFound
	}
	e.cancelRecordLocked(r)
	return r.frameSnapshot(), nil
}

var _ FrameService = (*Engine)(nil)
