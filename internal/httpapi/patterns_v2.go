package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

const PatternLibraryV2Contract = "nb1-pattern-library/experimental-v2"

// PatternStoreV2 is explicitly independent of the v1 library. These handlers
// never call Resolve or any service method; persistence is not admission.
type PatternStoreV2 interface {
	Path() string
	List() ([]patternsv2.Inspection, error)
	Get(string) (patternsv2.Inspection, error)
	Resolve(string, string) (patternsv2.Record, error)
	Export() ([]byte, error)
	Import([]byte) (patternstore.ImportResultV2, error)
}

// WithPatternStoreV2 opts in to one explicitly selected v2 store. It performs
// no store calls, path selection, default-directory lookup or v1 fallback.
// In particular, a pure preview never calls even Path on this store.
func WithPatternStoreV2(store PatternStoreV2) Option {
	return func(h *handler) {
		h.patternsV2, h.patternErrV2 = nil, nil
		if store == nil {
			return
		}
		// An interface holding a nil store must behave like an absent store.
		// Do not call any store method to inspect configuration.
		value := reflect.ValueOf(store)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
			if value.IsNil() {
				return
			}
		}
		h.patternsV2 = store
	}
}

// WithPatternDirectoryV2 selects an independent v2 library without filesystem
// I/O. Empty selects UserConfigDir()/nb1/patterns-v2. Only a valid save or import
// entering its write transaction may create the directory; failed publication
// does not promise to remove that directory. Invalid configuration disables this library
// while pure preview and ordinary device operations remain available.
func WithPatternDirectoryV2(dir string) Option {
	return func(h *handler) {
		h.patternsV2, h.patternErrV2 = nil, nil
		selected := dir
		var err error
		if selected == "" {
			selected, err = patternstore.DefaultV2Dir()
		}
		if err == nil {
			var store *patternstore.StoreV2
			store, err = patternstore.NewV2(selected)
			if err == nil {
				h.patternsV2 = store
			}
		}
		if err != nil {
			h.patternErrV2 = fmt.Errorf("Pattern v2 directory %q: %w", selected, err)
		}
	}
}

type patternIdentityV2 struct {
	RecordContract string `json:"record_contract"`
	ID             string `json:"id"`
	Revision       string `json:"revision"`
}

type patternExecutionV2 struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func patternExecutionUnavailableV2() patternExecutionV2 {
	return patternExecutionV2{Available: false, Reason: "pattern_identity_admission_not_integrated"}
}

type patternRecordResponseV2 struct {
	SchemaVersion   int                `json:"schema_version"`
	Contract        string             `json:"contract"`
	Record          patternsv2.Record  `json:"record"`
	Verification    patternsv2.Support `json:"verification"`
	ExecutionDigest string             `json:"execution_digest"`
}

type patternPreviewResponseV2 struct {
	patternRecordResponseV2
	Preview          patternsv2.PreviewFacts `json:"preview"`
	PatternExecution patternExecutionV2      `json:"pattern_execution"`
}

type patternLibraryInfoV2 struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	CreateOnly      bool   `json:"create_only"`
	MaxRecords      int    `json:"max_records"`
	MaxPackageBytes int    `json:"max_package_bytes"`
	RecordContract  string `json:"record_contract"`
	SnapshotKind    string `json:"snapshot_kind"`
}

type patternSummaryV2 struct {
	PatternRef        patternIdentityV2  `json:"pattern_ref"`
	Title             string             `json:"title"`
	SourceContract    string             `json:"source_contract"`
	ExecutionKind     string             `json:"execution_kind"`
	ExecutionContract string             `json:"execution_contract"`
	Verification      patternsv2.Support `json:"verification"`
	ExecutionDigest   string             `json:"execution_digest"`
}

type patternListResponseV2 struct {
	SchemaVersion    int                  `json:"schema_version"`
	Contract         string               `json:"contract"`
	Library          patternLibraryInfoV2 `json:"library"`
	PatternExecution patternExecutionV2   `json:"pattern_execution"`
	Patterns         []patternSummaryV2   `json:"patterns"`
}

