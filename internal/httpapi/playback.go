package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/b1ug/nb1/internal/control"
)

func (h *handler) servePlayback(w http.ResponseWriter, r *http.Request) bool {
	service, ok := h.service.(control.PlaybackService)
	if !ok {
		return false
	}
	switch r.URL.Path {
	case "/v1/playback":
		if !method(w, r, http.MethodGet) || !emptyPlaybackRequest(w, r) {
			return true
		}
		writeJSON(w, http.StatusOK, service.PlaybackStatus())
		return true
	case "/v1/playback/requests":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return true
		}
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "request bodies are not supported")
			return true
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || r.URL.ForceQuery && r.URL.RawQuery == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid query string")
			return true
		}
		limit := 0
		if raw, exists := query["limit"]; exists {
			if len(raw) != 1 {
				writeError(w, http.StatusBadRequest, "invalid_request", "limit must appear at most once")
				return true
			}
			parsed, err := strconv.Atoi(raw[0])
			if err != nil || parsed < 1 || parsed > 10000 {
				writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 10000")
				return true
			}
			limit = parsed
		}
		if len(query) > 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "only the limit query parameter is supported")
			return true
		}
		maximum := service.PlaybackStatus().HistoryLimit
		if maximum <= 0 {
			maximum = 100
		}
		if limit == 0 || limit > maximum {
			limit = maximum
		}
		requests, err := service.PlaybackRequests(limit)
		if err != nil {
			serviceError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, playbackRequestsResponse{Requests: requests, Limit: limit, Count: len(requests)})
		return true
	case "/v1/playback/override":
		switch r.Method {
		case http.MethodPost:
			var input playbackOverrideRequest
			if !decodeJSON(w, r, &input) {
				return true
			}
			if input.DurationMS < 1 || input.DurationMS > int64((30*24*time.Hour)/time.Millisecond) {
				writeError(w, http.StatusBadRequest, "invalid_request", "duration_ms must be between 1 and 2592000000")
				return true
			}
			state, err := service.SetPlaybackOverride(input.Mode, time.Duration(input.DurationMS)*time.Millisecond)
			if err != nil {
				serviceError(w, err)
				return true
			}
			writeJSON(w, http.StatusOK, state)
			return true
		case http.MethodDelete:
			if !emptyPlaybackRequest(w, r) {
				return true
			}
			state, err := service.ClearPlaybackOverride()
			if err != nil {
				serviceError(w, err)
				return true
			}
			writeJSON(w, http.StatusOK, state)
			return true
		default:
			w.Header().Set("Allow", "POST, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return true
		}
	default:
		return false
	}
}

func emptyPlaybackRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "query parameters and request bodies are not supported")
		return false
	}
	return true
}
