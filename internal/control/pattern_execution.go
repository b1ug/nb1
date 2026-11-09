package control

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/b1ug/nb1/internal/patternidentity"
)

// PatternExecution is a closed typed union of already frozen execution data.
// Its outer contract is derived from Kind. It carries no source verification,
// Ref, arbitrary JSON, device capability or permission to admit a job.
type PatternExecution struct {
	Kind         string
	Program      *Program
	FrameProgram *FrameProgram
}

// ValidatePatternExecution returns a deep copy only if the input is already
// fully normalized. It rejects defaults or RGB changes rather than changing the
// execution under an existing source identity. All original budgets apply.
func ValidatePatternExecution(input PatternExecution) (PatternExecution, error) {
	switch {
	case input.Kind == "program" && input.Program != nil && input.FrameProgram == nil:
		p, err := Normalize(*input.Program)
		if err != nil {
			return PatternExecution{}, err
		}
		if !reflect.DeepEqual(*input.Program, p) {
			return PatternExecution{}, fmt.Errorf("%w: frozen Program is not fully normalized", ErrInvalid)
		}
		return PatternExecution{Kind: "program", Program: &p}, nil
	case input.Kind == "frame_program" && input.FrameProgram != nil && input.Program == nil:
		p, err := NormalizeFrameProgram(*input.FrameProgram)
		if err != nil {
			return PatternExecution{}, err
		}
		if !reflect.DeepEqual(*input.FrameProgram, p) {
			return PatternExecution{}, fmt.Errorf("%w: frozen FrameProgram is not fully normalized", ErrInvalid)
		}
		return PatternExecution{Kind: "frame_program", FrameProgram: &p}, nil
	default:
		return PatternExecution{}, fmt.Errorf("%w: PatternExecution requires exactly its selected Program or FrameProgram", ErrInvalid)
	}
}

// CanonicalJSON encodes only the validated, closed ASCII/integer subset. Every
// object layer is a map with fixed keys, so encoding/json's lexical map order
// matches P1 canonical bytes. No generic parser, floats or struct field-order
// assumptions are used. Zero fields and array order are preserved.
func (input PatternExecution) CanonicalJSON() ([]byte, error) {
	e, err := ValidatePatternExecution(input)
	if err != nil {
		return nil, err
	}
	var program map[string]interface{}
	contract := CompileContract
	if e.Program != nil {
		p := e.Program
		steps := make([]interface{}, len(p.Steps))
		for i, s := range p.Steps {
			steps[i] = map[string]interface{}{"led": s.LED, "color": s.Color, "fade_ms": s.FadeMS, "hold_ms": s.HoldMS}
		}
		program = map[string]interface{}{"steps": steps, "repeat": p.Repeat, "on_finish": p.OnFinish}
	} else {
		p := e.FrameProgram
		contract = FrameContract
		frames := make([]interface{}, len(p.Frames))
		for i, f := range p.Frames {
			frames[i] = map[string]interface{}{"targets": patternTargetNodes(f.Targets), "fade_ms": f.FadeMS, "interval_ms": f.IntervalMS}
		}
		program = map[string]interface{}{"contract": p.Contract, "initial": patternTargetNodes(p.Initial), "frames": frames, "repeat": p.Repeat, "on_finish": p.OnFinish}
	}
	return json.Marshal(map[string]interface{}{"kind": e.Kind, "contract": contract, "program": program})
}

func patternTargetNodes(targets []FrameTarget) []interface{} {
	values := make([]interface{}, len(targets))
	for i, t := range targets {
		values[i] = map[string]interface{}{"led": t.LED, "color": t.Color}
	}
	return values
}

// Digest always recomputes the fixed execution-domain hash from validated
// canonical bytes; it never accepts a caller-provided digest as proof.
func (input PatternExecution) Digest() (string, error) {
	b, err := input.CanonicalJSON()
	if err != nil {
		return "", err
	}
	return patternidentity.HashExecutionCanonical(b), nil
}

// VerifyDigest compares the exact expected digest after recomputing it. A
// mismatch is rejected, never repaired. Neither match nor validation proves
// that an author Action compiled into this data; the P1 adapter owns that check.
func (input PatternExecution) VerifyDigest(expected string) error {
	if err := patternidentity.ValidateDigest(expected); err != nil {
		return fmt.Errorf("%w: invalid expected execution digest", ErrInvalid)
	}
	actual, err := input.Digest()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%w: execution digest mismatch", ErrInvalid)
	}
	return nil
}
