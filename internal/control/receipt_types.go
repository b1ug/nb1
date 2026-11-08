package control

import (
	"context"
	"errors"
)

var (
	ErrRequestConflict = errors.New("request id has a different accepted payload")
	ErrInstanceChanged = errors.New("daemon instance changed")
	ErrRequestExpired  = errors.New("request expired; original outcome may be unknown")
	ErrReceiptCapacity = errors.New("request receipt capacity exhausted")
	ErrRequestNotFound = errors.New("request receipt not found")
)

// ReceiptRequest binds one logical submission to one Engine instance. Both
// fields are required by SubmitWithReceipt; unkeyed callers keep using Submit.
type ReceiptRequest struct {
	RequestID                string `json:"request_id"`
	ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id"`
}

// ReceiptResult returns the original job's current snapshot, including terminal
// failure/cancellation. Replayed never means the program was executed again.
type ReceiptResult struct {
	Job              Job    `json:"job"`
	Replayed         bool   `json:"replayed"`
	DaemonInstanceID string `json:"daemon_instance_id"`
}

type InstanceInfo struct {
	DaemonInstanceID string          `json:"daemon_instance_id"`
	Idempotency      IdempotencyInfo `json:"idempotency"`
}

// Deadlines are monotonic Engine uptime milliseconds, not wall-clock dates.
// Append 32 random lowercase hexadecimal characters to RequestIDPrefix.
type IdempotencyInfo struct {
	Supported           bool   `json:"supported"`
	RequestIDPrefix     string `json:"request_id_prefix"`
	AcceptUntilUptimeMS int64  `json:"accept_until_uptime_ms"`
	MaxAcceptWindowMS   int64  `json:"max_accept_window_ms"`
	ReceiptRetentionMS  int64  `json:"receipt_retention_ms"`
	MaxEntries          int    `json:"max_entries"`
	MaxRetainedSteps    int    `json:"max_retained_steps"`
}

// ReceiptService is an optional extension. The original Service remains
// compatible with existing callers, adapters and test doubles.
type ReceiptService interface {
	Service
	Instance() InstanceInfo
	SubmitWithReceipt(context.Context, string, Program, string, ReceiptRequest) (ReceiptResult, error)
	Request(serial, requestID, expectedDaemonInstanceID string) (Job, error)
}
