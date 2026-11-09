package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternresolver"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

const patternExecutionBodyBytes = 4096
const PatternRefPreviewContract = "nb1-pattern-ref-preview/experimental-v1"

type patternRefPreviewRequest struct {
	PatternRef patternidentity.Ref `json:"pattern_ref"`
	Serial     *string             `json:"serial,omitempty"`
}

type patternSubmitRequestV2 struct {
	PatternRef               patternidentity.Ref `json:"pattern_ref"`
	Policy                   string              `json:"policy"`
	RequestID                string              `json:"request_id"`
	ExpectedDaemonInstanceID string              `json:"expected_daemon_instance_id"`
}

type patternCancelRequestV2 struct {
	PatternRef               patternidentity.Ref `json:"pattern_ref"`
	ExpectedDaemonInstanceID string              `json:"expected_daemon_instance_id"`
	ConfirmedJobID           *string             `json:"confirmed_job_id,omitempty"`
}

type patternRefPreviewResponse struct {
	SchemaVersion    int                 `json:"schema_version"`
	Contract         string              `json:"contract"`
	PatternRef       patternidentity.Ref `json:"pattern_ref"`
	ExecutionDigest  string              `json:"execution_digest"`
	Execution        json.RawMessage     `json:"execution"`
	Preview          interface{}         `json:"preview"` // closed Program / Frame preview union
	PatternExecution patternExecutionV2  `json:"pattern_execution"`
}

// Exact public paths are separate from P3 authoring. In particular, GET
// /v2/patterns/preview-ref still selects an ordinary, legal record ID.
func (h *handler) servePatternExecutionV2(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v2/patterns/preview-ref" {
		if r.Method == http.MethodGet {
			return false
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
			return true
		}
		h.previewPatternRefV2(w, r)
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "v2" || parts[1] != "devices" {
		return false
	}
	switch {
	case len(parts) == 4 && parts[3] == "pattern-jobs":
		if method(w, r, http.MethodPost) {
			h.submitPatternV2(w, r, parts[2])
		}
		return true
	case len(parts) == 5 && parts[3] == "pattern-requests" && parts[4] != "":
		if method(w, r, http.MethodGet) {
			h.queryPatternV2(w, r, parts[2], parts[4])
		}
		return true
	case len(parts) == 6 && parts[3] == "pattern-requests" && parts[4] != "" && parts[5] == "cancel":
		if method(w, r, http.MethodPost) {
			h.cancelPatternV2(w, r, parts[2], parts[4])
		}
		return true
	}
	return false
}

func decodePatternExecutionBody(w http.ResponseWriter, r *http.Request, out interface{}) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return invalidPatternExecution(w)
	}
	if !patternJSONMedia(r) {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "exactly one application/json Content-Type is required")
		return false
	}
	if r.Body == nil {
		return invalidPatternExecution(w)
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, patternExecutionBodyBytes+1))
	if len(data) > patternExecutionBodyBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Pattern execution request exceeds 4096 bytes")
		return false
	}
	// All accepted scalar values have closed ASCII grammars below. A lone
	// surrogate replaced by encoding/json therefore cannot pass validation.
	if err != nil || !utf8.Valid(data) || jsoninput.DecodeObject(data, out) != nil {
		return invalidPatternExecution(w)
	}
	return true
}

func invalidPatternExecution(w http.ResponseWriter) bool {
	writeError(w, http.StatusBadRequest, "invalid_pattern_request", "an exact Pattern reference, original receipt and canonical request fields are required")
	return false
}

func validPatternReceiptV2(serial string, ref patternidentity.Ref, receipt control.ReceiptRequest) bool {
	return control.ValidDeviceSerial(serial) && patternidentity.ValidateRef(ref) == nil && validPatternTicket(SubmitRequest{
		RequestID: &receipt.RequestID, ExpectedDaemonInstanceID: &receipt.ExpectedDaemonInstanceID,
	})
}

func (h *handler) patternServiceV2(w http.ResponseWriter) (control.PatternService, bool) {
	service, ok := h.service.(control.PatternService)
	if !ok {
		writeError(w, http.StatusNotImplemented, "pattern_execution_unsupported", "this service does not support source-bound Pattern execution")
	}
	return service, ok
}

// Constructing a resolver is deliberately not a configuration preflight.
// Core's accepted-receipt fast path must remain independent of this callback.
func (h *handler) patternResolverV2() control.PatternResolver {
	resolve := patternresolver.New(h.patternsV2)
	return func(ctx context.Context, ref patternidentity.Ref) (control.ResolvedPattern, error) {
		if err := ctx.Err(); err != nil {
			return control.ResolvedPattern{}, err
		}
		if h.patternErrV2 != nil {
			return control.ResolvedPattern{}, &control.PatternResolveError{Kind: control.PatternResolveConfiguration, Err: h.patternErrV2}
		}
		return resolve(ctx, ref)
	}
}

