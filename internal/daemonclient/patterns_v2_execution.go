package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
)

// PatternRefPreviewV2 preserves the exact frozen execution and pure preview
// wire JSON after validating both against closed typed core data. It verifies
// an execution digest, not the server's original source compiler or hardware.
type PatternRefPreviewV2 struct {
	SchemaVersion    int                            `json:"schema_version"`
	Contract         string                         `json:"contract"`
	PatternRef       patternidentity.Ref            `json:"pattern_ref"`
	ExecutionDigest  string                         `json:"execution_digest"`
	Execution        json.RawMessage                `json:"execution"`
	Preview          json.RawMessage                `json:"preview"`
	PatternExecution PatternExecutionAvailabilityV2 `json:"pattern_execution"`
}

type patternEnvelopeWireV2 struct {
	PatternRef      patternidentity.Ref `json:"pattern_ref"`
	ExecutionDigest string              `json:"execution_digest"`
	JobKind         string              `json:"job_kind"`
	Job             json.RawMessage     `json:"job"`
}

// PatternCancelResultV2 retains the last trusted snapshot on every later
// failure. CancelAttempted only records the one POST attempt, not delivery or
// completed cleanup. No failed POST is followed by a good GET to hide failure.
type PatternCancelResultV2 struct {
	Job              *control.PatternJobEnvelope `json:"job,omitempty"`
	DaemonInstanceID string                      `json:"daemon_instance_id,omitempty"`
	CancelAttempted  bool                        `json:"cancel_attempted"`
	AlreadyTerminal  bool                        `json:"already_terminal"`
}

func (c *Client) PreviewPatternRefV2(ctx context.Context, ref patternidentity.Ref, serial *string) (PatternRefPreviewV2, error) {
	if patternidentity.ValidateRef(ref) != nil || serial != nil && !control.ValidDeviceSerial(*serial) {
		return PatternRefPreviewV2{}, fmt.Errorf("invalid exact Pattern Ref or serial")
	}
	body, _ := json.Marshal(struct {
		PatternRef patternidentity.Ref `json:"pattern_ref"`
		Serial     *string             `json:"serial,omitempty"`
	}{ref, serial})
	var value PatternRefPreviewV2
	headers, err := c.patternExecutionResponseV2(ctx, http.MethodPost, "/v2/patterns/preview-ref", body, &value, http.StatusOK)
	if err != nil {
		return PatternRefPreviewV2{}, err
	}
	if err == nil {
		err = patternV2ReadHeaders(headers)
	}
	if err == nil {
		err = validatePatternRefPreviewV2(value, ref, serial)
	}
	if err != nil {
		return PatternRefPreviewV2{}, patternDecode(err)
	}
	return value, nil
}

// SubmitPatternV2WithReceipt sends exactly one execution POST. It neither
// prepares a ticket nor resolves/previews a library first: accepted receipts
// must replay even when the source library later disappears.
func (c *Client) SubmitPatternV2WithReceipt(ctx context.Context, serial string, ref patternidentity.Ref, policy string, key control.ReceiptRequest) (control.PatternReceiptResult, error) {
	if !validPatternBindingV2(serial, ref, key) || (policy != "replace" && policy != "queue" && policy != "reject") {
		return control.PatternReceiptResult{}, fmt.Errorf("Pattern run requires exact Ref, serial, explicit policy and complete original ticket")
	}
	body, _ := json.Marshal(struct {
		PatternRef patternidentity.Ref `json:"pattern_ref"`
		Policy     string              `json:"policy"`
		RequestID  string              `json:"request_id"`
		Instance   string              `json:"expected_daemon_instance_id"`
	}{ref, policy, key.RequestID, key.ExpectedDaemonInstanceID})
	var wire patternEnvelopeWireV2
	headers, err := c.patternExecutionResponseV2(ctx, http.MethodPost, "/v2/devices/"+url.PathEscape(serial)+"/pattern-jobs", body, &wire, http.StatusAccepted)
	if err != nil {
		return control.PatternReceiptResult{}, patternExecutionReceiptErrorV2(err, key)
	}
	job, err := validatePatternEnvelopeWireV2(wire, serial, ref, "", "")
	if err == nil {
		err = patternExecutionHeadersV2(headers, key, "submit", patternReceiptPathV2(serial, ref, key))
	}
	if err != nil {
		return control.PatternReceiptResult{}, patternExecutionReceiptErrorV2(&DecodeError{Status: http.StatusAccepted, Err: err}, key)
	}
	return control.PatternReceiptResult{Job: job, Replayed: headers.Get("X-NB1-Replayed") == "true", DaemonInstanceID: key.ExpectedDaemonInstanceID}, nil
}

