package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

const patternV2RecordContract = "nb1-pattern-record/experimental-v2"
const patternV2PreviewContract = "nb1-pattern-ref-preview/experimental-v1"
const patternV2ExecutionDomain = "nb1-pattern-execution/experimental-v1"

// Independent, closed wire types. These verify protocol data, never compile an
// author Action or import the controller, Store, CLI, or USB dependency tree.
type patternV2Ref struct {
	RecordContract string `json:"record_contract"`
	ID             string `json:"id"`
	Revision       string `json:"revision"`
}
type patternV2Execution struct {
	Kind     string          `json:"kind"`
	Contract string          `json:"contract"`
	Program  json.RawMessage `json:"program"`
}
type patternV2Envelope struct {
	PatternRef      patternV2Ref    `json:"pattern_ref"`
	ExecutionDigest string          `json:"execution_digest"`
	JobKind         string          `json:"job_kind"`
	Job             json.RawMessage `json:"job"`
}
type patternV2Preview struct {
	SchemaVersion    int                `json:"schema_version"`
	Contract         string             `json:"contract"`
	PatternRef       patternV2Ref       `json:"pattern_ref"`
	ExecutionDigest  string             `json:"execution_digest"`
	Execution        patternV2Execution `json:"execution"`
	Preview          json.RawMessage    `json:"preview"`
	PatternExecution frameExecutionWire `json:"pattern_execution"`
}
type patternV2Snapshot struct {
	id, device, status, problem string
	created                     time.Time
	started, finished           *time.Time
	completed, writes           int
}

func validPatternV2Ref(r patternV2Ref) bool {
	return r.RecordContract == patternV2RecordContract && patternID.MatchString(r.ID) && patternRevision.MatchString(r.Revision)
}

