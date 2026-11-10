package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

const patternAuthorLibraryContract = "nb1-pattern-library/experimental-v2"
const patternAuthorSnapshotKind = "nb1-pattern-snapshot/experimental-v2"
const maxPatternAuthorArguments = 128 << 10
const maxPatternAuthorAction = 64 << 10
const maxPatternAuthorRecord = 1 << 20
const maxPatternAuthorResponse = 2 << 20
const maxPatternAuthorOutput = 4 << 20

type patternAuthorSupport struct {
	Verified bool   `json:"verified"`
	Reason   string `json:"reason"`
}
type patternAuthorSource struct {
	Kind     string          `json:"kind"`
	Contract string          `json:"contract"`
	Action   json.RawMessage `json:"action"`
}
type patternAuthorCompiled struct {
	NormalizedAction json.RawMessage    `json:"normalized_action"`
	RecipeID         *string            `json:"recipe_id,omitempty"`
	Execution        patternV2Execution `json:"execution"`
}
type patternAuthorRecord struct {
	Contract string                `json:"record_contract"`
	ID       string                `json:"id"`
	Title    string                `json:"title"`
	Source   patternAuthorSource   `json:"source"`
	Compiled patternAuthorCompiled `json:"compiled"`
	Revision string                `json:"revision"`
}
type patternAuthorDraft struct {
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Source           patternAuthorSource `json:"source"`
	ExpectedRevision *string             `json:"expected_revision,omitempty"`
}
type patternAuthorRecordResponse struct {
	SchemaVersion   int                  `json:"schema_version"`
	Contract        string               `json:"contract"`
	Record          json.RawMessage      `json:"record"`
	Verification    patternAuthorSupport `json:"verification"`
	ExecutionDigest string               `json:"execution_digest"`
}
type patternAuthorFacts struct {
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
type patternAuthorPreviewResponse struct {
	SchemaVersion    int                  `json:"schema_version"`
	Contract         string               `json:"contract"`
	Record           json.RawMessage      `json:"record"`
	Verification     patternAuthorSupport `json:"verification"`
	ExecutionDigest  string               `json:"execution_digest"`
	Preview          patternAuthorFacts   `json:"preview"`
	PatternExecution frameExecutionWire   `json:"pattern_execution"`
}

// Complete Record is a canonical JSON string, so generic SDK/JS maps cannot
// round a legal unknown source number above 2^53. This is not a source compiler.
type patternAuthorRecordData struct {
	SchemaVersion   int                  `json:"schema_version"`
	Contract        string               `json:"contract"`
	PatternRef      patternV2Ref         `json:"pattern_ref"`
	RecordJSON      string               `json:"record_json"`
	Verification    patternAuthorSupport `json:"verification"`
	ExecutionDigest string               `json:"execution_digest"`
}
type patternAuthorPreviewData struct {
	SchemaVersion    int                  `json:"schema_version"`
	Contract         string               `json:"contract"`
	PatternRef       patternV2Ref         `json:"pattern_ref"`
	RecordJSON       string               `json:"record_json"`
	Verification     patternAuthorSupport `json:"verification"`
	ExecutionDigest  string               `json:"execution_digest"`
	Preview          patternAuthorFacts   `json:"preview"`
	PatternExecution frameExecutionWire   `json:"pattern_execution"`
}
type patternAuthorRecordInfo struct {
	wire patternAuthorRecord
	data patternAuthorRecordData
}

func patternAuthorDecode(data []byte, target any, limit, depth int) error {
	if limit < 1 || limit > maxPatternAuthorResponse {
		return errors.New("invalid Pattern wire budget")
	}
	got, err := parsePatternAuthorValue(data, limit, depth)
	if err != nil {
		return err
	}
	if _, ok := got.(map[string]any); !ok {
		return errors.New("one Pattern object is required")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return err
	}
	// The original wire is still bounded above. A typed round trip may expand
	// valid HTML characters to six-byte escapes; that representation change
	// must not make otherwise valid daemon metadata fail the wire-byte limit.
	// Generated field defaults add only a fixed bounded schema overhead.
	want, err := parsePatternAuthorValue(encoded, limit*6+4096, depth)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("Pattern response has missing, aliased, or noncanonical fields")
	}
	return nil
}
func patternAuthorToken(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '/' || c == '.') {
			return false
		}
	}
	return true
}
func patternAuthorIdentity(id, title string) bool {
	return patternID.MatchString(id) && len(title) > 0 && len(title) <= 1024 && utf8.ValidString(title) && utf8.RuneCountInString(title) <= 256
}
func patternAuthorCanonicalEqual(a, b []byte) bool {
	first, err := canonicalPatternAuthorRaw(a, maxPatternAuthorAction, 32)
	if err != nil {
		return false
	}
	second, err := canonicalPatternAuthorRaw(b, maxPatternAuthorAction, 32)
	return err == nil && bytes.Equal(first, second)
}
func patternAuthorSourceCheck(source patternAuthorSource, knownOnly bool) error {
	if source.Kind != "light_action" || !patternAuthorToken(source.Contract) || len(source.Action) > maxPatternAuthorAction {
		return errors.New("invalid bounded high-level source")
	}
	value, err := parsePatternAuthorValue(source.Action, maxPatternAuthorAction, 32)
	if err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("source action must be an object")
	}
	if _, err = canonicalPatternAuthorValue(value, maxPatternAuthorAction); err != nil {
		return err
	}
	switch source.Contract {
	case lightContract:
		_, err = normalizedLightStructure(source.Action)
	case orbitLightContract:
		_, err = normalizedOrbitStructure(source.Action)
	default:
		if knownOnly {
			err = errors.New("authoring requires a supported high-level source contract")
		}
	}
	return err
}
func patternAuthorReadDraft(raw []byte, save bool) (patternAuthorDraft, error) {
	var value patternAuthorDraft
	if len(raw) == 0 || len(raw) > maxPatternAuthorArguments {
		return value, errors.New("author arguments require one object <=128KiB")
	}
	if err := patternAuthorDecode(raw, &value, maxPatternAuthorArguments, 32); err != nil {
		return value, err
	}
	if !patternAuthorIdentity(value.ID, value.Title) {
		return value, errors.New("invalid bounded Pattern ID/title")
	}
	if save {
		if value.ExpectedRevision == nil || !patternRevision.MatchString(*value.ExpectedRevision) {
			return value, errors.New("save requires the exact expected_revision from the original preview")
		}
	} else if value.ExpectedRevision != nil {
		return value, errors.New("draft preview does not accept expected_revision")
	}
	return value, patternAuthorSourceCheck(value.Source, true)
}
func patternAuthorRecordSupport(r patternAuthorRecord) (patternAuthorSupport, error) {
	if err := patternAuthorSourceCheck(r.Source, false); err != nil {
		return patternAuthorSupport{}, err
	}
	normalized, err := parsePatternAuthorValue(r.Compiled.NormalizedAction, maxPatternAuthorAction, 32)
	if err != nil {
		return patternAuthorSupport{}, err
	}
	if _, ok := normalized.(map[string]any); !ok {
		return patternAuthorSupport{}, errors.New("normalized_action must be an object")
	}
	if r.Compiled.RecipeID != nil && !patternAuthorToken(*r.Compiled.RecipeID) {
		return patternAuthorSupport{}, errors.New("invalid recipe token")
	}
	switch r.Source.Contract {
	case lightContract:
		if r.Compiled.RecipeID != nil || r.Compiled.Execution.Kind != "program" {
			return patternAuthorSupport{}, errors.New("v1 source requires a Program and no recipe")
		}
		expected, e := normalizedLightStructure(r.Source.Action)
		if e != nil {
			return patternAuthorSupport{}, e
		}
		actual, e := normalizedLightStructure(r.Compiled.NormalizedAction)
		if e != nil {
			return patternAuthorSupport{}, e
		}
		if e = bindLightAction(expected, actual); e != nil {
			return patternAuthorSupport{}, e
		}
		encoded, _ := json.Marshal(actual)
		if !patternAuthorCanonicalEqual(encoded, r.Compiled.NormalizedAction) {
			return patternAuthorSupport{}, errors.New("normalized v1 action is not explicit and canonical")
		}
		return patternAuthorSupport{true, "verified"}, nil
	case orbitLightContract:
		if r.Compiled.RecipeID == nil || r.Compiled.Execution.Kind != "frame_program" {
			return patternAuthorSupport{}, errors.New("Orbit source requires a FrameProgram and explicit recipe")
		}
		expected, e := normalizedOrbitStructure(r.Source.Action)
		if e != nil {
			return patternAuthorSupport{}, e
		}
		actual, e := normalizedOrbitStructure(r.Compiled.NormalizedAction)
		if e != nil {
			return patternAuthorSupport{}, e
		}
		if e = bindOrbitAction(expected, actual); e != nil {
			return patternAuthorSupport{}, e
		}
		encoded, _ := json.Marshal(actual)
		if !patternAuthorCanonicalEqual(encoded, r.Compiled.NormalizedAction) {
			return patternAuthorSupport{}, errors.New("normalized Orbit action is not explicit and canonical")
		}
		recipe := *r.Compiled.RecipeID
		want := fmt.Sprintf("orbit-paired-linear-%d/experimental-v1", *expected.Segments)
		if recipe != want {
			if recipe == "orbit-paired-linear-4/experimental-v1" || recipe == "orbit-paired-linear-8/experimental-v1" {
				return patternAuthorSupport{}, errors.New("known recipe mismatches source segments")
			}
			return patternAuthorSupport{false, "unsupported_recipe"}, nil
		}
		return patternAuthorSupport{true, "verified"}, nil
	default:
		return patternAuthorSupport{false, "unsupported_source"}, nil
	}
}
func patternAuthorInspectRecord(raw []byte) (patternAuthorRecordInfo, error) {
	var r patternAuthorRecord
	bad := func(err error) (patternAuthorRecordInfo, error) { return patternAuthorRecordInfo{}, err }
	if err := patternAuthorDecode(raw, &r, maxPatternAuthorRecord, 32); err != nil {
		return bad(err)
	}
	ref := patternV2Ref{r.Contract, r.ID, r.Revision}
	if !validPatternV2Ref(ref) || !patternAuthorIdentity(r.ID, r.Title) {
		return bad(errors.New("invalid Pattern record identity"))
	}
	support, err := patternAuthorRecordSupport(r)
	if err != nil {
		return bad(err)
	}
	digest, err := validatePatternV2Execution(r.Compiled.Execution)
	if err != nil {
		return bad(err)
	}
	tree, err := parsePatternAuthorValue(raw, maxPatternAuthorRecord, 32)
	if err != nil {
		return bad(err)
	}
	payload := make(map[string]any)
	for key, value := range tree.(map[string]any) {
		if key != "revision" {
			payload[key] = value
		}
	}
	canonical, err := canonicalPatternAuthorValue(payload, maxPatternAuthorRecord)
	if err != nil {
		return bad(err)
	}
	hash := sha256.Sum256(append([]byte(patternV2RecordContract+"\n"), canonical...))
	if r.Revision != fmt.Sprintf("sha256:%x", hash) {
		return bad(errors.New("record revision differs from its canonical payload"))
	}
	complete, err := canonicalPatternAuthorValue(tree, maxPatternAuthorRecord)
	if err != nil {
		return bad(err)
	}
	return patternAuthorRecordInfo{r, patternAuthorRecordData{2, patternAuthorLibraryContract, ref, string(complete), support, digest}}, nil
}
func patternAuthorBindRecord(info patternAuthorRecordInfo, draft patternAuthorDraft) error {
	r := info.wire
	if !info.data.Verification.Verified || r.ID != draft.ID || r.Title != draft.Title || r.Source.Kind != draft.Source.Kind || r.Source.Contract != draft.Source.Contract || !patternAuthorCanonicalEqual(r.Source.Action, draft.Source.Action) {
		return errors.New("returned candidate changed the original high-level authoring source")
	}
	if draft.ExpectedRevision != nil && r.Revision != *draft.ExpectedRevision {
		return errors.New("saved record changed the original expected_revision")
	}
	return nil
}
func patternAuthorAvailability(value frameExecutionWire) error {
	if value.Available && value.Reason == "pattern_service_configured" {
		return nil
	}
	if !value.Available && (value.Reason == "pattern_service_unsupported" || value.Reason == "pattern_library_unconfigured" || value.Reason == "pattern_library_configuration_error") {
		return nil
	}
	return errors.New("contradictory Pattern software availability")
}
func patternAuthorPreviewFacts(e patternV2Execution) (patternAuthorFacts, error) {
	facts := patternAuthorFacts{Kind: e.Kind, Contract: e.Contract, TimingBasis: "host-nominal-excludes-native-io"}
	if e.Kind == "program" {
		var p previewProgram
		if err := patternAuthorDecode(e.Program, &p, maxPatternAuthorRecord, 32); err != nil {
			return facts, err
		}
		if err := validateFrozenPatternProgram(p); err != nil {
			return facts, err
		}
		for _, s := range p.Steps {
			facts.NominalDurationMS += int64(s.FadeMS) + int64(s.HoldMS) + 10
		}
		facts.NominalDurationMS *= int64(p.Repeat)
		total := len(p.Steps) * p.Repeat
		facts.TotalSteps = &total
		facts.RequiredCapabilities = []string{"set", "program", "raw-rgb"}
		if p.OnFinish == "hold" {
			facts.RequiredCapabilities = append(facts.RequiredCapabilities, "hold")
		}
	} else {
		var p frameProgramWire
		if err := patternAuthorDecode(e.Program, &p, maxPatternAuthorRecord, 32); err != nil {
			return facts, err
		}
		f, err := validateFrameProgram(p)
		if err != nil {
			return facts, err
		}
		facts.NominalDurationMS = f.duration
		facts.TotalFrames = &f.frames
		facts.StoredTargetWrites = &f.storedWrites
		facts.PlannedTargetWrites = &f.writes
		facts.CycleDurationMS = &f.cycle
		facts.NominalInitialMS = &f.initial
		facts.NominalTailMS = &f.tail
		facts.RequiredCapabilities = []string{"set", "raw-rgb", "frame-program-experimental-v1"}
		if p.OnFinish == "hold" {
			facts.RequiredCapabilities = append(facts.RequiredCapabilities, "hold")
		}
	}
	return facts, nil
}
func patternAuthorDisplayPath(path string) bool {
	if strings.HasPrefix(path, "/") {
		return true
	}
	if len(path) >= 3 && (path[0] >= 'A' && path[0] <= 'Z' || path[0] >= 'a' && path[0] <= 'z') && path[1] == ':' && (path[2] == '/' || path[2] == '\\') {
		return true
	}
	if strings.HasPrefix(path, `\\`) {
		parts := strings.SplitN(path[2:], `\`, 3)
		return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
	}
	return false
}
