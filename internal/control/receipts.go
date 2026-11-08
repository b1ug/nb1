package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	receiptWindowMS    = int64(60000)
	receiptRetentionMS = int64(60000)
	receiptMaxEntries  = 4096
	receiptMaxSteps    = 65536
)

type receiptKey struct{ serial, requestID string }
type receiptEntry struct {
	digest                     [32]byte
	record                     *record
	acceptUntilMS, expiresAtMS int64
}
type receiptState struct {
	instanceID                  string
	nowMS                       func() int64
	random                      io.Reader
	entries                     map[receiptKey]*receiptEntry
	steps, maxEntries, maxSteps int
	windowMS, retentionMS       int64
}

// Clock/random injection stays private; production uses monotonic elapsed time
// and crypto/rand. Tests can advance retention without waiting real minutes.
func newReceiptState(random io.Reader, nowMS func() int64) (*receiptState, error) {
	if random == nil {
		random = rand.Reader
	}
	if nowMS == nil {
		origin := time.Now()
		nowMS = func() int64 { return time.Since(origin).Milliseconds() }
	}
	id, err := randomID(random, "daemon_")
	if err != nil {
		return nil, err
	}
	return &receiptState{instanceID: id, nowMS: nowMS, random: random, entries: make(map[receiptKey]*receiptEntry), maxEntries: receiptMaxEntries, maxSteps: receiptMaxSteps, windowMS: receiptWindowMS, retentionMS: receiptRetentionMS}, nil
}

func randomID(random io.Reader, prefix string) (string, error) {
	var data [16]byte
	if _, err := io.ReadFull(random, data[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(data[:]), nil
}

// Instance supplies an optional receipt prefix without reserving cache space.
// Its identity changes on Engine creation, not on device disconnect/recovery.
func (e *Engine) Instance() InstanceInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.receipts
	deadline := s.nowMS() + s.windowMS
	return InstanceInfo{DaemonInstanceID: s.instanceID, Idempotency: IdempotencyInfo{Supported: true, RequestIDPrefix: fmt.Sprintf("req_%d_", deadline), AcceptUntilUptimeMS: deadline, MaxAcceptWindowMS: s.windowMS, ReceiptRetentionMS: s.retentionMS, MaxEntries: s.maxEntries, MaxRetainedSteps: s.maxSteps}}
}

func parseReceiptRequest(input ReceiptRequest) (int64, error) {
	if !validLowerHexID(input.ExpectedDaemonInstanceID, "daemon_") {
		return 0, fmt.Errorf("%w: expected_daemon_instance_id must be daemon_ plus 32 lowercase hexadecimal characters", ErrInvalid)
	}
	parts := strings.Split(input.RequestID, "_")
	if len(parts) != 3 || parts[0] != "req" || !validLowerHexID(parts[2], "") || len(parts[1]) == 0 || len(parts[1]) > 19 || parts[1][0] == '0' {
		return 0, fmt.Errorf("%w: request_id must be req_<positive uptime deadline>_<32 lowercase hexadecimal characters>", ErrInvalid)
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: invalid request deadline", ErrInvalid)
		}
	}
	deadline, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: request deadline overflow", ErrInvalid)
	}
	return deadline, nil
}

