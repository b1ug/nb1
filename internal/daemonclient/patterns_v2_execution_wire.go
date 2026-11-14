package daemonclient

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
)

type patternExecutionWireV2 struct {
	Kind     string          `json:"kind"`
	Contract string          `json:"contract"`
	Program  json.RawMessage `json:"program"`
}

func typedPatternExecutionV2(data []byte) (control.PatternExecution, error) {
	var wire patternExecutionWireV2
	if err := decodePatternPartV2(data, &wire); err != nil {
		return control.PatternExecution{}, err
	}
	var execution control.PatternExecution
	switch wire.Kind {
	case "program":
		if wire.Contract != control.CompileContract {
			return execution, fmt.Errorf("unsupported frozen Pattern Program contract")
		}
		var p control.Program
		if err := decodePatternPartV2(wire.Program, &p); err != nil {
			return execution, err
		}
		execution = control.PatternExecution{Kind: "program", Program: &p}
	case "frame_program":
		if wire.Contract != control.FrameContract {
			return execution, fmt.Errorf("unsupported frozen Pattern Frame contract")
		}
		var p control.FrameProgram
		if err := decodePatternPartV2(wire.Program, &p); err != nil {
			return execution, err
		}
		execution = control.PatternExecution{Kind: "frame_program", FrameProgram: &p}
	default:
		return execution, fmt.Errorf("unsupported frozen Pattern execution kind")
	}
	return control.ValidatePatternExecution(execution)
}
func validPatternJobIDV2(id string) bool {
	prefix := "job_"
	if strings.HasPrefix(id, "framejob_") {
		prefix = "framejob_"
	}
	return strings.HasPrefix(id, prefix) && len(id) == len(prefix)+32 && lowerHex(id[len(prefix):])
}
func patternJobIDV2(e control.PatternJobEnvelope) string {
	if e.ProgramJob != nil {
		return e.ProgramJob.ID
	}
	if e.FrameJob != nil {
		return e.FrameJob.ID
	}
	return ""
}
func patternStatusV2(e control.PatternJobEnvelope) string {
	if e.ProgramJob != nil {
		return e.ProgramJob.Status
	}
	if e.FrameJob != nil {
		return e.FrameJob.Status
	}
	return ""
}
func patternTerminalV2(s string) bool { return s == "succeeded" || s == "failed" || s == "cancelled" }

type patternLifecycleV2 struct {
	id, device, status, problem string
	created                     time.Time
	started, finished           *time.Time
	completed, writes           int
}

