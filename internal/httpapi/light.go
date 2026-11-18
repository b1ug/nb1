package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
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
	if !method(w, r, http.MethodGet) || !demoPresentationQuery(w, r) {
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "demo resources accept no request body")
		return
	}
	if r.URL.Path == "/demo" {
		target := "/demo/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
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
	case "/demo/patterns.html":
		file, contentType = "patterns.html", "text/html; charset=utf-8"
	case "/demo/patterns.js":
		file, contentType = "patterns.js", "text/javascript; charset=utf-8"
	case "/demo/patterns.css":
		file, contentType = "patterns.css", "text/css; charset=utf-8"
	case "/demo/i18n.js":
		file, contentType = "i18n.js", "text/javascript; charset=utf-8"
	case "/demo/locales.js":
		file, contentType = "locales.js", "text/javascript; charset=utf-8"
	case "/demo/action-icons.js":
		file, contentType = "action-icons.js", "text/javascript; charset=utf-8"
	case "/demo/locale-menu.css":
		file, contentType = "locale-menu.css", "text/css; charset=utf-8"
	case "/demo/typography.css":
		file, contentType = "typography.css", "text/css; charset=utf-8"
	case "/demo/json-editor.js":
		file, contentType = "json-editor.js", "text/javascript; charset=utf-8"
	case "/demo/json-editor.css":
		file, contentType = "json-editor.css", "text/css; charset=utf-8"
	case "/demo/editor-LICENSES.txt":
		file, contentType = "editor-LICENSES.txt", "text/plain; charset=utf-8"
	case "/demo/assets/nb1-icon.png":
		file, contentType = "assets/nb1-icon.png", "image/png"
	case "/demo/assets/nb1-mascot.webp":
		file, contentType = "assets/nb1-mascot.webp", "image/webp"
	case "/demo/fonts/fredoka-700-latin.woff2":
		file, contentType = "fonts/fredoka-700-latin.woff2", "font/woff2"
	case "/demo/fonts/fredoka-700-latin-ext.woff2":
		file, contentType = "fonts/fredoka-700-latin-ext.woff2", "font/woff2"
	case "/demo/fonts/mplusround-500-latin.woff2":
		file, contentType = "fonts/mplusround-500-latin.woff2", "font/woff2"
	case "/demo/fonts/mplusround-700-latin.woff2":
		file, contentType = "fonts/mplusround-700-latin.woff2", "font/woff2"
	case "/demo/fonts/Fredoka-OFL.txt":
		file, contentType = "fonts/Fredoka-OFL.txt", "text/plain; charset=utf-8"
	case "/demo/fonts/MPLUSRounded1c-OFL.txt":
		file, contentType = "fonts/MPLUSRounded1c-OFL.txt", "text/plain; charset=utf-8"
	default:
		var allowed bool
		file = strings.TrimPrefix(r.URL.Path, "/demo/")
		contentType, allowed = nightlabDemoAssetTypes[file]
		if !allowed || !webui.IsNightlab() {
			writeError(w, http.StatusNotFound, "not_found", "unknown demo resource")
			return
		}
	}
	data, err := webui.ReadFile(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "embedded demo resource unavailable")
		return
	}
	writeDemoAsset(w, contentType, data, rand.Reader, webui.IsNightlab())
}

// This route map is intentionally independent of names supplied by a manifest.
// Every logical path and MIME is reviewed alongside the closed embedded profile.
var nightlabDemoAssetTypes = map[string]string{
	"nightlab/airwave-tokens.css": "text/css; charset=utf-8", "nightlab/airwave.css": "text/css; charset=utf-8",
	"nightlab/studio.css": "text/css; charset=utf-8", "nightlab/embedded-studio.css": "text/css; charset=utf-8", "nightlab/studio-skins.css": "text/css; charset=utf-8",
	"nightlab/studio.js": "text/javascript; charset=utf-8", "nightlab/composer-ui.js": "text/javascript; charset=utf-8", "nightlab/studio-appearance.js": "text/javascript; charset=utf-8",
	"nightlab/studio-preview.js": "text/javascript; charset=utf-8", "nightlab/preview-timeline.js": "text/javascript; charset=utf-8", "nightlab/compiler-loader.js": "text/javascript; charset=utf-8",
	"nightlab/readback.js": "text/javascript; charset=utf-8", "nightlab/time-format.js": "text/javascript; charset=utf-8",
	"nightlab/studio-3d.js": "text/javascript; charset=utf-8", "nightlab/THREE-LICENSE.txt": "text/plain; charset=utf-8",
	"nightlab/light-model.js": "text/javascript; charset=utf-8", "nightlab/enclosure-fit.js": "text/javascript; charset=utf-8",
	"nightlab/compiler/nb1.wasm": "application/wasm", "nightlab/compiler/wasm_exec.js": "text/javascript; charset=utf-8",
	"nightlab/compiler/compiler-source.json": "application/json", "nightlab/compiler/GO-LICENSE.txt": "text/plain; charset=utf-8", "nightlab/compiler/NB1-LICENSE.txt": "text/plain; charset=utf-8",
	"nightlab/compiler/COLORKIT-LICENSE.txt":                     "text/plain; charset=utf-8",
	"nightlab/model-assets/blink1mk2_enclosure_diffuser_v3k.stl": "model/stl",
	"nightlab/model-assets/blink1mk2_enclosure_frame_v3k.stl":    "model/stl",
	"nightlab/model-assets/source.json":                          "application/json", "nightlab/model-assets/seating-fit.json": "application/json",
	"nightlab/model-assets/LICENSE.txt": "text/plain; charset=utf-8",
	"nightlab/model.html":               "text/html; charset=utf-8", "nightlab/home.css": "text/css; charset=utf-8",
	"nightlab/model-notes.js": "text/javascript; charset=utf-8",
}

// Presentation queries belong only to the workbench documents. Assets and
// execution APIs retain their separate no-query contracts.
func demoPresentationQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery == "" && !r.URL.ForceQuery {
		return true
	}
	switch r.URL.Path {
	case "/demo", "/demo/", "/demo/orbit.html", "/demo/patterns.html":
	default:
		return lightNoQuery(w, r)
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	valid := err == nil && len(query) > 0
	for key, values := range query {
		if len(values) != 1 {
			valid = false
			continue
		}
		switch key {
		case "skin":
			valid = valid && (values[0] == "nightlab" || values[0] == "airwave" || values[0] == "instrument")
		case "view":
			valid = valid && values[0] == "faces"
		default:
			valid = false
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request", "unknown or ambiguous workbench presentation query")
	}
	return valid
}
