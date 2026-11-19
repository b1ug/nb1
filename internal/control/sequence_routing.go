package control

import (
	"errors"
	"fmt"
)

var (
	ErrSequenceTargetRequired = errors.New("select a device serial: the daemon manages more than one device")
	ErrSequenceTargetAbsent   = errors.New("selected device is not managed by this daemon")
	ErrSequenceTargetOffline  = errors.New("selected device is not online")
)

// SequenceRoute describes the resolved request or binding, before checking the
// live worker states. Only a configured default may opt into substitution.
type SequenceRoute struct {
	Serial          string
	Source          string
	UseSingleOnline bool
}

func (e *Engine) resolveSequenceTargetLocked(route SequenceRoute) (string, string, error) {
	if route.Source == "" {
		return route.Serial, "", nil // Explicit submissions use existing v1 errors.
	}
	if route.Serial == "" {
		if route.Source != "default" {
			return "", "", fmt.Errorf("%w: routed binding requires a device serial", ErrInvalid)
		}
		if len(e.workers) != 1 {
			return "", "", ErrSequenceTargetRequired
		}
		for serial := range e.workers {
			route.Serial, route.Source = serial, "single_device"
		}
	}
	selected := e.workers[route.Serial]
	if selected != nil && selected.state.Connection == "online" {
		return route.Serial, route.Source, nil
	}
	if selected != nil && selected.state.Connection == "busy" {
		return "", "", ErrBusy
	}
	if route.Source == "default" && route.UseSingleOnline && (selected == nil || selected.state.Connection == "offline") {
		var alternate string
		for serial, w := range e.workers {
			if w.state.Connection != "online" {
				continue
			}
			if alternate != "" {
				return "", "", fmt.Errorf("%w: %s; more than one alternative is online", ErrSequenceTargetOffline, route.Serial)
			}
			alternate = serial
		}
		if alternate != "" {
			return alternate, "fallback", nil
		}
	}
	if selected == nil {
		return "", "", fmt.Errorf("%w: %s", ErrSequenceTargetAbsent, route.Serial)
	}
	return "", "", fmt.Errorf("%w: %s", ErrSequenceTargetOffline, route.Serial)
}
