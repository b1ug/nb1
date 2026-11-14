// Package patternsv2 implements experimental, bounded Pattern records only.
// It performs no persistence, device discovery, admission or execution.
package patternsv2

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
)

const (
	RecordContract  = patternidentity.RecordContract
	SnapshotKind    = "nb1-pattern-snapshot/experimental-v2"
	ExecutionDomain = patternidentity.ExecutionDomain
	SchemaVersion   = 2
	MaxBytes        = 1 << 20
	MaxRecords      = 128
	MaxDepth        = 32
)

var (
	ErrInvalid     = errors.New("invalid experimental pattern")
	ErrUnsupported = errors.New("unsupported experimental pattern verifier")
)

// Source retains the author's JSON meaning, including color notation and
// omitted defaults. Action is canonical JSON, not the original file bytes.
type Source struct {
	Kind     string          `json:"kind"`
	Contract string          `json:"contract"`
	Action   json.RawMessage `json:"action"`
}

// Execution is a typed union. Exactly the matching Program/FrameProgram is
// populated; the wire shape always has one "program" field. It is data, not an
// execution capability, and even a verified record does not qualify hardware.
type Execution struct {
	Kind         string                `json:"-"`
	Contract     string                `json:"-"`
	Program      *control.Program      `json:"-"`
	FrameProgram *control.FrameProgram `json:"-"`
}

func (e Execution) MarshalJSON() ([]byte, error) {
	var program interface{}
	switch {
	case e.Kind == "program" && e.Program != nil && e.FrameProgram == nil:
		program = e.Program
	case e.Kind == "frame_program" && e.FrameProgram != nil && e.Program == nil:
		program = e.FrameProgram
	default:
		return nil, invalid("execution requires exactly its selected typed program")
	}
	return json.Marshal(struct {
		Kind     string      `json:"kind"`
		Contract string      `json:"contract"`
		Program  interface{} `json:"program"`
	}{e.Kind, e.Contract, program})
}

type Compiled struct {
	NormalizedAction json.RawMessage `json:"normalized_action"`
	RecipeID         string          `json:"recipe_id,omitempty"`
	Execution        Execution       `json:"execution"`
}

type Record struct {
	Contract string   `json:"record_contract"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Source   Source   `json:"source"`
	Compiled Compiled `json:"compiled"`
	Revision string   `json:"revision"`
}

// Support describes semantic verification only. Verified does not mean an
// executor, device or persistence layer is available.
type Support struct {
	Verified bool   `json:"verified"`
	Reason   string `json:"reason"`
}

// Inspection deliberately separates an integrity-checked, read-only document
// from a record accepted by a known source/recipe verifier. Private snapshots
// cannot be changed by mutating a returned Record or byte slice.
type Inspection struct {
	record          Record
	support         Support
	canonical       []byte
	executionDigest string
}

func (i Inspection) Record() Record          { return cloneRecord(i.record) }
func (i Inspection) Support() Support        { return i.support }
func (i Inspection) CanonicalJSON() []byte   { return append([]byte(nil), i.canonical...) }
func (i Inspection) ExecutionDigest() string { return i.executionDigest }

// VerifiedRecord never treats an unknown recipe as executable/verified data.
func (i Inspection) VerifiedRecord() (Record, error) {
	if !i.support.Verified {
		return Record{}, fmt.Errorf("%w: %s", ErrUnsupported, i.support.Reason)
	}
	return i.Record(), nil
}

func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
