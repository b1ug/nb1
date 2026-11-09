package patternsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

func advOrbit(t *testing.T) Record {
	t.Helper()
	r, err := NewRecord("orbit", "红蓝环绕", Source{Kind: "light_action", Contract: lightactionv2.Contract, Action: json.RawMessage(`{"kind":"orbit","colors":["red","blue"],"period_ms":3000}`)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func advPulse(t *testing.T) Record {
	t.Helper()
	r, err := NewRecord("pulse", "脉冲", Source{Kind: "light_action", Contract: lightaction.Contract, Action: json.RawMessage(`{"kind":"pulse","color":"lime"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func advObject(t *testing.T, r Record) map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m map[string]interface{}
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// advRehash creates a wire document with the edited original number lexemes,
// but a correct digest of their canonical meaning. This distinguishes a
// grammar/binding check from a rejection caused only by a stale digest.
func advRehash(t *testing.T, r Record, edit func(map[string]interface{})) []byte {
	t.Helper()
	m := advObject(t, r)
	edit(m)
	delete(m, "revision")
	payload, err := canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	m["revision"] = hash(RecordContract, payload)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func advMap(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	m, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return m
}

func advArray(t *testing.T, value interface{}) []interface{} {
	t.Helper()
	a, ok := value.([]interface{})
	if !ok {
		t.Fatalf("expected array, got %T", value)
	}
	return a
}

func advReject(t *testing.T, wire []byte) {
	t.Helper()
	if i, err := InspectRecord(wire); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid record accepted or misclassified: support=%+v error=%v wire=%s", i.Support(), err, wire)
	}
	if r, err := DecodeRecord(wire); err == nil || !errors.Is(err, ErrInvalid) || r.ID != "" {
		t.Fatalf("invalid record decoded: id=%q error=%v", r.ID, err)
	}
}

func TestAdversarialNormalizedIntegersKeepTheirWireGrammar(t *testing.T) {
	orbit, pulse := advOrbit(t), advPulse(t)
	for _, tc := range []struct {
		name string
		r    Record
		key  string
		lex  string
	}{
		{"orbit-period", orbit, "period_ms", "3000.0"},
		{"orbit-cycles", orbit, "cycles", "1.0"},
		{"orbit-segments", orbit, "segments", "4.0"},
		{"pulse-attack", pulse, "attack_ms", "60.0"},
		{"pulse-led", pulse, "led", "0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := advRehash(t, tc.r, func(m map[string]interface{}) {
				compiled := advMap(t, m["compiled"])
				action := advMap(t, compiled["normalized_action"])
				action[tc.key] = json.Number(tc.lex)
			})
			advReject(t, wire)
		})
	}
}

func TestAdversarialRehashedFrozenBindingCannotDrift(t *testing.T) {
	orbit, pulse := advOrbit(t), advPulse(t)
	tests := []struct {
		name string
		r    Record
		edit func(map[string]interface{})
	}{
		{"target", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, m["compiled"])["execution"])
			program := advMap(t, p["program"])
			frame := advMap(t, advArray(t, program["frames"])[0])
			advMap(t, advArray(t, frame["targets"])[0])["color"] = "#010203"
		}},
		{"known-recipe-for-other-segments", orbit, func(m map[string]interface{}) {
			advMap(t, m["compiled"])["recipe_id"] = "orbit-paired-linear-8/experimental-v1"
		}},
		{"source-color", orbit, func(m map[string]interface{}) {
			a := advMap(t, advMap(t, m["source"])["action"])
			a["colors"] = []interface{}{"green", "blue"}
		}},
		{"normalized-color", orbit, func(m map[string]interface{}) {
			a := advMap(t, advMap(t, m["compiled"])["normalized_action"])
			a["colors"] = []interface{}{"#00ff00", "#0000ff"}
		}},
		{"uppercase-frame-color", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			frame := advMap(t, advArray(t, p["frames"])[2])
			advMap(t, advArray(t, frame["targets"])[0])["color"] = "#1A001A"
		}},
		{"missing-frame-repeat", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(p, "repeat")
		}},
		{"missing-frame-finish", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(p, "on_finish")
		}},
		{"missing-frame-fade", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(advMap(t, advArray(t, p["frames"])[0]), "fade_ms")
		}},
		{"inapplicable-normalized-zero", orbit, func(m map[string]interface{}) {
			a := advMap(t, advMap(t, m["compiled"])["normalized_action"])
			a["led"] = json.Number("0")
		}},
		{"missing-normalized-default", orbit, func(m map[string]interface{}) {
			delete(advMap(t, advMap(t, m["compiled"])["normalized_action"]), "cycles")
		}},
		{"frame-led-order", orbit, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			frame := advMap(t, advArray(t, p["frames"])[0])
			a := advArray(t, frame["targets"])
			a[0], a[1] = a[1], a[0]
		}},
		{"legacy-repeat-default", pulse, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(p, "repeat")
		}},
		{"legacy-omitted-zero", pulse, func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(advMap(t, advArray(t, p["steps"])[0]), "hold_ms")
		}},
		{"legacy-gains-recipe", pulse, func(m map[string]interface{}) {
			advMap(t, m["compiled"])["recipe_id"] = "future/recipe"
		}},
		{"execution-tag-mismatch", orbit, func(m map[string]interface{}) {
			advMap(t, advMap(t, m["compiled"])["execution"])["kind"] = "program"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { advReject(t, advRehash(t, tc.r, tc.edit)) })
	}
}

func TestAdversarialUnknownRecipeIsReadOnlyNotVerified(t *testing.T) {
	wire := advRehash(t, advOrbit(t), func(m map[string]interface{}) {
		advMap(t, m["compiled"])["recipe_id"] = "orbit-future/experimental-v99"
	})
	i, err := InspectRecord(wire)
	if err != nil || i.Support().Verified || i.Support().Reason != "unsupported_recipe" {
		t.Fatalf("unknown recipe inspection: %+v %v", i.Support(), err)
	}
	if r, err := i.VerifiedRecord(); !errors.Is(err, ErrUnsupported) || r.ID != "" {
		t.Fatalf("unknown recipe became verified: %+v %v", r, err)
	}
	if _, err := DecodeRecord(wire); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe DecodeRecord: %v", err)
	}
	r := i.Record()
	if _, err := NormalizeRecord(r); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe NormalizeRecord: %v", err)
	}
	if _, err := EncodeRecord(r); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe EncodeRecord: %v", err)
	}
	if _, err := CanonicalRecordJSON(r); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe CanonicalRecordJSON: %v", err)
	}
	if _, err := ExecutionDigest(r); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe ExecutionDigest: %v", err)
	}
	if i.ExecutionDigest() == "" {
		t.Fatal("read-only inspection lost its data digest")
	}
	before := i.CanonicalJSON()
	again, err := InspectRecord(before)
	if err != nil || again.Support().Verified || !bytes.Equal(before, again.CanonicalJSON()) {
		t.Fatalf("read-only canonical roundtrip changed support or bytes: %v", err)
	}
	// No exported object or byte slice may mutate an inspection's private copy.
	r.Source.Action[0] = '!'
	r.Compiled.NormalizedAction[0] = '!'
	r.Compiled.Execution.FrameProgram.Initial[0].Color = "#ffffff"
	r.Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
	before[0] = '!'
	if !bytes.Equal(i.CanonicalJSON(), again.CanonicalJSON()) || i.Record().Compiled.Execution.FrameProgram.Initial[0].Color == "#ffffff" {
		t.Fatal("inspection aliased a returned record or canonical buffer")
	}
	// Unknown recipe is not an escape hatch for an invalid frozen low-level plan.
	bad := advRehash(t, advOrbit(t), func(m map[string]interface{}) {
		compiled := advMap(t, m["compiled"])
		compiled["recipe_id"] = "orbit-future/experimental-v99"
		p := advMap(t, advMap(t, compiled["execution"])["program"])
		advMap(t, advArray(t, p["initial"])[0])["led"] = json.Number("0")
	})
	advReject(t, bad)
}

func TestAdversarialUnknownRecipeKeepsCanonicalAndSourceChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]interface{})
	}{
		{"normalized-time-fraction", func(m map[string]interface{}) {
			advMap(t, advMap(t, m["compiled"])["normalized_action"])["period_ms"] = json.Number("3000.0")
		}},
		{"normalized-color-mismatch", func(m map[string]interface{}) {
			advMap(t, advMap(t, m["compiled"])["normalized_action"])["colors"] = []interface{}{"#00ff00", "#0000ff"}
		}},
		{"omitted-program-default", func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			delete(p, "repeat")
		}},
		{"uppercase-program-target", func(m map[string]interface{}) {
			p := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
			frame := advMap(t, advArray(t, p["frames"])[2])
			advMap(t, advArray(t, frame["targets"])[0])["color"] = "#1A001A"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := advRehash(t, advOrbit(t), func(m map[string]interface{}) {
				advMap(t, m["compiled"])["recipe_id"] = "orbit-future/experimental-v99"
				tc.edit(m)
			})
			advReject(t, wire)
		})
	}
}

func TestAdversarialReadonlySnapshotCannotYieldPartialVerifiedResults(t *testing.T) {
	unknown := advRehash(t, advOrbit(t), func(m map[string]interface{}) {
		advMap(t, m["compiled"])["recipe_id"] = "orbit-future/experimental-v99"
	})
	known, err := EncodeRecord(advPulse(t))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(map[string]interface{}{
		"schema_version": 2, "kind": SnapshotKind,
		"records": []json.RawMessage{known, unknown},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := InspectSnapshot(wire)
	if err != nil {
		t.Fatal(err)
	}
	items := snapshot.Records()
	if len(items) != 2 || items[0].Record().ID != "orbit" || items[0].Support().Verified || items[1].Record().ID != "pulse" || !items[1].Support().Verified {
		t.Fatalf("snapshot lost readonly status/order: %+v", items)
	}
	if values, err := snapshot.VerifiedRecords(); !errors.Is(err, ErrUnsupported) || values != nil {
		t.Fatalf("partially verified result escaped: %+v %v", values, err)
	}
	if values, err := DecodeSnapshot(wire); !errors.Is(err, ErrUnsupported) || values != nil {
		t.Fatalf("readonly snapshot decoded as verified: %+v %v", values, err)
	}
	if data, err := EncodeSnapshot([]Record{items[0].Record(), items[1].Record()}); !errors.Is(err, ErrUnsupported) || data != nil {
		t.Fatalf("readonly records entered strict encoder: %s %v", data, err)
	}
	before := snapshot.CanonicalJSON()
	roundtrip, err := InspectSnapshot(before)
	if err != nil || !bytes.Equal(before, roundtrip.CanonicalJSON()) || roundtrip.Records()[0].Support().Verified {
		t.Fatalf("readonly snapshot backup failed: %v", err)
	}
	mutated := items[0].Record()
	mutated.Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
	items[0] = Inspection{}
	before[0] = '!'
	if snapshot.Records()[0].Record().ID != "orbit" || !bytes.Equal(snapshot.CanonicalJSON(), roundtrip.CanonicalJSON()) {
		t.Fatal("returned snapshot state aliased its private records or bytes")
	}
}

func TestAdversarialLexemesAreRejectedBeforeNormalization(t *testing.T) {
	for _, action := range []string{
		`{"kind":"orbit","period_ms":3000.0}`,
		`{"kind":"orbit","cycles":1.0}`,
		`{"kind":"orbit","cycles":-0}`,
		`{"kind":"orbit","period_ms":3e3}`,
		`{"kind":"orbit","period_ms":9223372036854775808}`,
		`{"kind":"orbit","kind":"orbit"}`,
		`{"kind":"orbit","\u006bind":"orbit"}`,
		`{"kind":"orbit","colors":["red",null]}`,
		`{"kind":"orbit","colors":[{"space":"hsv","h_deg":0,"s_pct":100,"v_pct":1.0000000},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"hsv","h_deg":-0.0,"s_pct":100,"v_pct":100},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"rgb8","r":1.0,"g":0,"b":0},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"hsv","h_deg":0,"s_pct":100,"v_pct":100,"\u0076_pct":50},"blue"]}`,
	} {
		t.Run(action, func(t *testing.T) {
			if _, err := NewRecord("id", "title", Source{Kind: "light_action", Contract: lightactionv2.Contract, Action: json.RawMessage(action)}); err == nil || !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid authoring lexemes accepted: %s %v", action, err)
			}
		})
	}
	r := advOrbit(t)
	wire, err := EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"trailing-value":          append(append([]byte(nil), wire...), []byte(` {}`)...),
		"duplicate-top-level":     []byte(strings.Replace(string(wire), `"id":"orbit"`, `"id":"orbit","\u0069d":"orbit"`, 1)),
		"unpaired-high-surrogate": []byte(strings.Replace(string(wire), `"title":"红蓝环绕"`, `"title":"\ud800"`, 1)),
		"unpaired-low-surrogate":  []byte(strings.Replace(string(wire), `"title":"红蓝环绕"`, `"title":"\udc00"`, 1)),
		"broken-surrogate-pair":   []byte(strings.Replace(string(wire), `"title":"红蓝环绕"`, `"title":"\ud800\u0041"`, 1)),
		"null-source":             []byte(strings.Replace(string(wire), `"source":{`, `"source":null,"ignored":{`, 1)),
		"invalid-utf8":            append([]byte{0xff}, wire...),
		"utf8-surrogate":          append([]byte{0xed, 0xa0, 0x80}, wire...),
		"bom":                     append([]byte{0xef, 0xbb, 0xbf}, wire...),
	} {
		t.Run(name, func(t *testing.T) { advReject(t, bad) })
	}
}

func TestAdversarialHSVSourceCanonicalizationAndCopyIsolation(t *testing.T) {
	input := json.RawMessage(`{"kind":"set","color":{"space":"hsv","h_deg":240.000000,"s_pct":100.0,"v_pct":50.000000}}`)
	r, err := NewRecord("hsv", "蓝色", Source{Kind: "light_action", Contract: lightaction.Contract, Action: input})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"color":{"h_deg":240,"s_pct":100,"space":"hsv","v_pct":50},"kind":"set"}`
	if string(r.Source.Action) != want || r.Compiled.Execution.Program.Steps[0].Color != "#000080" {
		t.Fatalf("HSV source not preserved/canonicalized: %s %+v", r.Source.Action, r.Compiled.Execution.Program)
	}
	input[0] = '!'
	if string(r.Source.Action) != want {
		t.Fatal("constructor retained caller's RawMessage")
	}
	encoded, err := EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	i, err := InspectRecord(encoded)
	if err != nil || !i.Support().Verified {
		t.Fatalf("verified inspection: %v", err)
	}
	copy, err := i.VerifiedRecord()
	if err != nil {
		t.Fatal(err)
	}
	copy.Compiled.Execution.Program.Steps[0].Color = "#ffffff"
	copy.Source.Action[0] = '!'
	copy.Compiled.NormalizedAction[0] = '!'
	encoded[0] = '!'
	if i.Record().Compiled.Execution.Program.Steps[0].Color != "#000080" || string(i.Record().Source.Action) != want || !bytes.Contains(i.CanonicalJSON(), []byte(`"title":"蓝色"`)) {
		t.Fatal("verified inspection aliases input or returned slices")
	}
	variant, err := NewRecord("hsv", "蓝色", Source{Kind: "light_action", Contract: lightaction.Contract, Action: json.RawMessage(want)})
	if err != nil || variant.Revision != r.Revision {
		t.Fatalf("HSV decimal trailing zero changed canonical identity: %v", err)
	}
}

func TestAdversarialBoundsAndUnicodeIdentity(t *testing.T) {
	source := Source{Kind: "light_action", Contract: lightaction.Contract, Action: json.RawMessage(`{"kind":"off"}`)}
	if _, err := NewRecord(strings.Repeat("A", 64), strings.Repeat("😀", 256), source); err != nil {
		t.Fatalf("valid exact identity bounds rejected: %v", err)
	}
	for _, id := range []string{"", "_x", "-x", "x/y", "é", strings.Repeat("A", 65)} {
		if _, err := NewRecord(id, "title", source); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid ID accepted: %q %v", id, err)
		}
	}
	for _, title := range []string{"", strings.Repeat("😀", 257), string([]byte{0xed, 0xa0, 0x80})} {
		if _, err := NewRecord("id", title, source); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid title accepted: %q %v", title, err)
		}
	}
	a, err := NewRecord("id", "é", source)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRecord("id", "e\u0301", source)
	if err != nil || a.Revision == b.Revision {
		t.Fatal("title was implicitly Unicode-normalized", err)
	}
	for _, data := range [][]byte{
		[]byte(strings.Repeat(" ", MaxBytes+1)),
		[]byte(strings.Repeat("[", MaxDepth+2) + "0" + strings.Repeat("]", MaxDepth+2)),
	} {
		if _, err := parse(data); !errors.Is(err, ErrInvalid) {
			t.Fatalf("JSON limit not enforced: %v", err)
		}
	}
	source.Action = append([]byte(`{"kind":"off"}`), bytes.Repeat([]byte(" "), lightaction.MaxRequestBytes)...)
	if _, err := NewRecord("id", "title", source); !errors.Is(err, ErrInvalid) {
		t.Fatalf("authoring byte limit not enforced: %v", err)
	}
	if _, err := canonical(map[string]interface{}{"large": strings.Repeat("\n", MaxBytes/2)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("canonical expansion limit not enforced: %v", err)
	}
}
