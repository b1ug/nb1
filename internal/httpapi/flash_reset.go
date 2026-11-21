package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/flashreset"
)

// FlashResetFailure retains the hardware progress alongside the normal API error.
type FlashResetFailure struct {
	Error  ErrorBody         `json:"error"`
	Result flashreset.Result `json:"result"`
}

type flashResetService interface {
	ResetFlash(context.Context, string, string) (flashreset.Result, error)
}

const FlashTicketHeader = "X-NB1-Flash-Ticket"
const flashTicketTTL = time.Minute
const flashTicketLimit = 256

type FlashResetTicket struct {
	Ticket string `json:"ticket"`
}

type flashTicket struct {
	serial  string
	expires time.Time
}

// Only unconsumed tickets exist in the current daemon. Deleting an entry
// before dispatch prevents concurrent and network-replayed POSTs from writing.
type flashTicketStore struct {
	mu     sync.Mutex
	issued map[string]flashTicket
}

func (s *flashTicketStore) issue(serial string, now time.Time) (FlashResetTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.issued {
		if !now.Before(entry.expires) {
			delete(s.issued, key)
		}
	}
	if len(s.issued) >= flashTicketLimit {
		return FlashResetTicket{}, false
	}
	if s.issued == nil {
		s.issued = make(map[string]flashTicket)
	}
	var random [16]byte
	rand.Read(random[:])
	key := hex.EncodeToString(random[:])
	s.issued[key] = flashTicket{serial, now.Add(flashTicketTTL)}
	return FlashResetTicket{key}, true
}

func (s *flashTicketStore) consume(key, serial string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.issued[key]
	if !ok || entry.serial != serial || !now.Before(entry.expires) {
		return false
	}
	delete(s.issued, key)
	return true
}

func (h *handler) issueFlashTicket(w http.ResponseWriter, r *http.Request, serial string) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) {
		return
	}
	if !nativeInspectionSerial(serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Flash reset requires an explicit 8-digit hexadecimal device serial")
		return
	}
	if _, ok := h.service.(flashResetService); !ok {
		writeError(w, http.StatusNotImplemented, "flash_reset_unsupported", "this service does not support Flash reset")
		return
	}
	if _, err := h.service.Device(serial); err != nil {
		if errors.Is(err, control.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device_not_managed", err.Error())
		} else {
			sequenceAPIError(w, err)
		}
		return
	}
	ticket, ok := h.flashTickets.issue(serial, time.Now())
	if !ok {
		writeError(w, http.StatusTooManyRequests, "flash_ticket_capacity", "too many outstanding Flash reset tickets")
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

func (h *handler) resetFlash(w http.ResponseWriter, r *http.Request, serial string) {
	if !method(w, r, http.MethodPost) {
		return
	}
	if !nativeInspectionSerial(serial) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Flash reset requires an explicit 8-digit hexadecimal device serial")
		return
	}
	input := struct {
		Preset string `json:"preset"`
	}{Preset: flashreset.Default}
	if !readSequenceJSON(w, r, &input) {
		return
	}
	if !flashreset.Valid(input.Preset) {
		writeError(w, http.StatusBadRequest, "invalid_request", "unknown Flash reset preset")
		return
	}
	service, ok := h.service.(flashResetService)
	if !ok {
		writeError(w, http.StatusNotImplemented, "flash_reset_unsupported", "this service does not support Flash reset")
		return
	}
	tickets := r.Header.Values(FlashTicketHeader)
	if len(tickets) != 1 || !h.flashTickets.consume(tickets[0], serial, time.Now()) {
		writeError(w, http.StatusConflict, "flash_ticket_used_or_expired", "Flash reset ticket is invalid, used or expired; an earlier use may already have written the device")
		return
	}
	result, err := service.ResetFlash(r.Context(), serial, input.Preset)
	if result.Status == "" {
		switch {
		case errors.Is(err, flashreset.ErrUnsupported):
			writeError(w, http.StatusUnprocessableEntity, "unsupported_capability", err.Error())
		case errors.Is(err, control.ErrNotFound):
			writeError(w, http.StatusNotFound, "device_not_managed", err.Error())
		case errors.Is(err, control.ErrOffline):
			writeError(w, http.StatusServiceUnavailable, "device_offline", err.Error())
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusServiceUnavailable, "flash_outcome_unknown", "Flash reset outcome is unknown after the request ended; do not retry automatically")
		case err != nil:
			sequenceAPIError(w, err)
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Flash reset returned no outcome")
		}
		return
	}
	if result.Validate(serial, input.Preset) != nil || (err == nil) != (result.Status == flashreset.Acknowledged) {
		writeError(w, http.StatusInternalServerError, "internal_error", "Flash reset returned an invalid outcome")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, FlashResetFailure{ErrorBody{"flash_" + result.Status, err.Error()}, result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
