package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/sequence"
)

// SequenceInput retains the original author input used to identify a Job.
type SequenceInput struct {
	Action   json.RawMessage `json:"action,omitempty"`
	Sequence json.RawMessage `json:"sequence,omitempty"`
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
	if (input.Action == nil) == (input.Sequence == nil) {
		return fmt.Errorf("exactly one action or sequence is required")
	}
	if input.Sequence != nil {
		_, err := sequence.Decode(input.Sequence)
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
		Action   json.RawMessage `json:"action,omitempty"`
		Sequence json.RawMessage `json:"sequence,omitempty"`
		Serial   string          `json:"serial,omitempty"`
	}{input.Action, input.Sequence, serial}
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
	request := struct {
		ID       string          `json:"id"`
		Serial   string          `json:"serial"`
		Action   json.RawMessage `json:"action,omitempty"`
		Sequence json.RawMessage `json:"sequence,omitempty"`
	}{id, serial, input.Action, input.Sequence}
	var job control.SequenceJob
	if err := c.sequenceV1Request(ctx, http.MethodPost, "/v1/jobs", request, &job, http.StatusOK, http.StatusAccepted); err != nil {
		return job, err
	}
	if err := validateSequenceJob(job, id); err != nil {
		return control.SequenceJob{}, err
	}
	if job.Serial != serial {
		return control.SequenceJob{}, &DecodeError{Err: fmt.Errorf("daemon changed the submitted device serial")}
	}
	return job, nil
}

func (c *Client) SequenceJob(ctx context.Context, id string) (control.SequenceJob, error) {
	return c.sequenceJobRequest(ctx, id, false)
}

func (c *Client) CancelSequenceJob(ctx context.Context, id string) (control.SequenceJob, error) {
	return c.sequenceJobRequest(ctx, id, true)
}

func (c *Client) sequenceJobRequest(ctx context.Context, id string, cancel bool) (control.SequenceJob, error) {
	if !control.ValidSequenceJobID(id) {
		return control.SequenceJob{}, fmt.Errorf("invalid job ID")
	}
	method, path := http.MethodGet, "/v1/jobs/"+url.PathEscape(id)
	statuses := []int{http.StatusOK}
	if cancel {
		method, path = http.MethodPost, path+"/cancel"
		statuses = append(statuses, http.StatusAccepted)
	}
	var job control.SequenceJob
	if err := c.sequenceV1Request(ctx, method, path, nil, &job, statuses...); err != nil {
		return job, err
	}
	return job, validateSequenceJob(job, id)
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
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.GetBody = nil
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeLightAPIError(response)
	}
	allowed := false
	for _, status := range statuses {
		allowed = allowed || response.StatusCode == status
	}
	if !allowed {
		return &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("unexpected response status")}
	}
	if err := decodeCompilerResponse(response, output); err != nil {
		return &DecodeError{Status: response.StatusCode, Err: err}
	}
	return nil
}
