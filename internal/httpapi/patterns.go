package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

const PatternLibraryContract = "nb1-pattern-library/v1"

// PatternStore owns only the bounded library. It never admits device work.
type PatternStore interface {
	Path() string
	List() ([]patterns.Record, error)
	Get(string) (patterns.Record, error)
	Resolve(string, string) (patterns.Record, error)
	Export() ([]byte, error)
	Import([]byte) (patternstore.ImportResult, error)
}

type Option func(*handler)

// WithPatternStore opts in to the library without changing existing New calls.
func WithPatternStore(store PatternStore) Option {
	return func(h *handler) {
		h.patterns, h.patternErr, h.patternPath = store, nil, ""
		if store != nil {
			h.patternPath = store.Path()
		}
	}
}

// WithPatternDirectory selects a library without opening or creating it. An
// invalid configuration disables only Pattern features, never ordinary lights.
// Empty selects the current service account's UserConfigDir()/nb1/patterns.
func WithPatternDirectory(dir string) Option {
	return func(h *handler) {
		selected := dir
		var err error
		if selected == "" {
			selected, err = patternstore.DefaultDir()
		}
		h.patternPath, h.patterns, h.patternErr = selected, nil, err
		if err == nil {
			store, err := patternstore.New(selected)
			h.patternErr = err
			if err == nil {
				h.patterns, h.patternPath = store, store.Path()
			}
		}
	}
}

type PatternRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type PatternLibraryInfo struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	CreateOnly      bool   `json:"create_only"`
	MaxRecords      int    `json:"max_records"`
	MaxPackageBytes int    `json:"max_package_bytes"`
	RecordContract  string `json:"record_contract"`
	CompileContract string `json:"compile_contract"`
	SnapshotKind    string `json:"snapshot_kind"`
}

