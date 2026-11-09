package patternsv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

// NewRecord freezes one known high-level source. There is no ID registry or
// replacement here; persistence and its create-only transaction remain absent.
func NewRecord(id, title string, source Source) (Record, error) {
	if err := identity(id, title); err != nil {
		return Record{}, err
	}
	s, err := prepareSource(source)
	if err != nil {
		return Record{}, err
	}
	compiled, support, err := compileSource(s)
	if err != nil {
		return Record{}, err
	}
	if !support.Verified {
		return Record{}, fmt.Errorf("%w: %s", ErrUnsupported, support.Reason)
	}
	s.Action, err = canonicalAction(s.Action)
	if err != nil {
		return Record{}, err
	}
	r := Record{Contract: RecordContract, ID: id, Title: title, Source: s, Compiled: compiled}
	payload, err := payloadJSON(r)
	if err != nil {
		return Record{}, err
	}
	r.Revision = hash(RecordContract, payload)
	if _, err := recordJSON(r); err != nil {
		return Record{}, err
	}
	return cloneRecord(r), nil
}

// DecodeRecord accepts only a fully verified record. InspectRecord is the
// explicitly read-only path for a structurally valid but unsupported recipe.
func DecodeRecord(data []byte) (Record, error) {
	i, err := InspectRecord(data)
	if err != nil {
		return Record{}, err
	}
	return i.VerifiedRecord()
}

func NormalizeRecord(r Record) (Record, error) {
	if err := boundRecord(r); err != nil {
		return Record{}, err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return Record{}, invalid("record: %v", err)
	}
	return DecodeRecord(b)
}

// EncodeRecord returns verified canonical wire bytes. It cannot publish an
// unknown recipe; Inspection.CanonicalJSON supplies its read-only export.
func EncodeRecord(r Record) ([]byte, error) {
	v, err := NormalizeRecord(r)
	if err != nil {
		return nil, err
	}
	return recordJSON(v)
}

// CanonicalRecordJSON is the SHA payload only: no revision or domain prefix.
func CanonicalRecordJSON(r Record) ([]byte, error) {
	v, err := NormalizeRecord(r)
	if err != nil {
		return nil, err
	}
	return payloadJSON(v)
}

func ExecutionDigest(r Record) (string, error) {
	v, err := NormalizeRecord(r)
	if err != nil {
		return "", err
	}
	b, err := canonicalValue(v.Compiled.Execution)
	if err != nil {
		return "", err
	}
	return hash(ExecutionDomain, b), nil
}

func InspectRecord(data []byte) (Inspection, error) {
	n, err := parse(data)
	if err != nil {
		return Inspection{}, err
	}
	return inspectNode(n)
}