type patternWriteResponseV2 struct {
	SchemaVersion int    `json:"schema_version"`
	Contract      string `json:"contract"`
	patternstore.ImportResultV2
}

// Only preview and import have special POST meanings under this namespace.
// GET preview/import still select ordinary IDs. No execution route is added.
func (h *handler) servePatternsV2(w http.ResponseWriter, r *http.Request) bool {
	const root = "/v2/patterns"
	if r.URL.Path != root && !strings.HasPrefix(r.URL.Path, root+"/") {
		return false
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case root + "/preview":
			h.previewPatternV2(w, r)
			return true
		case root:
			h.savePatternV2(w, r)
			return true
		case root + "/import":
			h.importPatternsV2(w, r)
			return true
		}
	}
	if (r.URL.Path == root || r.URL.Path == root+"/preview" || r.URL.Path == root+"/import") && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
		return true
	}
	if !method(w, r, http.MethodGet) {
		return true
	}
	format, ok := patternReadQueryV2(w, r)
	if !ok {
		return true
	}
	id := ""
	if r.URL.Path != root {
		id = strings.TrimPrefix(r.URL.Path, root+"/")
		if !validPatternID(id) {
			patternErrorV2(w, patternstore.ErrInvalid)
			return true
		}
	}
	if !h.patternAvailableV2(w) {
		return true
	}
	if id != "" {
		h.getPatternV2(w, id, format)
	} else if format == "snapshot" {
		data, err := h.patternsV2.Export()
		if err != nil {
			patternErrorV2(w, err)
		} else {
			writePatternSnapshot(w, data)
		}
	} else {
		h.listPatternsV2(w)
	}
	return true
}

func patternReadQueryV2(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		patternErrorV2(w, fmt.Errorf("%w: library reads accept no body", patternstore.ErrInvalid))
		return "", false
	}
	if r.URL.RawQuery == "" && !r.URL.ForceQuery {
		return "", true
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	values := query["format"]
	if err != nil || len(query) != 1 || len(values) != 1 || values[0] != "snapshot" {
		patternErrorV2(w, patternstore.ErrInvalid)
		return "", false
	}
	return "snapshot", true
}

func patternBodyV2(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		patternErrorV2(w, patternstore.ErrInvalid)
		return nil, false
	}
	if !patternJSONMedia(r) {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "exactly one application/json Content-Type is required")
		return nil, false
	}
	if r.Body == nil {
		patternErrorV2(w, patternstore.ErrInvalid)
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, patternsv2.MaxBytes+1))
	if len(data) > patternsv2.MaxBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "pattern_package_too_large", "pattern package exceeds 1 MiB")
		return nil, false
	}
	if err != nil {
		patternErrorV2(w, fmt.Errorf("%w: cannot read package", patternstore.ErrInvalid))
		return nil, false
	}
	return data, true
}

func (h *handler) patternAvailableV2(w http.ResponseWriter) bool {
	if h.patternErrV2 != nil {
		writeError(w, http.StatusServiceUnavailable, "pattern_library_unavailable", h.patternErrV2.Error())
		return false
	}
	if h.patternsV2 == nil {
		writeError(w, http.StatusNotImplemented, "pattern_library_unsupported", "Pattern v2 storage is not configured; pure preview remains available")
		return false
	}
	return true
}

func (h *handler) previewPatternV2(w http.ResponseWriter, r *http.Request) {
	data, ok := patternBodyV2(w, r)
	if !ok {
		return
	}
	c, err := patternsv2.PreviewDraft(data)
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	writeJSON(w, http.StatusOK, patternPreviewResponseV2{
		patternRecordResponseV2: patternRecordResponseV2{patternsv2.SchemaVersion, PatternLibraryV2Contract, c.Record(), c.Support(), c.ExecutionDigest()},
		Preview:                 c.Preview(), PatternExecution: patternExecutionUnavailableV2(),
	})
}

