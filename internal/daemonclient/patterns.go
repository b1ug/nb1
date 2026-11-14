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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

// PatternImportError is about publication, never execution. A lost or malformed
// response may follow a successful import; inspect the original IDs/revisions.
type PatternImportError struct {
	Err                error
	PublicationUnknown bool
}

func (e *PatternImportError) Error() string {
	return fmt.Sprintf("%v (publication_unknown=%t; read the original IDs/revisions before importing again)", e.Err, e.PublicationUnknown)
}
func (e *PatternImportError) Unwrap() error { return e.Err }

func (c *Client) ListPatterns(ctx context.Context) (httpapi.PatternList, error) {
	var value httpapi.PatternList
	if _, err := c.doLightResponse(ctx, http.MethodGet, "/v1/patterns", nil, &value, http.StatusOK); err != nil {
		return httpapi.PatternList{}, err
	}
	if err := validatePatternList(value); err != nil {
		return httpapi.PatternList{}, patternDecode(err)
	}
	return value, nil
}

func (c *Client) GetPattern(ctx context.Context, id string) (patterns.Record, error) {
	if !validPatternID(id) {
		return patterns.Record{}, fmt.Errorf("invalid pattern ID")
	}
	var value patterns.Record
	if _, err := c.doLightResponse(ctx, http.MethodGet, "/v1/patterns/"+url.PathEscape(id), nil, &value, http.StatusOK); err != nil {
		return patterns.Record{}, err
	}
	if err := validatePatternRecord(value); err != nil {
		return patterns.Record{}, patternDecode(err)
	}
	if value.ID != id {
		return patterns.Record{}, patternDecode(fmt.Errorf("pattern detail changed requested ID"))
	}
	return value, nil
}

// ImportPatterns validates locally but sends the original authoring/snapshot
// bytes once. It never creates a Job, retries, or substitutes a different file.
func (c *Client) ImportPatterns(ctx context.Context, data []byte) (patternstore.ImportResult, error) {
	expected, err := patterns.DecodePackage(data)
	if err != nil {
		return patternstore.ImportResult{}, err
	}
	var value patternstore.ImportResult
	_, err = c.doLightResponse(ctx, http.MethodPost, "/v1/patterns/import", data, &value, http.StatusOK)
	if err == nil {
		err = validatePatternImport(value, expected)
		if err != nil {
			err = patternDecode(err)
		}
	}
	if err != nil {
		unknown := true
		var api *Error
		if errors.As(err, &api) {
			unknown = api.Status >= 500 || api.Code == "pattern_commit_uncertain" || api.Code == "http_error" || api.Code == ""
		}
		return patternstore.ImportResult{}, &PatternImportError{Err: err, PublicationUnknown: unknown}
	}
	return value, nil
}

// ExportPatterns validates the complete canonical snapshot before returning
// bytes. id="" means the library. Returned path metadata is never opened.
func (c *Client) ExportPatterns(ctx context.Context, id string) ([]byte, error) {
	path := "/v1/patterns"
	if id != "" {
		if !validPatternID(id) {
			return nil, fmt.Errorf("invalid pattern ID")
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
		return nil, patternDecode(fmt.Errorf("snapshot requires HTTP200"))
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return nil, patternDecode(fmt.Errorf("snapshot requires one Content-Type"))
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" {
		return nil, patternDecode(fmt.Errorf("snapshot requires application/json"))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, patterns.MaxPackageBytes+1))
	if err != nil || len(data) > patterns.MaxPackageBytes {
		return nil, patternDecode(fmt.Errorf("snapshot unreadable or exceeds 1 MiB"))
	}
	records, err := patterns.DecodePackage(data)
	if err != nil {
		return nil, patternDecode(err)
	}
	canonical, err := patterns.EncodeSnapshot(records)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, patternDecode(fmt.Errorf("snapshot is not canonical frozen bytes"))
	}
	for i, r := range records {
		if i > 0 && records[i-1].ID >= r.ID {
			return nil, patternDecode(fmt.Errorf("snapshot IDs are not sorted"))
		}
	}
	if id != "" && (len(records) != 1 || records[0].ID != id) {
		return nil, patternDecode(fmt.Errorf("snapshot changed requested ID"))
	}
	return data, nil
}