func patternV2ProgramDigest(p previewProgram) (string, error) {
	if err := validateFrozenPatternProgram(p); err != nil {
		return "", err
	}
	steps := make([]any, len(p.Steps))
	for i, s := range p.Steps {
		steps[i] = map[string]any{"led": s.LED, "color": s.Color, "fade_ms": s.FadeMS, "hold_ms": s.HoldMS}
	}
	return patternV2Hash("program", compileContract, map[string]any{"steps": steps, "repeat": p.Repeat, "on_finish": p.OnFinish})
}
func patternV2FrameDigest(p frameProgramWire) (string, error) {
	if _, err := validateFrameProgram(p); err != nil {
		return "", err
	}
	targets := func(values []frameTargetWire) []any {
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = map[string]any{"led": v.LED, "color": v.Color}
		}
		return out
	}
	frames := make([]any, len(p.Frames))
	for i, f := range p.Frames {
		frames[i] = map[string]any{"targets": targets(f.Targets), "fade_ms": f.FadeMS, "interval_ms": f.IntervalMS}
	}
	return patternV2Hash("frame_program", frameCompileContract, map[string]any{"contract": p.Contract, "initial": targets(p.Initial), "frames": frames, "repeat": p.Repeat, "on_finish": p.OnFinish})
}
func patternV2Hash(kind, contract string, p map[string]any) (string, error) {
	// Every layer is closed ASCII/integer data. encoding/json sorts map keys;
	// array order and explicit zero fields match the frozen P1 hash domain.
	b, err := json.Marshal(map[string]any{"kind": kind, "contract": contract, "program": p})
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte(patternV2ExecutionDomain+"\n"), b...))
	return fmt.Sprintf("sha256:%x", h), nil
}
func validatePatternV2Execution(e patternV2Execution) (string, error) {
	switch e.Kind {
	case "program":
		if e.Contract != compileContract {
			return "", errors.New("unsupported Pattern Program contract")
		}
		var p previewProgram
		if err := decodeSoftwareWire(e.Program, &p); err != nil {
			return "", err
		}
		return patternV2ProgramDigest(p)
	case "frame_program":
		if e.Contract != frameCompileContract {
			return "", errors.New("unsupported Pattern FrameProgram contract")
		}
		var p frameProgramWire
		if err := decodeSoftwareWire(e.Program, &p); err != nil {
			return "", err
		}
		return patternV2FrameDigest(p)
	default:
		return "", errors.New("unsupported Pattern execution kind")
	}
}
func validatePatternV2ProgramJob(j patternJobWire) error {
	if err := validateFrozenPatternProgram(j.Program); err != nil {
		return err
	}
	if !validLowerHexID(j.ID, "job_") || !validFrameSerial(j.Device) || j.CreatedAt.IsZero() || j.TotalSteps != len(j.Program.Steps)*j.Program.Repeat || j.CompletedSteps < 0 || j.CompletedSteps > j.TotalSteps {
		return errors.New("invalid Pattern Program Job identity or counts")
	}
	return validatePatternV2Lifecycle(patternV2Snapshot{j.ID, j.Device, j.Status, j.Error, j.CreatedAt, j.StartedAt, j.FinishedAt, j.CompletedSteps, 0}, j.TotalSteps, 0)
}
func validatePatternV2Lifecycle(j patternV2Snapshot, total, writes int) error {
	if j.started != nil && j.started.Before(j.created) || j.finished != nil && j.finished.Before(j.created) || j.started != nil && j.finished != nil && j.finished.Before(*j.started) {
		return errors.New("Pattern Job timestamps are inconsistent")
	}
	switch j.status {
	case "queued":
		if j.started != nil || j.finished != nil || j.completed != 0 || j.writes != 0 {
			return errors.New("queued Pattern Job already reports execution")
		}
	case "running", "cancelling":
		if j.started == nil || j.finished != nil {
			return errors.New("active Pattern Job has invalid timestamps")
		}
	case "succeeded":
		if j.started == nil || j.finished == nil || j.completed != total || j.writes != writes || j.problem != "" {
			return errors.New("successful Pattern Job is not complete")
		}
	case "failed", "cancelled":
		if j.finished == nil {
			return errors.New("terminal Pattern Job lacks finished_at")
		}
	default:
		return errors.New("unknown Pattern Job status")
	}
	return nil
}
func validatePatternV2Envelope(e patternV2Envelope, ref patternV2Ref, serial string, knownID, knownDigest *string) (patternV2Snapshot, error) {
	var s patternV2Snapshot
	if !validPatternV2Ref(e.PatternRef) || e.PatternRef != ref || !patternRevision.MatchString(e.ExecutionDigest) {
		return s, errors.New("Pattern envelope changed its original Ref or digest syntax")
	}
	var digest string
	var err error
	switch e.JobKind {
	case "program":
		var j patternJobWire
		if err = decodeSoftwareWire(e.Job, &j); err != nil {
			return s, err
		}
		if err = validatePatternV2ProgramJob(j); err != nil {
			return s, err
		}
		digest, err = patternV2ProgramDigest(j.Program)
		s = patternV2Snapshot{j.ID, j.Device, j.Status, j.Error, j.CreatedAt, j.StartedAt, j.FinishedAt, j.CompletedSteps, 0}
	case "frame":
		var j frameJobWire
		if err = decodeSoftwareWire(e.Job, &j); err != nil {
			return s, err
		}
		if err = validateFrameJob(j); err != nil {
			return s, err
		}
		digest, err = patternV2FrameDigest(j.Program)
		s = patternV2Snapshot{j.ID, j.Device, j.Status, j.Error, j.CreatedAt, j.StartedAt, j.FinishedAt, j.CompletedFrames, j.CompletedTargetWrites}
	default:
		return s, errors.New("Pattern envelope has an unsupported job_kind")
	}
	if err != nil {
		return patternV2Snapshot{}, err
	}
	if digest != e.ExecutionDigest || s.device != serial || knownID != nil && s.id != *knownID || knownDigest != nil && digest != *knownDigest {
		return patternV2Snapshot{}, errors.New("Pattern envelope does not bind the original device, Job or recomputed execution digest")
	}
	return s, nil
}
func validatePatternV2Progress(before, after patternV2Envelope, serial string) error {
	a, err := validatePatternV2Envelope(before, before.PatternRef, serial, nil, nil)
	if err != nil {
		return err
	}
	b, err := validatePatternV2Envelope(after, before.PatternRef, serial, &a.id, &before.ExecutionDigest)
	if err != nil {
		return err
	}
	if before.JobKind != after.JobKind || !a.created.Equal(b.created) || a.started != nil && (b.started == nil || !a.started.Equal(*b.started)) || b.completed < a.completed || b.writes < a.writes {
		return errors.New("Pattern Job identity, timestamps or progress regressed")
	}
	// The digest binds the complete frozen program, not mutable progress.
	if patternV2Terminal(a.status) {
		if a.status != b.status || a.problem != b.problem || a.completed != b.completed || a.writes != b.writes || a.finished == nil || b.finished == nil || !a.finished.Equal(*b.finished) {
			return errors.New("terminal Pattern Job changed")
		}
		return nil
	}
	if a.status != "queued" && b.status == "queued" || a.status == "cancelling" && (b.status == "running" || b.status == "succeeded") {
		return errors.New("Pattern Job status regressed after cancellation")
	}
	return nil
}
func patternV2Terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}
func validatePatternV2Preview(v patternV2Preview, args patternV2PreviewArgs) error {
	if v.SchemaVersion != 1 || v.Contract != patternV2PreviewContract || v.PatternRef != args.PatternRef {
		return errors.New("Pattern preview changed its contract or selected Ref")
	}
	digest, err := validatePatternV2Execution(v.Execution)
	if err != nil {
		return err
	}
	if digest != v.ExecutionDigest {
		return errors.New("Pattern preview execution digest mismatch")
	}
	switch v.PatternExecution.Reason {
	case "pattern_service_configured":
		if !v.PatternExecution.Available {
			return errors.New("inconsistent Pattern software availability")
		}
	case "pattern_service_unsupported", "pattern_library_unconfigured", "pattern_library_configuration_error":
		if v.PatternExecution.Available {
			return errors.New("inconsistent Pattern software availability")
		}
	default:
		return errors.New("unsupported Pattern software availability reason")
	}
	if v.Execution.Kind == "program" {
		var p previewResult
		if err := decodeSoftwareWire(v.Preview, &p); err != nil {
			return err
		}
		var frozen previewProgram
		if err := decodeSoftwareWire(v.Execution.Program, &frozen); err != nil {
			return err
		}
		input := program{frozen.Steps, frozen.Repeat, frozen.OnFinish}
		return validatePreviewResult(p, previewArgs{Program: &input, Serial: args.Serial})
	}
	var p framePreviewWire
	if err := decodeSoftwareWire(v.Preview, &p); err != nil {
		return err
	}
	var frozen frameProgramWire
	if err := decodeSoftwareWire(v.Execution.Program, &frozen); err != nil {
		return err
	}
	facts, err := validateFrameProgram(frozen)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p.Program, frozen) || p.Contract != frameCompileContract || p.TotalFrames != facts.frames || p.StoredTargetWrites != facts.storedWrites || p.PlannedTargetWrites != facts.writes || p.CycleDurationMS != facts.cycle || p.NominalInitialMS != facts.initial || p.NominalTailMS != facts.tail || p.NominalDurationMS != facts.duration || p.TimingBasis != "host-nominal-excludes-native-io" {
		return errors.New("Pattern Frame preview arithmetic or frozen program mismatch")
	}
	if args.Serial == nil && p.Device != nil || args.Serial != nil && (p.Device == nil || p.Device.Serial != *args.Serial || p.Device.Connection == "") {
		return errors.New("Pattern Frame preview did not echo the requested cached device")
	}
	caps := []string{"set", "raw-rgb", "frame-program-experimental-v1"}
	if frozen.OnFinish == "hold" {
		caps = append(caps, "hold")
	}
	if !reflect.DeepEqual(p.RequiredCapabilities, caps) {
		return errors.New("Pattern Frame preview capabilities mismatch")
	}
	if p.Execution.Available {
		if args.Serial == nil || !v.PatternExecution.Available || p.Execution.Reason != "cached_profile_supported" || p.Device.Connection != "online" {
			return errors.New("unbound Pattern Frame availability")
		}
		return nil
	}
	switch p.Execution.Reason {
	case "not_selected":
		if args.Serial != nil {
			return errors.New("selected Pattern device was not checked")
		}
	case "device_not_configured":
		if args.Serial == nil || p.Device.Connection != "missing" {
			return errors.New("inconsistent missing Pattern device")
		}
	case "device_not_online":
		if args.Serial == nil || p.Device.Connection == "online" {
			return errors.New("inconsistent offline Pattern device")
		}
	case "missing_capabilities":
		if args.Serial == nil || p.Device.Connection != "online" {
			return errors.New("inconsistent Pattern capabilities check")
		}
	case "pattern_service_unsupported":
		if v.PatternExecution.Reason != "pattern_service_unsupported" {
			return errors.New("inconsistent Pattern service availability")
		}
	default:
		return errors.New("unsupported Pattern device availability reason")
	}
	return nil
}
