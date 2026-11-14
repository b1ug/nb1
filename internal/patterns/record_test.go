package patterns_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patterns"
)

func intPointer(value int) *int        { return &value }
func textPointer(value string) *string { return &value }

func steadyRecord(t *testing.T, id, title string) patterns.Record {
	t.Helper()
	value, err := patterns.NewRecord(id, title, patterns.Source{Effect: &control.Effect{Kind: "steady", Color: "#00A000"}})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRecordDefaultsCaseAndSourceIdentity(t *testing.T) {
	one := steadyRecord(t, "cue", "提示")
	two, err := patterns.NewRecord("cue", "提示", patterns.Source{Effect: &control.Effect{Kind: "steady", Color: "#00a000", LED: 0, FadeMS: intPointer(0)}})
	if err != nil || !reflect.DeepEqual(one, two) {
		t.Fatalf("equivalent defaults/case changed identity: %#v %v", two, err)
	}
	program, err := patterns.NewRecord("cue", "提示", patterns.Source{Program: &control.Program{Steps: []control.Step{{Color: "#00A000"}}, OnFinish: "hold"}})
	if err != nil || !reflect.DeepEqual(program.CompiledProgram, one.CompiledProgram) || program.Revision == one.Revision {
		t.Fatalf("source kind lost from digest: %#v %v", program, err)
	}
	for _, source := range []control.Program{
		{Steps: []control.Step{{Color: "#00A000"}}},
		{Steps: []control.Step{{LED: 0, Color: "#00a000", FadeMS: 0, HoldMS: 0}}, Repeat: 1, OnFinish: "off"},
	} {
		value, err := patterns.NewRecord("raw", "raw", patterns.Source{Program: &source})
		if err != nil {
			t.Fatal(err)
		}
		if source.Repeat == 0 {
			one = value
		} else if !reflect.DeepEqual(one, value) {
			t.Fatal("Program omitted/explicit defaults changed revision")
		}
	}
	for _, change := range []struct{ id, title string }{{"Cue", "raw"}, {"raw", "raw "}, {"raw", "é"}, {"raw", "e\u0301"}} {
		value, err := patterns.NewRecord(change.id, change.title, one.Source)
		if err != nil || value.Revision == one.Revision {
			t.Fatalf("id/title byte identity omitted: %v %v", change, err)
		}
	}
	composed := steadyRecord(t, "unicode", "é")
	decomposed := steadyRecord(t, "unicode", "e\u0301")
	if composed.Revision == decomposed.Revision {
		t.Fatal("title was implicitly Unicode normalized")
	}
}

func TestRecordPulseExpansionAndDefaultFreeze(t *testing.T) {
	value, err := patterns.NewRecord("pulse", "短提示", patterns.Source{Effect: &control.Effect{Kind: "pulse", Color: "#00A000", Cycles: intPointer(2), PeriodMS: intPointer(800)}})
	if err != nil {
		t.Fatal(err)
	}
	if value.Source.Effect == nil || value.Source.Effect.OnFinish == nil || *value.Source.Effect.OnFinish != "off" || value.CompiledProgram.Repeat != 2 || len(value.CompiledProgram.Steps) != 3 || value.CompiledProgram.Steps[1].FadeMS != 380 || value.CompiledProgram.Steps[2].FadeMS != 390 {
		t.Fatalf("pulse800x2 changed: %#v", value)
	}
	defaults, err := patterns.NewRecord("pulse", "default", patterns.Source{Effect: &control.Effect{Kind: "pulse", Color: "#123456"}})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := patterns.NewRecord("pulse", "default", patterns.Source{Effect: &control.Effect{Kind: "pulse", Color: "#123456", LED: 0, Cycles: intPointer(1), PeriodMS: intPointer(1000), OnFinish: textPointer("off")}})
	if err != nil || !reflect.DeepEqual(defaults, explicit) {
		t.Fatal("pulse defaults were not frozen identically", err)
	}
}

func TestRecordRejectsUnsupportedContractAndTampering(t *testing.T) {
	original := steadyRecord(t, "cue", "提示")
	for _, test := range []struct {
		name string
		edit func(*patterns.Record)
	}{
		{"title", func(r *patterns.Record) { r.Title += "!" }},
		{"id", func(r *patterns.Record) { r.ID += "2" }},
		{"source", func(r *patterns.Record) { r.Source.Effect.Color = "#ffffff" }},
		{"compiled", func(r *patterns.Record) { r.CompiledProgram.Steps[0].Color = "#ffffff" }},
		{"compiled-invalid", func(r *patterns.Record) { r.CompiledProgram.Steps[0].FadeMS = 1 }},
		{"revision", func(r *patterns.Record) { r.Revision = "sha256:" + strings.Repeat("0", 64) }},
		{"missing-revision", func(r *patterns.Record) { r.Revision = "" }},
		{"uppercase-revision", func(r *patterns.Record) { r.Revision = strings.ToUpper(r.Revision) }},
		{"old-contract", func(r *patterns.Record) { r.CompileContract = "nb1-program-effect/v0" }},
		{"future-contract", func(r *patterns.Record) { r.CompileContract = "nb1-program-effect/v2" }},
		{"missing-contract", func(r *patterns.Record) { r.CompileContract = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := patterns.NormalizeRecord(original)
			if err != nil {
				t.Fatal(err)
			}
			test.edit(&value)
			if got, err := patterns.NormalizeRecord(value); err == nil || !reflect.DeepEqual(got, patterns.Record{}) {
				t.Fatalf("tampered record accepted: %#v %v", got, err)
			}
			if data, err := patterns.CanonicalRecordJSON(value); err == nil || data != nil {
				t.Fatal("tampered record exposed canonical bytes")
			}
			if data, err := patterns.EncodeSnapshot([]patterns.Record{original, value}); err == nil || data != nil {
				t.Fatal("partial export returned after a bad record")
			}
		})
	}
}

func TestRecordCopiesAreIndependent(t *testing.T) {
	steps := []control.Step{{Color: "#112233"}}
	source := control.Program{Steps: steps}
	value, err := patterns.NewRecord("raw", "raw", patterns.Source{Program: &source})
	if err != nil {
		t.Fatal(err)
	}
	steps[0].Color = "#ffffff"
	if value.Source.Program.Steps[0].Color != "#112233" || value.CompiledProgram.Steps[0].Color != "#112233" || source.Repeat != 0 || source.OnFinish != "" {
		t.Fatal("NewRecord aliased or mutated source input")
	}
	copy, err := patterns.NormalizeRecord(value)
	if err != nil {
		t.Fatal(err)
	}
	copy.Source.Program.Steps[0].Color = "#000000"
	if copy.CompiledProgram.Steps[0].Color != "#112233" || value.Source.Program.Steps[0].Color != "#112233" {
		t.Fatal("source and compiled/result copies share a backing array")
	}
	fade := 100
	effect := control.Effect{Kind: "steady", Color: "#abcdef", FadeMS: &fade}
	value, err = patterns.NewRecord("fx", "fx", patterns.Source{Effect: &effect})
	if err != nil {
		t.Fatal(err)
	}
	fade = 200
	copy, err = patterns.NormalizeRecord(value)
	if err != nil {
		t.Fatal(err)
	}
	*copy.Source.Effect.FadeMS = 300
	if *value.Source.Effect.FadeMS != 100 || value.CompiledProgram.Steps[0].FadeMS != 100 {
		t.Fatal("Effect pointers aliased caller or another returned copy")
	}
	encoded, err := patterns.EncodeSnapshot([]patterns.Record{value})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := patterns.DecodePackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0].CompiledProgram.Steps[0].Color = "#000000"
	*decoded[0].Source.Effect.FadeMS = 400
	if *value.Source.Effect.FadeMS != 100 || value.CompiledProgram.Steps[0].Color != "#abcdef" {
		t.Fatal("snapshot decode aliased an earlier Record")
	}
}

