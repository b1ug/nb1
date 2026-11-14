package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/libraryv1"
	"github.com/b1ug/nb1/internal/sequence"
)

// WithSequenceAPI selects the new runtime's route table.
func WithSequenceAPI() Option {
	return func(h *handler) { h.sequenceAPI = true }
}

type sequenceInput struct {
	Action    json.RawMessage `json:"action,omitempty"`
	Sequence  json.RawMessage `json:"sequence,omitempty"`
	PatternID *string         `json:"pattern_id,omitempty"`
	Serial    *string         `json:"serial,omitempty"`
}

type deviceCheck struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type sequencePreview struct {
	Sequence             sequence.Sequence `json:"sequence"`
	TotalSteps           int               `json:"total_steps"`
	NominalDurationMS    int64             `json:"nominal_duration_ms"`
	RequiredCapabilities []string          `json:"required_capabilities"`
	DeviceCheck          deviceCheck       `json:"device_check"`
}

type sequenceDeviceState struct {
	Serial       string               `json:"serial"`
	Product      string               `json:"product"`
	Firmware     int                  `json:"firmware,omitempty"`
	Connection   string               `json:"connection"`
	Capabilities []string             `json:"capabilities"`
	ActiveJob    string               `json:"active_job,omitempty"`
	QueueDepth   int                  `json:"queue_depth"`
	Observed     *control.Observation `json:"observed"`
}

func publicSequenceCapabilities(capabilities []string) []string {
	result := make([]string, 0, len(capabilities))
	for _, name := range capabilities {
		if name == "program" {
			name = "sequence"
		}
		result = append(result, name)
	}
	return result
}

func publicSequenceDevice(state control.DeviceState) sequenceDeviceState {
	return sequenceDeviceState{
		Serial: state.Serial, Product: state.Product, Firmware: state.Firmware, Connection: state.Connection,
		Capabilities: publicSequenceCapabilities(state.Capabilities), ActiveJob: state.ActiveJob,
		QueueDepth: state.QueueDepth, Observed: state.Observed,
	}
}

func (h *handler) serveSequenceAPI(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/demo" || strings.HasPrefix(r.URL.Path, "/demo/") {
		h.serveLightDemo(w, r)
		return true
	}
	switch r.URL.Path {
	case "/healthz", "/v1/transport-metrics":
		return false // Shared runtime read routes.
	case "/v1/devices":
		if method(w, r, http.MethodGet) && frameNoBodyOrQuery(w, r) {
			result := make([]sequenceDeviceState, 0)
			for _, state := range h.service.Devices() {
				result = append(result, publicSequenceDevice(state))
			}
			writeJSON(w, http.StatusOK, result)
		}
		return true
	case "/v1/preview":
		h.previewSequence(w, r)
		return true
	case "/v1/colors/resolve":
		if r.Method == http.MethodPost {
			h.resolveSequenceColor(w, r)
			return true
		}
		if r.Method != http.MethodPut {
			w.Header().Set("Allow", "POST, PUT")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return true
		}
	case "/v1/jobs":
		h.submitSequence(w, r)
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if h.serveSequenceLibrary(w, r, parts) {
		return true
	}
	if len(parts) == 3 && parts[0] == "v1" && parts[1] == "devices" && parts[2] != "" {
		if method(w, r, http.MethodGet) && frameNoBodyOrQuery(w, r) {
			state, err := h.service.Device(parts[2])
			if err != nil {
				sequenceAPIError(w, err)
			} else {
				writeJSON(w, http.StatusOK, publicSequenceDevice(state))
			}
		}
		return true
	}
	if (len(parts) == 3 || len(parts) == 4 && parts[3] == "cancel") && parts[0] == "v1" && parts[1] == "jobs" {
		h.readSequenceJob(w, r, parts[2], len(parts) == 4)
		return true
	}
	writeError(w, http.StatusNotFound, "not_found", "unknown API resource")
	return true
}

func readSequenceJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if !sequenceNoQuery(w, r) {
		return false
	}
	body, ok := readJSONBody(w, r)
	if !ok {
		return false
	}
	if !jsoninput.ValidUnicode(body) {
		writeError(w, http.StatusBadRequest, "invalid_request", "JSON must contain valid Unicode")
		return false
	}
	if err := jsoninput.DecodeObject(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false
	}
	return true
}

func sequenceNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery == "" && !r.URL.ForceQuery {
		return true
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "unexpected query parameters")
	return false
}

func (in sequenceInput) validate(w http.ResponseWriter, requireSerial bool) bool {
	count := 0
	if in.Action != nil {
		count++
	}
	if in.Sequence != nil {
		count++
	}
	if in.PatternID != nil {
		count++
	}
	if count != 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "exactly one action, sequence or pattern_id is required")
		return false
	}
	if in.PatternID != nil && !control.ValidSequenceJobID(*in.PatternID) {
		writeError(w, http.StatusBadRequest, "invalid_request", "pattern_id must contain 1–64 ASCII letters, digits, hyphens or underscores")
		return false
	}
	if requireSerial && in.Serial == nil || in.Serial != nil && !control.ValidDeviceSerial(*in.Serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "a valid device serial is required")
		return false
	}
	return true
}

func (h *handler) compileSequenceInput(in sequenceInput) (sequence.Sequence, error) {
	if in.Action != nil {
		resolver := sequenceColorResolver{store: h.library}
		compiled, err := actionv1.Compile(in.Action, resolver.resolve)
		if resolver.unavailable {
			return sequence.Sequence{}, errSequenceLibraryUnavailable
		}
		return compiled, err
	}
	if in.PatternID != nil {
		if h.library == nil {
			return sequence.Sequence{}, errSequenceLibraryUnavailable
		}
		pattern, err := h.library.GetPattern(*in.PatternID)
		if err != nil {
			return sequence.Sequence{}, sequenceLibraryError(err)
		}
		return sequence.Normalize(pattern.Sequence)
	}
	resolver := sequenceColorResolver{store: h.library}
	compiled, err := sequence.DecodeAuthoring(in.Sequence, func(raw json.RawMessage) (string, error) {
		return actionv1.ResolveColor(raw, resolver.resolve)
	})
	if resolver.unavailable {
		return sequence.Sequence{}, errSequenceLibraryUnavailable
	}
	return compiled, err
}

func (in sequenceInput) fingerprint() ([32]byte, error) {
	kind, raw := "action", in.Action
	if raw == nil {
		kind, raw = "sequence", in.Sequence
	}
	if in.PatternID != nil {
		kind = "pattern_id"
		raw, _ = json.Marshal(*in.PatternID)
	}
	// JSON input has already been checked for duplicate keys and null. Preserve
	// number spelling, array order and strings while normalizing object order.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return [32]byte{}, err
	}
	canonical, err := json.Marshal(map[string]interface{}{kind: value})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func (h *handler) previewSequence(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var input sequenceInput
	if !readSequenceJSON(w, r, &input) || !input.validate(w, false) {
		return
	}
	compiled, err := h.compileSequenceInput(input)
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	check := deviceCheck{Status: "not_requested"}
	if input.Serial != nil {
		state, err := h.service.Device(*input.Serial)
		if err != nil {
			sequenceAPIError(w, err)
			return
		}
		check = sequenceDeviceCheck(compiled, state)
	}
	writeJSON(w, http.StatusOK, sequencePreview{
		Sequence: compiled, TotalSteps: len(compiled.Steps) * compiled.Repeat,
		NominalDurationMS: compiled.NominalDurationMS(), RequiredCapabilities: publicSequenceCapabilities(control.SequenceRequiredCapabilities(compiled)),
		DeviceCheck: check,
	})
}

func sequenceDeviceCheck(compiled sequence.Sequence, state control.DeviceState) deviceCheck {
	if state.Connection != "online" || len(state.Capabilities) == 0 {
		return deviceCheck{Status: "unknown", Reason: "device capabilities unavailable"}
	}
	if err := control.CheckSequenceDevice(compiled, state); err != nil {
		return deviceCheck{Status: "unsupported", Reason: "required device capabilities missing"}
	}
	return deviceCheck{Status: "supported"}
}

