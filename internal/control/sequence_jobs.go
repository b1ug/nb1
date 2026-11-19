package control

import (
	"container/list"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/b1ug/nb1/internal/sequence"
)

var (
	ErrSequenceJobConflict = errors.New("sequence job ID has a different accepted payload")
	ErrSequenceCapability  = errors.New("sequence device capability missing")
	ErrSequenceJobTarget   = errors.New("expected serial does not match the accepted Job target")
)

const (
	SequenceJobRetention                       = 30 * time.Minute
	sequenceJobMaxTerminalEntries              = 16384
	originSequenceJob             recordOrigin = originPatternV2 + 1
)

// SequenceJobLimits reports this daemon's startup policy, not a guaranteed
// deduplication interval. Capacity eviction and restart can remove records sooner.
func (e *Engine) SequenceJobLimits() (int, time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sequenceJobs.maxTerminalEntries, e.sequenceJobs.retention
}

// SequenceJob is the current status of an accepted v1 job. TotalSteps counts
// repeated steps; initialization and final cleanup do not count as steps.
type SequenceJob struct {
	ID             string     `json:"id"`
	Serial         string     `json:"serial"`
	TargetSource   string     `json:"target_source,omitempty"`
	Status         string     `json:"status"`
	CompletedSteps int        `json:"completed_steps"`
	TotalSteps     int        `json:"total_steps"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Error          string     `json:"error,omitempty"`
}

// SequenceService is the v1 execution interface. The fingerprint identifies
// the original request, while prepare resolves its execution input.
type SequenceService interface {
	SubmitSequence(ctx context.Context, serial, id string, fingerprint [32]byte, prepare func() (sequence.Sequence, error)) (SequenceJob, bool, error)
	SequenceJob(id string) (SequenceJob, error)
	CancelSequence(ctx context.Context, id string) (SequenceJob, error)
}

type sequenceJobEntry struct {
	fingerprint [32]byte
	record      *record
	expiresAt   time.Time
	terminal    *list.Element
}

type sequenceJobStore struct {
	entries            map[string]*sequenceJobEntry
	terminals          list.List
	maxTerminalEntries int
	retention          time.Duration
	now                func() time.Time
	random             io.Reader
}

func newSequenceJobStore() sequenceJobStore {
	return sequenceJobStore{
		entries:            make(map[string]*sequenceJobEntry),
		maxTerminalEntries: sequenceJobMaxTerminalEntries,
		retention:          SequenceJobRetention,
		now:                time.Now,
		random:             rand.Reader,
	}
}

var sequenceJobIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidSequenceJobID(id string) bool { return sequenceJobIDPattern.MatchString(id) }

// NewSequenceJobID returns j_ followed by ten Crockford Base32 characters.
func NewSequenceJobID() (string, error) { return readSequenceJobID(rand.Reader) }

func readSequenceJobID(random io.Reader) (string, error) {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var data [7]byte
	if _, err := io.ReadFull(random, data[:]); err != nil {
		return "", err
	}
	var bits uint64
	for _, value := range data {
		bits = bits<<8 | uint64(value)
	}
	var id [12]byte
	id[0], id[1] = 'j', '_'
	for i := len(id) - 1; i >= 2; i-- {
		id[i] = alphabet[bits&31]
		bits >>= 5
	}
	return string(id[:]), nil
}

// SubmitSequence resolves a new intent outside the engine mutex. An already
// accepted ID is checked on both sides of that work, even if resolution failed.
func (e *Engine) SubmitSequence(ctx context.Context, serial, id string, fingerprint [32]byte, prepare func() (sequence.Sequence, error)) (SequenceJob, bool, error) {
	if !ValidDeviceSerial(serial) {
		return SequenceJob{}, false, fmt.Errorf("%w: invalid serial or job ID", ErrInvalid)
	}
	return e.submitSequence(ctx, serial, id, fingerprint, func() (sequence.Sequence, SequenceRoute, error) {
		if prepare == nil {
			return sequence.Sequence{}, SequenceRoute{}, fmt.Errorf("%w: job requires a sequence", ErrInvalid)
		}
		input, err := prepare()
		return input, SequenceRoute{Serial: serial}, err
	})
}

// SubmitRoutedSequence checks the original request identity before resolving
// mutable trigger bindings or choosing a device from the current daemon state.
// An empty requested serial denotes an implicit target, never a wildcard ID.
// An explicit request serial is authoritative over the prepared routing hint.
func (e *Engine) SubmitRoutedSequence(ctx context.Context, requestedSerial, id string, fingerprint [32]byte, prepare func() (sequence.Sequence, SequenceRoute, error)) (SequenceJob, bool, error) {
	if requestedSerial != "" && !ValidDeviceSerial(requestedSerial) {
		return SequenceJob{}, false, fmt.Errorf("%w: invalid serial", ErrInvalid)
	}
	if requestedSerial != "" && prepare != nil {
		resolve := prepare
		prepare = func() (sequence.Sequence, SequenceRoute, error) {
			input, _, err := resolve()
			return input, SequenceRoute{Serial: requestedSerial, Source: "request"}, err
		}
	}
	return e.submitSequence(ctx, requestedSerial, id, fingerprint, prepare)
}

func (e *Engine) submitSequence(ctx context.Context, requestedSerial, id string, fingerprint [32]byte, prepare func() (sequence.Sequence, SequenceRoute, error)) (SequenceJob, bool, error) {
	if id != "" && !ValidSequenceJobID(id) {
		return SequenceJob{}, false, fmt.Errorf("%w: invalid job ID", ErrInvalid)
	}
	if id != "" {
		e.mu.Lock()
		e.pruneSequenceJobsLocked()
		job, replayed, err := e.sequenceReplayLocked(requestedSerial, id, fingerprint)
		e.mu.Unlock()
		if replayed || err != nil {
			return job, replayed, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SequenceJob{}, false, err
	}
	if prepare == nil {
		return SequenceJob{}, false, fmt.Errorf("%w: job requires a sequence", ErrInvalid)
	}

	input, route, prepareErr := prepare()
	var candidate *record
	if prepareErr == nil {
		input, candidate, prepareErr = prepareSequenceJob(input)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneSequenceJobsLocked()
	if id != "" {
		if job, replayed, err := e.sequenceReplayLocked(requestedSerial, id, fingerprint); replayed || err != nil {
			return job, replayed, err
		}
	}
	if prepareErr != nil {
		return SequenceJob{}, false, prepareErr
	}
	if err := ctx.Err(); err != nil {
		return SequenceJob{}, false, err
	}
	store := &e.sequenceJobs
	serial, targetSource, err := e.resolveSequenceTargetLocked(route)
	if err != nil {
		return SequenceJob{}, false, err
	}
	w, err := e.canAdmitLocked(serial, "reject")
	if err != nil {
		return SequenceJob{}, false, err
	}
	if err := CheckSequenceDevice(input, w.state); err != nil {
		return SequenceJob{}, false, err
	}
	if id == "" {
		id, err = e.allocateSequenceJobIDLocked()
		if err != nil {
			return SequenceJob{}, false, err
		}
	}
	candidate.state = recordState{ID: id, Device: serial, Status: "queued", CreatedAt: store.now().UTC()}
	candidate.targetSource = targetSource
	store.entries[id] = &sequenceJobEntry{fingerprint: fingerprint, record: candidate}
	e.publishLocked(w, candidate, "reject")
	return candidate.sequenceSnapshot(), false, nil
}

func (e *Engine) sequenceReplayLocked(serial, id string, fingerprint [32]byte) (SequenceJob, bool, error) {
	entry := e.sequenceJobs.entries[id]
	if entry == nil {
		if e.jobIDInUseLocked(id) {
			return SequenceJob{}, false, ErrSequenceJobConflict
		}
		return SequenceJob{}, false, nil
	}
	if (serial != "" && entry.record.state.Device != serial) || entry.fingerprint != fingerprint {
		return SequenceJob{}, false, ErrSequenceJobConflict
	}
	e.touchSequenceJobLocked(entry)
	return entry.record.sequenceSnapshot(), true, nil
}

func (e *Engine) jobIDInUseLocked(id string) bool {
	if _, ok := e.sequenceJobs.entries[id]; ok {
		return true
	}
	_, ok := e.jobs[id]
	return ok
}

func (e *Engine) allocateSequenceJobIDLocked() (string, error) {
	for attempt := 0; attempt < 4; attempt++ {
		id, err := readSequenceJobID(e.sequenceJobs.random)
		if err != nil {
			return "", fmt.Errorf("generate sequence job ID: %w", err)
		}
		if !e.jobIDInUseLocked(id) {
			return id, nil
		}
	}
	return "", errors.New("sequence job ID collision limit exceeded")
}

// Release the plan at terminal publication. Active execution reaches this
// after cleanup and its final read. Identity and diagnostic metadata remain.
func (e *Engine) finishSequenceJobLocked(r *record) {
	entry := e.sequenceJobs.entries[r.state.ID]
	entry.expiresAt = e.sequenceJobs.now().Add(e.sequenceJobs.retention)
	r.program, r.frames = nil, nil
	entry.terminal = e.sequenceJobs.terminals.PushFront(entry)
	for e.sequenceJobs.terminals.Len() > e.sequenceJobs.maxTerminalEntries {
		oldest := e.sequenceJobs.terminals.Back().Value.(*sequenceJobEntry)
		e.evictSequenceJobLocked(oldest)
	}
}

func (e *Engine) touchSequenceJobLocked(entry *sequenceJobEntry) {
	if entry.terminal != nil {
		e.sequenceJobs.terminals.MoveToFront(entry.terminal)
	}
}

func (e *Engine) evictSequenceJobLocked(entry *sequenceJobEntry) {
	if entry.terminal == nil {
		return
	}
	e.sequenceJobs.terminals.Remove(entry.terminal)
	entry.terminal = nil
	delete(e.sequenceJobs.entries, entry.record.state.ID)
	delete(e.jobs, entry.record.state.ID)
}

func (e *Engine) pruneSequenceJobsLocked() {
	store := &e.sequenceJobs
	now := store.now()
	for _, entry := range store.entries {
		if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
			continue
		}
		e.evictSequenceJobLocked(entry)
	}
}

func (e *Engine) SequenceJob(id string) (SequenceJob, error) {
	if !ValidSequenceJobID(id) {
		return SequenceJob{}, fmt.Errorf("%w: invalid job ID", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneSequenceJobsLocked()
	entry := e.sequenceJobs.entries[id]
	if entry == nil {
		return SequenceJob{}, ErrNotFound
	}
	e.touchSequenceJobLocked(entry)
	return entry.record.sequenceSnapshot(), nil
}

func (e *Engine) CancelSequence(ctx context.Context, id string) (SequenceJob, error) {
	return e.cancelSequence(ctx, id, "")
}

// CancelSequenceForDevice checks the accepted serial under the same lock that
// requests cancellation. The guard is independent of the current device route.
func (e *Engine) CancelSequenceForDevice(ctx context.Context, id, serial string) (SequenceJob, error) {
	if !ValidDeviceSerial(serial) {
		return SequenceJob{}, fmt.Errorf("%w: invalid device serial", ErrInvalid)
	}
	return e.cancelSequence(ctx, id, serial)
}

func (e *Engine) cancelSequence(ctx context.Context, id, serial string) (SequenceJob, error) {
	if !ValidSequenceJobID(id) {
		return SequenceJob{}, fmt.Errorf("%w: invalid job ID", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SequenceJob{}, err
	}
	e.pruneSequenceJobsLocked()
	entry := e.sequenceJobs.entries[id]
	if entry == nil {
		return SequenceJob{}, ErrNotFound
	}
	if serial != "" && entry.record.state.Device != serial {
		return SequenceJob{}, ErrSequenceJobTarget
	}
	e.cancelRecordLocked(entry.record)
	e.touchSequenceJobLocked(entry)
	return entry.record.sequenceSnapshot(), nil
}

func (r *record) sequenceSnapshot() SequenceJob {
	state := r.state
	job := SequenceJob{ID: state.ID, Serial: state.Device, TargetSource: r.targetSource, Status: state.Status,
		CompletedSteps: r.completedSteps, TotalSteps: r.totalSteps,
		CreatedAt: state.CreatedAt, Error: state.Error}
	if r.kind == recordFrames {
		job.CompletedSteps, job.TotalSteps = r.completedFrames, r.totalFrames
	}
	if state.StartedAt != nil {
		started := *state.StartedAt
		job.StartedAt = &started
	}
	if state.FinishedAt != nil {
		finished := *state.FinishedAt
		job.FinishedAt = &finished
	}
	return job
}

var _ SequenceService = (*Engine)(nil)