func validatePatternLifecycleV2(j patternLifecycleV2, total, writes int) error {
	if !validPatternJobIDV2(j.id) || !control.ValidDeviceSerial(j.device) || j.created.IsZero() || j.completed < 0 || j.completed > total || j.writes < 0 || j.writes > writes {
		return fmt.Errorf("invalid Pattern Job identity or progress")
	}
	if j.started != nil && (j.started.IsZero() || j.started.Before(j.created)) || j.finished != nil && (j.finished.IsZero() || j.finished.Before(j.created) || j.started != nil && j.finished.Before(*j.started)) {
		return fmt.Errorf("invalid Pattern Job lifecycle timestamps")
	}
	if j.started == nil && (j.completed != 0 || j.writes != 0) {
		return fmt.Errorf("Pattern Job reports progress before execution")
	}
	switch j.status {
	case "queued":
		if j.started != nil || j.finished != nil || j.problem != "" {
			return fmt.Errorf("queued Pattern Job contains execution metadata")
		}
	case "running", "cancelling":
		if j.started == nil || j.finished != nil || j.problem != "" {
			return fmt.Errorf("active Pattern Job has invalid metadata")
		}
	case "succeeded":
		if j.started == nil || j.finished == nil || j.problem != "" || j.completed != total || j.writes != writes {
			return fmt.Errorf("successful Pattern Job is not complete")
		}
	case "failed":
		if j.finished == nil {
			return fmt.Errorf("failed Pattern Job lacks finished_at")
		}
	case "cancelled":
		if j.finished == nil || j.problem != "" {
			return fmt.Errorf("cancelled Pattern Job lacks clean terminal metadata")
		}
	default:
		return fmt.Errorf("unknown Pattern Job status")
	}
	return nil
}
func validatePatternEnvelopeWireV2(w patternEnvelopeWireV2, serial string, ref patternidentity.Ref, knownID, knownDigest string) (control.PatternJobEnvelope, error) {
	if patternidentity.ValidateRef(w.PatternRef) != nil || w.PatternRef != ref || patternidentity.ValidateDigest(w.ExecutionDigest) != nil || knownDigest != "" && w.ExecutionDigest != knownDigest {
		return control.PatternJobEnvelope{}, fmt.Errorf("Pattern response changed original Ref or digest")
	}
	out := control.PatternJobEnvelope{PatternRef: w.PatternRef, ExecutionDigest: w.ExecutionDigest, JobKind: w.JobKind}
	var execution control.PatternExecution
	var snapshot patternLifecycleV2
	switch w.JobKind {
	case "program":
		var j control.Job
		if err := decodePatternPartV2(w.Job, &j); err != nil {
			return control.PatternJobEnvelope{}, err
		}
		if !strings.HasPrefix(j.ID, "job_") {
			return control.PatternJobEnvelope{}, fmt.Errorf("Pattern Program Job ID has wrong kind")
		}
		execution = control.PatternExecution{Kind: "program", Program: &j.Program}
		out.ProgramJob = &j
		p, err := control.Preview(&j.Program, nil)
		if err != nil || j.TotalSteps != p.TotalSteps {
			return control.PatternJobEnvelope{}, fmt.Errorf("Pattern Program Job counts differ from frozen execution")
		}
		snapshot = patternLifecycleV2{j.ID, j.Device, j.Status, j.Error, j.CreatedAt, j.StartedAt, j.FinishedAt, j.CompletedSteps, 0}
		if err = validatePatternLifecycleV2(snapshot, j.TotalSteps, 0); err != nil {
			return control.PatternJobEnvelope{}, err
		}
	case "frame":
		var j control.FrameJob
		if err := decodePatternPartV2(w.Job, &j); err != nil {
			return control.PatternJobEnvelope{}, err
		}
		if !strings.HasPrefix(j.ID, "framejob_") {
			return control.PatternJobEnvelope{}, fmt.Errorf("Pattern Frame Job ID has wrong kind")
		}
		execution = control.PatternExecution{Kind: "frame_program", FrameProgram: &j.Program}
		out.FrameJob = &j
		p, err := control.PreviewFrameProgram(j.Program)
		if err != nil || j.TotalFrames != p.TotalFrames || j.PlannedTargetWrites != p.PlannedTargetWrites {
			return control.PatternJobEnvelope{}, fmt.Errorf("Pattern Frame counts differ from frozen execution")
		}
		snapshot = patternLifecycleV2{j.ID, j.Device, j.Status, j.Error, j.CreatedAt, j.StartedAt, j.FinishedAt, j.CompletedFrames, j.CompletedTargetWrites}
		if err = validatePatternLifecycleV2(snapshot, j.TotalFrames, j.PlannedTargetWrites); err != nil {
			return control.PatternJobEnvelope{}, err
		}
		initial, perFrame := len(j.Program.Initial), len(j.Program.Initial)
		minimum := 0
		if j.CompletedFrames > 0 {
			minimum = initial + j.CompletedFrames*perFrame
		}
		maximum := initial + (j.CompletedFrames+1)*perFrame
		if maximum > j.PlannedTargetWrites {
			maximum = j.PlannedTargetWrites
		}
		if j.CompletedTargetWrites < minimum || j.CompletedTargetWrites > maximum {
			return control.PatternJobEnvelope{}, fmt.Errorf("Pattern frame/write progress disagree")
		}
	default:
		return control.PatternJobEnvelope{}, fmt.Errorf("unsupported Pattern Job kind")
	}
	execution, err := control.ValidatePatternExecution(execution)
	if err != nil {
		return control.PatternJobEnvelope{}, err
	}
	if err = execution.VerifyDigest(w.ExecutionDigest); err != nil {
		return control.PatternJobEnvelope{}, err
	}
	if snapshot.device != serial || knownID != "" && snapshot.id != knownID {
		return control.PatternJobEnvelope{}, fmt.Errorf("Pattern response changed original device or known Job")
	}
	return out, nil
}

