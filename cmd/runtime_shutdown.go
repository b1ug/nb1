package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const runtimeLifecycleContract = "nb1-daemon-lifecycle/experimental-v1"

// Records contain only internally generated identity, time and fixed enums.
// In particular, no error text, signal guess, path, environment or request is
// included. Connection LastError is device state, not a shutdown trigger.
type runtimeShutdownRecord struct {
	Event             string    `json:"event"`
	Contract          string    `json:"contract"`
	Time              time.Time `json:"time"`
	DaemonInstanceID  string    `json:"daemon_instance_id"`
	Trigger           string    `json:"trigger"`
	ParentContext     string    `json:"parent_context"`
	TriggerErrorClass string    `json:"trigger_error_class"`
	HTTPShutdown      string    `json:"http_shutdown,omitempty"`
	EngineClose       string    `json:"engine_close,omitempty"`
	EngineDone        *bool     `json:"engine_done,omitempty"`
	ElapsedMS         *int64    `json:"elapsed_ms,omitempty"`
	Outcome           string    `json:"outcome,omitempty"`
}

type runtimeShutdownCause struct {
	trigger       string
	parentContext string
	triggerError  error
	cause         error
}

// The observed branch is not a claim about unique causality. Parent and engine
// completion may be simultaneously ready, so the context state is saved too.
func waitRuntimeShutdown(parent context.Context, engineDone <-chan struct{}, served, startupErrors <-chan error) runtimeShutdownCause {
	var result runtimeShutdownCause
	select {
	case err := <-startupErrors:
		result.trigger, result.triggerError, result.cause = "startup_log_failed", err, err
	case <-parent.Done():
		result.trigger = "parent_context_done"
	case <-engineDone:
		result.trigger = "engine_stopped"
		parentErr := parent.Err()
		result.parentContext = runtimeErrorClass(parentErr)
		if parentErr == nil {
			result.cause = fmt.Errorf("device runtime stopped")
		}
	case err := <-served:
		result.trigger, result.triggerError = "http_server_returned", err
		if !errors.Is(err, http.ErrServerClosed) {
			result.cause = err
		}
	}
	if result.parentContext == "" {
		result.parentContext = runtimeErrorClass(parent.Err())
	}
	return result
}

// Do not call arbitrary error.Error, Is or Unwrap implementations while adding
// diagnostics to cleanup. Only exact standard sentinels get special classes;
// wrapped or platform errors are deliberately "other".
func runtimeErrorClass(err error) string {
	switch err {
	case nil:
		return "none"
	case context.Canceled:
		return "context_canceled"
	case context.DeadlineExceeded:
		return "deadline_exceeded"
	case http.ErrServerClosed:
		return "http_server_closed"
	default:
		return "other"
	}
}

func (c runtimeShutdownCause) record(instance, event string) runtimeShutdownRecord {
	return runtimeShutdownRecord{Event: event, Contract: runtimeLifecycleContract,
		Time: time.Now().UTC(), DaemonInstanceID: instance, Trigger: c.trigger,
		ParentContext: c.parentContext, TriggerErrorClass: runtimeErrorClass(c.triggerError)}
}

func (c runtimeShutdownCause) result(instance string, began time.Time, httpErr, engineErr, finalErr error, engineDone <-chan struct{}) runtimeShutdownRecord {
	record := c.record(instance, "daemon_shutdown_result")
	record.HTTPShutdown, record.EngineClose = runtimeErrorClass(httpErr), runtimeErrorClass(engineErr)
	done := false
	select {
	case <-engineDone:
		done = true
	default:
	}
	record.EngineDone = &done
	elapsed := time.Since(began).Milliseconds()
	record.ElapsedMS = &elapsed
	record.Outcome = "succeeded"
	if finalErr != nil {
		record.Outcome = "failed"
	}
	return record
}
