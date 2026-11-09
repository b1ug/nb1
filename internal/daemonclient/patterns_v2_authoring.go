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

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// PatternRecordV2 keeps the complete original record as JSON. InspectRecord
// validates its typed execution without losing valid future source/recipe data.
// A successful read does not grant permission to execute an unknown recipe.
type PatternRecordV2 struct {
	SchemaVersion   int                `json:"schema_version"`
	Contract        string             `json:"contract"`
	Record          json.RawMessage    `json:"record"`
	Verification    patternsv2.Support `json:"verification"`
	ExecutionDigest string             `json:"execution_digest"`
}

type PatternExecutionAvailabilityV2 struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// Keep this wire type flat for the common strict JSON field checker.
type PatternDraftPreviewV2 struct {
	SchemaVersion    int                            `json:"schema_version"`
	Contract         string                         `json:"contract"`
	Record           json.RawMessage                `json:"record"`
	Verification     patternsv2.Support             `json:"verification"`
	ExecutionDigest  string                         `json:"execution_digest"`
	Preview          patternsv2.PreviewFacts        `json:"preview"`
	PatternExecution PatternExecutionAvailabilityV2 `json:"pattern_execution"`
}

type PatternWriteResultV2 struct {
	SchemaVersion int               `json:"schema_version"`
	Contract      string            `json:"contract"`
	Records       []json.RawMessage `json:"records"`
	Created       []string          `json:"created"`
	Existing      []string          `json:"existing"`
	Total         int               `json:"total"`
}

// GetPatternV2 makes one bounded HTTP read and verifies identity, complete
// source/recipe integrity and the server's verification/digest metadata.
func (c *Client) GetPatternV2(ctx context.Context, id string) (PatternRecordV2, error) {
	if !validPatternID(id) {
		return PatternRecordV2{}, fmt.Errorf("invalid Pattern v2 ID")
	}
	var value PatternRecordV2
	headers, err := c.doLightResponse(ctx, http.MethodGet, "/v2/patterns/"+url.PathEscape(id), nil, &value, http.StatusOK)
	if err != nil {
		return PatternRecordV2{}, err
	}
	i, err := inspectPatternRecordV2(value)
	if err == nil && i.Record().ID != id {
		err = fmt.Errorf("Pattern v2 response changed requested ID")
	}
	if err == nil {
		err = patternV2ReadHeaders(headers)
	}
	if err != nil {
		return PatternRecordV2{}, patternDecode(err)
	}
	return value, nil
}

// PreviewPatternDraftV2 sends the author's exact bytes once. Local pure
// compilation only binds the response; no record is saved and no job is made.
func (c *Client) PreviewPatternDraftV2(ctx context.Context, data []byte) (PatternDraftPreviewV2, error) {
	expected, err := patternsv2.PreviewDraft(data)
	if err != nil {
		return PatternDraftPreviewV2{}, err
	}
	var value PatternDraftPreviewV2
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v2/patterns/preview", data, &value, http.StatusOK)
	if err != nil {
		return PatternDraftPreviewV2{}, err
	}
	i, err := inspectPatternRecordV2(PatternRecordV2{value.SchemaVersion, value.Contract, value.Record, value.Verification, value.ExecutionDigest})
	if err == nil && (!reflect.DeepEqual(i.Record(), expected.Record()) || !reflect.DeepEqual(value.Preview, expected.Preview())) {
		err = fmt.Errorf("Pattern v2 preview changed the original authoring candidate")
	}
	if err == nil {
		err = validatePatternAvailabilityV2(value.PatternExecution)
	}
	if err == nil {
		err = patternV2ReadHeaders(headers)
	}
	if err != nil {
		return PatternDraftPreviewV2{}, patternDecode(err)
	}
	return value, nil
}

func validatePatternAvailabilityV2(value PatternExecutionAvailabilityV2) error {
	if value.Available && value.Reason == "pattern_service_configured" {
		return nil
	}
	if !value.Available {
		switch value.Reason {
		case "pattern_identity_admission_not_integrated", "pattern_service_unsupported", "pattern_library_unconfigured", "pattern_library_configuration_error":
			return nil
		}
	}
	return fmt.Errorf("Pattern v2 software availability has an unknown or contradictory reason")
}

