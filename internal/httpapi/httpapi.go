// Package httpapi exposes the local device service without opening USB handles.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/libraryv1"
)

const MaxBodyBytes = control.MaxRequestBytes

type SubmitRequest struct {
	Program                  *control.Program `json:"program,omitempty"`
	Effect                   *control.Effect  `json:"effect,omitempty"`
	PatternRef               *PatternRef      `json:"pattern_ref,omitempty"`
	Policy                   string           `json:"policy"`
	RequestID                *string          `json:"request_id,omitempty"`
	ExpectedDaemonInstanceID *string          `json:"expected_daemon_instance_id,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type handler struct {
	service      control.Service
	sequenceAPI  bool
	routing      *sequenceRouting
	library      *libraryv1.Store
	patterns     PatternStore
	patternPath  string
	patternErr   error
	patternsV2   PatternStoreV2
	patternErrV2 error
	localAPI     http.Handler
}

func New(service control.Service, options ...Option) http.Handler {
	return newHandler(service, options...)
}

// NewWithRemote shares one API instance (including its library lock) between
// the loopback listener and a distinct listener with its own access checks.
// protect authenticates and restricts the network request before API dispatch.
func NewWithRemote(service control.Service, protect func(http.Handler) http.Handler, options ...Option) (http.Handler, http.Handler) {
	h := newHandler(service, options...)
	return h, protect(http.HandlerFunc(h.serveRoutes))
}

// WithLocalProtection runs protection after the existing loopback Host and
// Origin checks. Requests from the separate HTTPS listener use its own gate.
func WithLocalProtection(protect func(http.Handler) http.Handler) Option {
	return func(h *handler) { h.localAPI = protect(http.HandlerFunc(h.serveLocalRoutes)) }
}

func newHandler(service control.Service, options ...Option) *handler {
	h := &handler{service: service}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setResponseHeaders(w)
	path := r.URL.EscapedPath()
	browserStatus := path == "/v1/presence" || path == "/v1/service-status"
	if !localHost(r.Host) || (!browserStatus && !sameOrigin(r)) {
		writeError(w, http.StatusForbidden, "local_only", "use a loopback host and a same-origin or non-browser client")
		return
	}
	// The WebHID presence marker is constant. Device totals follow the local
	// API's optional token policy. Both endpoints enforce their browser origin policy.
	if h.localAPI != nil && path != "/v1/presence" {
		h.localAPI.ServeHTTP(w, r)
		return
	}
	h.serveLocalRoutes(w, r)
}

func (h *handler) serveLocalRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.URL.EscapedPath() {
	case "/v1/presence", "/v1/service-status":
		h.serveBrowserStatus(w, r)
	default:
		h.serveRoutes(w, r)
	}
}

func setResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *handler) serveRoutes(w http.ResponseWriter, r *http.Request) {
	setResponseHeaders(w)
	if h.sequenceAPI && r.URL.Path == "/" {
		h.serveWelcome(w, r)
		return
	}
	if h.sequenceAPI && h.serveSequenceAPI(w, r) {
		return
	}
	if h.serveNativeInspection(w, r) || h.serveFrames(w, r) || h.serveLightV2(w, r) || h.serveLight(w, r) {
		return
	}
	if h.servePatternExecutionV2(w, r) || h.servePatternsV2(w, r) || h.servePatterns(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path == "/healthz" {
		if !method(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "api_version": "v1"})
		return
	}
	if r.URL.Path == "/v1/capabilities" {
		h.capabilities(w, r)
		return
	}
	if r.URL.Path == "/v1/programs/preview" {
		h.preview(w, r)
		return
	}
	if r.URL.Path == "/v1/colors/resolve" {
		h.resolveColor(w, r)
		return
	}
	if r.URL.Path == "/v1/instance" {
		if !method(w, r, http.MethodGet) {
			return
		}
		service, ok := h.receiptService(w)
		if !ok {
			return
		}
		instance := service.Instance()
		w.Header().Set("X-NB1-Daemon-Instance-ID", instance.DaemonInstanceID)
		writeJSON(w, http.StatusOK, instance)
		return
	}
	if r.URL.Path == "/v1/transport-metrics" {
		if !method(w, r, http.MethodGet) {
			return
		}
		metrics := []control.TransportMetric{}
		if provider, ok := h.service.(interface {
			TransportMetrics() []control.TransportMetric
		}); ok {
			if value := provider.TransportMetrics(); value != nil {
				metrics = value
			}
		}
		writeJSON(w, http.StatusOK, metrics)
		return
	}
	if len(parts) == 2 && parts[0] == "v1" && parts[1] == "devices" {
		if !method(w, r, http.MethodGet) {
			return
		}
		devices := h.service.Devices()
		if devices == nil {
			devices = []control.DeviceState{}
		}
		writeJSON(w, http.StatusOK, devices)
		return
	}
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "devices" && parts[2] != "" {
		switch {
		case len(parts) == 3:
			if !method(w, r, http.MethodGet) {
				return
			}
			device, err := h.service.Device(parts[2])
			if err != nil {
				serviceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, device)
			return
		case len(parts) == 4 && parts[3] == "jobs":
			if !method(w, r, http.MethodPost) {
				return
			}
			var input SubmitRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			if input.PatternRef != nil {
				h.submitPattern(w, r, parts[2], input)
				return
			}
			if (input.Program == nil) == (input.Effect == nil) {
				writeError(w, http.StatusBadRequest, "invalid_request", "exactly one of program or effect is required")
				return
			}
			var program control.Program
			if input.Effect != nil {
				var err error
				program, err = control.CompileEffect(*input.Effect)
				if err != nil {
					serviceError(w, err)
					return
				}
			} else {
				program = *input.Program
			}
			if input.RequestID != nil || input.ExpectedDaemonInstanceID != nil {
				if input.RequestID == nil || input.ExpectedDaemonInstanceID == nil || *input.RequestID == "" || *input.ExpectedDaemonInstanceID == "" {
					writeError(w, http.StatusBadRequest, "invalid_request", "request_id and expected_daemon_instance_id must both be nonempty or both omitted")
					return
				}
				service, ok := h.receiptService(w)
				if !ok {
					return
				}
				result, err := service.SubmitWithReceipt(r.Context(), parts[2], program, input.Policy, control.ReceiptRequest{
					RequestID: *input.RequestID, ExpectedDaemonInstanceID: *input.ExpectedDaemonInstanceID,
				})
				if err != nil {
					serviceError(w, err)
					return
				}
				w.Header().Set("X-NB1-Replayed", strconv.FormatBool(result.Replayed))
				w.Header().Set("X-NB1-Daemon-Instance-ID", result.DaemonInstanceID)
				w.Header().Set("Location", receiptLocation(parts[2], *input.RequestID, *input.ExpectedDaemonInstanceID))
				writeJSON(w, http.StatusAccepted, result.Job)
				return
			}
			job, err := h.service.Submit(r.Context(), parts[2], program, input.Policy)
			if err != nil {
				serviceError(w, err)
				return
			}
			w.Header().Set("Location", "/v1/jobs/"+url.PathEscape(job.ID))
			writeJSON(w, http.StatusAccepted, job)
			return
		case len(parts) == 5 && parts[3] == "requests" && parts[4] != "":
			if !method(w, r, http.MethodGet) {
				return
			}
			query, err := url.ParseQuery(r.URL.RawQuery)
			values := query["expected_daemon_instance_id"]
			if err != nil || len(query) != 1 || len(values) != 1 || values[0] == "" {
				writeError(w, http.StatusBadRequest, "invalid_request", "exactly one nonempty expected_daemon_instance_id query parameter is required")
				return
			}
			service, ok := h.receiptService(w)
			if !ok {
				return
			}
			job, err := service.Request(parts[2], parts[4], values[0])
			if err != nil {
				serviceError(w, err)
				return
			}
			w.Header().Set("X-NB1-Daemon-Instance-ID", values[0])
			writeJSON(w, http.StatusOK, job)
			return
		}
	}
	if len(parts) >= 3 && parts[0] == "v1" && parts[1] == "jobs" && parts[2] != "" {
		switch {
		case len(parts) == 3:
			if !method(w, r, http.MethodGet) {
				return
			}
			job, err := h.service.Job(parts[2])
			if err != nil {
				serviceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, job)
			return
		case len(parts) == 4 && parts[3] == "cancel":
			if !method(w, r, http.MethodPost) {
				return
			}
			if r.Body != nil && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
				var input struct{}
				if !decodeJSON(w, r, &input) {
					return
				}
			}
			job, err := h.service.Cancel(r.Context(), parts[2])
			if err != nil {
				serviceError(w, err)
				return
			}
			writeJSON(w, http.StatusAccepted, job)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "unknown API route")
}

func (h *handler) receiptService(w http.ResponseWriter) (control.ReceiptService, bool) {
	service, ok := h.service.(control.ReceiptService)
	if !ok {
		writeError(w, http.StatusNotImplemented, "idempotency_unsupported", "this service does not support request receipts; do not resubmit without the request binding")
	}
	return service, ok
}

func receiptLocation(serial, requestID, instance string) string {
	return "/v1/devices/" + url.PathEscape(serial) + "/requests/" + url.PathEscape(requestID) +
		"?expected_daemon_instance_id=" + url.QueryEscape(instance)
}

// Browser UI and Chrome extension code may live on different origins. Only
// the published website (an origin shared across its Pages paths) and local
// HTTP(S) previews may read the public, constant presence marker.
func presenceOrigin(origin string) bool {
	if origin == "https://b1ug.github.io" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || origin != u.Scheme+"://"+u.Host {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		return err == nil && n > 0 && u.Host == net.JoinHostPort(host, port)
	}
	return u.Host == host || (strings.Contains(host, ":") && u.Host == "["+host+"]")
}

func (h *handler) serveBrowserStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", "Origin")
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && !presenceOrigin(origins[0]) {
		writeError(w, http.StatusForbidden, "local_only", "browser origin cannot read daemon presence")
		return
	}
	if !frameNoBodyOrQuery(w, r) {
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	if len(origins) == 1 {
		w.Header().Set("Access-Control-Allow-Origin", origins[0])
	}
	if r.URL.Path == "/v1/presence" {
		writeJSON(w, http.StatusOK, map[string]string{"service": "nb1", "api_version": "v1"})
		return
	}
	type totals struct {
		Configured int `json:"configured"`
		Online     int `json:"online"`
	}
	var devices *totals
	if h.service != nil {
		// Devices returns a cached snapshot without refreshing or opening USB.
		if snapshot := h.service.Devices(); snapshot != nil {
			devices = &totals{Configured: len(snapshot)}
			for _, device := range snapshot {
				if device.Connection == "online" {
					devices.Online++
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "nb1", "api_version": "v1", "health": "ok", "devices": devices,
	})
}

func localHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme &&
		u.User == nil && u.Host == r.Host && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use "+want)
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out interface{}) bool {
	body, ok := readJSONBody(w, r)
	if !ok {
		return false
	}
	if err := jsoninput.DecodeObject(body, out); err != nil {
		return jsonError(w, err)
	}
	return true
}

func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, jsonError(w, err)
	}
	if len(body) > MaxBodyBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB")
		return nil, false
	}
	return body, true
}

func jsonError(w http.ResponseWriter, err error) bool {
	writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
	return false
}

func serviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, control.ErrRequestConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, control.ErrInstanceChanged):
		writeError(w, http.StatusConflict, "daemon_instance_changed", err.Error())
	case errors.Is(err, control.ErrRequestExpired):
		writeError(w, http.StatusGone, "request_expired", err.Error())
	case errors.Is(err, control.ErrReceiptCapacity):
		writeError(w, http.StatusTooManyRequests, "idempotency_capacity", err.Error())
	case errors.Is(err, control.ErrRequestNotFound):
		writeError(w, http.StatusNotFound, "request_not_found", err.Error())
	case errors.Is(err, control.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, control.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, control.ErrQueueFull):
		writeError(w, http.StatusConflict, "queue_full", err.Error())
	case errors.Is(err, control.ErrBusy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, control.ErrOffline), errors.Is(err, control.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
