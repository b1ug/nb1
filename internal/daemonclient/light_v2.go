package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

type LightV2PreviewDevice struct {
	Serial     string `json:"serial"`
	Connection string `json:"connection"`
}

// Keep this wire type flat: the strict JSON checker deliberately does not
// flatten anonymous embedded structs when collecting permitted JSON fields.
type LightV2FramePreview struct {
	Contract             string                             `json:"contract"`
	Program              control.FrameProgram               `json:"program"`
	TotalFrames          int                                `json:"total_frames"`
	StoredTargetWrites   int                                `json:"stored_target_writes"`
	PlannedTargetWrites  int                                `json:"planned_target_writes"`
	CycleDurationMS      int64                              `json:"cycle_duration_ms"`
	NominalInitialMS     int64                              `json:"nominal_initial_ms"`
	NominalTailMS        int64                              `json:"nominal_tail_ms"`
	NominalDurationMS    int64                              `json:"nominal_duration_ms"`
	TimingBasis          string                             `json:"timing_basis"`
	RequiredCapabilities []string                           `json:"required_capabilities"`
	Execution            control.FrameExecutionAvailability `json:"execution"`
	Device               *LightV2PreviewDevice              `json:"device,omitempty"`
}

type LightV2Preview struct {
	Result  lightactionv2.Result `json:"result"`
	Preview LightV2FramePreview  `json:"preview"`
}

// LocalLightV2Preview performs pure compilation only; execution remains
// unchecked. Neither this helper nor the compiler opens a device or a socket.
func LocalLightV2Preview(data []byte) (LightV2Preview, error) {
	result, err := lightactionv2.CompileRequest(data)
	if err != nil {
		return LightV2Preview{}, err
	}
	preview, err := control.PreviewFrameProgram(result.Program)
	if err != nil {
		return LightV2Preview{}, err
	}
	return LightV2Preview{Result: result, Preview: lightV2PreviewWire(preview)}, nil
}

func lightV2PreviewWire(p control.FramePreview) LightV2FramePreview {
	return LightV2FramePreview{Contract: p.Contract, Program: p.Program, TotalFrames: p.TotalFrames,
		StoredTargetWrites: p.StoredTargetWrites, PlannedTargetWrites: p.PlannedTargetWrites,
		CycleDurationMS: p.CycleDurationMS, NominalInitialMS: p.NominalInitialMS, NominalTailMS: p.NominalTailMS,
		NominalDurationMS: p.NominalDurationMS, TimingBasis: p.TimingBasis,
		RequiredCapabilities: p.RequiredCapabilities, Execution: p.Execution}
}

func decodeLightV2Input(data []byte) (lightEnvelope, LightV2Preview, error) {
	expected, err := LocalLightV2Preview(data)
	if err != nil {
		return lightEnvelope{}, LightV2Preview{}, err
	}
	var input lightEnvelope
	if err := json.Unmarshal(data, &input); err != nil {
		return lightEnvelope{}, LightV2Preview{}, err
	}
	return input, expected, nil
}

// PreviewLightV2 sends the original high-level action. Local compilation only
// verifies the returned deterministic recipe and never replaces its request.
func (c *Client) PreviewLightV2(ctx context.Context, data []byte, serial *string) (LightV2Preview, error) {
	if serial != nil && !control.ValidDeviceSerial(*serial) {
		return LightV2Preview{}, fmt.Errorf("invalid v2 light preview serial")
	}
	input, expected, err := decodeLightV2Input(data)
	if err != nil {
		return LightV2Preview{}, err
	}
	body, err := marshalLightRequest(struct {
		lightEnvelope
		Serial *string `json:"serial,omitempty"`
	}{input, serial})
	if err != nil {
		return LightV2Preview{}, err
	}
	var value LightV2Preview
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v2/light/preview", body, &value, http.StatusOK)
	if err != nil {
		return LightV2Preview{}, lightRouteError(err, "light_v2_preview")
	}
	if len(headers.Values("Location")) != 0 || len(headers.Values("X-NB1-Daemon-Instance-ID")) != 0 || len(headers.Values("X-NB1-Replayed")) != 0 {
		return LightV2Preview{}, lightDecodeError(http.StatusOK, "v2 light preview contains submission metadata")
	}
	if err := validateLightV2Preview(value, expected, serial); err != nil {
		return LightV2Preview{}, lightDecodeError(http.StatusOK, err.Error())
	}
	return value, nil
}

