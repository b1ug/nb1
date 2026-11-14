package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightaction"
)

// LightPreview preserves both the authoring result and the device-aware
// Program preview. Every field is checked against the original request.
type LightPreview struct {
	Result  lightaction.Result    `json:"result"`
	Preview control.PreviewResult `json:"preview"`
}

type lightEnvelope struct {
	Contract string          `json:"contract"`
	Action   json.RawMessage `json:"action"`
}

func decodeLightInput(data []byte) (lightEnvelope, lightaction.Result, error) {
	expected, err := lightaction.CompileRequest(data)
	if err != nil {
		return lightEnvelope{}, lightaction.Result{}, err
	}
	var input lightEnvelope
	if err := json.Unmarshal(data, &input); err != nil {
		return lightEnvelope{}, lightaction.Result{}, err
	}
	return input, expected, nil
}

// PreviewLight sends original Action JSON, not the locally compiled Program.
// The shared compiler is used here only to bind the response to this input.
func (c *Client) PreviewLight(ctx context.Context, data []byte, serial *string) (LightPreview, error) {
	if serial != nil && !control.ValidDeviceSerial(*serial) {
		return LightPreview{}, fmt.Errorf("invalid light preview serial")
	}
	input, expected, err := decodeLightInput(data)
	if err != nil {
		return LightPreview{}, err
	}
	body, err := marshalLightRequest(struct {
		lightEnvelope
		Serial *string `json:"serial,omitempty"`
	}{input, serial})
	if err != nil {
		return LightPreview{}, err
	}
	var result LightPreview
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v1/light/preview", body, &result, http.StatusOK)
	if err != nil {
		return LightPreview{}, lightRouteError(err, "light_preview")
	}
	if headers.Get("Location") != "" || len(headers.Values("X-NB1-Daemon-Instance-ID")) != 0 || len(headers.Values("X-NB1-Replayed")) != 0 {
		return LightPreview{}, lightDecodeError(http.StatusOK, "light preview unexpectedly contains submission metadata")
	}
	if !reflect.DeepEqual(result.Result, expected) {
		return LightPreview{}, lightDecodeError(http.StatusOK, "light preview changed the original action's normalized compilation")
	}
	if err := control.ValidatePreviewResult(result.Preview, "program", serial); err != nil {
		return LightPreview{}, &DecodeError{Status: http.StatusOK, Err: err}
	}
	if !reflect.DeepEqual(result.Preview.Program, expected.Program) || result.Preview.TotalSteps != expected.TotalSteps || result.Preview.NominalDurationMS != expected.NominalDurationMS || !reflect.DeepEqual(result.Preview.RequiredCapabilities, expected.RequiredCapabilities) {
		return LightPreview{}, lightDecodeError(http.StatusOK, "authoring and Program preview disagree")
	}
	return result, nil
}

