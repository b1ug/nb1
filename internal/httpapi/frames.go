package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/b1ug/nb1/internal/control"
)

type framePreviewRequest struct {
	Program json.RawMessage `json:"program"`
	Serial  *string         `json:"serial,omitempty"`
}

type frameSubmitRequest struct {
	Program                  json.RawMessage `json:"program"`
	Policy                   string          `json:"policy"`
	RequestID                string          `json:"request_id"`
	ExpectedDaemonInstanceID string          `json:"expected_daemon_instance_id"`
}

type framePreviewDevice struct {
	Serial     string `json:"serial"`
	Connection string `json:"connection"`
}

type frameHTTPPreview struct {
	control.FramePreview
	Device *framePreviewDevice `json:"device,omitempty"`
}

// These routes retain the common Host/Origin guard and use the same service.
// A frame payload is never translated into sequential Program steps.
func (h *handler) serveFrames(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/v1/frame-capabilities" {
		if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) {
			return true
		}
		_, supported := h.service.(control.FrameService)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"schema_version": 1, "contract": control.FrameContract, "experimental": true,
			"software_supported": supported, "max_frames": control.MaxFrameProgramFrames,
			"max_executed_frames": control.MaxFrameProgramExecutedFrames,
			"max_target_writes":   control.MaxFrameProgramTargetWrites,
			"min_interval_ms":     control.MinFrameIntervalMS, "max_interval_ms": control.MaxFrameIntervalMS,
			"fade_quantum_ms": control.FrameFadeQuantumMS, "write_spacing_ms": control.FrameWriteSpacingMS,
			"max_nominal_duration_ms": control.MaxFrameNominalDurationMS,
			"policy":                  "reject", "initialization": "once_before_repeated_frames",
			"timing_basis": "host-nominal-excludes-native-io", "required_device_capabilities": []string{"set", "raw-rgb", "mk2-basic"},
		})
		return true
	}
	if r.URL.Path == "/v1/frame-programs/preview" {
		h.previewFrames(w, r)
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "devices" && parts[3] == "frame-jobs" {
		h.submitFrames(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "devices" && parts[3] == "frame-requests" && parts[4] != "" {
		if !method(w, r, http.MethodGet) || !frameNoBody(w, r) || !frameValidSerial(w, parts[2]) {
			return true
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		values := query["expected_daemon_instance_id"]
		if err != nil || len(query) != 1 || len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "exactly one expected_daemon_instance_id is required")
			return true
		}
		service, ok := h.frameService(w)
		if !ok {
			return true
		}
		job, err := service.FrameRequest(parts[2], parts[4], values[0])
		if err != nil {
			serviceError(w, err)
			return true
		}
		w.Header().Set("X-NB1-Daemon-Instance-ID", values[0])
		writeJSON(w, http.StatusOK, job)
		return true
	}
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "frame-jobs" && parts[2] != "" {
		if len(parts) != 3 && !(len(parts) == 4 && parts[3] == "cancel") {
			return false
		}
		want := http.MethodGet
		if len(parts) == 4 {
			want = http.MethodPost
		}
		if !method(w, r, want) || !lightNoQuery(w, r) {
			return true
		}
		if want == http.MethodGet {
			if !frameNoBody(w, r) {
				return true
			}
		} else if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			var empty struct{}
			if !decodeJSON(w, r, &empty) {
				return true
			}
		}
		service, ok := h.frameService(w)
		if !ok {
			return true
		}
		var job control.FrameJob
		var err error
		code := http.StatusOK
		if want == http.MethodPost {
			job, err = service.CancelFrame(r.Context(), parts[2])
			code = http.StatusAccepted
		} else {
			job, err = service.FrameJob(parts[2])
		}
		if err != nil {
			serviceError(w, err)
			return true
		}
		writeJSON(w, code, job)
		return true
	}
	return false
}

