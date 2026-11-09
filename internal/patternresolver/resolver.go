// Package patternresolver connects an explicitly selected verified Pattern v2
// store to control's source-bound admission. It registers no transport, selects
// no path and never owns an Engine, device or user configuration.
package patternresolver

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// Store deliberately exposes only exact resolution. Inspection, a current
// revision guess, a directory path or a source-only record cannot substitute.
type Store interface {
	Resolve(id, revision string) (patternsv2.Record, error)
}

var (
	ErrConfiguration = errors.New("Pattern v2 resolver store is not configured")
	ErrReference     = errors.New("resolved Pattern does not match requested ref")
)

// New constructs a lazy callback, without calling the store. Missing/typed-nil
// storage is reported only when a genuinely new key invokes it; existing core
// receipts can still be replayed, queried or cancelled without storage.
func New(store Store) control.PatternResolver {
	return func(ctx context.Context, ref patternidentity.Ref) (control.ResolvedPattern, error) {
		if err := ctx.Err(); err != nil {
			return control.ResolvedPattern{}, err
		}
		if err := patternidentity.ValidateRef(ref); err != nil {
			return control.ResolvedPattern{}, classified(control.PatternResolveIntegrity, err)
		}
		if missingStore(store) {
			return control.ResolvedPattern{}, classified(control.PatternResolveConfiguration, ErrConfiguration)
		}
		record, err := store.Resolve(ref.ID, ref.Revision)
		if err != nil {
			failure := classifyStoreError(err)
			// Keep a known integrity/configuration/unsupported diagnostic even if
			// cancellation raced it. Availability cannot hide cancelled waiting.
			if failure.Kind == control.PatternResolveAvailability {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return control.ResolvedPattern{}, ctxErr
				}
			}
			return control.ResolvedPattern{}, failure
		}
		if err := ctx.Err(); err != nil {
			return control.ResolvedPattern{}, err
		}
		if record.Contract != ref.RecordContract || record.ID != ref.ID || record.Revision != ref.Revision {
			return control.ResolvedPattern{}, classified(control.PatternResolveIntegrity, ErrReference)
		}
		if _, err := executionFromWire(record.Compiled.Execution); err != nil {
			return control.ResolvedPattern{}, err
		}
		// Do not trust even a Store-shaped interface to have verified P1 data.
		// NormalizeRecord checks bounded shape, canonical revision, author
		// grammar, recipe, normalized action and exact frozen execution. Unknown
		// source/recipe fails this verified path; no Inspection is accepted.
		verified, err := patternsv2.NormalizeRecord(record)
		if err != nil {
			return control.ResolvedPattern{}, classifyStoreError(err)
		}
		digest, err := patternsv2.ExecutionDigest(verified)
		if err != nil {
			return control.ResolvedPattern{}, classifyStoreError(err)
		}
		execution, err := executionFromWire(verified.Compiled.Execution)
		if err != nil {
			return control.ResolvedPattern{}, err
		}
		execution, err = control.ValidatePatternExecution(execution)
		if err != nil {
			return control.ResolvedPattern{}, classified(control.PatternResolveIntegrity, err)
		}
		if err := execution.VerifyDigest(digest); err != nil {
			return control.ResolvedPattern{}, classified(control.PatternResolveIntegrity, err)
		}
		if err := ctx.Err(); err != nil {
			return control.ResolvedPattern{}, err
		}
		return control.ResolvedPattern{Ref: ref, Execution: execution, ExecutionDigest: digest}, nil
	}
}

func missingStore(store Store) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func classified(kind control.PatternResolveErrorKind, err error) *control.PatternResolveError {
	return &control.PatternResolveError{Kind: kind, Err: err}
}

// Preserve the explicit P1 execution contract; deriving it solely from Kind
// could otherwise hide a poisoned/future wire contract behind a valid core union.
func executionFromWire(wire patternsv2.Execution) (control.PatternExecution, error) {
	switch {
	case wire.Kind == "program" && wire.Contract == control.CompileContract && wire.Program != nil && wire.FrameProgram == nil:
		return control.PatternExecution{Kind: "program", Program: wire.Program}, nil
	case wire.Kind == "frame_program" && wire.Contract == control.FrameContract && wire.FrameProgram != nil && wire.Program == nil:
		return control.PatternExecution{Kind: "frame_program", FrameProgram: wire.FrameProgram}, nil
	default:
		return control.PatternExecution{}, classified(control.PatternResolveIntegrity, fmt.Errorf("%w: frozen execution contract/union mismatch", control.ErrInvalid))
	}
}
