package httpapi

import (
	"io"
	"mime"
	"net/http"

	"github.com/b1ug/nb1/internal/colorinput"
)

// resolveColor is a bounded pure computation. In particular it must not call
// any service method, even for discovery, instance preparation or cached state.
func (h *handler) resolveColor(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, "invalid_request", "color resolution accepts no query parameters")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, colorinput.MaxRequestBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_color", "cannot read color request")
		return
	}
	if len(body) > colorinput.MaxRequestBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "color request exceeds 4096 bytes")
		return
	}
	resolved, err := colorinput.ResolveRequestJSON(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_color", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resolved)
}
