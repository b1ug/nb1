package httpapi

import (
	"errors"
	"net/http"

	"github.com/b1ug/nb1/internal/control"
)

func (h *handler) capabilities(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "capabilities accepts no query parameters or body")
		return
	}
	writeJSON(w, http.StatusOK, control.Capabilities())
}

func (h *handler) preview(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "preview accepts no query parameters")
		return
	}
	var input PreviewRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Serial != nil && !control.ValidDeviceSerial(*input.Serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "serial must be 1-128 ASCII letters, digits, hyphens or underscores")
		return
	}
	result, err := control.Preview(input.Program, input.Effect)
	if err != nil {
		serviceError(w, err)
		return
	}
	if input.Serial != nil {
		if h.service == nil {
			serviceError(w, control.ErrClosed)
			return
		}
		// Device returns the configured worker's copied in-memory snapshot. Never
		// discover, open or sample a device in response to a preview request.
		snapshot, err := h.service.Device(*input.Serial)
		if err != nil && !errors.Is(err, control.ErrNotFound) {
			serviceError(w, err)
			return
		}
		if err != nil {
			result = control.WithDeviceSnapshot(result, *input.Serial, nil)
		} else {
			result = control.WithDeviceSnapshot(result, *input.Serial, &snapshot)
		}
	}
	writeJSON(w, http.StatusOK, result)
}
