package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/b1ug/nb1/internal/patternidentity"
)

// ResolvedPattern is supplied only by a trusted, full source/recipe verifier.
// The Engine independently validates Ref, frozen execution and its digest, but
// cannot reconstruct an author's source from this bounded admission input.
type ResolvedPattern struct {
	Ref             patternidentity.Ref
	Execution       PatternExecution
	ExecutionDigest string
}

// PatternResolver runs at most once per Submit call, and only after a receipt
// miss. It runs outside Engine.mu and must release its storage transaction before
// returning. An accepted receipt is independent of this callback thereafter.
type PatternResolver func(context.Context, patternidentity.Ref) (ResolvedPattern, error)

type PatternResolveErrorKind string

const (
	PatternResolveAvailability  PatternResolveErrorKind = "availability"
	PatternResolveIntegrity     PatternResolveErrorKind = "integrity"
	PatternResolveUnsupported   PatternResolveErrorKind = "unsupported"
	PatternResolveConfiguration PatternResolveErrorKind = "configuration"
	PatternResolveUnknown       PatternResolveErrorKind = "unknown"
)

// PatternResolveError preserves the diagnostic and an explicit classification.
// Only a whitelisted availability failure can yield to a concurrently accepted,
// exactly matching receipt. Unknown errors and all integrity failures fail closed.
// Adapters must classify unsafe/corrupt data before any underlying I/O cause.
type PatternResolveError struct {
	Kind PatternResolveErrorKind
	Err  error
}

func (e *PatternResolveError) Error() string {
	if e == nil {
		return "pattern resolution failed"
	}
	if e.Err == nil {
		return "pattern resolution failed: " + string(e.Kind)
	}
	return fmt.Sprintf("pattern resolution %s: %v", e.Kind, e.Err)
}
func (e *PatternResolveError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func patternIntegrityError(err error) error {
	return &PatternResolveError{Kind: PatternResolveIntegrity, Err: err}
}

func replayablePatternResolution(err error) bool {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, patternidentity.ErrInvalid) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Inspect all branches, including Go errors with multi-Unwrap. An outer
	// availability classification must not hide a nested integrity failure;
	// unclassified parallel branches are not themselves availability evidence.
	var inspect func(error, bool, int) bool
	inspect = func(cause error, covered bool, depth int) bool {
		if cause == nil {
			return covered
		}
		if depth > 64 || cause == ErrInvalid || cause == patternidentity.ErrInvalid ||
			cause == context.Canceled || cause == context.DeadlineExceeded {
			return false
		}
		if classified, ok := cause.(*PatternResolveError); ok {
			if classified == nil || classified.Kind != PatternResolveAvailability {
				return false
			}
			covered = true
		}
		if multiple, ok := cause.(interface{ Unwrap() []error }); ok {
			children := multiple.Unwrap()
			if len(children) == 0 {
				return covered
			}
			for _, child := range children {
				if !inspect(child, covered, depth+1) {
					return false
				}
			}
			return true
		}
		if child := errors.Unwrap(cause); child != nil {
			return inspect(child, covered, depth+1)
		}
		return covered
	}
	return inspect(err, false, 0)
}

// PatternJobEnvelope adds provenance outside the unchanged Job/FrameJob wire
// shape. Exactly the selected typed snapshot is present; callers cannot submit
// this envelope as arbitrary execution input.
type PatternJobEnvelope struct {
	PatternRef      patternidentity.Ref `json:"pattern_ref"`
	ExecutionDigest string              `json:"execution_digest"`
	JobKind         string              `json:"job_kind"`
	ProgramJob      *Job                `json:"-"`
	FrameJob        *FrameJob           `json:"-"`
}

func (e PatternJobEnvelope) MarshalJSON() ([]byte, error) {
	if err := patternidentity.ValidateRef(e.PatternRef); err != nil {
		return nil, fmt.Errorf("%w: invalid Pattern envelope ref", ErrInvalid)
	}
	if err := patternidentity.ValidateDigest(e.ExecutionDigest); err != nil {
		return nil, fmt.Errorf("%w: invalid Pattern envelope digest", ErrInvalid)
	}
	var job interface{}
	switch {
	case e.JobKind == "program" && e.ProgramJob != nil && e.FrameJob == nil:
		job = e.ProgramJob
	case e.JobKind == "frame" && e.FrameJob != nil && e.ProgramJob == nil:
		job = e.FrameJob
	default:
		return nil, fmt.Errorf("%w: Pattern envelope requires exactly its selected Job", ErrInvalid)
	}
	return json.Marshal(struct {
		PatternRef      patternidentity.Ref `json:"pattern_ref"`
		ExecutionDigest string              `json:"execution_digest"`
		JobKind         string              `json:"job_kind"`
		Job             interface{}         `json:"job"`
	}{e.PatternRef, e.ExecutionDigest, e.JobKind, job})
}

type PatternReceiptResult struct {
	Job              PatternJobEnvelope `json:"job"`
	Replayed         bool               `json:"replayed"`
	DaemonInstanceID string             `json:"daemon_instance_id"`
}

// PatternService is opt-in; old interfaces and bare Job schemas stay unchanged.
// Query and cancel require the original receipt and never depend on storage.
type PatternService interface {
	Instance() InstanceInfo
	SubmitPatternWithReceipt(context.Context, string, patternidentity.Ref, string, ReceiptRequest, PatternResolver) (PatternReceiptResult, error)
	PatternRequest(string, ReceiptRequest, patternidentity.Ref) (PatternJobEnvelope, error)
	CancelPatternRequest(context.Context, string, ReceiptRequest, patternidentity.Ref, string) (PatternJobEnvelope, error)
}