func validLowerHexID(id, prefix string) bool {
	if len(id) != len(prefix)+32 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, c := range id[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *receiptState) checkNewDeadline(deadline, now int64) error {
	if deadline <= now {
		return ErrRequestExpired
	}
	if deadline-now > s.windowMS {
		return fmt.Errorf("%w: request deadline exceeds admission window", ErrInvalid)
	}
	return nil
}

// Receipt expiry is independent of normal terminal Job history eviction. Active
// entries are pinned; removing a terminal receipt never cancels its job.
func (s *receiptState) prune(now int64) {
	for key, entry := range s.entries {
		if entry.record.job.FinishedAt != nil && entry.expiresAtMS <= now {
			delete(s.entries, key)
			s.steps -= len(entry.record.job.Program.Steps)
			entry.record.receipt = nil
		}
	}
}

// Request resolves an accepted submission even after ordinary history eviction
// or Engine shutdown. An absent expired key cannot reveal its original outcome.
func (e *Engine) Request(serial, requestID, expectedDaemonInstanceID string) (Job, error) {
	deadline, err := parseReceiptRequest(ReceiptRequest{RequestID: requestID, ExpectedDaemonInstanceID: expectedDaemonInstanceID})
	if err != nil {
		return Job{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.receipts
	if expectedDaemonInstanceID != s.instanceID {
		return Job{}, ErrInstanceChanged
	}
	now := s.nowMS()
	s.prune(now)
	if entry := s.entries[receiptKey{serial, requestID}]; entry != nil {
		return cloneJob(entry.record.job), nil
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return Job{}, err
	}
	return Job{}, ErrRequestNotFound
}

// SubmitWithReceipt atomically deduplicates logical job admission in this
// Engine. It is in-memory only and never retries a failed or cancelled program.
func (e *Engine) SubmitWithReceipt(ctx context.Context, serial string, program Program, policy string, input ReceiptRequest) (ReceiptResult, error) {
	if err := ctx.Err(); err != nil {
		return ReceiptResult{}, err
	}
	p, policy, err := normalizeSubmission(program, policy)
	if err != nil {
		return ReceiptResult{}, err
	}
	deadline, err := parseReceiptRequest(input)
	if err != nil {
		return ReceiptResult{}, err
	}
	canonical, err := json.Marshal(struct {
		Version int
		Serial  string
		Program Program
		Policy  string
	}{1, serial, p, policy})
	if err != nil {
		return ReceiptResult{}, err
	}
	digest := sha256.Sum256(canonical)
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.receipts
	if input.ExpectedDaemonInstanceID != s.instanceID {
		return ReceiptResult{}, ErrInstanceChanged
	}
	now := s.nowMS()
	s.prune(now)
	key := receiptKey{serial, input.RequestID}
	if entry := s.entries[key]; entry != nil {
		if entry.digest != digest {
			return ReceiptResult{}, ErrRequestConflict
		}
		return ReceiptResult{Job: cloneJob(entry.record.job), Replayed: true, DaemonInstanceID: s.instanceID}, nil
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return ReceiptResult{}, err
	}
	if len(s.entries) >= s.maxEntries || len(p.Steps) > s.maxSteps-s.steps {
		return ReceiptResult{}, ErrReceiptCapacity
	}
	w, err := e.canAdmitLocked(serial, policy)
	if err != nil {
		return ReceiptResult{}, err
	}
	r, err := e.newRecordLocked(serial, p)
	if err != nil {
		return ReceiptResult{}, err
	}
	entry := &receiptEntry{digest: digest, record: r, acceptUntilMS: deadline}
	r.receipt = entry
	s.entries[key] = entry
	s.steps += len(p.Steps)
	e.publishLocked(w, r, policy)
	return ReceiptResult{Job: cloneJob(r.job), DaemonInstanceID: s.instanceID}, nil
}

func (e *Engine) newRecordLocked(serial string, p Program) (*record, error) {
	for attempt := 0; attempt < 4; attempt++ {
		id, err := randomID(e.receipts.random, "job_")
		if err != nil {
			return nil, err
		}
		if _, exists := e.jobs[id]; exists {
			continue
		}
		collision := false
		for _, entry := range e.receipts.entries {
			if entry.record.job.ID == id {
				collision = true
				break
			}
		}
		if !collision {
			return &record{job: Job{ID: id, Device: serial, Status: "queued", Program: p, TotalSteps: len(p.Steps) * p.Repeat, CreatedAt: time.Now().UTC()}, cancel: make(chan struct{})}, nil
		}
	}
	return nil, errors.New("random job ID collision limit exceeded")
}

var _ ReceiptService = (*Engine)(nil)