func inspectNode(n interface{}) (Inspection, error) {
	m, err := object(n, []string{"record_contract", "id", "title", "source", "compiled", "revision"})
	if err != nil {
		return Inspection{}, err
	}
	contract, err := textField(m, "record_contract")
	if err != nil {
		return Inspection{}, err
	}
	if contract != RecordContract {
		return Inspection{}, fmt.Errorf("%w: record contract %q", ErrUnsupported, contract)
	}
	id, err := textField(m, "id")
	if err != nil {
		return Inspection{}, err
	}
	title, err := textField(m, "title")
	if err != nil {
		return Inspection{}, err
	}
	if err := identity(id, title); err != nil {
		return Inspection{}, err
	}
	revision, err := textField(m, "revision")
	if err != nil {
		return Inspection{}, err
	}
	if !validRevision(revision) {
		return Inspection{}, invalid("revision must be sha256 plus 64 lowercase hexadecimal digits")
	}
	sm, err := object(m["source"], []string{"kind", "contract", "action"})
	if err != nil {
		return Inspection{}, err
	}
	source := Source{}
	if source.Kind, err = textField(sm, "kind"); err != nil {
		return Inspection{}, err
	}
	if source.Contract, err = textField(sm, "contract"); err != nil {
		return Inspection{}, err
	}
	if source.Action, err = raw(sm["action"]); err != nil {
		return Inspection{}, err
	}
	source, err = prepareSource(source)
	if err != nil {
		return Inspection{}, err
	}
	cm, err := object(m["compiled"], []string{"normalized_action", "execution"}, "recipe_id")
	if err != nil {
		return Inspection{}, err
	}
	if _, ok := cm["normalized_action"].(map[string]interface{}); !ok {
		return Inspection{}, invalid("normalized_action must be an object")
	}
	normalized, err := canonical(cm["normalized_action"])
	if err != nil {
		return Inspection{}, err
	}
	if len(normalized) > lightaction.MaxRequestBytes {
		return Inspection{}, invalid("normalized_action exceeds 65536 bytes")
	}
	recipe := ""
	if _, ok := cm["recipe_id"]; ok {
		if recipe, err = textField(cm, "recipe_id"); err != nil {
			return Inspection{}, err
		}
		if !versionToken(recipe) {
			return Inspection{}, invalid("recipe_id must be a nonempty bounded ASCII identifier")
		}
	}
	execution, err := decodeExecution(cm["execution"])
	if err != nil {
		return Inspection{}, err
	}
	r := Record{Contract: contract, ID: id, Title: title, Source: source, Compiled: Compiled{NormalizedAction: normalized, RecipeID: recipe, Execution: execution}, Revision: revision}
	// Hash the incoming meaning before replacing source numeric spelling. The
	// parser retains lexemes for semantic validation; the hash canonicalizes them.
	payload := make(map[string]interface{}, len(m)-1)
	for k, v := range m {
		if k != "revision" {
			payload[k] = v
		}
	}
	payloadBytes, err := canonical(payload)
	if err != nil {
		return Inspection{}, err
	}
	if hash(RecordContract, payloadBytes) != revision {
		return Inspection{}, invalid("revision does not match canonical record")
	}
	expected, support, err := compileSource(source)
	if err != nil {
		return Inspection{}, err
	}
	if support.Verified {
		if execution.Kind != expected.Execution.Kind || execution.Contract != expected.Execution.Contract {
			return Inspection{}, invalid("execution type differs from the versioned source contract")
		}
		// Keep the incoming numeric lexemes until the same author grammar has
		// rejected e.g. normalized_action.cycles=1.0. A canonical comparison
		// alone would collapse that forbidden spelling to integer 1.
		normalizedInput, err := raw(cm["normalized_action"])
		if err != nil {
			return Inspection{}, err
		}
		if _, _, err := compileSource(Source{Kind: "light_action", Contract: source.Contract, Action: normalizedInput}); err != nil {
			return Inspection{}, invalid("normalized_action: %v", err)
		}
		if source.Contract == lightaction.Contract {
			if recipe != "" {
				return Inspection{}, invalid("v1 Action does not declare a recipe_id")
			}
		} else {
			if recipe == "" {
				return Inspection{}, invalid("experimental Orbit requires recipe_id")
			}
			if recipe != expected.RecipeID {
				if knownRecipe(recipe) {
					return Inspection{}, invalid("known recipe does not match source segments")
				}
				support = Support{Reason: "unsupported_recipe"}
			}
		}
		if !bytes.Equal(normalized, expected.NormalizedAction) {
			return Inspection{}, invalid("normalized_action differs from the source")
		}
		if support.Verified {
			actualBytes, err := canonicalValue(r.Compiled)
			if err != nil {
				return Inspection{}, err
			}
			expectedBytes, err := canonicalValue(expected)
			if err != nil {
				return Inspection{}, err
			}
			if !bytes.Equal(actualBytes, expectedBytes) {
				return Inspection{}, invalid("frozen execution differs from source and recipe")
			}
		}
	}
	r.Source.Action, err = canonicalAction(source.Action)
	if err != nil {
		return Inspection{}, err
	}
	encoded, err := recordJSON(r)
	if err != nil {
		return Inspection{}, err
	}
	// Canonical encoding must preserve the exact verified digest even for an
	// unsupported source. No normalized defaults may be inserted into a record.
	canonicalPayload, err := payloadJSON(r)
	if err != nil {
		return Inspection{}, err
	}
	if !bytes.Equal(payloadBytes, canonicalPayload) {
		return Inspection{}, invalid("frozen fields are not in full canonical form")
	}
	eb, err := canonicalValue(execution)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{record: cloneRecord(r), support: support, canonical: encoded, executionDigest: hash(ExecutionDomain, eb)}, nil
}

