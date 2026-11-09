// Package transporttrace is opt-in, bounded diagnostic storage. It neither
// interprets transport errors for execution nor owns devices or cancellation.
package transporttrace

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	Contract       = "nb1-transport-attempt/experimental-v1"
	Capacity       = 1024
	QueueCapacity  = 64
	MaxRecordBytes = 768
	MaxLineBytes   = 2048
	LogBudget      = 8 << 20
	SummaryReserve = 4096
)

type Phase uint8

const (
	PhaseNone Phase = iota
	OpenFirmware
	ConnectRead
	IdleObserve
	PreflightHold
	ProgramStep
	ProgramObserve
	FrameInitial
	FrameTarget
	Cleanup
	CleanupVerify
	Shutdown
)

var phases = [...]string{"none", "open_firmware", "connect_read", "idle_observe", "preflight_hold", "program_step", "program_observe", "frame_initial", "frame_target", "cleanup", "cleanup_verify", "shutdown"}

func (p Phase) String() string {
	if int(p) >= len(phases) {
		return "invalid"
	}
	return phases[p]
}

type Operation uint8

const (
	UnknownOperation Operation = iota
	QueryFirmware
	QueryRGB
	SetColor
	DisableTickle
	StopPlayback
	FreezeRGB
	OffRGB
	QueryPlayback
)

var operations = [...]string{"unknown", "query_firmware", "query_rgb", "set_color", "disable_tickle", "stop_playback", "freeze_rgb", "off_rgb", "query_playback"}

func OperationFor(s string) Operation {
	for i, name := range operations {
		if name == s {
			return Operation(i)
		}
	}
	return UnknownOperation
}
func (o Operation) String() string {
	if int(o) >= len(operations) {
		return "unknown"
	}
	return operations[o]
}

// Indexes are zero based; -1 means absent. RGB/LED cannot identify a frame.
type Position struct{ Repeat, Step, Frame, ExecutedFrame, Initial, Target int32 }

func NoPosition() Position { return Position{-1, -1, -1, -1, -1, -1} }

// Scope is copied into fixed arrays at operation entry. It never retains an
// author document, request ticket, mutable record or arbitrary caller string.
type Scope struct {
	JobID, PatternID, Revision string
	Pattern                    bool
	Frame                      bool
	Phase                      Phase
	Mode                       string
	Position                   Position
}

func EmptyScope(phase Phase) Scope { return Scope{Phase: phase, Position: NoPosition()} }

type ScopeBinder interface{ SetTransportScope(Scope) }
type Target struct {
	LED      uint8
	RGB      [3]byte
	FadeMS   uint32
	HasColor bool
}

type fixedScope struct {
	JobID          [41]byte
	PatternID      [64]byte
	Revision       [71]byte
	Position       Position
	Phase          Phase
	Pattern, Frame bool
	Mode           uint8
}

