package control

import "time"

type recordKind uint8

const (
	recordSequential recordKind = iota
	recordFrames
)

// Lifecycle is shared privately; legacy Job and FrameJob remain distinct wire
// snapshots and cannot accidentally expose an empty or fabricated Program.
type recordState struct {
	ID, Device, Status string
	CreatedAt          time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
	Error              string
}

func (r *record) legacySnapshot() Job {
	return cloneJob(Job{ID: r.state.ID, Device: r.state.Device, Status: r.state.Status,
		Program: *r.program, CompletedSteps: r.completedSteps, TotalSteps: r.totalSteps,
		CreatedAt: r.state.CreatedAt, StartedAt: r.state.StartedAt, FinishedAt: r.state.FinishedAt, Error: r.state.Error})
}

func cloneFrameJob(j FrameJob) FrameJob {
	j.Program = cloneFrameProgram(j.Program)
	if j.StartedAt != nil {
		value := *j.StartedAt
		j.StartedAt = &value
	}
	if j.FinishedAt != nil {
		value := *j.FinishedAt
		j.FinishedAt = &value
	}
	return j
}

func (r *record) frameSnapshot() FrameJob {
	return cloneFrameJob(FrameJob{ID: r.state.ID, Device: r.state.Device, Status: r.state.Status,
		Program: *r.frames, CompletedFrames: r.completedFrames, TotalFrames: r.totalFrames,
		CompletedTargetWrites: r.completedTargetWrites, PlannedTargetWrites: r.plannedTargetWrites,
		CreatedAt: r.state.CreatedAt, StartedAt: r.state.StartedAt, FinishedAt: r.state.FinishedAt, Error: r.state.Error})
}

func (r *record) finishMode() string {
	if r.kind == recordFrames {
		return r.frames.OnFinish
	}
	return r.program.OnFinish
}

func (r *record) retainedCost() int {
	if r.kind == recordFrames {
		cost := len(r.frames.Initial)
		for _, frame := range r.frames.Frames {
			cost += len(frame.Targets)
		}
		return cost
	}
	return len(r.program.Steps)
}