func decodeExecution(n interface{}) (Execution, error) {
	m, err := object(n, []string{"kind", "contract", "program"})
	if err != nil {
		return Execution{}, err
	}
	kind, err := textField(m, "kind")
	if err != nil {
		return Execution{}, err
	}
	contract, err := textField(m, "contract")
	if err != nil {
		return Execution{}, err
	}
	b, err := raw(m["program"])
	if err != nil {
		return Execution{}, err
	}
	var e Execution
	switch kind {
	case "program":
		if contract == control.FrameContract {
			return e, invalid("program kind cannot contain the frame contract")
		}
		if contract != control.CompileContract {
			return e, fmt.Errorf("%w: program contract", ErrUnsupported)
		}
		var p control.Program
		if err := jsoninput.DecodeObject(b, &p); err != nil {
			return e, invalid("program: %v", err)
		}
		norm, err := control.Normalize(p)
		if err != nil {
			return e, invalid("program: %v", err)
		}
		e = Execution{Kind: kind, Contract: contract, Program: &norm}
	case "frame_program":
		if contract == control.CompileContract {
			return e, invalid("frame_program kind cannot contain the sequential contract")
		}
		if contract != control.FrameContract {
			return e, fmt.Errorf("%w: frame contract", ErrUnsupported)
		}
		p, err := control.DecodeFrameProgram(b)
		if err != nil {
			return e, invalid("frame program: %v", err)
		}
		e = Execution{Kind: kind, Contract: contract, FrameProgram: &p}
	default:
		return e, invalid("execution kind must be program or frame_program")
	}
	incoming, err := canonical(m["program"])
	if err != nil {
		return Execution{}, err
	}
	var normalized []byte
	if e.Program != nil {
		normalized, err = canonicalValue(e.Program)
	} else {
		normalized, err = canonicalValue(e.FrameProgram)
	}
	if err != nil {
		return Execution{}, err
	}
	if !bytes.Equal(incoming, normalized) {
		return Execution{}, invalid("frozen program must contain every normalized field and canonical RGB")
	}
	return e, nil
}

func prepareSource(s Source) (Source, error) {
	if s.Kind != "light_action" || !versionToken(s.Contract) {
		return Source{}, invalid("source requires light_action and a bounded contract")
	}
	if len(s.Action) > lightaction.MaxRequestBytes {
		return Source{}, invalid("source action exceeds 65536 bytes")
	}
	n, err := parse(s.Action)
	if err != nil {
		return Source{}, err
	}
	if _, ok := n.(map[string]interface{}); !ok {
		return Source{}, invalid("source action must be an object")
	}
	s.Action, err = raw(n)
	return s, err
}

func canonicalAction(data []byte) ([]byte, error) {
	n, err := parse(data)
	if err != nil {
		return nil, err
	}
	b, err := canonical(n)
	if len(b) > lightaction.MaxRequestBytes {
		return nil, invalid("source action exceeds 65536 canonical bytes")
	}
	return b, err
}

func compileSource(s Source) (Compiled, Support, error) {
	switch s.Contract {
	case lightaction.Contract:
		r, err := lightaction.CompileJSON(s.Action)
		if err != nil {
			return Compiled{}, Support{}, invalid("source action: %v", err)
		}
		a, err := canonicalValue(r.Action)
		if err != nil {
			return Compiled{}, Support{}, err
		}
		return Compiled{NormalizedAction: a, Execution: Execution{Kind: "program", Contract: r.ProgramCompileContract, Program: &r.Program}}, Support{Verified: true, Reason: "verified"}, nil
	case lightactionv2.Contract:
		r, err := lightactionv2.CompileJSON(s.Action)
		if err != nil {
			return Compiled{}, Support{}, invalid("source action: %v", err)
		}
		a, err := canonicalValue(r.Action)
		if err != nil {
			return Compiled{}, Support{}, err
		}
		return Compiled{NormalizedAction: a, RecipeID: r.RecipeID, Execution: Execution{Kind: "frame_program", Contract: r.ProgramCompileContract, FrameProgram: &r.Program}}, Support{Verified: true, Reason: "verified"}, nil
	default:
		return Compiled{}, Support{Reason: "unsupported_source"}, nil
	}
}