func validateLightV2Preview(value, expected LightV2Preview, serial *string) error {
	if !reflect.DeepEqual(value.Result, expected.Result) {
		return fmt.Errorf("v2 high-level action or compiled recipe changed")
	}
	plain := value.Preview
	plain.Device = nil
	plain.Execution = expected.Preview.Execution
	if !reflect.DeepEqual(plain, expected.Preview) {
		return fmt.Errorf("v2 frame preview differs from the original action compilation")
	}
	execution := value.Preview.Execution
	device := value.Preview.Device
	if serial == nil {
		if device != nil || execution.Available || execution.Reason != "not_selected" && execution.Reason != "frame_service_unsupported" {
			return fmt.Errorf("v2 preview selected a device or claimed execution without a serial")
		}
		return nil
	}
	if device == nil || device.Serial != *serial {
		return fmt.Errorf("v2 preview does not identify the requested cached device")
	}
	switch device.Connection {
	case "missing", "connecting", "online", "offline", "busy", "error", "closing", "closed":
	default:
		return fmt.Errorf("v2 preview has an unknown device connection state")
	}
	if execution.Available {
		if execution.Reason != "cached_profile_supported" || device.Connection != "online" {
			return fmt.Errorf("v2 preview execution claim disagrees with the cached device")
		}
		return nil
	}
	switch execution.Reason {
	case "frame_service_unsupported":
		return nil
	case "device_not_configured":
		if device.Connection == "missing" {
			return nil
		}
	case "device_not_online":
		if device.Connection != "online" && device.Connection != "missing" {
			return nil
		}
	case "missing_capabilities":
		if device.Connection == "online" {
			return nil
		}
	}
	return fmt.Errorf("v2 preview has an inconsistent execution availability")
}

// SubmitLightV2WithReceipt issues one high-level POST with the original ticket.
// It never prepares a replacement, falls back to low-level frames, or retries.
func (c *Client) SubmitLightV2WithReceipt(ctx context.Context, serial string, data []byte, receipt control.ReceiptRequest) (control.FrameReceiptResult, error) {
	if !control.ValidDeviceSerial(serial) || !validPatternReceipt(receipt) {
		return control.FrameReceiptResult{}, fmt.Errorf("v2 light run requires a valid serial and complete original receipt")
	}
	input, expected, err := decodeLightV2Input(data)
	if err != nil {
		return control.FrameReceiptResult{}, err
	}
	body, err := marshalLightRequest(struct {
		lightEnvelope
		Policy                   string `json:"policy"`
		RequestID                string `json:"request_id"`
		ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id"`
	}{input, "reject", receipt.RequestID, receipt.ExpectedDaemonInstanceID})
	if err != nil {
		return control.FrameReceiptResult{}, err
	}
	var job control.FrameJob
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v2/devices/"+url.PathEscape(serial)+"/light", body, &job, http.StatusAccepted)
	if err != nil {
		bound := receiptError(err, receipt).(*ReceiptError)
		bound.Err = lightRouteError(err, "light_v2_run")
		return control.FrameReceiptResult{}, bound
	}
	if err := validateLightV2Job(job, serial, expected.Result, ""); err != nil {
		return control.FrameReceiptResult{}, receiptError(lightDecodeError(http.StatusAccepted, err.Error()), receipt)
	}
	instance, replay, location := headers.Values("X-NB1-Daemon-Instance-ID"), headers.Values("X-NB1-Replayed"), headers.Values("Location")
	if len(instance) != 1 || instance[0] != receipt.ExpectedDaemonInstanceID || len(replay) != 1 || replay[0] != "true" && replay[0] != "false" || len(location) != 1 || location[0] != lightV2ReceiptPath(serial, receipt) {
		return control.FrameReceiptResult{}, receiptError(lightDecodeError(http.StatusAccepted, "v2 daemon did not uniquely confirm the original receipt"), receipt)
	}
	return control.FrameReceiptResult{Job: job, DaemonInstanceID: instance[0], Replayed: replay[0] == "true"}, nil
}

