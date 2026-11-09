package patternsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

func authoringFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "authoring", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func authoringWire(action, contract string) string {
	return `{"id":"draft","title":"作者图案","source":{"kind":"light_action","contract":"` + contract + `","action":` + action + `}}`
}

func authoringSave(wire, revision string) []byte {
	return []byte(strings.TrimSuffix(wire, "}") + `,"expected_revision":"` + revision + `"}`)
}

func authoringCanonical(t *testing.T, data []byte) []byte {
	t.Helper()
	n, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonical(n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// These fixtures predate this implementation and were generated from d71's
// pure codec, not from the authoring API under test. Only P3 envelope fields
// belonging to this pure slice are compared; there is no HTTP handler here.
func TestAuthoringPlanGoldens(t *testing.T) {
	for _, name := range []string{"orbit-red-blue", "pulse-lime"} {
		t.Run(name, func(t *testing.T) {
			input := authoringFile(t, "requests/preview-"+name+".json")
			c, err := PreviewDraft(input)
			if err != nil {
				t.Fatal(err)
			}
			var expected struct {
				Record       json.RawMessage `json:"record"`
				Verification Support         `json:"verification"`
				Digest       string          `json:"execution_digest"`
				Preview      json.RawMessage `json:"preview"`
			}
			if err := json.Unmarshal(authoringFile(t, "responses/preview-"+name+".json"), &expected); err != nil {
				t.Fatal(err)
			}
			recordBytes, err := EncodeRecord(c.Record())
			if err != nil || !bytes.Equal(recordBytes, authoringCanonical(t, expected.Record)) {
				t.Fatalf("record/revision drift from plan golden: %v", err)
			}
			if c.Support() != expected.Verification || c.ExecutionDigest() != expected.Digest {
				t.Fatalf("verification/digest drift: %+v %s", c.Support(), c.ExecutionDigest())
			}
			facts, err := json.Marshal(c.Preview())
			if err != nil || !bytes.Equal(authoringCanonical(t, facts), authoringCanonical(t, expected.Preview)) {
				t.Fatalf("nominal facts drift: %s, %v", facts, err)
			}
			s, err := PrepareSave(authoringFile(t, "requests/save-"+name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.Candidate().Record(), c.Record()) || !reflect.DeepEqual(s.Candidate().Preview(), c.Preview()) {
				t.Fatal("save and preview do not construct the same complete candidate")
			}
			wantSnapshot, err := EncodeSnapshot([]Record{c.Record()})
			if err != nil || !bytes.Equal(s.SnapshotJSON(), wantSnapshot) {
				t.Fatalf("save snapshot changed frozen contract: %v", err)
			}
			decoded, err := DecodeSnapshot(s.SnapshotJSON())
			if err != nil || len(decoded) != 1 || !reflect.DeepEqual(decoded[0], c.Record()) {
				t.Fatalf("save snapshot is not one fully bound record: %v", err)
			}
		})
	}
}

func TestAuthoringPlanNegativeFixtures(t *testing.T) {
	for _, name := range []string{"preview-duplicate-title.json.txt", "preview-lone-surrogate.json.txt", "preview-fractional-time.json", "preview-null.json", "preview-forbidden-serial.json", "save-missing-revision.json", "save-forbidden-execution.json"} {
		t.Run(name, func(t *testing.T) {
			b := authoringFile(t, "requests/"+name)
			var err error
			if strings.HasPrefix(name, "save-") {
				_, err = PrepareSave(b)
			} else {
				_, err = PreviewDraft(b)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid input misclassified: %v", err)
			}
		})
	}
	s, err := PrepareSave(authoringFile(t, "requests/save-revision-mismatch.json"))
	if !errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrInvalid) || s.Candidate().Record().ID != "" || len(s.SnapshotJSON()) != 0 {
		t.Fatalf("revision conflict leaked candidate or changed category: %+v %v", s, err)
	}
}

func TestAuthoringRawJSONBoundary(t *testing.T) {
	base := authoringWire(`{"kind":"set","color":"red"}`, lightaction.Contract)
	cases := map[string]string{
		"array-root":           "[]",
		"empty-input":          "",
		"trailing-object":      base + " {}",
		"trailing-garbage":     base + " x",
		"unknown-root":         strings.TrimSuffix(base, "}") + `,"run":false}`,
		"source-unknown":       strings.Replace(base, `"kind":"light_action"`, `"kind":"light_action","compiled":{}`, 1),
		"action-unknown":       strings.Replace(base, `"kind":"set"`, `"kind":"set","serial":"20005601"`, 1),
		"duplicate-root":       strings.Replace(base, `"title":`, `"title":"other","title":`, 1),
		"escaped-duplicate":    strings.Replace(base, `"title":`, `"t\u0069tle":"other","title":`, 1),
		"source-duplicate":     strings.Replace(base, `"kind":"light_action"`, `"kind":"light_action","kind":"light_action"`, 1),
		"action-duplicate":     strings.Replace(base, `"kind":"set"`, `"kind":"set","kind":"set"`, 1),
		"null-root":            "null",
		"null-title":           strings.Replace(base, `"作者图案"`, "null", 1),
		"null-source":          `{"id":"draft","title":"标题","source":null}`,
		"null-action":          authoringWire("null", lightaction.Contract),
		"nested-null":          authoringWire(`{"kind":"set","color":{"space":"hsv","h_deg":null}}`, lightaction.Contract),
		"title-high-surrogate": strings.Replace(base, `"作者图案"`, `"\ud800"`, 1),
		"title-low-surrogate":  strings.Replace(base, `"作者图案"`, `"\udfff"`, 1),
		"title-bad-pair":       strings.Replace(base, `"作者图案"`, `"\ud800\u0041"`, 1),
		"source-surrogate":     strings.Replace(base, `"light_action"`, `"\ud800"`, 1),
		"action-surrogate":     strings.Replace(base, `"red"`, `"\udfff"`, 1),
		"key-surrogate":        strings.Replace(base, `"title":`, `"\ud800":`, 1),
		"non-ASCII-key":        strings.TrimSuffix(base, "}") + `,"标题":"x"}`,
		"title-invalid-UTF8":   strings.Replace(base, "作者图案", string([]byte{0xff}), 1),
		"bad-title-type":       strings.Replace(base, `"作者图案"`, "123", 1),
		"bad-source-type":      `{"id":"draft","title":"标题","source":[]}`,
		"bad-action-type":      authoringWire("[]", lightaction.Contract),
		"bare-program":         `{"id":"draft","title":"标题","source":{"kind":"program","contract":"nb1-program-effect/v1","action":{"kind":"set"}}}`,
		"title-empty":          strings.Replace(base, "作者图案", "", 1),
		"title-over-scalars":   strings.Replace(base, "作者图案", strings.Repeat("a", 257), 1),
		"id-over-bytes":        strings.Replace(base, `"draft"`, `"`+strings.Repeat("a", 65)+`"`, 1),
		"id-non-ASCII":         strings.Replace(base, `"draft"`, `"图案"`, 1),
		"input-budget":         strings.Repeat(" ", MaxBytes) + base,
		"action-budget":        authoringWire(`{"payload":"`+strings.Repeat("a", lightaction.MaxRequestBytes)+`"}`, "future/v3"),
		"depth-budget":         authoringWire(strings.Repeat(`{"nested":`, MaxDepth)+`{}`+strings.Repeat("}", MaxDepth), "future/v3"),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := PreviewDraft([]byte(wire))
			if !errors.Is(err, ErrInvalid) || c.Record().ID != "" || c.Support().Verified || c.ExecutionDigest() != "" {
				t.Fatalf("invalid raw preview accepted/leaked: %v", err)
			}
			// Structural, Unicode and nested semantic failures must remain strict
			// when the same original bytes have a syntactically valid confirmation.
			s, err := PrepareSave(authoringSave(wire, "sha256:"+strings.Repeat("0", 64)))
			if !errors.Is(err, ErrInvalid) || len(s.SnapshotJSON()) != 0 {
				t.Fatalf("invalid raw save accepted/leaked: %v", err)
			}
		})
	}
}

