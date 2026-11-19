package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/b1ug/nb1/internal/triggerv1"
)

// SequenceInput retains the original author input used to identify a Job.
type SequenceInput struct {
	Action    json.RawMessage `json:"action,omitempty"`
	Sequence  json.RawMessage `json:"sequence,omitempty"`
	PatternID *string         `json:"pattern_id,omitempty"`
}

type SequenceDeviceCheck struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type SequencePreview struct {
	Sequence             sequence.Sequence   `json:"sequence"`
	TotalSteps           int                 `json:"total_steps"`
	NominalDurationMS    int64               `json:"nominal_duration_ms"`
	RequiredCapabilities []string            `json:"required_capabilities"`
	DeviceCheck          SequenceDeviceCheck `json:"device_check"`
}

func DecodeSequenceInput(data []byte) (SequenceInput, error) {
	if len(data) > sequence.MaxInputBytes || !jsoninput.ValidUnicode(data) {
		return SequenceInput{}, fmt.Errorf("input must contain valid Unicode and be at most 1 MiB")
	}
	var input SequenceInput
	if err := jsoninput.DecodeObject(data, &input); err != nil {
		return SequenceInput{}, err
	}
	return input, input.validate()
}

func (input SequenceInput) validate() error {
	sources := 0
	if input.Action != nil {
		sources++
	}
	if input.Sequence != nil {
		sources++
	}
	if input.PatternID != nil {
		sources++
	}
	if sources != 1 {
		return fmt.Errorf("exactly one action, sequence or pattern_id is required")
	}
	if input.PatternID != nil {
		if !control.ValidSequenceJobID(*input.PatternID) {
			return fmt.Errorf("pattern_id must contain 1 to 64 ASCII letters, digits, hyphens or underscores")
		}
		return nil
	}
	if input.Sequence != nil {
		_, err := sequence.DecodeAuthoring(input.Sequence, func(raw json.RawMessage) (string, error) {
			return actionv1.ResolveColor(raw, func(string) (string, error) { return "#000000", nil })
		})
		return err
	}
	// Validate alias syntax and the complete Action locally. Its original color
	// references remain in the request for the daemon's palette resolution.
	_, err := actionv1.Compile(input.Action, func(string) (string, error) { return "#000000", nil })
	return err
}

func (c *Client) PreviewSequence(ctx context.Context, input SequenceInput, serial string) (SequencePreview, error) {
	if err := input.validate(); err != nil {
		return SequencePreview{}, err
	}
	if serial != "" && !control.ValidDeviceSerial(serial) {
		return SequencePreview{}, fmt.Errorf("invalid device serial")
	}
	request := struct {
		Action    json.RawMessage `json:"action,omitempty"`
		Sequence  json.RawMessage `json:"sequence,omitempty"`
		PatternID *string         `json:"pattern_id,omitempty"`
		Serial    string          `json:"serial,omitempty"`
	}{Action: input.Action, Sequence: input.Sequence, PatternID: input.PatternID, Serial: serial}
	var result SequencePreview
	if err := c.sequenceV1Request(ctx, http.MethodPost, "/v1/preview", request, &result, http.StatusOK); err != nil {
		return result, err
	}
	normalized, err := sequence.Normalize(result.Sequence)
	if err != nil || !reflect.DeepEqual(result.Sequence, normalized) || result.TotalSteps != len(normalized.Steps)*normalized.Repeat || result.NominalDurationMS != normalized.NominalDurationMS() {
		return SequencePreview{}, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid normalized Sequence preview")}
	}
	if serial == "" && result.DeviceCheck.Status != "not_requested" || serial != "" && result.DeviceCheck.Status != "supported" && result.DeviceCheck.Status != "unsupported" && result.DeviceCheck.Status != "unknown" {
		return SequencePreview{}, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid device check status")}
	}
	return result, nil
}

func (c *Client) SubmitSequence(ctx context.Context, input SequenceInput, serial, id string) (control.SequenceJob, error) {
	if err := input.validate(); err != nil {
		return control.SequenceJob{}, err
	}
	if !control.ValidDeviceSerial(serial) || !control.ValidSequenceJobID(id) {
		return control.SequenceJob{}, fmt.Errorf("a valid device serial and explicit job ID are required")
	}
	return c.submitSequence(ctx, sequenceSubmission{ID: id, Serial: serial, SequenceInput: input})
}

type sequenceSubmission struct {
	ID      string `json:"id"`
	Serial  string `json:"serial,omitempty"`
	Trigger string `json:"trigger,omitempty"`
	SequenceInput
}

// SequenceSubmissionRejected identifies subsystem errors that are returned
// before admission. Generic 5xx or transport failures can leave a Job active.
func SequenceSubmissionRejected(err error) bool {
	var rejected *Error
	if !errors.As(err, &rejected) {
		return false
	}
	return (rejected.Status >= 400 && rejected.Status < 500) ||
		(rejected.Status == 503 && (rejected.Code == "device_offline" || rejected.Code == "invalid_trigger_config"))
}

func (c *Client) SubmitDefaultSequence(ctx context.Context, input SequenceInput, id string) (control.SequenceJob, error) {
	if err := input.validate(); err != nil {
		return control.SequenceJob{}, err
	}
	return c.submitSequence(ctx, sequenceSubmission{ID: id, SequenceInput: input})
}

func (c *Client) SubmitTrigger(ctx context.Context, name, serial, id string) (control.SequenceJob, error) {
	if !triggerv1.ValidName(name) {
		return control.SequenceJob{}, fmt.Errorf("invalid trigger name")
	}
	if serial != "" && !control.ValidDeviceSerial(serial) {
		return control.SequenceJob{}, fmt.Errorf("invalid device serial")
	}
	return c.submitSequence(ctx, sequenceSubmission{ID: id, Serial: serial, Trigger: name})
}