func (h *handler) previewFrames(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) {
		return
	}
	var input framePreviewRequest
	if !decodeFrameEnvelope(w, r, &input, []string{"program", "serial"}, control.MaxFrameProgramBytes) {
		return
	}
	program, err := control.DecodeFrameProgram(input.Program)
	if err != nil {
		serviceError(w, err)
		return
	}
	preview, err := h.framePreview(program, input.Serial)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *handler) submitFrames(w http.ResponseWriter, r *http.Request, serial string) {
	if !method(w, r, http.MethodPost) || !lightNoQuery(w, r) || !frameValidSerial(w, serial) {
		return
	}
	var input frameSubmitRequest
	if !decodeFrameEnvelope(w, r, &input, []string{"program", "policy", "request_id", "expected_daemon_instance_id"}, control.MaxFrameProgramBytes) {
		return
	}
	program, err := control.DecodeFrameProgram(input.Program)
	if err != nil {
		serviceError(w, err)
		return
	}
	h.admitFrame(w, r, serial, program, input.Policy, input.RequestID, input.ExpectedDaemonInstanceID)
}

func (h *handler) admitFrame(w http.ResponseWriter, r *http.Request, serial string, program control.FrameProgram, policy, requestID, instance string) {
	if policy != "reject" || requestID == "" || instance == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "frame submissions require explicit reject policy and a complete original receipt binding")
		return
	}
	service, ok := h.frameService(w)
	if !ok {
		return
	}
	result, err := service.SubmitFrameWithReceipt(r.Context(), serial, program, policy, control.ReceiptRequest{RequestID: requestID, ExpectedDaemonInstanceID: instance})
	if err != nil {
		serviceError(w, err)
		return
	}
	w.Header().Set("X-NB1-Replayed", strconv.FormatBool(result.Replayed))
	w.Header().Set("X-NB1-Daemon-Instance-ID", result.DaemonInstanceID)
	w.Header().Set("Location", frameReceiptLocation(serial, requestID, instance))
	writeJSON(w, http.StatusAccepted, result.Job)
}

func (h *handler) framePreview(program control.FrameProgram, serial *string) (frameHTTPPreview, error) {
	preview, err := control.PreviewFrameProgram(program)
	if err != nil {
		return frameHTTPPreview{}, err
	}
	result := frameHTTPPreview{FramePreview: preview}
	_, supported := h.service.(control.FrameService)
	if !supported {
		result.Execution = control.FrameExecutionAvailability{Reason: "frame_service_unsupported"}
	} else {
		result.Execution = control.FrameExecutionForDevice(nil)
	}
	if serial == nil {
		return result, nil
	}
	if !control.ValidDeviceSerial(*serial) {
		return frameHTTPPreview{}, control.ErrInvalid
	}
	result.Device = &framePreviewDevice{Serial: *serial, Connection: "missing"}
	if h.service == nil {
		return result, nil
	}
	state, err := h.service.Device(*serial) // cached snapshot only, no USB I/O
	if err != nil {
		if errors.Is(err, control.ErrNotFound) {
			result.Execution = control.FrameExecutionAvailability{Reason: "device_not_configured"}
			return result, nil
		}
		return frameHTTPPreview{}, err
	}
	result.Device.Connection = state.Connection
	if supported {
		result.Execution = control.FrameExecutionForDevice(&state)
	}
	return result, nil
}

func (h *handler) frameService(w http.ResponseWriter) (control.FrameService, bool) {
	service, ok := h.service.(control.FrameService)
	if !ok {
		writeError(w, http.StatusNotImplemented, "frame_unsupported", "this service does not execute frame programs; do not fall back to sequential steps")
	}
	return service, ok
}

func frameReceiptLocation(serial, requestID, instance string) string {
	return "/v1/devices/" + url.PathEscape(serial) + "/frame-requests/" + url.PathEscape(requestID) + "?expected_daemon_instance_id=" + url.QueryEscape(instance)
}

func frameValidSerial(w http.ResponseWriter, serial string) bool {
	if control.ValidDeviceSerial(serial) {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "an explicit valid device serial is required")
	return false
}

func frameNoBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 && len(r.TransferEncoding) == 0 {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "this route accepts no body")
	return false
}

func frameNoBodyOrQuery(w http.ResponseWriter, r *http.Request) bool {
	return lightNoQuery(w, r) && frameNoBody(w, r)
}

// RawMessage preserves nested program/action input for its own strict decoder.
func decodeFrameEnvelope(w http.ResponseWriter, r *http.Request, out interface{}, allowed []string, limit int) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		return jsonError(w, err)
	}
	if len(data) > limit {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "frame request exceeds its byte limit")
		return false
	}
	if err := validateLightEnvelope(data, allowed); err != nil {
		return jsonError(w, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return jsonError(w, err)
	}
	return true
}