func (h *handler) submitPatternV2(w http.ResponseWriter, r *http.Request, serial string) {
	var input patternSubmitRequestV2
	if !decodePatternExecutionBody(w, r, &input) {
		return
	}
	receipt := control.ReceiptRequest{RequestID: input.RequestID, ExpectedDaemonInstanceID: input.ExpectedDaemonInstanceID}
	if !validPatternReceiptV2(serial, input.PatternRef, receipt) || (input.Policy != "replace" && input.Policy != "queue" && input.Policy != "reject") {
		invalidPatternExecution(w)
		return
	}
	service, ok := h.patternServiceV2(w)
	if !ok {
		return
	}
	result, err := service.SubmitPatternWithReceipt(r.Context(), serial, input.PatternRef, input.Policy, receipt, h.patternResolverV2())
	if err != nil {
		patternExecutionErrorV2(w, err)
		return
	}
	w.Header().Set("X-NB1-Daemon-Instance-ID", result.DaemonInstanceID)
	w.Header().Set("X-NB1-Replayed", strconv.FormatBool(result.Replayed))
	w.Header().Set("Location", patternReceiptLocationV2(serial, receipt, input.PatternRef))
	writeJSON(w, http.StatusAccepted, result.Job)
}

func patternReceiptLocationV2(serial string, receipt control.ReceiptRequest, ref patternidentity.Ref) string {
	query := url.Values{
		"expected_daemon_instance_id": {receipt.ExpectedDaemonInstanceID},
		"record_contract":             {ref.RecordContract}, "pattern_id": {ref.ID}, "revision": {ref.Revision},
	}
	return "/v2/devices/" + url.PathEscape(serial) + "/pattern-requests/" + url.PathEscape(receipt.RequestID) + "?" + query.Encode()
}

