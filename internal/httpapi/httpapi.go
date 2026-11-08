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
	"strings"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
)

const MaxBodyBytes = 1 << 20

type SubmitRequest struct {
	Program control.Program `json:"program"`
	Policy  string          `json:"policy"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type handler struct{ service control.Service }

func New(service control.Service) http.Handler { return &handler{service: service} }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Loopback binding alone does not reject browser DNS rebinding. Only local
	// hosts and same-origin browser requests may address this unauthenticated API.
	if !localHost(r.Host) || !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "local_only", "use a loopback host and a same-origin or non-browser client")
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
			job, err := h.service.Submit(r.Context(), parts[2], input.Program, input.Policy)
			if err != nil {
				serviceError(w, err)
				return
			}
			w.Header().Set("Location", "/v1/jobs/"+url.PathEscape(job.ID))
			writeJSON(w, http.StatusAccepted, job)
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
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return jsonError(w, err)
	}
	if len(body) > MaxBodyBytes {
		w.Header().Set("Connection", "close")
		r.Close = true
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB")
		return false
	}
	if err = jsoninput.DecodeObject(body, out); err != nil {
		return jsonError(w, err)
	}
	return true
}

func jsonError(w http.ResponseWriter, err error) bool {
	writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
	return false
}

func serviceError(w http.ResponseWriter, err error) {
	switch {
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