func TestRecordIDTitleAndSourceBounds(t *testing.T) {
	for _, id := range []string{"A", "0", "a_b-C9", strings.Repeat("a", 64)} {
		if _, err := patterns.NewRecord(id, strings.Repeat("😀", 256), patterns.Source{Effect: &control.Effect{Kind: "steady", Color: "#000000"}}); err != nil {
			t.Fatalf("valid identity bound: %q %v", id, err)
		}
	}
	for _, id := range []string{"", "-a", "_a", "a.b", "a/b", "中文", "a b", strings.Repeat("a", 65), string([]byte{0xff})} {
		if _, err := patterns.NewRecord(id, "title", patterns.Source{Effect: &control.Effect{Kind: "steady", Color: "#000000"}}); err == nil {
			t.Fatalf("invalid id accepted: %q", id)
		}
	}
	for _, title := range []string{"", strings.Repeat("😀", 257), string([]byte{0xff}), string([]byte{0xed, 0xa0, 0x80})} {
		if _, err := patterns.NewRecord("id", title, patterns.Source{Effect: &control.Effect{Kind: "steady", Color: "#000000"}}); err == nil {
			t.Fatalf("invalid title accepted: %q", title)
		}
	}
	for _, source := range []patterns.Source{
		{}, {Program: &control.Program{}, Effect: &control.Effect{}},
		{Effect: &control.Effect{Kind: "steady", Color: "green"}},
		{Effect: &control.Effect{Kind: "steady", Color: "#0f8"}},
		{Effect: &control.Effect{Kind: "steady", Color: "#000000", Cycles: intPointer(0)}},
		{Effect: &control.Effect{Kind: "pulse", Color: "#000000", FadeMS: intPointer(0)}},
		{Effect: &control.Effect{Kind: "pulse", Color: "#000000", Cycles: intPointer(0)}},
		{Program: &control.Program{Steps: []control.Step{{Color: "#112233", LED: 3}}}},
	} {
		if value, err := patterns.NewRecord("id", "title", source); err == nil || !reflect.DeepEqual(value, patterns.Record{}) {
			t.Fatalf("invalid source accepted: %#v %v", value, err)
		}
	}
}

func TestRecordCanonicalStringContract(t *testing.T) {
	var c0 strings.Builder
	for i := 0; i < 32; i++ {
		c0.WriteByte(byte(i))
	}
	title := c0.String() + `"\</>&` + "\u2028\u2029😀"
	value := steadyRecord(t, "escape", title)
	encoded, err := patterns.CanonicalRecordJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if !bytes.Contains(encoded, []byte(fmt.Sprintf(`\u00%02x`, i))) {
			t.Fatalf("C0 did not use fixed lowercase escape: %d %s", i, encoded)
		}
	}
	if bytes.Contains(encoded, []byte(`\n`)) || bytes.Contains(encoded, []byte(`\t`)) || !bytes.Contains(encoded, []byte(`\"\\</>&`+"\u2028\u2029😀")) || encoded[len(encoded)-1] != '}' {
		t.Fatalf("noncanonical escaping: %s", encoded)
	}
	var decoded struct{ Title string }
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Title != title {
		t.Fatal("canonical string did not roundtrip exact Unicode", err)
	}
}
