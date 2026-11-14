package daemonclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// PatternSnapshotImportErrorV2 retains the exact import input and requested
// references after an uncertain or rejected publication. It is not an execution
// receipt. Errors.As can still find the underlying PatternImportError.
type PatternSnapshotImportErrorV2 struct {
	Err      error
	original []byte
	refs     []patternidentity.Ref
}

func (e *PatternSnapshotImportErrorV2) Error() string { return e.Err.Error() }
func (e *PatternSnapshotImportErrorV2) Unwrap() error { return e.Err }
func (e *PatternSnapshotImportErrorV2) OriginalSnapshotJSON() []byte {
	return append([]byte(nil), e.original...)
}
func (e *PatternSnapshotImportErrorV2) RequestedRefs() []patternidentity.Ref {
	return append([]patternidentity.Ref(nil), e.refs...)
}

// ImportPatternsV2 validates the complete verified snapshot before one POST.
// Original bytes are sent without a retry, redirect, authoring conversion or
// device operation. Unsupported read-only exports cannot be imported.
func (c *Client) ImportPatternsV2(ctx context.Context, data []byte) (PatternWriteResultV2, error) {
	expected, err := patternsv2.DecodeSnapshot(data)
	if err != nil {
		return PatternWriteResultV2{}, err
	}
	failure := func(cause error) (PatternWriteResultV2, error) {
		refs := make([]patternidentity.Ref, 0, len(expected))
		for _, record := range expected {
			refs = append(refs, patternidentity.Ref{RecordContract: record.Contract, ID: record.ID, Revision: record.Revision})
		}
		return PatternWriteResultV2{}, &PatternSnapshotImportErrorV2{
			Err:      patternV2WriteError(cause),
			original: append([]byte(nil), data...), refs: refs,
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v2/patterns/import", bytes.NewReader(data))
	if err != nil {
		return PatternWriteResultV2{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	response, err := c.http.Do(request)
	if err != nil {
		return failure(fmt.Errorf("contact daemon: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		cause := decodeLightAPIError(response)
		if err := patternV2ReadHeaders(response.Header); err != nil {
			cause = &DecodeError{Status: response.StatusCode, Err: fmt.Errorf("untrusted import rejection: %v: %w", err, cause)}
		}
		return failure(cause)
	}
	var value PatternWriteResultV2
	err = decodeLightResponse(response, &value)
	if err == nil && response.StatusCode != http.StatusOK {
		err = fmt.Errorf("Pattern v2 import requires HTTP200")
	}
	if err == nil {
		err = patternV2ReadHeaders(response.Header)
	}
	if err == nil {
		err = validatePatternImportV2(value, expected)
	}
	if err != nil {
		return failure(&DecodeError{Status: response.StatusCode, Err: err})
	}
	return value, nil
}

func validatePatternImportV2(value PatternWriteResultV2, expected []patternsv2.Record) error {
	if value.SchemaVersion != patternsv2.SchemaVersion || value.Contract != httpapi.PatternLibraryV2Contract ||
		value.Records == nil || value.Created == nil || value.Existing == nil || len(value.Records) != len(expected) ||
		value.Total < len(expected) || value.Total > patternsv2.MaxRecords ||
		len(value.Created)+len(value.Existing) != len(expected) {
		return fmt.Errorf("invalid Pattern v2 import result")
	}
	requested := make(map[string]bool, len(expected))
	for index, record := range expected {
		inspection, err := patternsv2.InspectRecord(value.Records[index])
		if err != nil || !inspection.Support().Verified || !reflect.DeepEqual(inspection.Record(), record) {
			return fmt.Errorf("Pattern v2 import changed the complete requested record at index %d", index)
		}
		requested[record.ID] = true
	}
	seen := make(map[string]bool, len(expected))
	for _, group := range [][]string{value.Created, value.Existing} {
		for index, id := range group {
			if !requested[id] || seen[id] || index > 0 && group[index-1] >= id {
				return fmt.Errorf("Pattern v2 import created/existing IDs do not partition the requested records")
			}
			seen[id] = true
		}
	}
	return nil
}

// ExportPatternsV2 returns an integrity-checked canonical frozen snapshot.
// id="" selects the library. Valid unknown source/recipe records are preserved
// for read-only backup; returning them does not make them importable or runnable.
func (c *Client) ExportPatternsV2(ctx context.Context, id string) ([]byte, error) {
	path := "/v2/patterns"
	if id != "" {
		if !validPatternID(id) {
			return nil, fmt.Errorf("invalid Pattern v2 export ID")
		}
		path += "/" + url.PathEscape(id)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path+"?format=snapshot", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeLightAPIError(response)
	}
	if response.StatusCode != http.StatusOK {
		return nil, patternDecode(fmt.Errorf("Pattern v2 export requires HTTP200"))
	}
	if err := patternV2ReadHeaders(response.Header); err != nil {
		return nil, patternDecode(err)
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return nil, patternDecode(fmt.Errorf("Pattern v2 snapshot requires one Content-Type"))
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" {
		return nil, patternDecode(fmt.Errorf("Pattern v2 snapshot requires application/json"))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, patternsv2.MaxBytes+1))
	if err != nil || len(data) > patternsv2.MaxBytes {
		return nil, patternDecode(fmt.Errorf("Pattern v2 snapshot is unreadable or exceeds 1 MiB"))
	}
	snapshot, err := patternsv2.InspectSnapshot(data)
	if err != nil {
		return nil, patternDecode(err)
	}
	if !bytes.Equal(snapshot.CanonicalJSON(), data) {
		return nil, patternDecode(fmt.Errorf("Pattern v2 snapshot is not canonical frozen bytes"))
	}
	records := snapshot.Records()
	if id != "" && (len(records) != 1 || records[0].Record().ID != id) {
		return nil, patternDecode(fmt.Errorf("Pattern v2 snapshot changed the requested ID"))
	}
	return data, nil
}