// LookupLightV2Request can recover a lost acceptance response without a Job ID.
// If a Job ID was already confirmed, require that same ID on every later read.
// Any failed lookup preserves uncertainty about the original accepted action.
func (c *Client) LookupLightV2Request(ctx context.Context, serial string, data []byte, receipt control.ReceiptRequest, jobID string) (control.FrameJob, error) {
	if !control.ValidDeviceSerial(serial) || !validPatternReceipt(receipt) || jobID != "" && !validLightV2JobID(jobID) {
		return control.FrameJob{}, fmt.Errorf("v2 light lookup requires the original valid serial, ticket and optional confirmed Job ID")
	}
	_, expected, err := decodeLightV2Input(data)
	if err != nil {
		return control.FrameJob{}, err
	}
	var job control.FrameJob
	headers, err := c.doLightResponse(ctx, http.MethodGet, lightV2ReceiptPath(serial, receipt), nil, &job, http.StatusOK)
	if err == nil {
		instance := headers.Values("X-NB1-Daemon-Instance-ID")
		if len(instance) != 1 || instance[0] != receipt.ExpectedDaemonInstanceID || len(headers.Values("X-NB1-Replayed")) != 0 || len(headers.Values("Location")) != 0 {
			err = lightDecodeError(http.StatusOK, "v2 receipt lookup did not uniquely confirm the original daemon")
		}
	}
	if err == nil {
		if validationErr := validateLightV2Job(job, serial, expected.Result, jobID); validationErr != nil {
			err = lightDecodeError(http.StatusOK, validationErr.Error())
		}
	}
	if err != nil {
		return control.FrameJob{}, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	return job, nil
}

func lightV2ReceiptPath(serial string, receipt control.ReceiptRequest) string {
	return "/v1/devices/" + url.PathEscape(serial) + "/frame-requests/" + url.PathEscape(receipt.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(receipt.ExpectedDaemonInstanceID)
}

func validLightV2JobID(id string) bool {
	const prefix = "framejob_"
	return strings.HasPrefix(id, prefix) && len(id) == len(prefix)+32 && lowerHex(id[len(prefix):])
}

func validateLightV2Job(job control.FrameJob, serial string, expected lightactionv2.Result, jobID string) error {
	if !validLightV2JobID(job.ID) || jobID != "" && job.ID != jobID || job.Device != serial || !reflect.DeepEqual(job.Program, expected.Program) || job.CreatedAt.IsZero() || job.TotalFrames != expected.TotalFrames || job.PlannedTargetWrites != expected.PlannedTargetWrites || job.CompletedFrames < 0 || job.CompletedFrames > job.TotalFrames || job.CompletedTargetWrites < 0 || job.CompletedTargetWrites > job.PlannedTargetWrites {
		return fmt.Errorf("v2 Job identity, full frame program, timestamps or counts differ from the original action")
	}
	if job.StartedAt != nil && (job.StartedAt.IsZero() || job.StartedAt.Before(job.CreatedAt)) || job.FinishedAt != nil && (job.FinishedAt.IsZero() || job.FinishedAt.Before(job.CreatedAt) || job.StartedAt != nil && job.FinishedAt.Before(*job.StartedAt)) {
		return fmt.Errorf("v2 Job timestamps are inconsistent")
	}
	initial, perFrame := len(job.Program.Initial), len(job.Program.Initial)
	minimum := 0
	if job.CompletedFrames > 0 {
		minimum = initial + job.CompletedFrames*perFrame
	}
	maximum := initial + (job.CompletedFrames+1)*perFrame
	if maximum > job.PlannedTargetWrites {
		maximum = job.PlannedTargetWrites
	}
	if job.CompletedTargetWrites < minimum || job.CompletedTargetWrites > maximum || job.StartedAt == nil && (job.CompletedFrames != 0 || job.CompletedTargetWrites != 0) {
		return fmt.Errorf("v2 Job frame and write progress disagree")
	}
	switch job.Status {
	case "queued":
		if job.StartedAt != nil || job.FinishedAt != nil || job.Error != "" {
			return fmt.Errorf("queued v2 Job has execution metadata")
		}
	case "running", "cancelling":
		if job.StartedAt == nil || job.FinishedAt != nil || job.Error != "" {
			return fmt.Errorf("active v2 Job has invalid lifecycle metadata")
		}
	case "succeeded":
		if job.StartedAt == nil || job.FinishedAt == nil || job.Error != "" || job.CompletedFrames != job.TotalFrames || job.CompletedTargetWrites != job.PlannedTargetWrites {
			return fmt.Errorf("successful v2 Job has incomplete progress or lifecycle")
		}
	case "failed":
		if job.FinishedAt == nil || job.Error == "" {
			return fmt.Errorf("failed v2 Job lacks final failure metadata")
		}
	case "cancelled":
		if job.FinishedAt == nil || job.Error != "" {
			return fmt.Errorf("cancelled v2 Job lacks a clean terminal state")
		}
	default:
		return fmt.Errorf("unknown v2 Job status")
	}
	return nil
}

// ValidateLightV2Progress compares two individually validated snapshots from
// one original action. Callers retaining history must also bind immutable job
// timestamps and monotonic progress; a valid isolated snapshot is not enough.
func ValidateLightV2Progress(previous, current control.FrameJob) error {
	if previous.ID == "" || current.ID != previous.ID || current.Device != previous.Device || !reflect.DeepEqual(current.Program, previous.Program) || !current.CreatedAt.Equal(previous.CreatedAt) || current.TotalFrames != previous.TotalFrames || current.PlannedTargetWrites != previous.PlannedTargetWrites {
		return fmt.Errorf("v2 Job identity or immutable compilation changed between snapshots")
	}
	if previous.StartedAt != nil && (current.StartedAt == nil || !current.StartedAt.Equal(*previous.StartedAt)) {
		return fmt.Errorf("v2 Job start time changed between snapshots")
	}
	if current.CompletedFrames < previous.CompletedFrames || current.CompletedTargetWrites < previous.CompletedTargetWrites {
		return fmt.Errorf("v2 Job progress moved backwards")
	}
	allowed := false
	switch previous.Status {
	case "queued":
		allowed = true // Current status was checked by the response validator.
	case "running":
		allowed = current.Status != "queued"
	case "cancelling":
		allowed = current.Status == "cancelling" || current.Status == "cancelled" || current.Status == "failed"
	case "succeeded", "failed", "cancelled":
		allowed = reflect.DeepEqual(previous, current)
	}
	if !allowed {
		return fmt.Errorf("v2 Job lifecycle moved backwards or changed after completion")
	}
	return nil
}
