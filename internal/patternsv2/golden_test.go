package patternsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/b1ug/nb1/internal/patterns"
)

func designFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "design", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDesignGoldenRecords(t *testing.T) {
	var expected struct {
		Records []struct {
			File     string `json:"file"`
			Revision string `json:"revision"`
			Digest   string `json:"execution_digest"`
			Duration int64  `json:"expected_nominal_duration_ms"`
			Frames   int    `json:"expected_total_frames"`
			Writes   int    `json:"expected_planned_target_writes"`
			Steps    int    `json:"expected_total_steps"`
		} `json:"records"`
	}
	if err := json.Unmarshal(designFile(t, "expectations.json"), &expected); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range expected.Records {
		t.Run(fixture.File, func(t *testing.T) {
			data := designFile(t, fixture.File)
			r, err := DecodeRecord(data)
			if err != nil {
				t.Fatal(err)
			}
			if r.Revision != fixture.Revision {
				t.Fatalf("revision=%s want %s", r.Revision, fixture.Revision)
			}
			payload, err := CanonicalRecordJSON(r)
			if err != nil {
				t.Fatal(err)
			}
			payloadFile := fixture.File[:len(fixture.File)-len("record.json")] + "canonical-payload.json"
			if !bytes.Equal(payload, designFile(t, payloadFile)) {
				t.Fatal("canonical payload differs from independent Python golden")
			}
			digest, err := ExecutionDigest(r)
			if err != nil || digest != fixture.Digest {
				t.Fatalf("execution digest=%s: %v", digest, err)
			}
			rebuilt, err := NewRecord(r.ID, r.Title, r.Source)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rebuilt, r) {
				t.Fatal("rebuilding source changed frozen record")
			}
			encoded, err := EncodeRecord(r)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeRecord(encoded)
			if err != nil || !reflect.DeepEqual(decoded, r) {
				t.Fatalf("round trip: %v", err)
			}
			if fixture.Frames != 0 {
				preview, err := control.PreviewFrameProgram(*r.Compiled.Execution.FrameProgram)
				if err != nil || preview.NominalDurationMS != fixture.Duration || preview.TotalFrames != fixture.Frames || preview.PlannedTargetWrites != fixture.Writes {
					t.Fatalf("frozen frame facts: %+v %v", preview, err)
				}
			} else if fixture.Steps != 0 {
				preview, err := control.Preview(r.Compiled.Execution.Program, nil)
				if err != nil || preview.NominalDurationMS != fixture.Duration || preview.TotalSteps != fixture.Steps {
					t.Fatalf("frozen program facts: %+v %v", preview, err)
				}
			}
		})
	}
}

func TestDesignCanonicalVectors(t *testing.T) {
	var vectors []struct{ Label, Input, Canonical string }
	if err := json.Unmarshal(designFile(t, "canonical-vectors.json"), &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Label, func(t *testing.T) {
			n, err := parse([]byte(vector.Input))
			if err != nil {
				t.Fatal(err)
			}
			b, err := canonical(n)
			if err != nil || string(b) != vector.Canonical {
				t.Fatalf("got %s (%v), want %s", b, err, vector.Canonical)
			}
		})
	}
}

func TestAllExistingHighLevelActionsRemainTyped(t *testing.T) {
	for _, action := range []string{
		`{"kind":"set","color":"red"}`, `{"kind":"off"}`, `{"kind":"fade","color":"blue","duration_ms":20}`,
		`{"kind":"blink","color":"lime","cycles":2}`, `{"kind":"breathe","color":"white"}`, `{"kind":"pulse","color":"lime"}`,
		`{"kind":"alternate","colors":["red","blue"]}`, `{"kind":"rainbow"}`,
	} {
		t.Run(action, func(t *testing.T) {
			s := Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(action)}
			r, err := NewRecord("action", "高层动作", s)
			if err != nil {
				t.Fatal(err)
			}
			if r.Compiled.Execution.Program == nil || r.Compiled.Execution.FrameProgram != nil || r.Compiled.RecipeID != "" {
				t.Fatal("v1 action changed execution kind")
			}
			b, err := EncodeRecord(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeRecord(b); err != nil {
				t.Fatal(err)
			}
		})
	}
	r, err := NewRecord("orbit8", "八段", Source{Kind: "light_action", Contract: lightactionv2.Contract, Action: []byte(`{"kind":"orbit","segments":8}`)})
	if err != nil || r.Compiled.RecipeID != "orbit-paired-linear-8/experimental-v1" || len(r.Compiled.Execution.FrameProgram.Frames) != 8 {
		t.Fatalf("eight segment recipe: %+v %v", r, err)
	}
}

