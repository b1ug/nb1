package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func (e *Engine) SubmitFrameWithReceipt(ctx context.Context, serial string, program FrameProgram, policy string, input ReceiptRequest) (FrameReceiptResult, error) {
	if err := ctx.Err(); err != nil {
		return FrameReceiptResult{}, err
	}
	if policy != "reject" || !ValidDeviceSerial(serial) {
		return FrameReceiptResult{}, invalidFrame("submission requires explicit serial and policy reject")
	}
	p, err := NormalizeFrameProgram(program)
	if err != nil {
		return FrameReceiptResult{}, err
	}
	deadline, err := parseReceiptRequest(input)
	if err != nil {
		return FrameReceiptResult{}, err
	}
	canonical, err := json.Marshal(struct {
		Serial  string
		Program FrameProgram
		Policy  string
	}{serial, p, policy})
	if err != nil {
		return FrameReceiptResult{}, err
	}
	// Distinct domain, same key space. Existing sequential receipt bytes and
	// digests remain untouched, and cross-kind key reuse is always a conflict.
	digest := sha256.Sum256(append([]byte("nb1-frame-admission/experimental-v1\n"), canonical...))
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.receipts
	if input.ExpectedDaemonInstanceID != s.instanceID {
		return FrameReceiptResult{}, ErrInstanceChanged
	}
	now := s.nowMS()
	s.prune(now)
	key := receiptKey{serial, input.RequestID}
	if entry := s.entries[key]; entry != nil {
		if entry.record.origin != originLegacy || entry.record.kind != recordFrames || entry.digest != digest {
			return FrameReceiptResult{}, ErrRequestConflict
		}
		return FrameReceiptResult{Job: entry.record.frameSnapshot(), Replayed: true, DaemonInstanceID: s.instanceID}, nil
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return FrameReceiptResult{}, err
	}
	r := &record{kind: recordFrames, frames: &p, cancel: make(chan struct{})}
	cost := r.retainedCost()
	if len(s.entries) >= s.maxEntries || cost > s.maxSteps-s.steps {
		return FrameReceiptResult{}, ErrReceiptCapacity
	}
	if err := ctx.Err(); err != nil {
		return FrameReceiptResult{}, err
	}
	w, err := e.canAdmitLocked(serial, policy)
	if err != nil {
		return FrameReceiptResult{}, err
	}
	if availability := FrameExecutionForDevice(&w.state); !availability.Available {
		return FrameReceiptResult{}, fmt.Errorf("%w: frame execution unavailable: %s", ErrInvalid, availability.Reason)
	}
	r.state, err = e.newRecordStateLocked(serial, "framejob_")
	if err != nil {
		return FrameReceiptResult{}, err
	}
	facts, err := frameProgramFacts(p)
	if err != nil {
		return FrameReceiptResult{}, err
	}
	r.totalFrames, r.plannedTargetWrites = int(facts.totalFrames), int(facts.plannedTargetWrites)
	entry := &receiptEntry{digest: digest, record: r, acceptUntilMS: deadline}
	r.receipt = entry
	s.entries[key] = entry
	s.steps += cost
	e.publishLocked(w, r, policy)
	return FrameReceiptResult{Job: r.frameSnapshot(), DaemonInstanceID: s.instanceID}, nil
}

func (e *Engine) FrameRequest(serial, requestID, expectedDaemonInstanceID string) (FrameJob, error) {
	deadline, err := parseReceiptRequest(ReceiptRequest{RequestID: requestID, ExpectedDaemonInstanceID: expectedDaemonInstanceID})
	if err != nil {
		return FrameJob{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.receipts
	if expectedDaemonInstanceID != s.instanceID {
		return FrameJob{}, ErrInstanceChanged
	}
	now := s.nowMS()
	s.prune(now)
	if entry := s.entries[receiptKey{serial, requestID}]; entry != nil {
		if entry.record.origin != originLegacy || entry.record.kind != recordFrames {
			return FrameJob{}, ErrNotFound
		}
		return entry.record.frameSnapshot(), nil
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return FrameJob{}, err
	}
	return FrameJob{}, ErrRequestNotFound
}