// LookupPatternV2Request reads only the exact original receipt. Optional known
// job/digest values constrain recovery; empty strings mean no extra constraint.
func (c *Client) LookupPatternV2Request(ctx context.Context, serial string, ref patternidentity.Ref, key control.ReceiptRequest, knownJobID, knownDigest string) (control.PatternJobEnvelope, error) {
	if !validPatternBindingV2(serial, ref, key) || knownJobID != "" && !validPatternJobIDV2(knownJobID) || knownDigest != "" && patternidentity.ValidateDigest(knownDigest) != nil {
		return control.PatternJobEnvelope{}, fmt.Errorf("invalid original Pattern receipt or known identity")
	}
	var wire patternEnvelopeWireV2
	headers, err := c.patternExecutionResponseV2(ctx, http.MethodGet, patternReceiptPathV2(serial, ref, key), nil, &wire, http.StatusOK)
	if err != nil {
		return control.PatternJobEnvelope{}, &ReceiptError{Err: err, Receipt: key, OutcomeUnknown: true}
	}
	if err == nil {
		err = patternExecutionHeadersV2(headers, key, "query", "")
	}
	var job control.PatternJobEnvelope
	if err == nil {
		job, err = validatePatternEnvelopeWireV2(wire, serial, ref, knownJobID, knownDigest)
	}
	if err != nil {
		return control.PatternJobEnvelope{}, &ReceiptError{Err: &DecodeError{Status: http.StatusOK, Err: err}, Receipt: key, OutcomeUnknown: true}
	}
	return job, nil
}

func (c *Client) CancelPatternV2Request(ctx context.Context, serial string, ref patternidentity.Ref, key control.ReceiptRequest, knownJobID, knownDigest string) (PatternCancelResultV2, error) {
	var result PatternCancelResultV2
	before, err := c.LookupPatternV2Request(ctx, serial, ref, key, knownJobID, knownDigest)
	if err != nil {
		return result, err
	}
	result.Job, result.DaemonInstanceID = &before, key.ExpectedDaemonInstanceID
	status := patternStatusV2(before)
	if patternTerminalV2(status) {
		result.AlreadyTerminal = true
		return result, nil
	}
	if status == "cancelling" {
		return result, nil
	}
	uncertain := func(err error) (PatternCancelResultV2, error) {
		return result, &ReceiptError{Err: err, Receipt: key, OutcomeUnknown: true}
	}
	if err := ctx.Err(); err != nil {
		return uncertain(err)
	}
	body, _ := json.Marshal(struct {
		PatternRef patternidentity.Ref `json:"pattern_ref"`
		Instance   string              `json:"expected_daemon_instance_id"`
		JobID      string              `json:"confirmed_job_id"`
	}{ref, key.ExpectedDaemonInstanceID, patternJobIDV2(before)})
	var wire patternEnvelopeWireV2
	result.CancelAttempted = true
	headers, err := c.patternExecutionResponseV2(ctx, http.MethodPost, "/v2/devices/"+url.PathEscape(serial)+"/pattern-requests/"+url.PathEscape(key.RequestID)+"/cancel", body, &wire, http.StatusAccepted)
	if err != nil {
		return uncertain(err)
	}
	after, err := validatePatternEnvelopeWireV2(wire, serial, ref, patternJobIDV2(before), before.ExecutionDigest)
	if err == nil {
		err = patternExecutionHeadersV2(headers, key, "cancel", "")
	}
	if err == nil {
		err = ValidatePatternV2Progress(before, after)
	}
	if err == nil && !patternTerminalV2(patternStatusV2(after)) && patternStatusV2(after) != "cancelling" {
		err = fmt.Errorf("Pattern cancel did not acknowledge cancellation or a terminal race")
	}
	if err != nil {
		return uncertain(&DecodeError{Status: http.StatusAccepted, Err: err})
	}
	result.Job = &after
	return result, nil
}