func fixed(s Scope) (fixedScope, bool) {
	var f fixedScope
	if len(s.JobID) > len(f.JobID) || len(s.PatternID) > len(f.PatternID) || len(s.Revision) > len(f.Revision) || int(s.Phase) >= len(phases) {
		return f, false
	}
	for _, v := range []string{s.JobID, s.PatternID, s.Revision} {
		for _, c := range []byte(v) {
			if c < 32 || c > 126 {
				return f, false
			}
		}
	}
	for _, i := range []int32{s.Position.Repeat, s.Position.Step, s.Position.Frame, s.Position.ExecutedFrame, s.Position.Initial, s.Position.Target} {
		if i < -1 {
			return f, false
		}
	}
	switch s.Mode {
	case "":
	case "off":
		f.Mode = 1
	case "hold":
		f.Mode = 2
	default:
		return f, false
	}
	copy(f.JobID[:], s.JobID)
	copy(f.PatternID[:], s.PatternID)
	copy(f.Revision[:], s.Revision)
	f.Position, f.Phase, f.Pattern, f.Frame = s.Position, s.Phase, s.Pattern, s.Frame
	return f, true
}
func text(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// Event contains fixed-size values only; a bounded recorder owns no error or
// source references. JSON formatting happens on the consumer goroutine.
type Event struct {
	Sequence, OperationID                                                 uint64
	MonotonicNS, CallStartNS, DurationNS                                  int64
	scope                                                                 fixedScope
	Serial                                                                [8]byte
	NativePlatform                                                        [16]byte
	NativeCode                                                            uint64
	Hint                                                                  [160]byte
	HintLen                                                               uint8
	NativePlatformLen                                                     uint8
	Target                                                                Target
	Operation                                                             Operation
	Attempt                                                               uint8
	End, Success, RetryEligible, WillRetry, HasNative, HintTruncated      bool
	HintUTF8Replaced, NativePlatformTruncated, NativePlatformUTF8Replaced bool
	BackoffMS                                                             uint8
}

// MarshalJSON is diagnostic only. Presence of zero indexes and native code0
// is explicit; an absent code must not masquerade as native success.
func (e Event) MarshalJSON() ([]byte, error) {
	indexes := make(map[string]int32, 6)
	for k, v := range map[string]int32{"repeat": e.scope.Position.Repeat, "step": e.scope.Position.Step, "frame": e.scope.Position.Frame, "executed_frame": e.scope.Position.ExecutedFrame, "initial": e.scope.Position.Initial, "target": e.scope.Position.Target} {
		if v >= 0 {
			indexes[k] = v
		}
	}
	w := map[string]interface{}{"event": "transport_attempt", "seq": e.Sequence, "operation_id": e.OperationID, "monotonic_ns": e.MonotonicNS, "serial": text(e.Serial[:]), "operation": e.Operation.String(), "phase": e.scope.Phase.String(), "attempt": e.Attempt, "edge": "begin"}
	if job := text(e.scope.JobID[:]); job != "" {
		w["job_id"] = job
		w["origin"] = "legacy"
		w["job_kind"] = "program"
		if e.scope.Frame {
			w["job_kind"] = "frame"
		}
		if e.scope.Pattern {
			w["origin"] = "pattern_v2"
			w["pattern_id"] = text(e.scope.PatternID[:])
			w["revision"] = text(e.scope.Revision[:])
		}
	}
	if len(indexes) > 0 {
		w["position"] = indexes
	}
	if e.scope.Mode != 0 {
		w["mode"] = map[uint8]string{1: "off", 2: "hold"}[e.scope.Mode]
	}
	if e.Target.LED != 0 || e.Target.HasColor {
		w["led"] = e.Target.LED
	}
	if e.Target.HasColor {
		w["rgb"] = e.Target.RGB
		w["fade_ms"] = e.Target.FadeMS
	}
	if e.End {
		w["edge"] = "end"
		w["duration_ns"] = e.DurationNS
		w["call_start_ns"] = e.CallStartNS
		w["success"] = e.Success
		w["retry_eligible"] = e.RetryEligible
		w["will_retry"] = e.WillRetry
		w["backoff_ms"] = e.BackoffMS
		if e.HasNative {
			w["native_platform"] = string(e.NativePlatform[:e.NativePlatformLen])
			w["native_code"] = e.NativeCode
			if e.NativePlatformTruncated {
				w["native_platform_truncated"] = true
			}
			if e.NativePlatformUTF8Replaced {
				w["native_platform_utf8_replaced"] = true
			}
		}
		if e.HintLen > 0 {
			w["sdk_error_hint"] = string(e.Hint[:e.HintLen])
			w["hint_truncated"] = e.HintTruncated
			if e.HintUTF8Replaced {
				w["hint_utf8_replaced"] = true
			}
		}
	}
	return json.Marshal(w)
}

type Header struct {
	Event            string    `json:"event"`
	Contract         string    `json:"contract"`
	DaemonInstanceID string    `json:"daemon_instance_id"`
	UTCAnchor        time.Time `json:"utc_anchor"`
	Capacity         int       `json:"capacity"`
	QueueCapacity    int       `json:"queue_capacity"`
	LogBudget        int       `json:"log_budget_bytes"`
	SourceKnown      bool      `json:"source_known"`
	SourceRevision   string    `json:"source_revision,omitempty"`
	SourceModified   bool      `json:"source_modified"`
}
type Stats struct {
	Event            string `json:"event"`
	Events           uint64 `json:"events"`
	EarliestSequence uint64 `json:"earliest_seq"`
	LatestSequence   uint64 `json:"latest_seq"`
	Evicted          uint64 `json:"evicted"`
	QueueDropped     uint64 `json:"queue_dropped"`
	LogSuppressed    uint64 `json:"log_suppressed"`
	WriterErrors     uint64 `json:"writer_errors"`
	InvalidScopes    uint64 `json:"invalid_scopes"`
	InFlight         uint64 `json:"in_flight"`
	Closed           bool   `json:"closed"`
}
type Snapshot struct {
	Events []Event
	Stats  Stats
}

type Recorder struct {
	mu            sync.Mutex
	ring          [Capacity]Event
	queue         chan Event
	start         time.Time
	now           func() time.Time
	header        Header
	stats         Stats
	nextOperation uint64
}

func New() *Recorder { return newRecorder(time.Now) }
func newRecorder(now func() time.Time) *Recorder {
	start := now()
	return &Recorder{queue: make(chan Event, QueueCapacity), start: start, now: now, header: Header{Event: "transport_trace_started", Contract: Contract, UTCAnchor: start.UTC(), Capacity: Capacity, QueueCapacity: QueueCapacity, LogBudget: LogBudget}, stats: Stats{Event: "transport_trace_summary"}}
}

// SetInstance is startup-only metadata and uses the already existing identity.
func (r *Recorder) SetInstance(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(id) <= 39 {
		r.header.DaemonInstanceID = strings.Clone(id)
	}
}

// SetSource uses only an already available Go build stamp, never a repository
// read or subprocess. Unknown/missing build metadata remains explicitly so.
func (r *Recorder) SetSource(revision string, modified bool) {
	if len(revision) != 40 {
		return
	}
	for _, c := range revision {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.header.SourceKnown = true
	r.header.SourceRevision = strings.Clone(revision)
	r.header.SourceModified = modified
}
func (r *Recorder) Header() Header       { r.mu.Lock(); defer r.mu.Unlock(); return r.header }
func (r *Recorder) Events() <-chan Event { return r.queue }
func (r *Recorder) Stats() Stats         { r.mu.Lock(); defer r.mu.Unlock(); return r.stats }
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.stats.Events
	if n > Capacity {
		n = Capacity
	}
	s := Snapshot{Events: make([]Event, int(n)), Stats: r.stats}
	for i := range s.Events {
		seq := r.stats.EarliestSequence + uint64(i)
		s.Events[i] = r.ring[(seq-1)%Capacity]
	}
	return s
}
func (r *Recorder) NoteSuppressed()  { r.mu.Lock(); r.stats.LogSuppressed++; r.mu.Unlock() }
func (r *Recorder) NoteWriterError() { r.mu.Lock(); r.stats.WriterErrors++; r.mu.Unlock() }

// Close must be called only after all device workers have returned. Logging
// consumers never close this queue or wait for a native call to finish.
func (r *Recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stats.Closed {
		r.stats.Closed = true
		close(r.queue)
	}
}
func (r *Recorder) append(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats.Closed {
		return
	}
	r.stats.Events++
	if e.End {
		if r.stats.InFlight > 0 {
			r.stats.InFlight--
		}
	} else {
		r.stats.InFlight++
	}
	e.Sequence = r.stats.Events
	r.stats.LatestSequence = e.Sequence
	if e.Sequence > Capacity {
		r.stats.Evicted++
		r.stats.EarliestSequence = e.Sequence - Capacity + 1
	} else {
		r.stats.EarliestSequence = 1
	}
	r.ring[(e.Sequence-1)%Capacity] = e
	select {
	case r.queue <- e:
	default:
		r.stats.QueueDropped++
	}
}

type OperationTrace struct {
	recorder *Recorder
	scope    fixedScope
	serial   [8]byte
	kind     Operation
	id       uint64
	target   Target
}

func (r *Recorder) Operation(serial string, scope Scope, kind Operation, target Target) *OperationTrace {
	f, ok := fixed(scope)
	r.mu.Lock()
	defer r.mu.Unlock()
	serialOK := len(serial) > 0 && len(serial) <= 8
	for _, c := range []byte(serial) {
		if c < 32 || c > 126 {
			serialOK = false
		}
	}
	if !ok || !serialOK || kind == UnknownOperation || int(kind) >= len(operations) {
		r.stats.InvalidScopes++
		return nil
	}
	r.nextOperation++
	o := &OperationTrace{recorder: r, scope: f, kind: kind, id: r.nextOperation, target: target}
	copy(o.serial[:], serial)
	return o
}

type Attempt struct {
	operation *OperationTrace
	start     time.Time
	returned  time.Time
	number    uint8
}

func (o *OperationTrace) Begin(number int) Attempt {
	if o == nil {
		return Attempt{}
	}
	now := o.recorder.now()
	a := Attempt{operation: o, start: now, number: uint8(number)}
	o.recorder.append(Event{OperationID: o.id, MonotonicNS: now.Sub(o.recorder.start).Nanoseconds(), scope: o.scope, Serial: o.serial, Target: o.target, Operation: o.kind, Attempt: a.number})
	// Sample after the bounded begin append, so lock contention/copying there
	// is not attributed to the SDK call. No callback or I/O runs between here
	// and the owning retry loop's existing synchronous SDK call.
	a.start = o.recorder.now()
	return a
}

// Returned samples the host SDK return before metrics/error classification.
// End may then attach that decision without charging it to native duration.
func (a *Attempt) Returned() {
	if a.operation != nil && a.returned.IsZero() {
		a.returned = a.operation.recorder.now()
	}
}
func (a Attempt) End(err error, eligible, willRetry bool, backoffMS int) {
	if a.operation == nil {
		return
	}
	o := a.operation
	now := a.returned
	if now.IsZero() {
		now = o.recorder.now()
	}
	e := Event{OperationID: o.id, MonotonicNS: now.Sub(o.recorder.start).Nanoseconds(), CallStartNS: a.start.Sub(o.recorder.start).Nanoseconds(), DurationNS: now.Sub(a.start).Nanoseconds(), scope: o.scope, Serial: o.serial, Target: o.target, Operation: o.kind, Attempt: a.number, End: true, Success: err == nil, RetryEligible: eligible, WillRetry: willRetry, BackoffMS: uint8(backoffMS)}
	if err != nil {
		var native interface {
			NativePlatform() string
			NativeCode() uint64
		}
		if errors.As(err, &native) {
			platform := native.NativePlatform()
			if platform != "" {
				e.HasNative = true
				e.NativeCode = native.NativeCode()
				n, truncated, replaced := copyUTF8(e.NativePlatform[:], platform)
				e.NativePlatformLen, e.NativePlatformTruncated, e.NativePlatformUTF8Replaced = uint8(n), truncated, replaced
			}
		}
		n, truncated, replaced := copyUTF8(e.Hint[:], err.Error())
		e.HintLen, e.HintTruncated, e.HintUTF8Replaced = uint8(n), truncated, replaced
	}
	o.recorder.append(e)
}

func copyUTF8(dst []byte, value string) (written int, truncated, replaced bool) {
	replaced = !utf8.ValidString(value)
	for _, c := range value {
		n := utf8.RuneLen(c)
		if written+n > len(dst) {
			return written, true, replaced
		}
		written += utf8.EncodeRune(dst[written:], c)
	}
	return written, false, replaced
}
