// Package patterns implements bounded, pure Pattern package and record codecs.
// It has no storage, service, execution, ownership or device behavior.
package patterns

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
)

const (
	SchemaVersion     = 1
	SnapshotKind      = "nb1-pattern-snapshot"
	RevisionDomain    = "nb1-pattern-record/v1"
	CompileContract   = "nb1-program-effect/v1"
	MaxRecords        = 128
	MaxPackageBytes   = 1 << 20
	MaxCanonicalBytes = 1 << 20
)

type Source struct {
	Program *control.Program `json:"program,omitempty"`
	Effect  *control.Effect  `json:"effect,omitempty"`
}

type Record struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Source          Source          `json:"source"`
	CompiledProgram control.Program `json:"compiled_program"`
	CompileContract string          `json:"compile_contract"`
	Revision        string          `json:"revision"`
}

// NewRecord freezes a normalized source and an independent Program snapshot.
// It never changes caller-owned slices, pointers, ID or title text.
func NewRecord(id, title string, source Source) (Record, error) {
	if control.CompileContract != CompileContract {
		return Record{}, invalid("the v1 compiler validator is unavailable")
	}
	if !validID(id) {
		return Record{}, invalid("id must be 1-64 ASCII letters/digits with optional subsequent hyphens or underscores")
	}
	// A scalar uses at most four UTF-8 bytes. Bound direct Go callers before
	// scanning text, just as DecodePackage bounds its complete wire input.
	if len(title) < 1 || len(title) > 4*256 || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 256 {
		return Record{}, invalid("title must contain 1-256 Unicode scalar values")
	}
	if (source.Program == nil) == (source.Effect == nil) {
		return Record{}, invalid("source requires exactly one program or effect")
	}
	var compiled control.Program
	var normalized Source
	if source.Program != nil {
		value, err := control.Normalize(*source.Program)
		if err != nil {
			return Record{}, invalid("program: %v", err)
		}
		compiled = cloneProgram(value)
		normalized.Program = &value
	} else {
		value, err := control.CompileEffect(*source.Effect)
		if err != nil {
			return Record{}, invalid("effect: %v", err)
		}
		compiled = value
		effect := normalizeEffect(*source.Effect, value)
		normalized.Effect = &effect
	}
	record := Record{ID: id, Title: title, Source: normalized, CompiledProgram: compiled, CompileContract: CompileContract}
	canonical, err := encodeRecordJSON(record)
	if err != nil {
		return Record{}, err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(RevisionDomain + "\n"))
	_, _ = digest.Write(canonical)
	record.Revision = fmt.Sprintf("sha256:%x", digest.Sum(nil))
	return record, nil
}

// NormalizeRecord validates a frozen record under its declared supported
// contract and returns a canonical deep copy with the same verified revision.
// Equivalent source/compiled defaults are normalized; the contract is not
// upgraded, and an inconsistent or unverified snapshot is never returned.
func NormalizeRecord(record Record) (Record, error) {
	if record.CompileContract != CompileContract {
		return Record{}, invalid("unsupported compile_contract; only nb1-program-effect/v1 is accepted")
	}
	expected, err := NewRecord(record.ID, record.Title, record.Source)
	if err != nil {
		return Record{}, err
	}
	compiled, err := control.Normalize(record.CompiledProgram)
	if err != nil {
		return Record{}, invalid("compiled_program: %v", err)
	}
	if !reflect.DeepEqual(compiled, expected.CompiledProgram) {
		return Record{}, invalid("compiled_program differs from the declared source expansion")
	}
	if record.Revision != expected.Revision {
		return Record{}, invalid("revision does not match the canonical record")
	}
	return expected, nil
}

// CanonicalRecordJSON returns only the verified record's hash-domain JSON.
// Revision and the domain/LF prefix are excluded; there is no trailing newline.
func CanonicalRecordJSON(record Record) ([]byte, error) {
	value, err := NormalizeRecord(record)
	if err != nil {
		return nil, err
	}
	return encodeRecordJSON(value)
}

func normalizeEffect(input control.Effect, compiled control.Program) control.Effect {
	if input.Kind == "steady" {
		fade := 0
		if input.FadeMS != nil {
			fade = *input.FadeMS
		}
		return control.Effect{Kind: "steady", Color: compiled.Steps[0].Color, LED: input.LED, FadeMS: &fade}
	}
	cycles, period, finish := 1, 1000, "off"
	if input.Cycles != nil {
		cycles = *input.Cycles
	}
	if input.PeriodMS != nil {
		period = *input.PeriodMS
	}
	if input.OnFinish != nil {
		finish = *input.OnFinish
	}
	return control.Effect{Kind: "pulse", Color: compiled.Steps[1].Color, LED: input.LED, Cycles: &cycles, PeriodMS: &period, OnFinish: &finish}
}

func cloneProgram(value control.Program) control.Program {
	value.Steps = append([]control.Step(nil), value.Steps...)
	return value
}

func validID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := range value {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return false
		}
	}
	return true
}

func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("invalid pattern: "+format, args...)
}