// ValidatePatternV2Progress revalidates both mutable exported envelopes and
// requires identity, timestamps and progress to remain bound to one receipt.
func ValidatePatternV2Progress(previous, current control.PatternJobEnvelope) error {
	validate := func(e control.PatternJobEnvelope) (control.PatternJobEnvelope, error) {
		b, err := json.Marshal(e)
		if err != nil {
			return control.PatternJobEnvelope{}, err
		}
		var w patternEnvelopeWireV2
		if err = decodePatternPartV2(b, &w); err != nil {
			return control.PatternJobEnvelope{}, err
		}
		serial := ""
		if e.ProgramJob != nil {
			serial = e.ProgramJob.Device
		} else if e.FrameJob != nil {
			serial = e.FrameJob.Device
		}
		return validatePatternEnvelopeWireV2(w, serial, e.PatternRef, "", "")
	}
	before, err := validate(previous)
	if err != nil {
		return err
	}
	after, err := validate(current)
	if err != nil {
		return err
	}
	if before.PatternRef != after.PatternRef || before.ExecutionDigest != after.ExecutionDigest || before.JobKind != after.JobKind {
		return fmt.Errorf("Pattern identity or execution changed between snapshots")
	}
	if before.FrameJob != nil {
		return ValidateLightV2Progress(*before.FrameJob, *after.FrameJob)
	}
	a, b := before.ProgramJob, after.ProgramJob
	if a.ID != b.ID || a.Device != b.Device || !reflect.DeepEqual(a.Program, b.Program) || !a.CreatedAt.Equal(b.CreatedAt) || a.TotalSteps != b.TotalSteps || a.StartedAt != nil && (b.StartedAt == nil || !a.StartedAt.Equal(*b.StartedAt)) || b.CompletedSteps < a.CompletedSteps {
		return fmt.Errorf("Pattern Program identity, timestamps or progress changed")
	}
	switch a.Status {
	case "queued":
		return nil
	case "running":
		if b.Status != "queued" {
			return nil
		}
	case "cancelling":
		if b.Status == "cancelling" || b.Status == "cancelled" || b.Status == "failed" {
			return nil
		}
	default:
		if reflect.DeepEqual(a, b) {
			return nil
		}
	}
	return fmt.Errorf("Pattern Program lifecycle regressed or changed after completion")
}

func validatePatternRefPreviewV2(v PatternRefPreviewV2, ref patternidentity.Ref, serial *string) error {
	if v.SchemaVersion != 1 || v.Contract != httpapi.PatternRefPreviewContract || v.PatternRef != ref {
		return fmt.Errorf("Pattern preview changed contract or selected Ref")
	}
	execution, err := typedPatternExecutionV2(v.Execution)
	if err != nil {
		return err
	}
	if err = execution.VerifyDigest(v.ExecutionDigest); err != nil {
		return err
	}
	if err = validatePatternAvailabilityV2(v.PatternExecution); err != nil {
		return err
	}
	if v.PatternExecution.Reason == "pattern_identity_admission_not_integrated" {
		return fmt.Errorf("legacy author-preview availability is not a Pattern reference preview contract")
	}
	if execution.Program != nil {
		var p control.PreviewResult
		if err = decodePatternPartV2(v.Preview, &p); err != nil {
			return err
		}
		if !reflect.DeepEqual(p.Program, *execution.Program) {
			return fmt.Errorf("Pattern Program preview changed frozen execution")
		}
		return control.ValidatePreviewResult(p, "program", serial)
	}
	var p LightV2FramePreview
	if err = decodePatternPartV2(v.Preview, &p); err != nil {
		return err
	}
	pure, err := control.PreviewFrameProgram(*execution.FrameProgram)
	if err != nil {
		return err
	}
	expected := lightV2PreviewWire(pure)
	plain := p
	plain.Device = nil
	plain.Execution = expected.Execution
	if !reflect.DeepEqual(plain, expected) {
		return fmt.Errorf("Pattern Frame preview facts differ from frozen execution")
	}
	if serial == nil {
		if p.Device != nil || p.Execution.Available || p.Execution.Reason != "not_selected" && p.Execution.Reason != "pattern_service_unsupported" {
			return fmt.Errorf("untargeted Pattern preview selected a device")
		}
		return nil
	}
	if p.Device == nil || p.Device.Serial != *serial {
		return fmt.Errorf("Pattern preview cached evidence changed selected serial")
	}
	switch p.Device.Connection {
	case "missing", "connecting", "online", "offline", "busy", "error", "closing", "closed":
	default:
		return fmt.Errorf("Pattern preview has unknown connection state")
	}
	if p.Execution.Available {
		if !v.PatternExecution.Available || p.Device.Connection != "online" || p.Execution.Reason != "cached_profile_supported" {
			return fmt.Errorf("Pattern cached execution claim is inconsistent")
		}
		return nil
	}
	switch p.Execution.Reason {
	case "pattern_service_unsupported":
		if !v.PatternExecution.Available && v.PatternExecution.Reason == "pattern_service_unsupported" {
			return nil
		}
	case "device_not_configured":
		if p.Device.Connection == "missing" {
			return nil
		}
	case "device_not_online":
		if p.Device.Connection != "online" && p.Device.Connection != "missing" {
			return nil
		}
	case "missing_capabilities":
		if p.Device.Connection == "online" {
			return nil
		}
	}
	return fmt.Errorf("Pattern Frame availability disagrees with cached device")
}