func (h *handler) submitSequence(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	// Decode with an explicit wire type so strict validation sees every field.
	var body struct {
		Action    json.RawMessage `json:"action,omitempty"`
		Sequence  json.RawMessage `json:"sequence,omitempty"`
		PatternID *string         `json:"pattern_id,omitempty"`
		Serial    *string         `json:"serial,omitempty"`
		ID        string          `json:"id,omitempty"`
	}
	if !readSequenceJSON(w, r, &body) {
		return
	}
	input := sequenceInput{Action: body.Action, Sequence: body.Sequence, PatternID: body.PatternID, Serial: body.Serial}
	if !input.validate(w, true) {
		return
	}
	if body.ID != "" && !control.ValidSequenceJobID(body.ID) {
		writeError(w, http.StatusBadRequest, "invalid_request", "id must contain 1–64 ASCII letters, digits, hyphens or underscores")
		return
	}
	service, ok := h.sequenceService(w)
	if !ok {
		return
	}
	fingerprint, err := input.fingerprint()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid action, sequence or pattern_id")
		return
	}
	job, replayed, err := service.SubmitSequence(r.Context(), *input.Serial, body.ID, fingerprint, func() (sequence.Sequence, error) {
		return h.compileSequenceInput(input)
	})
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+job.ID)
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, job)
}

func (h *handler) readSequenceJob(w http.ResponseWriter, r *http.Request, id string, cancel bool) {
	wanted := http.MethodGet
	if cancel {
		wanted = http.MethodPost
	}
	if !method(w, r, wanted) || !sequenceNoQuery(w, r) {
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "job lookup and cancellation accept no body")
		return
	}
	service, ok := h.sequenceService(w)
	if !ok {
		return
	}
	var job control.SequenceJob
	var err error
	if cancel {
		job, err = service.CancelSequence(r.Context(), id)
	} else {
		job, err = service.SequenceJob(id)
	}
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	status := http.StatusOK
	if cancel && job.FinishedAt == nil {
		status = http.StatusAccepted
	}
	writeJSON(w, status, job)
}

func (h *handler) sequenceService(w http.ResponseWriter) (control.SequenceService, bool) {
	service, ok := h.service.(control.SequenceService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "sequence execution is unavailable")
	}
	return service, ok
}

func (h *handler) resolveSequenceColor(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var input struct {
		Color json.RawMessage `json:"color"`
	}
	if !readSequenceJSON(w, r, &input) {
		return
	}
	result, err := h.resolveSequenceColorInput(input.Color)
	if err != nil {
		if errors.Is(err, errSequenceLibraryUnavailable) {
			sequenceAPIError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"color": result})
}

func sequenceAPIError(w http.ResponseWriter, err error) {
	var fieldError *actionv1.FieldError
	if errors.As(err, &fieldError) {
		field := "action"
		if fieldError.Field != "" {
			field += "." + fieldError.Field
		}
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": map[string]string{
			"code": "invalid_request", "message": fieldError.Message, "field": field,
		}})
		return
	}
	switch {
	case errors.Is(err, errSequenceLibraryUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "local library is unavailable")
	case errors.Is(err, libraryv1.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, libraryv1.ErrCapacity):
		writeError(w, http.StatusTooManyRequests, "library_capacity", err.Error())
	case errors.Is(err, libraryv1.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "request ended before the operation could complete")
	case errors.Is(err, control.ErrSequenceJobConflict):
		writeError(w, http.StatusConflict, "job_conflict", err.Error())
	case errors.Is(err, control.ErrSequenceCapability):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_capability", "required device capabilities missing")
	case errors.Is(err, control.ErrBusy), errors.Is(err, control.ErrQueueFull):
		writeError(w, http.StatusConflict, "device_busy", err.Error())
	case errors.Is(err, sequence.ErrInvalid), errors.Is(err, control.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		serviceError(w, err)
	}
}