// SavePatternV2 validates confirmation against the complete candidate, sends
// the exact bytes once and accepts only the original saved record. Publication
// uncertainty is separate from execution: this method never creates a job.
func (c *Client) SavePatternV2(ctx context.Context, data []byte) (PatternWriteResultV2, error) {
	expected, err := patternsv2.PrepareSave(data)
	if err != nil {
		return PatternWriteResultV2{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v2/patterns", bytes.NewReader(data))
	if err != nil {
		return PatternWriteResultV2{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	response, err := c.http.Do(request)
	if err != nil {
		return PatternWriteResultV2{}, patternV2WriteError(fmt.Errorf("contact daemon: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PatternWriteResultV2{}, patternV2WriteError(decodeLightAPIError(response))
	}
	var value PatternWriteResultV2
	err = decodeLightResponse(response, &value)
	if err == nil {
		err = validatePatternSaveV2(value, expected.Candidate().Record(), response.StatusCode, response.Header)
	}
	if err != nil {
		return PatternWriteResultV2{}, patternV2WriteError(&DecodeError{Status: response.StatusCode, Err: err})
	}
	return value, nil
}

func inspectPatternRecordV2(value PatternRecordV2) (patternsv2.Inspection, error) {
	if value.SchemaVersion != patternsv2.SchemaVersion || value.Contract != httpapi.PatternLibraryV2Contract {
		return patternsv2.Inspection{}, fmt.Errorf("unsupported Pattern v2 response contract")
	}
	i, err := patternsv2.InspectRecord(value.Record)
	if err != nil {
		return patternsv2.Inspection{}, err
	}
	if value.Verification != i.Support() || value.ExecutionDigest != i.ExecutionDigest() {
		return patternsv2.Inspection{}, fmt.Errorf("Pattern v2 verification or execution digest differs from the complete record")
	}
	return i, nil
}

func patternV2ReadHeaders(headers http.Header) error {
	for _, name := range []string{"Location", "X-NB1-Daemon-Instance-ID", "X-NB1-Replayed"} {
		if len(headers.Values(name)) != 0 {
			return fmt.Errorf("non-execution Pattern v2 response contains submission metadata")
		}
	}
	return nil
}

func validatePatternSaveV2(value PatternWriteResultV2, expected patternsv2.Record, status int, headers http.Header) error {
	if value.SchemaVersion != patternsv2.SchemaVersion || value.Contract != httpapi.PatternLibraryV2Contract ||
		len(value.Records) != 1 || value.Total < 1 || value.Total > patternsv2.MaxRecords || value.Created == nil || value.Existing == nil {
		return fmt.Errorf("invalid Pattern v2 save result")
	}
	i, err := patternsv2.InspectRecord(value.Records[0])
	if err != nil || !i.Support().Verified || !reflect.DeepEqual(i.Record(), expected) {
		return fmt.Errorf("Pattern v2 save result changed the confirmed record")
	}
	created := len(value.Created) == 1 && value.Created[0] == expected.ID && len(value.Existing) == 0
	existing := len(value.Existing) == 1 && value.Existing[0] == expected.ID && len(value.Created) == 0
	if !((status == http.StatusCreated && created) || (status == http.StatusOK && existing)) {
		return fmt.Errorf("Pattern v2 save status differs from created/existing IDs")
	}
	locations := headers.Values("Location")
	if len(locations) != 1 || locations[0] != "/v2/patterns/"+url.PathEscape(expected.ID) ||
		len(headers.Values("X-NB1-Daemon-Instance-ID")) != 0 || len(headers.Values("X-NB1-Replayed")) != 0 {
		return fmt.Errorf("Pattern v2 save did not uniquely identify the original record")
	}
	return nil
}

func patternV2WriteError(err error) error {
	// Keep the established publication-only uncertainty classification and
	// original cause. It does not imply a device submission or an execution ticket.
	unknown := true
	var api *Error
	if errors.As(err, &api) {
		switch api.Status {
		case http.StatusBadRequest:
			unknown = api.Code != "invalid_pattern_request"
		case http.StatusConflict:
			switch api.Code {
			case "pattern_revision_conflict", "pattern_conflict", "pattern_library_limit", "pattern_library_busy", "pattern_library_read_only":
				unknown = false
			}
		case http.StatusUnsupportedMediaType:
			unknown = api.Code != "json_required"
		case http.StatusRequestEntityTooLarge:
			unknown = api.Code != "pattern_package_too_large"
		case http.StatusNotImplemented:
			switch api.Code {
			case "pattern_library_unsupported", "pattern_source_unsupported":
				unknown = false
			}
		}
	}
	return &PatternImportError{Err: err, PublicationUnknown: unknown}
}