func (c *Client) SequenceTriggers(ctx context.Context) ([]triggerv1.Trigger, error) {
	var result struct {
		Items []triggerv1.Trigger `json:"items"`
	}
	if err := c.sequenceV1ListRequest(ctx, "/v1/triggers", &result); err != nil {
		return nil, err
	}
	for i, trigger := range result.Items {
		if !triggerv1.ValidName(trigger.Name) || !control.ValidSequenceJobID(trigger.PatternID) ||
			(trigger.Serial != "" && !control.ValidDeviceSerial(trigger.Serial)) ||
			(i > 0 && result.Items[i-1].Name >= trigger.Name) {
			return nil, &DecodeError{Status: http.StatusOK, Err: fmt.Errorf("invalid trigger list identity or ordering")}
		}
	}
	return result.Items, nil
}

// CheckSequenceTriggers checks stored Pattern references and device bindings without playing anything.
func (c *Client) CheckSequenceTriggers(ctx context.Context) (triggerv1.CheckReport, error) {
	var report triggerv1.CheckReport
	err := c.sequenceV1Request(ctx, http.MethodGet, "/v1/triggers/check", nil, &report, http.StatusOK)
	return report, err
}

func (c *Client) submitSequence(ctx context.Context, request sequenceSubmission) (control.SequenceJob, error) {
	if !control.ValidSequenceJobID(request.ID) {
		return control.SequenceJob{}, fmt.Errorf("a valid explicit job ID is required")
	}
	var job control.SequenceJob
	if err := c.sequenceV1Request(ctx, http.MethodPost, "/v1/jobs", request, &job, http.StatusOK, http.StatusAccepted); err != nil {
		return job, err
	}
	if err := validateSequenceJob(job, request.ID); err != nil {
		return control.SequenceJob{}, err
	}
	if request.Serial != "" && job.Serial != request.Serial {
		return control.SequenceJob{}, &DecodeError{Err: fmt.Errorf("daemon changed the submitted device serial")}
	}
	return job, nil
}

func (c *Client) SequenceJob(ctx context.Context, id string) (control.SequenceJob, error) {
	return c.sequenceJobRequest(ctx, id, false, "")
}

func (c *Client) CancelSequenceJob(ctx context.Context, id string) (control.SequenceJob, error) {
	return c.sequenceJobRequest(ctx, id, true, "")
}

// CancelSequenceJobOnDevice requests cancellation only if the Job has this
// accepted serial. A server that rejects the guard is never retried without it.
func (c *Client) CancelSequenceJobOnDevice(ctx context.Context, id, serial string) (control.SequenceJob, error) {
	if !control.ValidDeviceSerial(serial) {
		return control.SequenceJob{}, fmt.Errorf("invalid device serial")
	}
	return c.sequenceJobRequest(ctx, id, true, serial)
}

func (c *Client) sequenceJobRequest(ctx context.Context, id string, cancel bool, serial string) (control.SequenceJob, error) {
	if !control.ValidSequenceJobID(id) {
		return control.SequenceJob{}, fmt.Errorf("invalid job ID")
	}
	method, path := http.MethodGet, "/v1/jobs/"+url.PathEscape(id)
	statuses := []int{http.StatusOK}
	if cancel {
		method, path = http.MethodPost, path+"/cancel"
		statuses = append(statuses, http.StatusAccepted)
		if serial != "" {
			path += "?serial=" + url.QueryEscape(serial)
		}
	}
	var job control.SequenceJob
	if err := c.sequenceV1Request(ctx, method, path, nil, &job, statuses...); err != nil {
		return job, err
	}
	if err := validateSequenceJob(job, id); err != nil {
		return control.SequenceJob{}, err
	}
	if serial != "" && job.Serial != serial {
		return control.SequenceJob{}, &DecodeError{Err: fmt.Errorf("daemon returned a Job for a different device serial")}
	}
	return job, nil
}

func validateSequenceJob(job control.SequenceJob, id string) error {
	validStatus := false
	switch job.Status {
	case "queued", "running", "cancelling", "succeeded", "cancelled", "failed":
		validStatus = true
	}
	if job.ID != id || !control.ValidDeviceSerial(job.Serial) || !validStatus || job.TotalSteps < 1 || job.TotalSteps > sequence.MaxExecutedSteps || job.CompletedSteps < 0 || job.CompletedSteps > job.TotalSteps {
		return &DecodeError{Err: fmt.Errorf("daemon returned an invalid Job identity, status or progress")}
	}
	return nil
}

func (c *Client) sequenceV1Request(ctx context.Context, method, path string, input, output interface{}, statuses ...int) error {
	return c.sequenceV1Response(ctx, method, path, input, nil, func(response *http.Response) error {
		return decodeCompilerResponse(response, output)
	}, statuses...)
}

func (c *Client) sequenceV1Response(ctx context.Context, method, path string, input interface{}, headers map[string]string, decode func(*http.Response) error, statuses ...int) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
		if len(body) > sequence.MaxInputBytes {
			return fmt.Errorf("request exceeds 1 MiB")
		}
	}
	request, err := c.newRequest(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	request.GetBody = nil
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()
	allowed := false
	for _, status := range statuses {
		allowed = allowed || response.StatusCode == status
	}
	if !allowed {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return decodeLightAPIError(response)
		}
		return &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("unexpected response status")}
	}
	if err := decode(response); err != nil {
		return &DecodeError{Status: response.StatusCode, Err: err}
	}
	return nil
}
