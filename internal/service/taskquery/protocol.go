package taskquery

import (
	"strconv"

	"github.com/b1ug/nb1/internal/jsoninput"
)

type request struct {
	SchemaVersion int    `json:"schema_version"`
	SID           string `json:"sid"`
	TaskPath      string `json:"task_path"`
}

type response struct {
	SchemaVersion  *int    `json:"schema_version"`
	Outcome        *string `json:"outcome"`
	CallerSID      *string `json:"caller_sid"`
	TaskPath       *string `json:"task_path"`
	Stage          *string `json:"stage"`
	HRESULT        *string `json:"hresult,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
	SchedulerState *uint32 `json:"scheduler_state,omitempty"`
	LastTaskResult *int32  `json:"last_task_result,omitempty"`
}

func parseResponse(data []byte, owner identity, status Status) (Status, error) {
	var out response
	bad := func() (Status, error) {
		status.Failure = &Failure{Kind: "protocol", Stage: "bridge"}
		return status, ErrProtocol
	}
	if len(data) > maxOutput || jsoninput.DecodeObject(data, &out) != nil ||
		out.SchemaVersion == nil || *out.SchemaVersion != 1 ||
		out.Outcome == nil || out.CallerSID == nil || *out.CallerSID != owner.sid ||
		out.TaskPath == nil || *out.TaskPath != owner.taskPath || out.Stage == nil {
		return bad()
	}
	if *out.Outcome == "present" {
		if *out.Stage != "complete" || out.HRESULT != nil || out.Enabled == nil ||
			out.SchedulerState == nil || out.LastTaskResult == nil || *out.SchedulerState > 4 {
			return bad()
		}
		names := [...]string{"unknown", "disabled", "queued", "ready", "running"}
		status.Presence = Present
		status.Task = &Properties{Enabled: *out.Enabled, SchedulerState: *out.SchedulerState,
			SchedulerStateName: names[*out.SchedulerState], LastTaskResult: *out.LastTaskResult}
		return status, nil
	}
	if *out.Outcome != "error" || out.Enabled != nil || out.SchedulerState != nil || out.LastTaskResult != nil ||
		out.HRESULT == nil || !validHRESULT(*out.HRESULT) {
		return bad()
	}
	switch *out.Stage {
	case "bootstrap", "input", "identity", "activate", "connect", "folder", "get_task", "properties", "release":
	default:
		return bad()
	}
	status.Failure = &Failure{Kind: "native", Stage: *out.Stage, HRESULT: *out.HRESULT}
	if *out.Stage == "get_task" && *out.HRESULT == "0x80070002" {
		// GetTask documents HRESULT, not an exact missing-task code contract.
		// Keep this candidate distinguishable for future native validation.
		status.Failure.Kind = "not_found_unverified"
		return status, ErrAbsenceUnverified
	}
	return status, ErrNative
}

func validHRESULT(value string) bool {
	if len(value) != 10 || value[:2] != "0x" {
		return false
	}
	for _, c := range value[2:] {
		if !(c >= '0' && c <= '9') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	_, err := strconv.ParseUint(value[2:], 16, 32)
	return err == nil
}
