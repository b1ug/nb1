package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

// Bound HTTP waiting independently of a caller-supplied deadline. The engine
// retains its worker/handle until synchronous native I/O actually returns.
const nativeInspectionWaitTimeout = 5 * time.Second

// Native inspection is deliberately optional. Older services keep their
// existing interface and never fall back to opening another device handle.
type nativeInspectionService interface {
	InspectNative(context.Context, string, nativeinspection.Request) (nativeinspection.Result, error)
}

// serveNativeInspection runs after the common loopback and Origin guards.
// These experimental GETs collect observations through the owning worker;
// they neither submit a Job nor import, save or play a host Pattern.
func (h *handler) serveNativeInspection(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "devices" || parts[3] != "native-sequence" {
		return false
	}
	if len(parts) != 5 || (parts[4] != nativeinspection.RAM && parts[4] != nativeinspection.Playback) {
		writeError(w, http.StatusNotFound, "not_found", "unknown native inspection resource")
		return true
	}
	if !method(w, r, http.MethodGet) || !frameNoBody(w, r) {
		return true
	}
	if !nativeInspectionSerial(parts[2]) {
		writeError(w, http.StatusBadRequest, "invalid_request", "an explicit 8-digit hexadecimal device serial is required")
		return true
	}
	request, ok := nativeInspectionRequest(w, r, parts[4])
	if !ok {
		return true
	}
	service, ok := h.service.(nativeInspectionService)
	if !ok {
		writeError(w, http.StatusNotImplemented, "native_inspection_unsupported", "this service does not support native inspection; no direct-device fallback is performed")
		return true
	}
	// This is a cached lookup only. It rejects unknown configured devices before
	// collection; the worker must still perform authoritative admission afterward.
	if _, err := h.service.Device(parts[2]); err != nil {
		serviceError(w, err)
		return true
	}
	waitContext, stopWaiting := context.WithTimeout(r.Context(), nativeInspectionWaitTimeout)
	defer stopWaiting()
	result, err := service.InspectNative(waitContext, parts[2], request.Clone())
	if result.Contract == "" && result.StartedAt.IsZero() && err != nil {
		nativeInspectionError(w, err)
		return true
	}
	// A collection can fail before its first ReadFeature (for example while
	// sending the request). ReadAttempted is not an admission discriminator.
	if result.Contract != nativeinspection.Contract || result.StartedAt.IsZero() ||
		nativeinspection.ValidateResult(result, parts[2], request) != nil ||
		(err != nil && (result.Complete || result.Error == nil)) {
		writeError(w, http.StatusInternalServerError, "invalid_native_inspection_result", "native inspection returned an invalid observation envelope")
		return true
	}
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil || len(encoded)+1 > nativeinspection.MaxResponseBytes {
		writeError(w, http.StatusInternalServerError, "invalid_native_inspection_result", "native inspection returned an invalid observation envelope")
		return true
	}
	w.Header().Set("X-NB1-Daemon-Instance-ID", result.DaemonInstanceID)
	// Status/Complete/Error and every partial raw observation stay in the
	// envelope. HTTP200 means a collection result, not a successful full read.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(encoded, '\n'))
	return true
}

func nativeInspectionSerial(serial string) bool {
	if len(serial) != 8 {
		return false
	}
	_, err := hex.DecodeString(serial)
	return err == nil
}

func nativeInspectionRequest(w http.ResponseWriter, r *http.Request, kind string) (nativeinspection.Request, bool) {
	request := nativeinspection.Request{Kind: kind}
	if kind == nativeinspection.Playback {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, http.StatusBadRequest, "invalid_request", "playback inspection accepts no query parameters")
			return nativeinspection.Request{}, false
		}
		return request, true
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(strings.Split(r.URL.RawQuery, "&")) != 2 || len(values) != 2 || len(values["start_line"]) != 1 || len(values["line_count"]) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "exactly one explicit start_line and line_count are required")
		return nativeinspection.Request{}, false
	}
	start, startOK := nativeDecimal(values["start_line"][0])
	count, countOK := nativeDecimal(values["line_count"][0])
	request.Range = &devicesequence.Range{StartLine: start, LineCount: count}
	if !startOK || !countOK || request.Validate() != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "start_line must be decimal 0..31 and line_count decimal 1..32 within the 32-line RAM range")
		return nativeinspection.Request{}, false
	}
	return request, true
}

func nativeDecimal(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 31)
	return int(n), err == nil
}

func nativeInspectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, nativeinspection.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, nativeinspection.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "native_inspection_unsupported", err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "native inspection wait ended; an in-flight native operation may still be running")
	default:
		serviceError(w, err)
	}
}