func TestAuthoringRevisionConfirmation(t *testing.T) {
	wire := authoringWire(`{"kind":"set","color":"red"}`, lightaction.Contract)
	c, err := PreviewDraft([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"", "sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 63), strings.Repeat("a", 64), c.Record().Revision + "x"} {
		t.Run("invalid-"+revision, func(t *testing.T) {
			s, err := PrepareSave(authoringSave(wire, revision))
			if !errors.Is(err, ErrInvalid) || len(s.SnapshotJSON()) != 0 {
				t.Fatalf("invalid revision accepted/misclassified: %v", err)
			}
		})
	}
	for _, bad := range []string{
		wire,
		strings.TrimSuffix(wire, "}") + `,"expected_revision":null}`,
		strings.TrimSuffix(wire, "}") + `,"expected_revision":123}`,
		string(authoringSave(wire, c.Record().Revision))[:len(authoringSave(wire, c.Record().Revision))-1] + `,"expected_revision":"` + c.Record().Revision + `"}`,
	} {
		if _, err := PrepareSave([]byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("missing/null/typed/duplicate confirmation accepted: %v", err)
		}
	}
	if _, err := PreviewDraft(authoringSave(wire, c.Record().Revision)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("preview accepted save-only field: %v", err)
	}
	for _, changed := range []string{
		strings.Replace(wire, "作者图案", "新标题", 1),
		strings.Replace(wire, `"draft"`, `"other"`, 1),
		strings.Replace(wire, `"red"`, `"#ff0000"`, 1),
	} {
		if s, err := PrepareSave(authoringSave(changed, c.Record().Revision)); !errors.Is(err, ErrRevisionConflict) || len(s.SnapshotJSON()) != 0 {
			t.Fatalf("confirmation did not bind full author content: %v", err)
		}
	}
	hex, err := PreviewDraft([]byte(strings.Replace(wire, `"red"`, `"#ff0000"`, 1)))
	if err != nil || hex.ExecutionDigest() != c.ExecutionDigest() || hex.Record().Revision == c.Record().Revision {
		t.Fatalf("source representation and execution identity collapsed: %v", err)
	}
}

func TestAuthoringSourceGrammarAndPreviewShapes(t *testing.T) {
	for _, action := range []string{
		`{"kind":"set","color":"red"}`, `{"kind":"off"}`, `{"kind":"fade","color":"blue","duration_ms":20}`,
		`{"kind":"blink","color":"lime","cycles":2}`, `{"kind":"breathe","color":"white"}`, `{"kind":"pulse","color":"lime"}`,
		`{"kind":"alternate","colors":["red","blue"]}`, `{"kind":"rainbow"}`,
	} {
		t.Run(action, func(t *testing.T) {
			c, err := PreviewDraft([]byte(authoringWire(action, lightaction.Contract)))
			if err != nil {
				t.Fatal(err)
			}
			p := c.Preview()
			if p.Kind != "program" || p.TotalSteps == nil || p.TotalFrames != nil || p.NominalTailMS != nil || p.TimingBasis != "host-nominal-excludes-native-io" {
				t.Fatalf("program facts union: %+v", p)
			}
		})
	}
	for _, segments := range []string{"4", "8"} {
		c, err := PreviewDraft([]byte(authoringWire(`{"kind":"orbit","segments":`+segments+`,"on_finish":"hold"}`, lightactionv2.Contract)))
		if err != nil {
			t.Fatal(err)
		}
		p := c.Preview()
		wantTail := int64(20)
		if segments == "8" {
			wantTail = 25 // interval 375ms, device fade quantized to 380ms
		}
		if p.Kind != "frame_program" || p.TotalSteps != nil || p.TotalFrames == nil || p.NominalTailMS == nil || *p.NominalTailMS != wantTail || p.NominalDurationMS != 3020+wantTail {
			t.Fatalf("frame hold facts lost the final fade allowance: %+v", p)
		}
		b, _ := json.Marshal(p)
		if !bytes.Contains(b, []byte(`"nominal_tail_ms":`)) || bytes.Contains(b, []byte(`"total_steps"`)) {
			t.Fatalf("frame wire facts changed: %s", b)
		}
	}
	for _, action := range []string{
		`{"kind":"fade","color":"red","duration_ms":20.0}`,
		`{"kind":"blink","color":"red","cycles":1.0}`,
		`{"kind":"blink","color":"red","cycles":1e0}`,
		`{"kind":"set","color":"red","led":0.0}`,
	} {
		if _, err := PreviewDraft([]byte(authoringWire(action, lightaction.Contract))); !errors.Is(err, ErrInvalid) {
			t.Fatalf("canonicalization erased forbidden integer lexeme: %s %v", action, err)
		}
	}
	for _, action := range []string{`{"kind":"orbit","period_ms":3000.0}`, `{"kind":"orbit","segments":4.0}`, `{"kind":"orbit","cycles":1.0}`} {
		if _, err := PreviewDraft([]byte(authoringWire(action, lightactionv2.Contract))); !errors.Is(err, ErrInvalid) {
			t.Fatalf("orbit integer lexeme accepted: %s %v", action, err)
		}
	}
	a, err := PreviewDraft([]byte(authoringWire(`{"kind":"set","color":{"space":"hsv","h_deg":240.000000,"s_pct":100.000000,"v_pct":50.500000}}`, lightaction.Contract)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := PreviewDraft([]byte(authoringWire(`{"color":{"v_pct":50.5,"s_pct":100,"h_deg":240,"space":"hsv"},"kind":"set"}`, lightaction.Contract)))
	if err != nil || a.Record().Revision != b.Record().Revision || !bytes.Contains(a.Record().Source.Action, []byte(`"v_pct":50.5`)) {
		t.Fatalf("valid HSV decimal semantics changed: %v", err)
	}
}

func TestAuthoringUnicodeAndUnsupportedSource(t *testing.T) {
	base := authoringWire(`{"kind":"off"}`, lightaction.Contract)
	escaped := strings.Replace(base, "作者图案", `\ud83d\udca1`, 1)
	literal := strings.Replace(base, "作者图案", "💡", 1)
	a, err := PreviewDraft([]byte(escaped))
	if err != nil {
		t.Fatal(err)
	}
	b, err := PreviewDraft([]byte(literal))
	if err != nil || a.Record().Title != "💡" || a.Record().Revision != b.Record().Revision {
		t.Fatalf("valid surrogate pair lost scalar identity: %v", err)
	}
	if _, err := PreviewDraft([]byte(strings.Replace(base, "作者图案", `\ufffd`, 1))); err != nil {
		t.Fatalf("literal replacement scalar rejected: %v", err)
	}
	unknown := authoringWire(`{"future":"uninterpreted"}`, "future/v3")
	if c, err := PreviewDraft([]byte(unknown)); !errors.Is(err, ErrUnsupported) || c.Support().Verified || c.Record().ID != "" {
		t.Fatalf("unsupported source was promoted: %v", err)
	}
	if s, err := PrepareSave(authoringSave(unknown, "sha256:"+strings.Repeat("0", 64))); !errors.Is(err, ErrUnsupported) || len(s.SnapshotJSON()) != 0 {
		t.Fatalf("unsupported source produced writable bytes: %v", err)
	}
}

func TestAuthoringCopiesAndConcurrentReaders(t *testing.T) {
	for _, name := range []string{"orbit-red-blue", "pulse-lime"} {
		t.Run(name, func(t *testing.T) {
			wire := authoringFile(t, "requests/save-"+name+".json")
			s, err := PrepareSave(wire)
			if err != nil {
				t.Fatal(err)
			}
			c := s.Candidate()
			wantRecord, wantFacts, wantBytes := c.Record(), c.Preview(), s.SnapshotJSON()
			for i := range wire {
				wire[i] = 'x'
			}
			var wg sync.WaitGroup
			for n := 0; n < 8; n++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, p, snapshot := c.Record(), c.Preview(), s.SnapshotJSON()
					r.Source.Action[0] = '!'
					r.Compiled.NormalizedAction[0] = '!'
					if e := r.Compiled.Execution; e.Program != nil {
						e.Program.Steps[0].Color = "invalid"
						*p.TotalSteps = -1
					} else {
						e.FrameProgram.Initial[0].Color = "invalid"
						e.FrameProgram.Frames[0].Targets[0].Color = "invalid"
						*p.TotalFrames, *p.StoredTargetWrites, *p.PlannedTargetWrites = -1, -1, -1
						*p.CycleDurationMS, *p.NominalInitialMS, *p.NominalTailMS = -1, -1, -1
					}
					p.RequiredCapabilities[0] = "invalid"
					snapshot[0] = '!'
				}()
			}
			wg.Wait()
			if !reflect.DeepEqual(c.Record(), wantRecord) || !reflect.DeepEqual(c.Preview(), wantFacts) || !bytes.Equal(s.SnapshotJSON(), wantBytes) {
				t.Fatal("mutable return/input aliases the frozen candidate")
			}
		})
	}
}