func recordJSON(r Record) ([]byte, error) { return canonicalValue(r) }
func payloadJSON(r Record) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, invalid("record: %v", err)
	}
	n, err := parse(b)
	if err != nil {
		return nil, err
	}
	m := n.(map[string]interface{})
	delete(m, "revision")
	return canonical(m)
}

func hash(domain string, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain + "\n"))
	_, _ = h.Write(payload)
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func knownRecipe(value string) bool {
	return value == "orbit-paired-linear-4/experimental-v1" || value == "orbit-paired-linear-8/experimental-v1"
}
func versionToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '/' || c == '.') {
			return false
		}
	}
	return true
}
func identity(id, title string) error {
	if len(id) < 1 || len(id) > 64 {
		return invalid("id must contain 1–64 ASCII characters")
	}
	for i, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return invalid("invalid id")
		}
	}
	if len(title) < 1 || len(title) > 1024 || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 256 {
		return invalid("title requires 1–256 Unicode scalars")
	}
	return nil
}
func validRevision(value string) bool {
	if len(value) != len("sha256:")+64 || value[:7] != "sha256:" {
		return false
	}
	for _, c := range value[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func cloneRecord(r Record) Record {
	r.Source.Action = append(json.RawMessage(nil), r.Source.Action...)
	r.Compiled.NormalizedAction = append(json.RawMessage(nil), r.Compiled.NormalizedAction...)
	if r.Compiled.Execution.Program != nil {
		p := *r.Compiled.Execution.Program
		p.Steps = append([]control.Step(nil), p.Steps...)
		r.Compiled.Execution.Program = &p
	}
	if r.Compiled.Execution.FrameProgram != nil {
		p := *r.Compiled.Execution.FrameProgram
		p.Initial = append([]control.FrameTarget(nil), p.Initial...)
		p.Frames = append([]control.Frame(nil), p.Frames...)
		for i := range p.Frames {
			p.Frames[i].Targets = append([]control.FrameTarget(nil), p.Frames[i].Targets...)
		}
		r.Compiled.Execution.FrameProgram = &p
	}
	return r
}

// Direct Go callers have the same bounded-allocation boundary as JSON callers.
// Check shape and strings before json.Marshal could copy an unbounded slice.
func boundRecord(r Record) error {
	if err := identity(r.ID, r.Title); err != nil {
		return err
	}
	if len(r.Contract) > 128 || len(r.Source.Kind) > 128 || len(r.Source.Contract) > 128 || len(r.Source.Action) > lightaction.MaxRequestBytes || len(r.Compiled.NormalizedAction) > lightaction.MaxRequestBytes || len(r.Compiled.RecipeID) > 128 || len(r.Revision) > 71 {
		return invalid("record field exceeds its bounded size")
	}
	e := r.Compiled.Execution
	if len(e.Contract) > 128 || len(e.Kind) > 32 {
		return invalid("execution identity exceeds its bounded size")
	}
	switch {
	case e.Kind == "program" && e.Program != nil && e.FrameProgram == nil:
		p := e.Program
		if len(p.Steps) < 1 || len(p.Steps) > control.MaxProgramSteps || len(p.OnFinish) > 4 {
			return invalid("sequential program exceeds its bounded shape")
		}
		for _, step := range p.Steps {
			if len(step.Color) != 7 {
				return invalid("frozen RGB must contain seven bytes")
			}
		}
	case e.Kind == "frame_program" && e.FrameProgram != nil && e.Program == nil:
		p := e.FrameProgram
		if len(p.Contract) > 128 || len(p.OnFinish) > 4 || len(p.Initial) < 1 || len(p.Initial) > control.MaxFrameProgramTargets || len(p.Frames) < 1 || len(p.Frames) > control.MaxFrameProgramFrames {
			return invalid("frame program exceeds its bounded shape")
		}
		checkTargets := func(targets []control.FrameTarget) bool {
			if len(targets) < 1 || len(targets) > control.MaxFrameProgramTargets {
				return false
			}
			for _, target := range targets {
				if len(target.Color) != 7 {
					return false
				}
			}
			return true
		}
		if !checkTargets(p.Initial) {
			return invalid("invalid frozen initial target shape")
		}
		for _, frame := range p.Frames {
			if !checkTargets(frame.Targets) {
				return invalid("invalid frozen frame target shape")
			}
		}
	default:
		return invalid("execution requires exactly its selected typed program")
	}
	return nil
}
