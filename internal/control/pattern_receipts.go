package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/b1ug/nb1/internal/patternidentity"
)

const patternAdmissionDomain = "nb1-pattern-admission/experimental-v1"

func validatePatternRequest(serial string, ref patternidentity.Ref, input ReceiptRequest) (int64, error) {
	if !ValidDeviceSerial(serial) {
		return 0, fmt.Errorf("%w: explicit valid Pattern device serial required", ErrInvalid)
	}
	if err := patternidentity.ValidateRef(ref); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return parseReceiptRequest(input)
}

func patternAdmissionDigest(serial string, ref patternidentity.Ref, policy string, execution PatternExecution, digest string) ([32]byte, error) {
	data, err := json.Marshal(map[string]interface{}{
		"origin": "pattern_v2", "serial": serial, "policy": policy,
		"pattern_ref":    map[string]interface{}{"record_contract": ref.RecordContract, "id": ref.ID, "revision": ref.Revision},
		"execution_kind": execution.Kind, "execution_digest": digest,
	})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(append([]byte(patternAdmissionDomain+"\n"), data...)), nil
}

func preparePatternRecord(serial string, ref patternidentity.Ref, policy string, resolved ResolvedPattern) (*record, [32]byte, error) {
	if resolved.Ref != ref {
		return nil, [32]byte{}, patternIntegrityError(fmt.Errorf("%w: resolver returned a different Pattern ref", ErrInvalid))
	}
	execution, err := ValidatePatternExecution(resolved.Execution)
	if err != nil {
		return nil, [32]byte{}, patternIntegrityError(err)
	}
	if err := execution.VerifyDigest(resolved.ExecutionDigest); err != nil {
		return nil, [32]byte{}, patternIntegrityError(err)
	}
	digest, err := patternAdmissionDigest(serial, ref, policy, execution, resolved.ExecutionDigest)
	if err != nil {
		return nil, [32]byte{}, patternIntegrityError(err)
	}
	r := &record{origin: originPatternV2, pattern: patternAdmissionIdentity{ref: ref, executionDigest: resolved.ExecutionDigest, policy: policy}, cancel: make(chan struct{})}
	if execution.Program != nil {
		r.kind, r.program = recordSequential, execution.Program
		r.totalSteps = len(r.program.Steps) * r.program.Repeat
	} else {
		r.kind, r.frames = recordFrames, execution.FrameProgram
		facts, err := frameProgramFacts(*r.frames)
		if err != nil {
			return nil, [32]byte{}, patternIntegrityError(err)
		}
		r.totalFrames, r.plannedTargetWrites = int(facts.totalFrames), int(facts.plannedTargetWrites)
	}
	return r, digest, nil
}

// patternReceiptLocked never consults storage. An optional policy binds retries;
// receipt queries/cancel use the already accepted record's immutable policy.
func (e *Engine) patternReceiptLocked(serial string, input ReceiptRequest, ref patternidentity.Ref, policy string) (*record, error) {
	entry := e.receipts.entries[receiptKey{serial, input.RequestID}]
	if entry == nil {
		return nil, nil
	}
	r := entry.record
	if r.origin != originPatternV2 || r.pattern.ref != ref || (policy != "" && r.pattern.policy != policy) {
		return nil, ErrRequestConflict
	}
	return r, nil
}

func (e *Engine) patternResultLocked(r *record, replayed bool) PatternReceiptResult {
	return PatternReceiptResult{Job: r.patternSnapshot(), Replayed: replayed, DaemonInstanceID: e.receipts.instanceID}
}