func (c *Client) PreviewPattern(ctx context.Context, ref httpapi.PatternRef, serial *string) (httpapi.PatternPreview, error) {
	if !validPatternRef(ref) || serial != nil && !control.ValidDeviceSerial(*serial) {
		return httpapi.PatternPreview{}, fmt.Errorf("invalid fixed pattern reference or serial")
	}
	body, _ := json.Marshal(httpapi.PreviewRequest{PatternRef: &ref, Serial: serial})
	var value httpapi.PatternPreview
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v1/programs/preview", body, &value, http.StatusOK)
	if err != nil {
		return httpapi.PatternPreview{}, err
	}
	if headers.Get("Location") != "" || len(headers.Values("X-NB1-Daemon-Instance-ID")) != 0 || len(headers.Values("X-NB1-Replayed")) != 0 {
		return httpapi.PatternPreview{}, patternDecode(fmt.Errorf("pattern preview contained submission metadata"))
	}
	if value.PatternRef != ref {
		return httpapi.PatternPreview{}, patternDecode(fmt.Errorf("pattern preview changed the original reference"))
	}
	if err = control.ValidatePreviewResult(value.Preview, "program", serial); err != nil {
		return httpapi.PatternPreview{}, patternDecode(err)
	}
	return value, nil
}

// SubmitPatternWithReceipt performs one target preview and at most one bound
// jobs POST. The preview's independent frozen Program binds the accepted Job.
func (c *Client) SubmitPatternWithReceipt(ctx context.Context, serial string, ref httpapi.PatternRef, receipt control.ReceiptRequest) (control.ReceiptResult, error) {
	if !validPatternRef(ref) || !control.ValidDeviceSerial(serial) || !validPatternReceipt(receipt) {
		return control.ReceiptResult{}, fmt.Errorf("pattern run requires a valid serial, fixed reference and complete original ticket")
	}
	preview, err := c.PreviewPattern(ctx, ref, &serial)
	if err != nil {
		return control.ReceiptResult{}, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	if preview.Preview.Validation.Device != "supported" {
		return control.ReceiptResult{}, &ReceiptError{Err: fmt.Errorf("pattern target is not confirmed supported; no execution POST; query the original ticket for any earlier attempt"), Receipt: receipt, OutcomeUnknown: true}
	}
	body, _ := json.Marshal(httpapi.SubmitRequest{PatternRef: &ref, Policy: "reject", RequestID: &receipt.RequestID, ExpectedDaemonInstanceID: &receipt.ExpectedDaemonInstanceID})
	var job control.Job
	headers, err := c.doLightResponse(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", body, &job, http.StatusAccepted)
	if err != nil {
		return control.ReceiptResult{}, patternReceiptError(err, receipt)
	}
	if err = validatePatternJob(job, serial, preview.Preview.Program, ""); err != nil {
		return control.ReceiptResult{}, patternReceiptError(patternDecode(err), receipt)
	}
	instance, replayed, location := headers.Values("X-NB1-Daemon-Instance-ID"), headers.Values("X-NB1-Replayed"), headers.Values("Location")
	wantLocation := "/v1/devices/" + url.PathEscape(serial) + "/requests/" + url.PathEscape(receipt.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(receipt.ExpectedDaemonInstanceID)
	if len(instance) != 1 || instance[0] != receipt.ExpectedDaemonInstanceID || len(replayed) != 1 || replayed[0] != "true" && replayed[0] != "false" || len(location) != 1 || location[0] != wantLocation {
		return control.ReceiptResult{}, patternReceiptError(patternDecode(fmt.Errorf("pattern Job did not uniquely confirm the original ticket")), receipt)
	}
	return control.ReceiptResult{Job: job, DaemonInstanceID: instance[0], Replayed: replayed[0] == "true"}, nil
}

// LookupPatternRequest never resolves the library again. It binds only to the
// accepted Job's ID and frozen Program and the original receipt.
func (c *Client) LookupPatternRequest(ctx context.Context, serial string, receipt control.ReceiptRequest, accepted control.Job) (control.Job, error) {
	if !control.ValidDeviceSerial(serial) || !validPatternReceipt(receipt) || accepted.Device != serial || !control.ValidDeviceSerial(accepted.ID) {
		return control.Job{}, fmt.Errorf("pattern lookup requires the original serial, receipt and accepted Job")
	}
	if err := validatePatternJob(accepted, serial, accepted.Program, accepted.ID); err != nil {
		return control.Job{}, err
	}
	path := "/v1/devices/" + url.PathEscape(serial) + "/requests/" + url.PathEscape(receipt.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(receipt.ExpectedDaemonInstanceID)
	var job control.Job
	headers, err := c.doLightResponse(ctx, http.MethodGet, path, nil, &job, http.StatusOK)
	if err == nil {
		values := headers.Values("X-NB1-Daemon-Instance-ID")
		if len(values) != 1 || values[0] != receipt.ExpectedDaemonInstanceID {
			err = patternDecode(fmt.Errorf("pattern lookup changed original instance"))
		}
	}
	if err == nil {
		err = validatePatternJob(job, serial, accepted.Program, accepted.ID)
	}
	if err != nil {
		return control.Job{}, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	return job, nil
}

func patternReceiptError(err error, receipt control.ReceiptRequest) error {
	result := receiptError(err, receipt).(*ReceiptError)
	var api *Error
	if errors.As(err, &api) && strings.HasPrefix(api.Code, "pattern_") {
		result.OutcomeUnknown = true
	}
	return result
}
func patternDecode(err error) *DecodeError { return &DecodeError{Status: http.StatusOK, Err: err} }

func validPatternID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for i, c := range []byte(id) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return false
		}
	}
	return true
}
func validPatternRef(ref httpapi.PatternRef) bool {
	return validPatternID(ref.ID) && len(ref.Revision) == 71 && strings.HasPrefix(ref.Revision, "sha256:") && lowerHex(ref.Revision[7:])
}
func validPatternReceipt(receipt control.ReceiptRequest) bool {
	if len(receipt.ExpectedDaemonInstanceID) != 39 || !strings.HasPrefix(receipt.ExpectedDaemonInstanceID, "daemon_") || !lowerHex(receipt.ExpectedDaemonInstanceID[7:]) {
		return false
	}
	parts := strings.Split(receipt.RequestID, "_")
	if len(parts) != 3 || parts[0] != "req" || len(parts[1]) < 1 || len(parts[1]) > 19 || parts[1][0] == '0' || len(parts[2]) != 32 || !lowerHex(parts[2]) {
		return false
	}
	for _, c := range []byte(parts[1]) {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	return err == nil && n > 0
}
func validatePatternRecord(value patterns.Record) error {
	canonical, err := patterns.NormalizeRecord(value)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(value, canonical) {
		return fmt.Errorf("pattern record is not canonical")
	}
	return nil
}
func validatePatternList(value httpapi.PatternList) error {
	library := value.Library
	if value.SchemaVersion != 1 || value.Contract != httpapi.PatternLibraryContract || library.State != "ready" || !library.CreateOnly || library.MaxRecords != patterns.MaxRecords || library.MaxPackageBytes != patterns.MaxPackageBytes || library.RecordContract != patterns.RevisionDomain || library.CompileContract != patterns.CompileContract || library.SnapshotKind != patterns.SnapshotKind || !absolutePatternDisplayPath(library.Path) || len(value.Patterns) > patterns.MaxRecords {
		return fmt.Errorf("invalid pattern library contract or limits")
	}
	for i, p := range value.Patterns {
		if !validPatternRef(httpapi.PatternRef{ID: p.ID, Revision: p.Revision}) || i > 0 && value.Patterns[i-1].ID >= p.ID || p.CompileContract != patterns.CompileContract || !utf8.ValidString(p.Title) || len(p.Title) < 1 || len(p.Title) > 1024 || utf8.RuneCountInString(p.Title) > 256 || p.TotalSteps < 1 || p.TotalSteps > control.MaxProgramSteps*control.MaxProgramRepeat || p.NominalDurationMS < int64(p.TotalSteps)*control.StepOverheadMS || p.NominalDurationMS > control.MaxNominalDurationMS {
			return fmt.Errorf("invalid or unsorted pattern summary")
		}
		caps := []string{"set", "program", "raw-rgb"}
		if p.OnFinish == "hold" {
			caps = append(caps, "hold")
		} else if p.OnFinish != "off" {
			return fmt.Errorf("invalid pattern finish mode")
		}
		if !reflect.DeepEqual(p.RequiredCapabilities, caps) {
			return fmt.Errorf("invalid pattern required capabilities")
		}
	}
	return nil
}

// The path is display metadata, never a local file read. Permit absolute path
// syntax from all supported daemon platforms, including loopback forwarding.
func absolutePatternDisplayPath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && (path[0] >= 'A' && path[0] <= 'Z' || path[0] >= 'a' && path[0] <= 'z') && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	if strings.HasPrefix(path, `\\`) {
		parts := strings.Split(path[2:], `\`)
		return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
	}
	return false
}
func validatePatternImport(value patternstore.ImportResult, expected []patterns.Record) error {
	if value.Total < len(expected) || value.Total > patterns.MaxRecords || !reflect.DeepEqual(value.Records, expected) {
		return fmt.Errorf("import response changed original records or totals")
	}
	created, existing := map[string]bool{}, map[string]bool{}
	for _, id := range value.Created {
		if created[id] {
			return fmt.Errorf("duplicate created ID")
		}
		created[id] = true
	}
	for _, id := range value.Existing {
		if existing[id] || created[id] {
			return fmt.Errorf("duplicate/overlapping existing ID")
		}
		existing[id] = true
	}
	if len(created)+len(existing) != len(expected) {
		return fmt.Errorf("import classification is incomplete")
	}
	ci, ei := 0, 0
	for _, record := range value.Records {
		if err := validatePatternRecord(record); err != nil {
			return err
		}
		if created[record.ID] {
			if ci >= len(value.Created) || value.Created[ci] != record.ID {
				return fmt.Errorf("created IDs changed input order")
			}
			ci++
		} else if existing[record.ID] {
			if ei >= len(value.Existing) || value.Existing[ei] != record.ID {
				return fmt.Errorf("existing IDs changed input order")
			}
			ei++
		} else {
			return fmt.Errorf("unexpected import ID")
		}
	}
	return nil
}
func validatePatternJob(job control.Job, serial string, frozen control.Program, id string) error {
	expected, err := control.Preview(&frozen, nil)
	if err != nil || !reflect.DeepEqual(frozen, expected.Program) || !control.ValidDeviceSerial(job.ID) || id != "" && job.ID != id || job.Device != serial || !reflect.DeepEqual(job.Program, frozen) || job.TotalSteps != expected.TotalSteps || job.CompletedSteps < 0 || job.CompletedSteps > job.TotalSteps || job.CreatedAt.IsZero() {
		return fmt.Errorf("pattern Job identity, frozen Program or progress changed")
	}
	if job.Status == "succeeded" && job.CompletedSteps != job.TotalSteps {
		return fmt.Errorf("successful pattern Job has incomplete progress")
	}
	switch job.Status {
	case "queued", "running", "cancelling", "succeeded", "failed", "cancelled":
		return nil
	default:
		return fmt.Errorf("unknown pattern Job status")
	}
}
