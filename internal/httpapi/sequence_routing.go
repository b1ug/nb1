package httpapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/libraryv1"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/b1ug/nb1/internal/triggerv1"
)

var errSequenceRoutingUnavailable = errors.New("sequence routing unavailable")

type sequenceRouting struct {
	defaultSerial   string
	useSingleOnline bool
	triggers        *triggerv1.Store
}

// WithSequenceRouting enables server-side default targets and local triggers.
func WithSequenceRouting(defaultSerial string, useSingleOnline bool, libraryDir string) Option {
	return func(h *handler) {
		h.routing = &sequenceRouting{defaultSerial: defaultSerial, useSingleOnline: useSingleOnline, triggers: triggerv1.New(libraryDir)}
	}
}

func (h *handler) listTriggers(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) {
		return
	}
	items, err := h.routing.triggers.List()
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *handler) checkTriggers(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) {
		return
	}
	triggers, err := h.routing.triggers.List()
	if err != nil {
		sequenceAPIError(w, err)
		return
	}
	connections := make(map[string]string)
	for _, state := range h.service.Devices() {
		connections[state.Serial] = state.Connection
	}
	patternIssues := make(map[string]triggerv1.CheckIssue)
	report := triggerv1.CheckReport{Items: make([]triggerv1.CheckedTrigger, 0, len(triggers))}
	for _, trigger := range triggers {
		if err := r.Context().Err(); err != nil {
			sequenceAPIError(w, err)
			return
		}
		row := triggerv1.CheckedTrigger{
			Name: trigger.Name, PatternID: trigger.PatternID, Serial: trigger.Serial, Title: trigger.Title,
			Issues: []triggerv1.CheckIssue{},
		}
		issue, checked := patternIssues[trigger.PatternID]
		if !checked {
			issue = h.checkTriggerPattern(trigger.PatternID)
			patternIssues[trigger.PatternID] = issue
		}
		if issue.Code != "" {
			row.Issues = append(row.Issues, issue)
		}
		if trigger.Serial != "" {
			connection, managed := connections[trigger.Serial]
			if managed {
				row.Connection = connection
			} else {
				row.Issues = append(row.Issues, triggerv1.CheckIssue{
					Code: "device_not_managed", Message: "the bound device is not managed by this daemon",
				})
			}
		}
		report.Items = append(report.Items, row)
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *handler) checkTriggerPattern(id string) triggerv1.CheckIssue {
	err := errSequenceLibraryUnavailable
	if h.library != nil {
		_, err = h.library.GetPattern(id)
	}
	switch {
	case err == nil:
		return triggerv1.CheckIssue{}
	case errors.Is(err, libraryv1.ErrNotFound):
		return triggerv1.CheckIssue{Code: "pattern_not_found", Message: "the saved Pattern does not exist"}
	default:
		return triggerv1.CheckIssue{Code: "pattern_unavailable", Message: "the saved Pattern is unreadable or invalid"}
	}
}

func (h *handler) submitRoutedSequence(ctx context.Context, input sequenceInput, id string, fingerprint [32]byte, policy string) (control.SequenceJob, bool, error) {
	service, ok := h.service.(interface {
		SubmitRoutedSequenceWithPolicy(context.Context, string, string, [32]byte, string, func() (sequence.Sequence, control.SequenceRoute, error)) (control.SequenceJob, bool, error)
	})
	if !ok || h.routing == nil {
		return control.SequenceJob{}, false, errSequenceRoutingUnavailable
	}
	var requested string
	if input.Serial != nil {
		requested = *input.Serial
	}
	// Separate routed intent from explicit v1 submissions and record whether
	// the request supplied a target. Neither remapping nor reconnection can
	// change the identity of a Job retained by the daemon.
	identity := append([]byte("nb1 routed job\x00"+requested+"\x00"), fingerprint[:]...)
	return service.SubmitRoutedSequenceWithPolicy(ctx, requested, id, sha256.Sum256(identity), policy, func() (sequence.Sequence, control.SequenceRoute, error) {
		route := control.SequenceRoute{Serial: h.routing.defaultSerial, Source: "default", UseSingleOnline: h.routing.useSingleOnline}
		if input.Trigger != nil {
			entry, err := h.routing.triggers.Get(*input.Trigger)
			if err != nil {
				return sequence.Sequence{}, control.SequenceRoute{}, err
			}
			input.PatternID = &entry.PatternID
			if entry.Serial != "" {
				route = control.SequenceRoute{Serial: entry.Serial, Source: "trigger"}
			}
		}
		if requested != "" {
			route = control.SequenceRoute{Serial: requested, Source: "request"}
		}
		compiled, err := h.compileSequenceInput(input)
		return compiled, route, err
	})
}