// SubmitPatternWithReceipt preserves a verified source identity at the same
// linearization point as its receipt and worker queue publication. No old Submit
// is called, and no side table or in-flight reservation is created.
func (e *Engine) SubmitPatternWithReceipt(ctx context.Context, serial string, ref patternidentity.Ref, policy string, input ReceiptRequest, resolve PatternResolver) (PatternReceiptResult, error) {
	if err := ctx.Err(); err != nil {
		return PatternReceiptResult{}, err
	}
	if policy != "replace" && policy != "queue" && policy != "reject" {
		return PatternReceiptResult{}, fmt.Errorf("%w: Pattern admission requires explicit replace, queue or reject", ErrInvalid)
	}
	deadline, err := validatePatternRequest(serial, ref, input)
	if err != nil {
		return PatternReceiptResult{}, err
	}

	// First receipt lookup: accepted work must not depend on a current store,
	// device connection, admission deadline, queue capacity or open runtime.
	e.mu.Lock()
	s := e.receipts
	if input.ExpectedDaemonInstanceID != s.instanceID {
		e.mu.Unlock()
		return PatternReceiptResult{}, ErrInstanceChanged
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return PatternReceiptResult{}, err
	}
	now := s.nowMS()
	s.prune(now)
	prior, err := e.patternReceiptLocked(serial, input, ref, policy)
	if err != nil || prior != nil {
		var result PatternReceiptResult
		if prior != nil {
			result = e.patternResultLocked(prior, true)
		}
		e.mu.Unlock()
		return result, err
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		e.mu.Unlock()
		return PatternReceiptResult{}, err
	}
	if len(s.entries) >= s.maxEntries {
		e.mu.Unlock()
		return PatternReceiptResult{}, ErrReceiptCapacity
	}
	if _, err := e.canAdmitLocked(serial, policy); err != nil {
		e.mu.Unlock()
		return PatternReceiptResult{}, err
	}
	e.mu.Unlock()

	// No Engine or worker lock is held across source verification or storage.
	// Each call invokes its resolver at most once, even when many calls miss the
	// same key concurrently. Only publication, not Resolve, is deduplicated.
	var candidate *record
	var digest [32]byte
	var resolveErr error
	if err := ctx.Err(); err != nil {
		resolveErr = err
	} else if resolve == nil {
		resolveErr = &PatternResolveError{Kind: PatternResolveConfiguration, Err: fmt.Errorf("%w: Pattern resolver required for a new request", ErrInvalid)}
	} else {
		var resolved ResolvedPattern
		resolved, resolveErr = resolve(ctx, ref)
		if resolveErr == nil {
			candidate, digest, resolveErr = preparePatternRecord(serial, ref, policy, resolved)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	s = e.receipts
	if input.ExpectedDaemonInstanceID != s.instanceID {
		return PatternReceiptResult{}, ErrInstanceChanged
	}
	now = s.nowMS()
	s.prune(now)
	// Corrupt source/Ref/frozen execution/digest, unsupported/configuration and
	// unknown errors cannot be erased by a concurrent good receipt. Callers may
	// separately query the original ticket while retaining this diagnostic.
	if resolveErr != nil && !replayablePatternResolution(resolveErr) {
		return PatternReceiptResult{}, resolveErr
	}
	// A resolver may ignore its context. Preserve that caller's cancellation
	// even when another call admitted an exact receipt while it was resolving.
	if err := ctx.Err(); err != nil {
		return PatternReceiptResult{}, err
	}
	prior, err = e.patternReceiptLocked(serial, input, ref, policy)
	if err != nil {
		return PatternReceiptResult{}, err
	}
	if prior != nil {
		if resolveErr == nil && prior.receipt.digest != digest {
			return PatternReceiptResult{}, ErrRequestConflict
		}
		return e.patternResultLocked(prior, true), nil
	}
	if err := ctx.Err(); err != nil {
		return PatternReceiptResult{}, err
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return PatternReceiptResult{}, err
	}
	if resolveErr != nil {
		return PatternReceiptResult{}, resolveErr
	}
	cost := candidate.retainedCost()
	if len(s.entries) >= s.maxEntries || cost > s.maxSteps-s.steps {
		return PatternReceiptResult{}, ErrReceiptCapacity
	}
	w, err := e.canAdmitLocked(serial, policy)
	if err != nil {
		return PatternReceiptResult{}, err
	}
	if err := patternDeviceExecution(candidate, &w.state); err != nil {
		return PatternReceiptResult{}, err
	}
	prefix := "job_"
	if candidate.kind == recordFrames {
		prefix = "framejob_"
	}
	candidate.state, err = e.newRecordStateLocked(serial, prefix)
	if err != nil {
		return PatternReceiptResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PatternReceiptResult{}, err
	}
	if err := s.checkNewDeadline(deadline, s.nowMS()); err != nil {
		return PatternReceiptResult{}, err
	}
	// Nothing after this point may fail admission. In particular replacement
	// side effects happen only after source, capacity, capabilities and ID pass.
	entry := &receiptEntry{digest: digest, record: candidate, acceptUntilMS: deadline}
	candidate.receipt = entry
	s.entries[receiptKey{serial, input.RequestID}] = entry
	s.steps += cost
	e.publishLocked(w, candidate, policy)
	return e.patternResultLocked(candidate, false), nil
}

func patternDeviceExecution(r *record, state *DeviceState) error {
	if r.kind == recordFrames {
		availability := FrameExecutionForDevice(state)
		if !availability.Available {
			return fmt.Errorf("%w: Pattern frame execution unavailable: %s", ErrInvalid, availability.Reason)
		}
		if r.frames.OnFinish == "hold" {
			for _, capability := range state.Capabilities {
				if capability == "hold" {
					return nil
				}
			}
			return fmt.Errorf("%w: Pattern hold capability missing", ErrInvalid)
		}
		return nil
	}
	preview, err := Preview(r.program, nil)
	if err != nil {
		return err
	}
	checked := WithDeviceSnapshot(preview, state.Serial, state)
	if checked.Validation.Device != "supported" {
		return fmt.Errorf("%w: Pattern program execution unavailable: %s", ErrInvalid, checked.Validation.Reason)
	}
	return nil
}

// PatternRequest resolves only the original in-memory receipt. Store failures,
// ordinary history eviction and runtime closure cannot strip its provenance.
func (e *Engine) PatternRequest(serial string, input ReceiptRequest, ref patternidentity.Ref) (PatternJobEnvelope, error) {
	deadline, err := validatePatternRequest(serial, ref, input)
	if err != nil {
		return PatternJobEnvelope{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.patternRequestLocked(serial, input, ref, deadline)
}

func (e *Engine) patternRequestLocked(serial string, input ReceiptRequest, ref patternidentity.Ref, deadline int64) (PatternJobEnvelope, error) {
	s := e.receipts
	if input.ExpectedDaemonInstanceID != s.instanceID {
		return PatternJobEnvelope{}, ErrInstanceChanged
	}
	now := s.nowMS()
	s.prune(now)
	r, err := e.patternReceiptLocked(serial, input, ref, "")
	if err != nil {
		return PatternJobEnvelope{}, err
	}
	if r != nil {
		return r.patternSnapshot(), nil
	}
	if err := s.checkNewDeadline(deadline, now); err != nil {
		return PatternJobEnvelope{}, err
	}
	return PatternJobEnvelope{}, ErrRequestNotFound
}

// CancelPatternRequest binds source and original receipt under the same lock as
// cancellation. A known ID is an additional check, never a replacement ticket.
func (e *Engine) CancelPatternRequest(ctx context.Context, serial string, input ReceiptRequest, ref patternidentity.Ref, knownID string) (PatternJobEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return PatternJobEnvelope{}, err
	}
	deadline, err := validatePatternRequest(serial, ref, input)
	if err != nil {
		return PatternJobEnvelope{}, err
	}
	if knownID != "" && !validLowerHexID(knownID, "job_") && !validLowerHexID(knownID, "framejob_") {
		return PatternJobEnvelope{}, fmt.Errorf("%w: invalid known Pattern Job ID", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = e.patternRequestLocked(serial, input, ref, deadline)
	if err != nil {
		return PatternJobEnvelope{}, err
	}
	r := e.receipts.entries[receiptKey{serial, input.RequestID}].record
	if knownID != "" && knownID != r.state.ID {
		return PatternJobEnvelope{}, ErrRequestConflict
	}
	if err := ctx.Err(); err != nil {
		return PatternJobEnvelope{}, err
	}
	e.cancelRecordLocked(r)
	return r.patternSnapshot(), nil
}

var _ PatternService = (*Engine)(nil)