func (h *handler) savePatternV2(w http.ResponseWriter, r *http.Request) {
	data, ok := patternBodyV2(w, r)
	if !ok {
		return
	}
	s, err := patternsv2.PrepareSave(data)
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	if !h.patternAvailableV2(w) {
		return
	}
	result, err := h.patternsV2.Import(s.SnapshotJSON())
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	w.Header().Set("Location", "/v2/patterns/"+s.Candidate().Record().ID)
	status := http.StatusOK
	if len(result.Created) != 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, patternWriteResponseV2{patternsv2.SchemaVersion, PatternLibraryV2Contract, result})
}

func (h *handler) importPatternsV2(w http.ResponseWriter, r *http.Request) {
	data, ok := patternBodyV2(w, r)
	if !ok || !h.patternAvailableV2(w) {
		return
	}
	// Preserve original input bytes. StoreV2 validates the whole frozen package
	// before any create transaction; no authoring fallback or retry is used.
	result, err := h.patternsV2.Import(data)
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	writeJSON(w, http.StatusOK, patternWriteResponseV2{patternsv2.SchemaVersion, PatternLibraryV2Contract, result})
}

func (h *handler) listPatternsV2(w http.ResponseWriter) {
	values, err := h.patternsV2.List()
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	result := patternListResponseV2{
		SchemaVersion: patternsv2.SchemaVersion, Contract: PatternLibraryV2Contract,
		Library: patternLibraryInfoV2{Path: h.patternsV2.Path(), State: "ready", CreateOnly: true, MaxRecords: patternsv2.MaxRecords,
			MaxPackageBytes: patternsv2.MaxBytes, RecordContract: patternsv2.RecordContract, SnapshotKind: patternsv2.SnapshotKind},
		PatternExecution: patternExecutionUnavailableV2(), Patterns: make([]patternSummaryV2, 0, len(values)),
	}
	for _, value := range values {
		r, support := value.Record(), value.Support()
		if !support.Verified {
			result.Library.State = "read_only"
		}
		result.Patterns = append(result.Patterns, patternSummaryV2{patternIdentityV2{r.Contract, r.ID, r.Revision}, r.Title,
			r.Source.Contract, r.Compiled.Execution.Kind, r.Compiled.Execution.Contract, support, value.ExecutionDigest()})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) getPatternV2(w http.ResponseWriter, id, format string) {
	i, err := h.patternsV2.Get(id)
	if err != nil {
		patternErrorV2(w, err)
		return
	}
	if format == "snapshot" {
		data := append([]byte(`{"kind":"`+patternsv2.SnapshotKind+`","records":[`), i.CanonicalJSON()...)
		data = append(data, []byte(`],"schema_version":2}`)...)
		// Inspect, rather than strict EncodeSnapshot, preserves valid unknown
		// recipes/sources without converting them to verified write candidates.
		s, err := patternsv2.InspectSnapshot(data)
		if err != nil {
			patternErrorV2(w, fmt.Errorf("%w: invalid single-record snapshot", patternstore.ErrCorrupt))
			return
		}
		writePatternSnapshot(w, s.CanonicalJSON())
		return
	}
	writeJSON(w, http.StatusOK, patternRecordResponseV2{patternsv2.SchemaVersion, PatternLibraryV2Contract, i.Record(), i.Support(), i.ExecutionDigest()})
}

func patternErrorV2(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, patternstore.ErrCommitUncertain):
		writeError(w, http.StatusServiceUnavailable, "pattern_commit_uncertain", "publication may have succeeded; read original IDs and compare complete records before another save or import; do not automatically retry")
	case errors.Is(err, patternsv2.ErrRevisionConflict):
		writeError(w, http.StatusConflict, "pattern_revision_conflict", err.Error())
	case errors.Is(err, patternsv2.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_pattern_request", err.Error())
	case errors.Is(err, patternsv2.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "pattern_source_unsupported", err.Error())
	case errors.Is(err, patternstore.ErrReadOnly):
		writeError(w, http.StatusConflict, "pattern_library_read_only", err.Error())
	case errors.Is(err, patternstore.ErrFormatMismatch):
		writeError(w, http.StatusServiceUnavailable, "pattern_library_format_mismatch", err.Error())
	default:
		patternError(w, err)
	}
}
