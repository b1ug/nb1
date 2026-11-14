package patternsv2

import (
	"errors"

	"github.com/b1ug/nb1/internal/control"
)

// ErrRevisionConflict means the author's expected_revision does not identify
// the newly compiled candidate. It is not a conflict with a stored record.
var ErrRevisionConflict = errors.New("experimental pattern candidate revision conflict")

// PreviewFacts contains nominal, pure compiler facts. The selected execution
// kind determines which optional fields exist, including meaningful zeroes.
// It does not establish device support, admission or observed wall time.
type PreviewFacts struct {
	Kind                 string   `json:"kind"`
	Contract             string   `json:"contract"`
	NominalDurationMS    int64    `json:"nominal_duration_ms"`
	TimingBasis          string   `json:"timing_basis"`
	RequiredCapabilities []string `json:"required_capabilities"`
	TotalSteps           *int     `json:"total_steps,omitempty"`
	TotalFrames          *int     `json:"total_frames,omitempty"`
	StoredTargetWrites   *int     `json:"stored_target_writes,omitempty"`
	PlannedTargetWrites  *int     `json:"planned_target_writes,omitempty"`
	CycleDurationMS      *int64   `json:"cycle_duration_ms,omitempty"`
	NominalInitialMS     *int64   `json:"nominal_initial_ms,omitempty"`
	NominalTailMS        *int64   `json:"nominal_tail_ms,omitempty"`
}

// Candidate is an immutable, verified authoring result. Its Record retains the
// author's high-level source alongside the frozen execution and revision.
// Accessors return independent mutable data; no store or executor is involved.
type Candidate struct {
	inspection Inspection
	preview    PreviewFacts
}

func (c Candidate) Record() Record          { return c.inspection.Record() }
func (c Candidate) Support() Support        { return c.inspection.Support() }
func (c Candidate) ExecutionDigest() string { return c.inspection.ExecutionDigest() }
func (c Candidate) Preview() PreviewFacts   { return clonePreviewFacts(c.preview) }

// SaveCandidate adds one canonical snapshot to a confirmed Candidate. It is
// only an input for a later create-only store transaction, not a saved record,
// overwrite authorization, execution ticket or device capability check.
type SaveCandidate struct {
	candidate Candidate
	snapshot  []byte
}

func (s SaveCandidate) Candidate() Candidate { return s.candidate }
func (s SaveCandidate) SnapshotJSON() []byte { return append([]byte(nil), s.snapshot...) }

// PreviewDraft strictly decodes raw authoring JSON {id,title,source}, then uses
// NewRecord and the existing pure preview functions. expected_revision and all
// other additional fields are rejected. Decode the original bytes here rather
// than unmarshaling them first: encoding/json can replace lone surrogates.
func PreviewDraft(data []byte) (Candidate, error) {
	c, _, err := authoringCandidate(data, false)
	return c, err
}

// PrepareSave strictly decodes {id,title,source,expected_revision}. Confirmation
// binds the complete candidate, including the author's source notation. The
// returned snapshot uses the existing frozen Snapshot contract unchanged.
// No persistence, path lookup, device discovery or execution occurs.
func PrepareSave(data []byte) (SaveCandidate, error) {
	c, expected, err := authoringCandidate(data, true)
	if err != nil {
		return SaveCandidate{}, err
	}
	if c.Record().Revision != expected {
		return SaveCandidate{}, ErrRevisionConflict
	}
	b, err := EncodeSnapshot([]Record{c.Record()})
	if err != nil {
		return SaveCandidate{}, err
	}
	return SaveCandidate{candidate: c, snapshot: b}, nil
}

func authoringCandidate(data []byte, save bool) (Candidate, string, error) {
	n, err := parse(data)
	if err != nil {
		return Candidate{}, "", err
	}
	fields := []string{"id", "title", "source"}
	if save {
		fields = append(fields, "expected_revision")
	}
	m, err := object(n, fields)
	if err != nil {
		return Candidate{}, "", err
	}
	id, err := textField(m, "id")
	if err != nil {
		return Candidate{}, "", err
	}
	title, err := textField(m, "title")
	if err != nil {
		return Candidate{}, "", err
	}
	if err := identity(id, title); err != nil {
		return Candidate{}, "", err
	}
	expected := ""
	if save {
		if expected, err = textField(m, "expected_revision"); err != nil {
			return Candidate{}, "", err
		}
		if !validRevision(expected) {
			return Candidate{}, "", invalid("expected_revision must be sha256 plus 64 lowercase hexadecimal digits")
		}
	}
	sm, err := object(m["source"], []string{"kind", "contract", "action"})
	if err != nil {
		return Candidate{}, "", err
	}
	s := Source{}
	if s.Kind, err = textField(sm, "kind"); err != nil {
		return Candidate{}, "", err
	}
	if s.Contract, err = textField(sm, "contract"); err != nil {
		return Candidate{}, "", err
	}
	// raw retains json.Number lexemes until the versioned action compiler has
	// checked them. Canonicalizing here could turn illegal cycles:1.0 into 1.
	if s.Action, err = raw(sm["action"]); err != nil {
		return Candidate{}, "", err
	}
	r, err := NewRecord(id, title, s)
	if err != nil {
		return Candidate{}, "", err
	}
	b, err := recordJSON(r)
	if err != nil {
		return Candidate{}, "", err
	}
	i, err := InspectRecord(b)
	if err != nil {
		return Candidate{}, "", err
	}
	p, err := previewFacts(r)
	if err != nil {
		return Candidate{}, "", err
	}
	return Candidate{inspection: i, preview: p}, expected, nil
}

func previewFacts(r Record) (PreviewFacts, error) {
	e := r.Compiled.Execution
	f := PreviewFacts{Kind: e.Kind, Contract: e.Contract, TimingBasis: "host-nominal-excludes-native-io"}
	if e.Program != nil {
		p, err := control.Preview(e.Program, nil)
		if err != nil {
			return PreviewFacts{}, err
		}
		f.NominalDurationMS, f.TotalSteps = p.NominalDurationMS, &p.TotalSteps
		f.RequiredCapabilities = p.RequiredCapabilities
	} else {
		p, err := control.PreviewFrameProgram(*e.FrameProgram)
		if err != nil {
			return PreviewFacts{}, err
		}
		f.NominalDurationMS, f.TotalFrames = p.NominalDurationMS, &p.TotalFrames
		f.StoredTargetWrites, f.PlannedTargetWrites = &p.StoredTargetWrites, &p.PlannedTargetWrites
		f.CycleDurationMS, f.NominalInitialMS, f.NominalTailMS = &p.CycleDurationMS, &p.NominalInitialMS, &p.NominalTailMS
		f.RequiredCapabilities = p.RequiredCapabilities
	}
	return f, nil
}

func clonePreviewFacts(f PreviewFacts) PreviewFacts {
	cloneInt := func(p *int) *int {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	cloneInt64 := func(p *int64) *int64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	f.RequiredCapabilities = append([]string(nil), f.RequiredCapabilities...)
	f.TotalSteps, f.TotalFrames = cloneInt(f.TotalSteps), cloneInt(f.TotalFrames)
	f.StoredTargetWrites, f.PlannedTargetWrites = cloneInt(f.StoredTargetWrites), cloneInt(f.PlannedTargetWrites)
	f.CycleDurationMS, f.NominalInitialMS, f.NominalTailMS = cloneInt64(f.CycleDurationMS), cloneInt64(f.NominalInitialMS), cloneInt64(f.NominalTailMS)
	return f
}