type PatternSummary struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Revision             string   `json:"revision"`
	CompileContract      string   `json:"compile_contract"`
	TotalSteps           int      `json:"total_steps"`
	NominalDurationMS    int64    `json:"nominal_duration_ms"`
	OnFinish             string   `json:"on_finish"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

type PatternList struct {
	SchemaVersion int                `json:"schema_version"`
	Contract      string             `json:"contract"`
	Library       PatternLibraryInfo `json:"library"`
	Patterns      []PatternSummary   `json:"patterns"`
}

type PatternPreview struct {
	PatternRef PatternRef            `json:"pattern_ref"`
	Preview    control.PreviewResult `json:"preview"`
}

// These routes run after the common local Host/Origin guard. The name import
// remains a legal ID for GET; only POST /v1/patterns/import imports a package.
func (h *handler) servePatterns(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v1/patterns" && !strings.HasPrefix(r.URL.Path, "/v1/patterns/") {
		return false
	}
	if r.URL.Path == "/v1/patterns/import" && r.Method == http.MethodPost {
		h.importPatterns(w, r)
		return true
	}
	if !method(w, r, http.MethodGet) {
		return true
	}
	format, ok := patternReadQuery(w, r)
	if !ok {
		return true
	}
	if !h.patternAvailable(w) {
		return true
	}
	if r.URL.Path == "/v1/patterns" {
		if format == "snapshot" {
			data, err := h.patterns.Export()
			if err != nil {
				patternError(w, err)
				return true
			}
			writePatternSnapshot(w, data)
			return true
		}
		records, err := h.patterns.List()
		if err != nil {
			patternError(w, err)
			return true
		}
		result := PatternList{SchemaVersion: 1, Contract: PatternLibraryContract,
			Library:  PatternLibraryInfo{Path: h.patternPath, State: "ready", CreateOnly: true, MaxRecords: patterns.MaxRecords, MaxPackageBytes: patterns.MaxPackageBytes, RecordContract: patterns.RevisionDomain, CompileContract: patterns.CompileContract, SnapshotKind: patterns.SnapshotKind},
			Patterns: make([]PatternSummary, 0, len(records))}
		for _, record := range records {
			preview, err := control.Preview(&record.CompiledProgram, nil)
			if err != nil {
				patternError(w, fmt.Errorf("%w: invalid frozen program", patternstore.ErrCorrupt))
				return true
			}
			result.Patterns = append(result.Patterns, PatternSummary{record.ID, record.Title, record.Revision, record.CompileContract, preview.TotalSteps, preview.NominalDurationMS, record.CompiledProgram.OnFinish, preview.RequiredCapabilities})
		}
		writeJSON(w, http.StatusOK, result)
		return true
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/patterns/")
	if !validPatternID(id) {
		patternError(w, patternstore.ErrInvalid)
		return true
	}
	record, err := h.patterns.Get(id)
	if err != nil {
		patternError(w, err)
		return true
	}
	if format == "snapshot" {
		data, err := patterns.EncodeSnapshot([]patterns.Record{record})
		if err != nil {
			patternError(w, fmt.Errorf("%w: invalid frozen record", patternstore.ErrCorrupt))
			return true
		}
		writePatternSnapshot(w, data)
	} else {
		writeJSON(w, http.StatusOK, record)
	}
	return true
}

func patternReadQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		patternError(w, fmt.Errorf("%w: library reads accept no body", patternstore.ErrInvalid))
		return "", false
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 {
		patternError(w, patternstore.ErrInvalid)
		return "", false
	}
	if len(query) == 0 {
		return "", true
	}
	values, ok := query["format"]
	if !ok || len(values) != 1 || values[0] != "snapshot" {
		patternError(w, patternstore.ErrInvalid)
		return "", false
	}
	return values[0], true
}

func (h *handler) patternAvailable(w http.ResponseWriter) bool {
	if h.patternErr != nil {
		patternError(w, fmt.Errorf("library configuration %q: %v", h.patternPath, h.patternErr))
		return false
	}
	if h.patterns == nil {
		patternError(w, patternstore.ErrUnsupported)
		return false
	}
	return true
}

func patternJSONMedia(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	value, _, err := mime.ParseMediaType(values[0])
	return err == nil && value == "application/json"
}

func (h *handler) importPatterns(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		patternError(w, patternstore.ErrInvalid)
		return
	}
	if !patternJSONMedia(r) {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "exactly one application/json Content-Type is required")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, patterns.MaxPackageBytes+1))
	if err != nil {
		patternError(w, fmt.Errorf("%w: cannot read package", patternstore.ErrInvalid))
		return
	}
	if len(data) > patterns.MaxPackageBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "pattern_package_too_large", "pattern package exceeds 1 MiB")
		return
	}
	if !h.patternAvailable(w) {
		return
	}
	// Pass original bytes through once: the Store performs complete validation
	// before opening a create transaction and returns no partial success.
	result, err := h.patterns.Import(data)
	if err != nil {
		patternError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writePatternSnapshot(w http.ResponseWriter, data []byte) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func patternError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "pattern_library_unavailable"
	switch {
	case errors.Is(err, patternstore.ErrCommitUncertain):
		code = "pattern_commit_uncertain"
	case errors.Is(err, patternstore.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_pattern_request"
	case errors.Is(err, patternstore.ErrLimit):
		status, code = http.StatusConflict, "pattern_library_limit"
	case errors.Is(err, patternstore.ErrNotFound):
		status, code = http.StatusNotFound, "pattern_not_found"
	case errors.Is(err, patternstore.ErrRevisionConflict):
		status, code = http.StatusConflict, "pattern_revision_conflict"
	case errors.Is(err, patternstore.ErrConflict):
		status, code = http.StatusConflict, "pattern_conflict"
	case errors.Is(err, patternstore.ErrBusy):
		status, code = http.StatusConflict, "pattern_library_busy"
	case errors.Is(err, patternstore.ErrUnsupported):
		status, code = http.StatusNotImplemented, "pattern_library_unsupported"
	}
	message := err.Error()
	if errors.Is(err, patternstore.ErrCommitUncertain) {
		message += "; publication may have succeeded; read the affected IDs and revisions before another import"
	}
	writeError(w, status, code, message)
}

func validPatternID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i, c := range []byte(value) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return false
		}
	}
	return true
}

func validPatternRevision(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && patternLowerHex(value[7:])
}

func patternLowerHex(value string) bool {
	for _, c := range []byte(value) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validPatternTicket(input SubmitRequest) bool {
	if input.RequestID == nil || input.ExpectedDaemonInstanceID == nil {
		return false
	}
	instance := *input.ExpectedDaemonInstanceID
	if len(instance) != 39 || !strings.HasPrefix(instance, "daemon_") || !patternLowerHex(instance[7:]) {
		return false
	}
	parts := strings.Split(*input.RequestID, "_")
	if len(parts) != 3 || parts[0] != "req" || len(parts[1]) == 0 || len(parts[1]) > 19 || parts[1][0] == '0' || len(parts[2]) != 32 || !patternLowerHex(parts[2]) {
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

func (h *handler) resolvePattern(w http.ResponseWriter, ref PatternRef, priorTicket bool) (patterns.Record, bool) {
	var record patterns.Record
	var err error
	if !validPatternID(ref.ID) || !validPatternRevision(ref.Revision) {
		err = patternstore.ErrInvalid
	} else if h.patternErr != nil {
		err = fmt.Errorf("library configuration %q: %v", h.patternPath, h.patternErr)
	} else if h.patterns == nil {
		err = patternstore.ErrUnsupported
	} else {
		record, err = h.patterns.Resolve(ref.ID, ref.Revision)
	}
	if err != nil {
		if priorTicket {
			err = fmt.Errorf("%w; no new admission was attempted; an earlier use of the original ticket may have a Job, so query that receipt", err)
		}
		patternError(w, err)
		return patterns.Record{}, false
	}
	return record, true
}

func (h *handler) previewPattern(w http.ResponseWriter, r *http.Request, input PreviewRequest) {
	if input.Program != nil || input.Effect != nil || !patternJSONMedia(r) {
		patternError(w, patternstore.ErrInvalid)
		return
	}
	record, ok := h.resolvePattern(w, *input.PatternRef, false)
	if !ok {
		return
	}
	preview, err := control.Preview(&record.CompiledProgram, nil)
	if err != nil {
		patternError(w, fmt.Errorf("%w: invalid frozen program", patternstore.ErrCorrupt))
		return
	}
	if input.Serial != nil {
		if h.service == nil {
			serviceError(w, control.ErrClosed)
			return
		}
		snapshot, err := h.service.Device(*input.Serial)
		if err != nil && !errors.Is(err, control.ErrNotFound) {
			serviceError(w, err)
			return
		}
		if err != nil {
			preview = control.WithDeviceSnapshot(preview, *input.Serial, nil)
		} else {
			preview = control.WithDeviceSnapshot(preview, *input.Serial, &snapshot)
		}
	}
	writeJSON(w, http.StatusOK, PatternPreview{PatternRef: *input.PatternRef, Preview: preview})
}

func (h *handler) submitPattern(w http.ResponseWriter, r *http.Request, serial string, input SubmitRequest) {
	if input.Program != nil || input.Effect != nil || input.Policy != "reject" || !validPatternTicket(input) || !control.ValidDeviceSerial(serial) || r.URL.RawQuery != "" || !patternJSONMedia(r) {
		patternError(w, fmt.Errorf("%w: pattern_ref requires one exact reference, explicit serial, reject and the complete original request ticket", patternstore.ErrInvalid))
		return
	}
	record, ok := h.resolvePattern(w, *input.PatternRef, true)
	if !ok {
		return
	}
	service, ok := h.receiptService(w)
	if !ok {
		return
	}
	// Resolve returns an independent frozen Program and closes its short store
	// transaction before this sole call to the existing admission executor.
	result, err := service.SubmitWithReceipt(r.Context(), serial, record.CompiledProgram, input.Policy, control.ReceiptRequest{RequestID: *input.RequestID, ExpectedDaemonInstanceID: *input.ExpectedDaemonInstanceID})
	if err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("X-NB1-Replayed", strconv.FormatBool(result.Replayed))
	w.Header().Set("X-NB1-Daemon-Instance-ID", result.DaemonInstanceID)
	w.Header().Set("Location", receiptLocation(serial, *input.RequestID, *input.ExpectedDaemonInstanceID))
	writeJSON(w, http.StatusAccepted, result.Job)
}