func (h *handler) queryPatternV2(w http.ResponseWriter, r *http.Request, serial, requestID string) {
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || strings.Contains(r.URL.RawQuery, "&&") || strings.HasPrefix(r.URL.RawQuery, "&") || strings.HasSuffix(r.URL.RawQuery, "&") {
		invalidPatternExecution(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	keys := []string{"expected_daemon_instance_id", "record_contract", "pattern_id", "revision"}
	if err != nil || len(query) != len(keys) {
		invalidPatternExecution(w)
		return
	}
	for _, key := range keys {
		if len(query[key]) != 1 || query[key][0] == "" {
			invalidPatternExecution(w)
			return
		}
	}
	receipt := control.ReceiptRequest{RequestID: requestID, ExpectedDaemonInstanceID: query.Get(keys[0])}
	ref := patternidentity.Ref{RecordContract: query.Get(keys[1]), ID: query.Get(keys[2]), Revision: query.Get(keys[3])}
	if !validPatternReceiptV2(serial, ref, receipt) {
		invalidPatternExecution(w)
		return
	}
	service, ok := h.patternServiceV2(w)
	if !ok {
		return
	}
	job, err := service.PatternRequest(serial, receipt, ref)
	if err != nil {
		patternExecutionErrorV2(w, err)
		return
	}
	w.Header().Set("X-NB1-Daemon-Instance-ID", receipt.ExpectedDaemonInstanceID)
	writeJSON(w, http.StatusOK, job)
}

func (h *handler) cancelPatternV2(w http.ResponseWriter, r *http.Request, serial, requestID string) {
	var input patternCancelRequestV2
	if !decodePatternExecutionBody(w, r, &input) {
		return
	}
	receipt := control.ReceiptRequest{RequestID: requestID, ExpectedDaemonInstanceID: input.ExpectedDaemonInstanceID}
	knownID := ""
	if input.ConfirmedJobID != nil {
		knownID = *input.ConfirmedJobID
		prefix := "job_"
		if strings.HasPrefix(knownID, "framejob_") {
			prefix = "framejob_"
		}
		if len(knownID) != len(prefix)+32 || !strings.HasPrefix(knownID, prefix) || !patternLowerHex(knownID[len(prefix):]) {
			invalidPatternExecution(w)
			return
		}
	}
	if !validPatternReceiptV2(serial, input.PatternRef, receipt) {
		invalidPatternExecution(w)
		return
	}
	service, ok := h.patternServiceV2(w)
	if !ok {
		return
	}
	job, err := service.CancelPatternRequest(r.Context(), serial, receipt, input.PatternRef, knownID)
	if err != nil {
		patternExecutionErrorV2(w, err)
		return
	}
	w.Header().Set("X-NB1-Daemon-Instance-ID", receipt.ExpectedDaemonInstanceID)
	writeJSON(w, http.StatusAccepted, job)
}

func (h *handler) previewPatternRefV2(w http.ResponseWriter, r *http.Request) {
	var input patternRefPreviewRequest
	if !decodePatternExecutionBody(w, r, &input) {
		return
	}
	if patternidentity.ValidateRef(input.PatternRef) != nil || (input.Serial != nil && !control.ValidDeviceSerial(*input.Serial)) {
		invalidPatternExecution(w)
		return
	}
	resolved, err := h.patternResolverV2()(r.Context(), input.PatternRef)
	if err != nil {
		patternExecutionErrorV2(w, err)
		return
	}
	execution, err := resolved.Execution.CanonicalJSON()
	if err != nil {
		patternExecutionErrorV2(w, &control.PatternResolveError{Kind: control.PatternResolveIntegrity, Err: err})
		return
	}
	var state *control.DeviceState
	if input.Serial != nil && h.service != nil {
		snapshot, err := h.service.Device(*input.Serial) // cached snapshot, never USB
		if err != nil && !errors.Is(err, control.ErrNotFound) {
			patternExecutionErrorV2(w, err)
			return
		}
		if err == nil {
			state = &snapshot
		}
	}
	var preview interface{}
	if resolved.Execution.Program != nil {
		value, err := control.Preview(resolved.Execution.Program, nil)
		if err != nil {
			patternExecutionErrorV2(w, &control.PatternResolveError{Kind: control.PatternResolveIntegrity, Err: err})
			return
		}
		if input.Serial != nil {
			value = control.WithDeviceSnapshot(value, *input.Serial, state)
		}
		preview = value
	} else {
		value, err := control.PreviewFrameProgram(*resolved.Execution.FrameProgram)
		if err != nil {
			patternExecutionErrorV2(w, &control.PatternResolveError{Kind: control.PatternResolveIntegrity, Err: err})
			return
		}
		value.Execution = control.FrameExecutionForDevice(nil)
		if input.Serial != nil {
			if state == nil || state.Serial != *input.Serial {
				value.Execution = control.FrameExecutionAvailability{Reason: "device_not_configured"}
			} else {
				value.Execution = control.FrameExecutionForDevice(state)
				if value.Execution.Available && value.Program.OnFinish == "hold" && !hasPatternCapability(state, "hold") {
					value.Execution = control.FrameExecutionAvailability{Reason: "missing_capabilities"}
				}
			}
		}
		if _, ok := h.service.(control.PatternService); !ok {
			value.Execution = control.FrameExecutionAvailability{Reason: "pattern_service_unsupported"}
		}
		result := frameHTTPPreview{FramePreview: value}
		if input.Serial != nil {
			result.Device = &framePreviewDevice{Serial: *input.Serial, Connection: "missing"}
			if state != nil && state.Serial == *input.Serial {
				result.Device.Connection = state.Connection
			}
		}
		preview = result
	}
	if err := r.Context().Err(); err != nil {
		patternExecutionErrorV2(w, err)
		return
	}
	writeJSON(w, http.StatusOK, patternRefPreviewResponse{1, PatternRefPreviewContract, resolved.Ref, resolved.ExecutionDigest, execution, preview, h.patternExecutionStatusV2()})
}

func hasPatternCapability(state *control.DeviceState, wanted string) bool {
	for _, value := range state.Capabilities {
		if value == wanted {
			return true
		}
	}
	return false
}

// This reports wiring/configuration only, not source verification, admission,
// cached hardware support or physical qualification. Never call the store here.
func (h *handler) patternExecutionStatusV2() patternExecutionV2 {
	if _, ok := h.service.(control.PatternService); !ok {
		return patternExecutionV2{Reason: "pattern_service_unsupported"}
	}
	if h.patternErrV2 != nil {
		return patternExecutionV2{Reason: "pattern_library_configuration_error"}
	}
	if h.patternsV2 == nil {
		return patternExecutionV2{Reason: "pattern_library_unconfigured"}
	}
	return patternExecutionV2{Available: true, Reason: "pattern_service_configured"}
}

func patternExecutionErrorV2(w http.ResponseWriter, err error) {
	var resolved *control.PatternResolveError
	if errors.As(err, &resolved) && resolved != nil {
		status, code := http.StatusServiceUnavailable, "pattern_library_unavailable"
		switch resolved.Kind {
		case control.PatternResolveIntegrity:
			switch {
			case errors.Is(err, patternstore.ErrRevisionConflict):
				status, code = http.StatusConflict, "pattern_revision_conflict"
			case errors.Is(err, patternstore.ErrCorrupt), errors.Is(err, patternstore.ErrUnsafe):
			default:
				status, code = http.StatusInternalServerError, "pattern_integrity_error"
			}
		case control.PatternResolveUnsupported:
			status, code = http.StatusNotImplemented, "pattern_library_unsupported"
			if errors.Is(err, patternsv2.ErrUnsupported) {
				code = "pattern_source_unsupported"
			}
		case control.PatternResolveConfiguration:
			if errors.Is(err, patternresolver.ErrConfiguration) {
				status, code = http.StatusNotImplemented, "pattern_library_unsupported"
			} else if errors.Is(err, patternstore.ErrFormatMismatch) {
				code = "pattern_library_format_mismatch"
			}
		case control.PatternResolveAvailability:
			if errors.Is(err, patternstore.ErrNotFound) {
				status, code = http.StatusNotFound, "pattern_not_found"
			} else if errors.Is(err, patternstore.ErrBusy) {
				status, code = http.StatusConflict, "pattern_library_busy"
			}
		}
		writeError(w, status, code, "the exact verified Pattern cannot currently be resolved")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "request context ended; preserve the original receipt and query its outcome before any new submission")
		return
	}
	serviceError(w, err)
}
