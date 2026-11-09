package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/webui"
)

type lightPreviewRequest struct {
	Contract string          `json:"contract"`
	Action   json.RawMessage `json:"action"`
	Serial   *string         `json:"serial,omitempty"`
}

type lightSubmitRequest struct {
	Contract                 string          `json:"contract"`
	Action                   json.RawMessage `json:"action"`
	Policy                   string          `json:"policy"`
	RequestID                string          `json:"request_id"`
	ExpectedDaemonInstanceID string          `json:"expected_daemon_instance_id"`
}

type lightPreviewResponse struct {
	Result  lightaction.Result    `json:"result"`
	Preview control.PreviewResult `json:"preview"`
}

// serveLight is called only after the common Host/Origin guard. It adds a
// versioned authoring surface without changing the legacy Program/Effect API.
func (h *handler) serveLight(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v1/light/preview" {
		h.previewLight(w, r)
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "devices" && parts[3] == "light" {
		h.submitLight(w, r, parts[2])
		return true
	}
	if r.URL.Path == "/demo" || strings.HasPrefix(r.URL.Path, "/demo/") {
		h.serveLightDemo(w, r)
		return true
	}
	return false
}

func (h *handler) previewLight(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) {
		return
	}
	var input lightPreviewRequest
	if !decodeLightJSON(w, r, &input) {
		return
	}
	if input.Serial != nil && !control.ValidDeviceSerial(*input.Serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "serial must be an explicit valid device serial")
		return
	}
	result, err := compileLight(input.Contract, input.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_light_action", err.Error())
		return
	}
	preview, err := control.Preview(&result.Program, nil)
	if err != nil {
		serviceError(w, err)
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
	writeJSON(w, http.StatusOK, lightPreviewResponse{Result: result, Preview: preview})
}

func (h *handler) submitLight(w http.ResponseWriter, r *http.Request, serial string) {
	if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) {
		return
	}
	if !control.ValidDeviceSerial(serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "an explicit valid device serial is required")
		return
	}
	var input lightSubmitRequest
	if !decodeLightJSON(w, r, &input) {
		return
	}
	if input.Policy != "reject" || input.RequestID == "" || input.ExpectedDaemonInstanceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "light submissions require policy reject and a complete request/daemon-instance receipt binding")
		return
	}
	result, err := compileLight(input.Contract, input.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_light_action", err.Error())
		return
	}
	service, ok := h.receiptService(w)
	if !ok {
		return
	}
	receipt, err := service.SubmitWithReceipt(r.Context(), serial, result.Program, input.Policy, control.ReceiptRequest{RequestID: input.RequestID, ExpectedDaemonInstanceID: input.ExpectedDaemonInstanceID})
	if err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("X-NB1-Replayed", strconv.FormatBool(receipt.Replayed))
	w.Header().Set("X-NB1-Daemon-Instance-ID", receipt.DaemonInstanceID)
	w.Header().Set("Location", receiptLocation(serial, input.RequestID, input.ExpectedDaemonInstanceID))
	writeJSON(w, http.StatusAccepted, receipt.Job)
}

func compileLight(contract string, action json.RawMessage) (lightaction.Result, error) {
	// RawMessage preserves numeric spelling, duplicate keys and color forms for
	// the one authoring compiler. HTTP never reimplements effect compilation.
	data, err := json.Marshal(struct {
		Contract string          `json:"contract"`
		Action   json.RawMessage `json:"action"`
	}{contract, action})
	if err != nil {
		return lightaction.Result{}, err
	}
	return lightaction.CompileRequest(data)
}

func lightNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, "invalid_request", "light routes accept no query parameters")
		return false
	}
	return true
}

func decodeLightJSON(w http.ResponseWriter, r *http.Request, input interface{}) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, lightaction.MaxRequestBytes+1))
	if err != nil {
		return jsonError(w, err)
	}
	if len(data) > lightaction.MaxRequestBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "light request exceeds 65536 bytes")
		return false
	}
	allowed := []string{"contract", "action", "serial"}
	if _, ok := input.(*lightSubmitRequest); ok {
		allowed = []string{"contract", "action", "policy", "request_id", "expected_daemon_instance_id"}
	}
	if err := validateLightEnvelope(data, allowed); err != nil {
		return jsonError(w, err)
	}
	if err := json.Unmarshal(data, input); err != nil {
		return jsonError(w, err)
	}
	return true
}

// Preserve the action's original JSON bytes for the one strict core decoder.
// The legacy jsoninput reflection validator deliberately does not treat a
// RawMessage slice as an arbitrary JSON object, so it remains unchanged.
func validateLightEnvelope(data []byte, allowed []string) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("request must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("one JSON object is required")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate or invalid request field")
		}
		seen[key] = true
		known := false
		for _, name := range allowed {
			known = known || name == key
		}
		if !known {
			return fmt.Errorf("unknown request field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("explicit null is not accepted")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return fmt.Errorf("invalid JSON object end")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON is not accepted")
	}
	return nil
}

func (h *handler) serveLightDemo(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !lightNoQuery(w, r) {
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "demo resources accept no request body")
		return
	}
	if r.URL.Path == "/demo" {
		http.Redirect(w, r, "/demo/", http.StatusTemporaryRedirect)
		return
	}
	var file, contentType string
	switch r.URL.Path {
	case "/demo/":
		file, contentType = "index.html", "text/html; charset=utf-8"
	case "/demo/demo.js":
		file, contentType = "demo.js", "text/javascript; charset=utf-8"
	case "/demo/demo.css":
		file, contentType = "demo.css", "text/css; charset=utf-8"
	case "/demo/orbit.html":
		file, contentType = "orbit.html", "text/html; charset=utf-8"
	case "/demo/orbit.js":
		file, contentType = "orbit.js", "text/javascript; charset=utf-8"
	case "/demo/orbit.css":
		file, contentType = "orbit.css", "text/css; charset=utf-8"
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown demo resource")
		return
	}
	data, err := webui.ReadFile(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "embedded demo resource unavailable")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