// SubmitLightWithReceipt makes exactly one bound POST with policy reject. It
// never prepares a replacement ticket, retries, or falls back to /jobs/direct.
func (c *Client) SubmitLightWithReceipt(ctx context.Context, serial string, data []byte, receipt control.ReceiptRequest) (control.ReceiptResult, error) {
	if !control.ValidDeviceSerial(serial) || receipt.RequestID == "" || receipt.ExpectedDaemonInstanceID == "" {
		return control.ReceiptResult{}, fmt.Errorf("light run requires a valid serial and complete nonempty receipt")
	}
	input, expected, err := decodeLightInput(data)
	if err != nil {
		return control.ReceiptResult{}, err
	}
	body, err := marshalLightRequest(struct {
		lightEnvelope
		Policy                   string `json:"policy"`
		RequestID                string `json:"request_id"`
		ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id"`
	}{input, "reject", receipt.RequestID, receipt.ExpectedDaemonInstanceID})
	if err != nil {
		return control.ReceiptResult{}, err
	}
	var job control.Job
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/light", body, &job, http.StatusAccepted)
	if err != nil {
		// Classify uncertainty before adding an old-daemon route hint: a
		// malformed 404 must not become a trusted non-admission response.
		bound := receiptError(err, receipt).(*ReceiptError)
		bound.Err = lightRouteError(err, "light_run")
		return control.ReceiptResult{}, bound
	}
	if err := validateLightJob(job, serial, expected, ""); err != nil {
		return control.ReceiptResult{}, receiptError(&DecodeError{Status: http.StatusAccepted, Err: err}, receipt)
	}
	instance := headers.Values("X-NB1-Daemon-Instance-ID")
	replayed := headers.Values("X-NB1-Replayed")
	location := headers.Values("Location")
	expectedLocation := "/v1/devices/" + url.PathEscape(serial) + "/requests/" + url.PathEscape(receipt.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(receipt.ExpectedDaemonInstanceID)
	if len(instance) != 1 || instance[0] != receipt.ExpectedDaemonInstanceID || len(replayed) != 1 || replayed[0] != "true" && replayed[0] != "false" || len(location) != 1 || location[0] != expectedLocation {
		return control.ReceiptResult{}, receiptError(lightDecodeError(http.StatusAccepted, "daemon did not uniquely confirm the original light request binding"), receipt)
	}
	return control.ReceiptResult{Job: job, DaemonInstanceID: instance[0], Replayed: replayed[0] == "true"}, nil
}

// LookupLightRequest reads the original receipt with a fixed accepted Job ID.
// A lookup failure never establishes that the original execution did not occur.
func (c *Client) LookupLightRequest(ctx context.Context, serial string, data []byte, receipt control.ReceiptRequest, jobID string) (control.Job, error) {
	if !control.ValidDeviceSerial(serial) || receipt.RequestID == "" || receipt.ExpectedDaemonInstanceID == "" || !control.ValidDeviceSerial(jobID) {
		return control.Job{}, fmt.Errorf("light lookup requires the original serial, receipt and accepted Job ID")
	}
	_, expected, err := decodeLightInput(data)
	if err != nil {
		return control.Job{}, err
	}
	path := "/v1/devices/" + url.PathEscape(serial) + "/requests/" + url.PathEscape(receipt.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(receipt.ExpectedDaemonInstanceID)
	var job control.Job
	headers, err := c.doLightResponse(ctx, http.MethodGet, path, nil, &job, http.StatusOK)
	if err == nil {
		values := headers.Values("X-NB1-Daemon-Instance-ID")
		if len(values) != 1 || values[0] != receipt.ExpectedDaemonInstanceID {
			err = lightDecodeError(http.StatusOK, "lookup did not confirm the original daemon instance")
		}
	}
	if err == nil {
		err = validateLightJob(job, serial, expected, jobID)
	}
	if err != nil {
		return control.Job{}, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	return job, nil
}

func validateLightJob(job control.Job, serial string, expected lightaction.Result, jobID string) error {
	if !control.ValidDeviceSerial(job.ID) || jobID != "" && job.ID != jobID || job.Device != serial || !reflect.DeepEqual(job.Program, expected.Program) || job.TotalSteps != expected.TotalSteps || job.CompletedSteps < 0 || job.CompletedSteps > job.TotalSteps || job.CreatedAt.IsZero() {
		return fmt.Errorf("light Job identity, canonical Program or progress differs from the original request")
	}
	if job.Status == "succeeded" && job.CompletedSteps != job.TotalSteps {
		return fmt.Errorf("successful light Job has incomplete progress")
	}
	switch job.Status {
	case "queued", "running", "cancelling", "succeeded", "failed", "cancelled":
		return nil
	default:
		return fmt.Errorf("unknown light Job status")
	}
}

func marshalLightRequest(input interface{}) ([]byte, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if len(data) > lightaction.MaxRequestBytes {
		return nil, fmt.Errorf("complete light request exceeds %d bytes", lightaction.MaxRequestBytes)
	}
	return data, nil
}

func lightDecodeError(status int, message string) *DecodeError {
	return &DecodeError{Status: status, Err: fmt.Errorf("%s", message)}
}

func (c *Client) doLightResponse(ctx context.Context, method, path string, body []byte, output interface{}, status int) (http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		// A connection-level failure must not turn a new Action POST into an
		// implicit replay by net/http. Preserve the original receipt instead.
		request.GetBody = nil
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeLightAPIError(response)
	}
	if response.StatusCode != status {
		return nil, lightDecodeError(response.StatusCode, fmt.Sprintf("light response requires HTTP %d", status))
	}
	if err := decodeLightResponse(response, output); err != nil {
		return nil, err
	}
	return response.Header, nil
}

func lightRouteError(err error, route string) error {
	var malformed *DecodeError
	if errors.As(err, &malformed) && (malformed.Status == http.StatusNotFound || malformed.Status == http.StatusNotImplemented) {
		return &Error{Status: malformed.Status, Code: route + "_unsupported", Message: "daemon does not support " + route + "; no direct fallback; error response could not be trusted"}
	}
	return compilerRouteError(err, route)
}

// Apply the same wire checks to every new light response. The older decoder
// validates its typed structure after these lossless, bounded input checks.
func decodeLightResponse(response *http.Response, output interface{}) error {
	malformed := func(message string) error { return lightDecodeError(response.StatusCode, message) }
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return malformed("light response requires one JSON Content-Type")
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" {
		return malformed("light response requires application/json")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return malformed("light response is unreadable or exceeds 2 MiB")
	}
	if !utf8.Valid(data) || !lightResponseUnicode(data) {
		return malformed("light response contains invalid Unicode")
	}
	copy := *response
	copy.Body = io.NopCloser(bytes.NewReader(data))
	if err := decodeCompilerResponse(&copy, output); err != nil {
		return &DecodeError{Status: response.StatusCode, Err: err}
	}
	return nil
}

// Rejection metadata determines whether a write may have happened. It must
// pass the same wire and typed checks as a confirmation of acceptance.
func decodeLightAPIError(response *http.Response) error {
	var value httpapi.ErrorResponse
	if err := decodeLightResponse(response, &value); err != nil {
		return err
	}
	if value.Error.Code == "" {
		return lightDecodeError(response.StatusCode, "light error response has no error code")
	}
	return &Error{Status: response.StatusCode, Code: value.Error.Code, Message: value.Error.Message}
}

// encoding/json replaces unmatched UTF-16 escape surrogates with U+FFFD.
// Reject that lossy transformation before using any decoded response fields.
func lightResponseUnicode(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data); i++ {
			if data[i] == '"' {
				break
			}
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) {
				return false
			}
			if data[i] != 'u' {
				continue
			}
			if i+4 >= len(data) {
				return false
			}
			value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 4
			if value >= 0xdc00 && value <= 0xdfff {
				return false
			}
			if value >= 0xd800 && value <= 0xdbff {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return false
				}
				low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}
