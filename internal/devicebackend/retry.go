package devicebackend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/ownership"
)

const mk2MaxAttempts = 3

// OperationError retains the final native cause, the exact profile operation,
// and its attempt count. A successful retry does not turn a permanent failure
// or a failed cleanup verification into success.
type OperationError struct {
	Operation string
	Attempts  int
	Err       error
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("mk2 %s failed after %d attempt(s): %v", e.Operation, e.Attempts, e.Err)
}

func (e *OperationError) Unwrap() error { return e.Err }

// RetryMetrics describes this driver's mk2 SDK calls, including calls made
// during firmware identification. Reopens of the same serial retain counters.
// Retries counts attempts actually issued after the first, not logical jobs.
type RetryMetrics struct {
	Serial              string    `json:"serial"`
	Operations          uint64    `json:"operations"`
	Attempts            uint64    `json:"attempts"`
	Retries             uint64    `json:"retries"`
	RecoveredOperations uint64    `json:"recovered_operations"`
	FailedOperations    uint64    `json:"failed_operations"`
	ExhaustedOperations uint64    `json:"exhausted_operations"`
	LastRetryOperation  string    `json:"last_retry_operation,omitempty"`
	LastRetryError      string    `json:"last_retry_error,omitempty"`
	LastRetryAttempt    int       `json:"last_retry_attempt,omitempty"`
	LastRetryAt         time.Time `json:"last_retry_at,omitempty"`
	LastFailure         string    `json:"last_failure,omitempty"`
	SampledAt           time.Time `json:"sampled_at"`
}

type retryTracker struct {
	mu         sync.Mutex
	data       RetryMetrics
	operations map[string]*control.TransportMetric
}

type retryRegistry struct {
	mu      sync.Mutex
	devices map[string]*retryTracker
}

func (r *retryRegistry) device(serial string) *retryTracker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.devices == nil {
		r.devices = make(map[string]*retryTracker)
	}
	tracker := r.devices[serial]
	if tracker == nil {
		tracker = &retryTracker{data: RetryMetrics{Serial: serial}}
		r.devices[serial] = tracker
	}
	return tracker
}

// SnapshotMetrics returns a detached, serial-sorted snapshot for a driver
// created by New. It is safe while device workers are making native calls.
// Other control.Driver implementations return an empty slice.
func SnapshotMetrics(source control.Driver) []RetryMetrics {
	result := make([]RetryMetrics, 0)
	d, ok := source.(*driver)
	if !ok || d == nil {
		return result
	}
	d.metrics.mu.Lock()
	trackers := make([]*retryTracker, 0, len(d.metrics.devices))
	for _, tracker := range d.metrics.devices {
		trackers = append(trackers, tracker)
	}
	d.metrics.mu.Unlock()
	for _, tracker := range trackers {
		tracker.mu.Lock()
		data := tracker.data
		tracker.mu.Unlock()
		data.SampledAt = time.Now().UTC()
		result = append(result, data)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Serial < result[j].Serial })
	return result
}

func (d *driver) Metrics() []control.TransportMetric {
	result := make([]control.TransportMetric, 0)
	if d == nil {
		return result
	}
	d.metrics.mu.Lock()
	trackers := make([]*retryTracker, 0, len(d.metrics.devices))
	for _, tracker := range d.metrics.devices {
		trackers = append(trackers, tracker)
	}
	d.metrics.mu.Unlock()
	for _, tracker := range trackers {
		tracker.mu.Lock()
		for _, metric := range tracker.operations {
			result = append(result, *metric)
		}
		tracker.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Serial == result[j].Serial {
			return result[i].Operation < result[j].Operation
		}
		return result[i].Serial < result[j].Serial
	})
	return result
}

func transientMK2Transport(err error) bool {
	for _, permanent := range []error{context.Canceled, context.DeadlineExceeded, control.ErrInvalid, control.ErrClosed, control.ErrOffline, control.ErrBusy, ownership.ErrBusy} {
		if errors.Is(err, permanent) {
			return false
		}
	}
	if err == nil {
		return false
	}
	// Prefer raw IOKit codes; the structural interface also keeps the older
	// supported GID release buildable without this additive diagnostic API.
	var native interface {
		NativePlatform() string
		NativeCode() uint64
	}
	if errors.As(err, &native) {
		if native.NativePlatform() != "darwin" {
			return false
		}
		switch native.NativeCode() {
		case 0xe00002bc, 0xe00002d5, 0xe00002d6: // Error, Busy, Timeout (IOReturn.h)
			return true
		default:
			return false
		}
	}
	// Older GID releases expose only plain native leaf messages.
	// Match complete leaf causes, never substrings of wrapped validation or
	// disconnection messages. These may be permanent too: the bound still
	// applies and the final cause must be returned after the last failure.
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(err) {
		err = cause
	}
	switch err.Error() {
	case "general error", "device is busy", "I/O Timeout":
		return true
	default:
		return false
	}
}

// retryMK2 is only for this profile's idempotent color/stop commands and
// complete query transactions. It is never installed below the SDK and cannot
// retry raw commands, pattern writes, flash programming or legacy CLI actions.
// Backoff bounds additional waiting, not the duration of synchronous native I/O.
func retryMK2(tracker *retryTracker, pause func(time.Duration), kind, operation string, call func() error) error {
	if pause == nil {
		pause = time.Sleep
	}
	if tracker != nil {
		tracker.mu.Lock()
		tracker.data.Operations++
		if tracker.operations == nil {
			tracker.operations = make(map[string]*control.TransportMetric)
		}
		if tracker.operations[kind] == nil {
			tracker.operations[kind] = &control.TransportMetric{Serial: tracker.data.Serial, Operation: kind}
		}
		tracker.operations[kind].Calls++
		tracker.mu.Unlock()
	}
	for attempt := 1; ; attempt++ {
		if tracker != nil {
			tracker.mu.Lock()
			tracker.data.Attempts++
			tracker.operations[kind].Attempts++
			if attempt > 1 {
				tracker.data.Retries++
				tracker.operations[kind].Retries++
			}
			tracker.mu.Unlock()
		}
		err := call()
		if err == nil {
			if attempt > 1 && tracker != nil {
				tracker.mu.Lock()
				tracker.data.RecoveredOperations++
				tracker.mu.Unlock()
			}
			return nil
		}
		if tracker != nil {
			tracker.mu.Lock()
			tracker.operations[kind].LastError = fmt.Sprintf("%s attempt %d: %v", operation, attempt, err)
			tracker.mu.Unlock()
		}
		eligible := transientMK2Transport(err)
		if !eligible || attempt == mk2MaxAttempts {
			failure := &OperationError{Operation: operation, Attempts: attempt, Err: err}
			if tracker != nil {
				tracker.mu.Lock()
				tracker.data.FailedOperations++
				tracker.operations[kind].Failures++
				if eligible && attempt == mk2MaxAttempts {
					tracker.data.ExhaustedOperations++
				}
				tracker.data.LastFailure = failure.Error()
				tracker.mu.Unlock()
			}
			return failure
		}
		if tracker != nil {
			tracker.mu.Lock()
			tracker.data.LastRetryOperation = operation
			tracker.data.LastRetryError = err.Error()
			tracker.data.LastRetryAttempt = attempt + 1
			tracker.data.LastRetryAt = time.Now().UTC()
			tracker.mu.Unlock()
		}
		delay := 5 * time.Millisecond
		if attempt == 2 {
			delay = 15 * time.Millisecond
		}
		pause(delay)
	}
}

func (b *backend) operation(kind, stage string, call func() error) error {
	return retryMK2(b.metrics, b.pause, kind, stage, call)
}
