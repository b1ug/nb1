// Package taskquery reads one fixed current-user Task Scheduler registration.
// It never establishes ownership, changes a task, or identifies a daemon PID.
package taskquery

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	Present = "present"
	// Absent is reserved until native GetTask missing-HRESULT contracts exist.
	// This version deliberately returns Unknown for every failed GetTask.
	Absent      = "absent"
	Unknown     = "unknown"
	queryBudget = 3 * time.Second
	maxOutput   = 16 * 1024
)

var (
	ErrUnsupported       = errors.New("taskquery requires Windows")
	ErrIdentity          = errors.New("taskquery current identity is unverified or changed")
	ErrTrust             = errors.New("taskquery system PowerShell trust could not be established")
	ErrTransport         = errors.New("taskquery bridge execution failed; task state is unknown")
	ErrProtocol          = errors.New("taskquery bridge response is invalid; task state is unknown")
	ErrNative            = errors.New("taskquery native query failed; task state is unknown")
	ErrAbsenceUnverified = errors.New("taskquery missing HRESULT has no native contract yet; absence is unverified")
)

type Properties struct {
	Enabled            bool   `json:"enabled"`
	SchedulerState     uint32 `json:"scheduler_state"`
	SchedulerStateName string `json:"scheduler_state_name"`
	LastTaskResult     int32  `json:"last_task_result"`
}

type Failure struct {
	Kind    string `json:"kind"`
	Stage   string `json:"stage"`
	HRESULT string `json:"hresult,omitempty"`
}

type Status struct {
	SchemaVersion int         `json:"schema_version"`
	CheckedAt     time.Time   `json:"checked_at"`
	TaskPath      string      `json:"task_path"`
	Presence      string      `json:"presence"`
	Ownership     string      `json:"ownership"`
	Task          *Properties `json:"task,omitempty"`
	Failure       *Failure    `json:"failure,omitempty"`
}

// identity is populated only from taskuser.Current in the production adapter.
// A caller cannot supply a SID, task path, user, server or executable.
type identity struct {
	valid    bool
	sid      string
	taskPath string
	session  uint32
}

type bridgeResult struct {
	output    []byte
	exitCode  int
	truncated bool
}

type runner interface {
	run(context.Context, []byte) (bridgeResult, error)
}

type Reader struct {
	owner   identity
	current func() (identity, error)
	bridge  runner
	now     func() time.Time
}

// New verifies the current ordinary Windows identity and system bridge path.
// Construction does not contact Task Scheduler. Other platforms are unsupported.
func New() (*Reader, error) { return newPlatformReader() }

// Query reads only this process's verified fixed current-user task registration.
// A returned registration is never classified as managed. No action is retried.
func (r *Reader) Query(ctx context.Context) (Status, error) {
	status := Status{SchemaVersion: 1, Presence: Unknown, Ownership: "not_checked"}
	if r == nil || r.current == nil || r.bridge == nil || r.now == nil || !r.owner.valid {
		status.Failure = &Failure{Kind: "identity", Stage: "parent"}
		return status, ErrIdentity
	}
	status.CheckedAt, status.TaskPath = r.now().UTC(), r.owner.taskPath
	ctx, cancel := context.WithTimeout(ctx, queryBudget)
	defer cancel()
	if err := ctx.Err(); err != nil {
		status.Failure = &Failure{Kind: "cancelled", Stage: "parent"}
		return status, err
	}
	fresh, err := r.current()
	if err != nil || !fresh.valid || fresh.sid != r.owner.sid || fresh.taskPath != r.owner.taskPath || fresh.session != r.owner.session {
		status.Failure = &Failure{Kind: "identity", Stage: "parent"}
		return status, ErrIdentity
	}
	input, err := json.Marshal(request{SchemaVersion: 1, SID: fresh.sid, TaskPath: fresh.taskPath})
	if err != nil { // all fields are fixed native identity strings
		status.Failure = &Failure{Kind: "protocol", Stage: "parent"}
		return status, ErrProtocol
	}
	result, err := r.bridge.run(ctx, input)
	if ctx.Err() != nil {
		status.Failure = &Failure{Kind: "cancelled", Stage: "bridge"}
		return status, ctx.Err()
	}
	if errors.Is(err, ErrTrust) {
		status.Failure = &Failure{Kind: "trust", Stage: "bridge"}
		return status, ErrTrust
	}
	if err != nil || result.exitCode != 0 || result.truncated || len(result.output) > maxOutput {
		status.Failure = &Failure{Kind: "transport", Stage: "bridge"}
		return status, ErrTransport
	}
	return parseResponse(result.output, fresh, status)
}