func validPatternBindingV2(serial string, ref patternidentity.Ref, key control.ReceiptRequest) bool {
	return control.ValidDeviceSerial(serial) && patternidentity.ValidateRef(ref) == nil && validPatternReceipt(key)
}
func patternReceiptPathV2(serial string, ref patternidentity.Ref, key control.ReceiptRequest) string {
	return "/v2/devices/" + url.PathEscape(serial) + "/pattern-requests/" + url.PathEscape(key.RequestID) + "?" + url.Values{"expected_daemon_instance_id": {key.ExpectedDaemonInstanceID}, "record_contract": {ref.RecordContract}, "pattern_id": {ref.ID}, "revision": {ref.Revision}}.Encode()
}
func patternExecutionHeadersV2(h http.Header, key control.ReceiptRequest, operation, location string) error {
	instances := h.Values("X-NB1-Daemon-Instance-ID")
	if len(instances) != 1 || instances[0] != key.ExpectedDaemonInstanceID {
		return fmt.Errorf("Pattern response did not uniquely confirm original instance")
	}
	if operation == "submit" {
		replayed, locations := h.Values("X-NB1-Replayed"), h.Values("Location")
		if len(replayed) != 1 || replayed[0] != "true" && replayed[0] != "false" || len(locations) != 1 || locations[0] != location {
			return fmt.Errorf("Pattern submission did not uniquely preserve original receipt")
		}
	} else if len(h.Values("Location")) != 0 || len(h.Values("X-NB1-Replayed")) != 0 {
		return fmt.Errorf("Pattern receipt response contains unexpected submission headers")
	}
	return nil
}

// Only known status/code pairs settle this admission rejection. Unknown routes,
// redirects, 5xx, expired/missing receipts and changed instances stay uncertain;
// even a definite rejection never authorizes inventing a replacement ticket.
func patternExecutionReceiptErrorV2(err error, key control.ReceiptRequest) error {
	unknown := true
	var api *Error
	if errors.As(err, &api) {
		switch api.Status {
		case 400:
			unknown = api.Code != "invalid_pattern_request" && api.Code != "invalid_request"
		case 404:
			unknown = api.Code != "pattern_not_found"
		case 409:
			switch api.Code {
			case "busy", "queue_full", "idempotency_conflict", "pattern_revision_conflict", "pattern_library_busy":
				unknown = false
			}
		case 429:
			unknown = api.Code != "idempotency_capacity"
		}
	}
	return &ReceiptError{Err: err, Receipt: key, OutcomeUnknown: unknown}
}

func (c *Client) patternExecutionResponseV2(ctx context.Context, method, path string, body []byte, out interface{}, status int) (http.Header, error) {
	if len(body) > 4096 {
		return nil, fmt.Errorf("Pattern execution request exceeds4096 bytes")
	}
	r, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
		r.GetBody = nil
	}
	response, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("contact Pattern daemon: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = decodeLightAPIError(response)
		var api *Error
		if errors.As(err, &api) && (api.Message == "" || len(response.Header.Values("Location")) != 0 || len(response.Header.Values("X-NB1-Replayed")) != 0 || len(response.Header.Values("X-NB1-Daemon-Instance-ID")) != 0) {
			err = lightDecodeError(response.StatusCode, "Pattern error response has empty message or contradictory success metadata")
		}
	} else if response.StatusCode != status {
		err = lightDecodeError(response.StatusCode, fmt.Sprintf("Pattern response requires HTTP%d", status))
	} else {
		err = decodeLightResponse(response, out)
	}
	closeErr := response.Body.Close()
	if closeErr != nil {
		return nil, lightDecodeError(response.StatusCode, fmt.Sprintf("Pattern response could not be closed: %v; prior decode: %v", closeErr, err))
	}
	if err != nil {
		return nil, err
	}
	return response.Header, nil
}

func decodePatternPartV2(data []byte, out interface{}) error {
	r := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}
	return decodeCompilerResponse(r, out)
}