func TestAuthorRepresentationAndHSVCanonicalization(t *testing.T) {
	named, err := DecodeRecord(designFile(t, "records/orbit-red-blue.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	hex, err := DecodeRecord(designFile(t, "records/orbit-hex-equivalent.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if named.Revision == hex.Revision || !reflect.DeepEqual(named.Compiled, hex.Compiled) {
		t.Fatal("author meaning collapsed or execution changed")
	}
	if bytes.Contains(named.Source.Action, []byte(`"leds"`)) || !bytes.Contains(named.Source.Action, []byte(`"red"`)) {
		t.Fatal("author defaults or color notation overwritten")
	}
	a, err := NewRecord("hsv", "HSV", Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(`{"kind":"set","color":{"space":"hsv","h_deg":240.000000,"s_pct":100.000000,"v_pct":50.500000}}`)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRecord("hsv", "HSV", Source{Kind: "light_action", Contract: lightaction.Contract, Action: []byte(`{"color":{"v_pct":50.5,"s_pct":100,"h_deg":240,"space":"hsv"},"kind":"set"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != b.Revision || !bytes.Contains(a.Source.Action, []byte(`"v_pct":50.5`)) {
		t.Fatal("exact decimal normalization differs")
	}
}

func TestDesignNegativeRecordsAndReadonlyUnknownRecipe(t *testing.T) {
	for _, name := range []string{"rehashed-source-mismatch.record.json", "wrong-revision.record.json", "duplicate-key.json", "explicit-null.json"} {
		t.Run(name, func(t *testing.T) {
			if _, err := InspectRecord(designFile(t, "cases/"+name)); err == nil {
				t.Fatal("accepted invalid record")
			}
		})
	}
	data := designFile(t, "cases/unknown-recipe.record.json")
	i, err := InspectRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if i.Support().Verified || i.Support().Reason != "unsupported_recipe" || len(i.CanonicalJSON()) == 0 || i.ExecutionDigest() == "" {
		t.Fatalf("unknown recipe inspection: %+v", i.Support())
	}
	if _, err := DecodeRecord(data); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe strict decode: %v", err)
	}
	if _, err := i.VerifiedRecord(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe upgraded: %v", err)
	}
	if _, err := EncodeRecord(i.Record()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown recipe publish accepted: %v", err)
	}
	i2, err := InspectRecord(i.CanonicalJSON())
	if err != nil || !bytes.Equal(i.CanonicalJSON(), i2.CanonicalJSON()) {
		t.Fatalf("read-only backup changed: %v", err)
	}
}

func TestFrozenSnapshotsAndLegacyV1Unchanged(t *testing.T) {
	data := designFile(t, "library.snapshot.json")
	values, err := DecodeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSnapshot([]Record{values[1], values[0]})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSnapshot(encoded)
	if err != nil || !reflect.DeepEqual(decoded, values) {
		t.Fatalf("snapshot ordering/roundtrip: %v", err)
	}
	if _, err := DecodeSnapshot(designFile(t, "cases/duplicate-id.snapshot.json")); err == nil {
		t.Fatal("duplicate id snapshot accepted")
	}
	if _, err := EncodeSnapshot([]Record{values[0], values[0]}); err == nil {
		t.Fatal("duplicate id encode accepted")
	}
	empty, err := EncodeSnapshot(nil)
	if err != nil || string(empty) != `{"kind":"nb1-pattern-snapshot/experimental-v2","records":[],"schema_version":2}` {
		t.Fatalf("empty snapshot: %s %v", empty, err)
	}
	if _, err := patterns.DecodePackage(encoded); err == nil {
		t.Fatal("v1 accepted v2 snapshot")
	}
	legacy := designFile(t, "legacy-v1.snapshot.json")
	v1, err := patterns.DecodePackage(legacy)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := patterns.EncodeSnapshot(v1)
	if err != nil || !bytes.Equal(legacy, reencoded) {
		t.Fatalf("legacy bytes changed: %v", err)
	}
	if _, err := DecodeSnapshot(legacy); err == nil {
		t.Fatal("v2 reinterpreted legacy snapshot")
	}
}
