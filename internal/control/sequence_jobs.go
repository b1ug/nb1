package control

import (
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
	ErrSequenceJobCapacity = errors.New("sequence job capacity exhausted")
	ErrSequenceCapability  = errors.New("sequence device capability missing")
)

const (
	SequenceJobRetention                       = 30 * time.Minute
	sequenceJobMaxEntries                      = 4096
	sequenceJobMaxRetainedTargets              = 65536
	originSequenceJob             recordOrigin = originPatternV2 + 1
)

// SequenceJob is the current status of an accepted v1 job. TotalSteps counts
// repeated steps; initialization and final cleanup do not count as steps.
type SequenceJob struct {
	ID             string     `json:"id"`
	Serial         string     `json:"serial"`
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
}

type sequenceJobStore struct {
	entries         map[string]*sequenceJobEntry
	retainedTargets int
	maxEntries      int
	maxTargets      int
	now             func() time.Time
	random          io.Reader
}

func newSequenceJobStore() sequenceJobStore {
	return sequenceJobStore{
		entries:    make(map[string]*sequenceJobEntry),
		maxEntries: sequenceJobMaxEntries,
		maxTargets: sequenceJobMaxRetainedTargets,
		now:        time.Now,
		random:     rand.Reader,
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
	if !ValidDeviceSerial(serial) || (id != "" && !ValidSequenceJobID(id)) {
		return SequenceJob{}, false, fmt.Errorf("%w: invalid serial or job ID", ErrInvalid)
	}
	if id != "" {
		e.mu.Lock()
		e.pruneSequenceJobsLocked()
		job, replayed, err := e.sequenceReplayLocked(serial, id, fingerprint)
		e.mu.Unlock()
		if replayed || err != nil {
			return job, replayed, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SequenceJob{}, false, err
	}

	input, candidate, prepareErr := prepareSequenceJob(prepare)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneSequenceJobsLocked()
	if id != "" {
		if job, replayed, err := e.sequenceReplayLocked(serial, id, fingerprint); replayed || err != nil {
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
	cost := candidate.retainedCost()
	if len(store.entries) >= store.maxEntries || cost > store.maxTargets-store.retainedTargets {
		return SequenceJob{}, false, ErrSequenceJobCapacity
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
	store.entries[id] = &sequenceJobEntry{fingerprint: fingerprint, record: candidate}
	store.retainedTargets += cost
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
	if entry.record.state.Device != serial || entry.fingerprint != fingerprint {
		return SequenceJob{}, false, ErrSequenceJobConflict
	}
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

func (e *Engine) pruneSequenceJobsLocked() {
	store := &e.sequenceJobs
	now := store.now()
	for id, entry := range store.entries {
		if entry.expiresAt.IsZero() || now.Before(entry.expiresAt) {
			continue
		}
		delete(store.entries, id)
		delete(e.jobs, id)
		store.retainedTargets -= entry.record.retainedCost()
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
	return entry.record.sequenceSnapshot(), nil
}

func (e *Engine) CancelSequence(ctx context.Context, id string) (SequenceJob, error) {
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
	e.cancelRecordLocked(entry.record)
	return entry.record.sequenceSnapshot(), nil
}

func (r *record) sequenceSnapshot() SequenceJob {
	state := r.state
	job := SequenceJob{ID: state.ID, Serial: state.Device, Status: state.Status,
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
