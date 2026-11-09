package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/b1ug/nb1/internal/lightactionv2"
)

type lightV2PreviewResponse struct {
	Result  lightactionv2.Result `json:"result"`
	Preview frameHTTPPreview     `json:"preview"`
}

func (h *handler) serveLightV2(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v2/light/preview" {
		if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) {
			return true
		}
		var input lightPreviewRequest
		if !decodeFrameEnvelope(w, r, &input, []string{"contract", "action", "serial"}, 65536) {
			return true
		}
		result, err := compileLightV2(input.Contract, input.Action)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_light_action", err.Error())
			return true
		}
		preview, err := h.framePreview(result.Program, input.Serial)
		if err != nil {
			serviceError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, lightV2PreviewResponse{Result: result, Preview: preview})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "v2" && parts[1] == "devices" && parts[3] == "light" {
		if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) || !frameValidSerial(w, parts[2]) {
			return true
		}
		var input lightSubmitRequest
		if !decodeFrameEnvelope(w, r, &input, []string{"contract", "action", "policy", "request_id", "expected_daemon_instance_id"}, 65536) {
			return true
		}
		result, err := compileLightV2(input.Contract, input.Action)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_light_action", err.Error())
			return true
		}
		h.admitFrame(w, r, parts[2], result.Program, input.Policy, input.RequestID, input.ExpectedDaemonInstanceID)
		return true
	}
	return false
}

func compileLightV2(contract string, action json.RawMessage) (lightactionv2.Result, error) {
	data, err := json.Marshal(struct {
		Contract string          `json:"contract"`
		Action   json.RawMessage `json:"action"`
	}{contract, action})
	if err != nil {
		return lightactionv2.Result{}, err
	}
	return lightactionv2.CompileRequest(data)
}
