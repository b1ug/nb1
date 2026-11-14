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

const authoringCue = `{"schema_version":1,"patterns":[{"id":"cue","title":"提示","effect":{"kind":"steady","color":"#00A000"}}]}`

func TestPackageAuthoringSnapshotRoundtripAndOrdering(t *testing.T) {
	first, err := patterns.DecodePackage([]byte(authoringCue))
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	second := steadyRecord(t, "second", "second")
	first = append(first, second)
	encoded, err := patterns.EncodeSnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte(`{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[{"id":"cue","title":`)) || encoded[len(encoded)-1] != '}' || bytes.Contains(encoded, []byte(`\u63d0`)) {
		t.Fatalf("snapshot shape or UTF-8 encoding: %s", encoded)
	}
	decoded, err := patterns.DecodePackage(encoded)
	if err != nil || !reflect.DeepEqual(first, decoded) {
		t.Fatalf("snapshot lost frozen fields: %#v %v", decoded, err)
	}
	again, err := patterns.EncodeSnapshot(decoded)
	if err != nil || !bytes.Equal(again, encoded) {
		t.Fatal("canonical snapshot was not byte-stable", err)
	}
	reversed, err := patterns.EncodeSnapshot([]patterns.Record{second, first[0]})
	if err != nil {
		t.Fatal(err)
	}
	values, err := patterns.DecodePackage(reversed)
	if err != nil || values[0].ID != "second" || values[0].Revision != second.Revision || values[1].Revision != first[0].Revision {
		t.Fatal("package ordering changed per-record identity", err)
	}
	// Same code point with alternate JSON spelling, and every object key in a
	// different order, must still canonicalize to exactly the same frozen bytes.
	var data interface{}
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatal(err)
	}
	noncanonical, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	noncanonical = bytes.ReplaceAll(noncanonical, []byte("提示"), []byte(`\u63d0\u793a`))
	values, err = patterns.DecodePackage(noncanonical)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := patterns.EncodeSnapshot(values)
	if err != nil || !bytes.Equal(canonical, encoded) {
		t.Fatal("key order/space/Unicode escape changed snapshot", err)
	}
}

func TestPackageEmptyAndDuplicateIDs(t *testing.T) {
	const empty = `{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[]}`
	for _, input := range []string{`{"schema_version":1,"patterns":[]}`, empty} {
		values, err := patterns.DecodePackage([]byte(input))
		if err != nil || values == nil || len(values) != 0 {
			t.Fatalf("empty package is not a valid non-nil empty result: %#v %v", values, err)
		}
		encoded, err := patterns.EncodeSnapshot(values)
		if err != nil || string(encoded) != empty {
			t.Fatalf("empty snapshot: %s %v", encoded, err)
		}
	}
	encoded, err := patterns.EncodeSnapshot(nil)
	if err != nil || string(encoded) != empty {
		t.Fatal("nil library should export an empty array", err)
	}
	duplicate := strings.Replace(authoringCue, `}]}`, `},{"id":"cue","title":"提示","effect":{"kind":"steady","color":"#00a000"}}]}`, 1)
	if got, err := patterns.DecodePackage([]byte(duplicate)); err == nil || got != nil {
		t.Fatal("duplicate authoring ID was silently deduplicated", err)
	}
	value := steadyRecord(t, "cue", "提示")
	if data, err := patterns.EncodeSnapshot([]patterns.Record{value, value}); err == nil || data != nil {
		t.Fatal("duplicate snapshot ID accepted", err)
	}
	one, err := patterns.EncodeSnapshot([]patterns.Record{value})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(one, &wire); err != nil {
		t.Fatal(err)
	}
	wire["records"] = []patterns.Record{value, value}
	duplicateSnapshot, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := patterns.DecodePackage(duplicateSnapshot); err == nil || got != nil {
		t.Fatal("duplicate frozen ID accepted", err)
	}
}

func TestPackageStrictWireRejectsAmbiguityAndLegacy(t *testing.T) {
	tests := []string{
		``, `null`, `[]`, `{}`, `{"patterns":[]}`, `{"schema_version":2,"patterns":[]}`,
		`{"schema_version":1,"patterns":null}`, `{"schema_version":1,"records":[]}`,
		`{"schema_version":1,"kind":"future","records":[]}`,
		`{"schema_version":1,"kind":"nb1-pattern-snapshot","patterns":[]}`,
		`{"schema_version":1,"kind":"nb1-pattern-snapshot","patterns":[],"records":[]}`,
		`{"schema_version":1,"patterns":[],"serial":"device"}`,
		`{"schema_version":1,"patterns":[],"Schema_version":1}`,
		`{"schema_version":1,"patterns":[],"schema_\u0076ersion":1}`,
		`{"schema_version":1,"patterns":[]} {}`,
		`{"name":"legacy","repeat":0,"seq":["red"],"len":1}`,
		strings.Replace(authoringCue, `"title":"提示"`, `"title":null`, 1),
		strings.Replace(authoringCue, `"title":"提示"`, `"title":"提示","ti\u0074le":"提示"`, 1),
		strings.Replace(authoringCue, `"title":"提示"`, `"Title":"提示"`, 1),
		strings.Replace(authoringCue, `"id":"cue"`, `"id":"cue","revision":"self-reported"`, 1),
		strings.Replace(authoringCue, `"effect":`, `"effect":null,"program":`, 1),
		strings.Replace(authoringCue, `"color":"#00A000"`, `"color":"green"`, 1),
		strings.Replace(authoringCue, `"color":"#00A000"`, `"color":"#0f8"`, 1),
		strings.Replace(authoringCue, `"color":"#00A000"`, `"color":{"space":"rgb8","r":0,"g":255,"b":0}`, 1),
		strings.Replace(authoringCue, `"color":"#00A000"`, `"color":"#00A000","led":0,"\u006ced":0`, 1),
		strings.Replace(authoringCue, `"color":"#00A000"`, `"color":"#00A000","cycles":0`, 1),
	}
	for index, input := range tests {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if got, err := patterns.DecodePackage([]byte(input)); err == nil || got != nil {
				t.Fatalf("ambiguous/legacy input accepted: %s %#v %v", input, got, err)
			}
		})
	}
	for _, number := range []string{"1.0", "1e0", "1E+0", "-0", "-1", "01", "+1"} {
		input := strings.Replace(authoringCue, `"schema_version":1`, `"schema_version":`+number, 1)
		if got, err := patterns.DecodePackage([]byte(input)); err == nil || got != nil {
			t.Fatalf("bad version lexeme accepted: %s", number)
		}
	}
	for _, field := range []string{"led", "fade_ms"} {
		for _, number := range []string{"0.0", "0e0", "-0", "-1"} {
			input := strings.Replace(authoringCue, `"color":"#00A000"`, `"color":"#00A000","`+field+`":`+number, 1)
			if got, err := patterns.DecodePackage([]byte(input)); err == nil || got != nil {
				t.Fatalf("bad field lexeme accepted: %s %s", field, number)
			}
		}
	}
}

func TestPackageUnicodeSurrogatesAndPreciseTitles(t *testing.T) {
	for _, title := range []string{`"\ud800"`, `"\udfff"`, `"\ud800x"`, `"\ud800\ud800"`, `"\udc00\ud800"`, `"\ud800\u0000"`, `"\ud800\\udfff"`, `"\uD800\uDE0"`} {
		input := strings.Replace(authoringCue, `"提示"`, title, 1)
		if got, err := patterns.DecodePackage([]byte(input)); err == nil || got != nil {
			t.Fatalf("unpaired surrogate accepted: %s", title)
		}
	}
	for _, title := range []struct{ raw, want string }{
		{`"\ud83d\ude00"`, "😀"}, {`"😀"`, "😀"},
		{`"\ud800\udc00"`, "\U00010000"}, {`"\udbff\udfff"`, "\U0010ffff"},
	} {
		input := strings.Replace(authoringCue, `"提示"`, title.raw, 1)
		values, err := patterns.DecodePackage([]byte(input))
		if err != nil || values[0].Title != title.want || values[0].Revision != steadyRecord(t, "cue", title.want).Revision {
			t.Fatal("valid surrogate pair was not equivalent to direct UTF-8", err)
		}
	}
	input := bytes.Replace([]byte(authoringCue), []byte("提示"), []byte{0xff}, 1)
	if _, err := patterns.DecodePackage(input); err == nil {
		t.Fatal("invalid UTF-8 replaced silently")
	}
	for _, title := range []string{" ", "\x00", "\u2028\u2029", `\ud800`} {
		literal, err := json.Marshal(title)
		if err != nil {
			t.Fatal(err)
		}
		input := strings.Replace(authoringCue, `"提示"`, string(literal), 1)
		values, err := patterns.DecodePackage([]byte(input))
		if err != nil || values[0].Title != title {
			t.Fatalf("title was trimmed or escape-interpreted twice: %q %v", title, err)
		}
	}
}

func TestPackageSnapshotRejectsAnyBadRecordWithoutPartialResult(t *testing.T) {
	one, two := steadyRecord(t, "one", "one"), steadyRecord(t, "two", "two")
	for _, change := range []func(*patterns.Record){
		func(r *patterns.Record) { r.Revision = strings.Repeat("0", 71) },
		func(r *patterns.Record) { r.CompileContract = "nb1-program-effect/v0" },
		func(r *patterns.Record) { r.CompileContract = "nb1-program-effect/v2" },
		func(r *patterns.Record) { r.CompiledProgram.Steps[0].HoldMS = 1 },
		func(r *patterns.Record) { r.Source.Effect.Color = "#ffffff" },
	} {
		value, err := patterns.NormalizeRecord(two)
		if err != nil {
			t.Fatal(err)
		}
		change(&value)
		wire, err := json.Marshal(struct {
			Version int               `json:"schema_version"`
			Kind    string            `json:"kind"`
			Records []patterns.Record `json:"records"`
		}{1, patterns.SnapshotKind, []patterns.Record{one, value}})
		if err != nil {
			t.Fatal(err)
		}
		if records, err := patterns.DecodePackage(wire); err == nil || records != nil {
			t.Fatal("tampered snapshot returned partial records", err)
		}
	}
}

func TestPackageFrozenEquivalentSpellingAndOwnedBuffers(t *testing.T) {
	// An external snapshot may spell the same v1 source with omitted defaults
	// and uppercase Hex6. The verified returned value is canonical, not a new
	// contract or a changed revision.
	value := steadyRecord(t, "cue", "提示")
	input := []byte(`{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[{"id":"cue","title":"\u63d0\u793a","source":{"effect":{"kind":"steady","color":"#00A000"}},"compiled_program":{"steps":[{"color":"#00A000"}],"on_finish":"hold"},"compile_contract":"nb1-program-effect/v1","revision":"` + value.Revision + `"}]}`)
	values, err := patterns.DecodePackage(input)
	if err != nil || len(values) != 1 || !reflect.DeepEqual(values[0], value) {
		t.Fatal("same-contract equivalent snapshot changed identity", err)
	}
	for i := range input {
		input[i] = 'x'
	}
	canonical, err := patterns.CanonicalRecordJSON(values[0])
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), canonical...)
	canonical[0] = 'x'
	again, err := patterns.CanonicalRecordJSON(values[0])
	if err != nil || !bytes.Equal(again, want) || values[0].Title != "提示" {
		t.Fatal("returned canonical or record data aliased caller buffers", err)
	}
	for _, bad := range []string{
		strings.Replace(string(mustSnapshot(t, value)), `"source":{`, `"source":null,"old_source":{`, 1),
		strings.Replace(string(mustSnapshot(t, value)), `"source":{"effect":`, `"source":{"program":{},"effect":`, 1),
		strings.Replace(string(mustSnapshot(t, value)), `"compiled_program":`, `"compiled_program":null,"old_compiled":`, 1),
		strings.Replace(string(mustSnapshot(t, value)), `"revision":`, `"Revision":`, 1),
	} {
		if got, err := patterns.DecodePackage([]byte(bad)); err == nil || got != nil {
			t.Fatal("bad snapshot shape accepted", err)
		}
	}
}

func mustSnapshot(t *testing.T, value patterns.Record) []byte {
	t.Helper()
	data, err := patterns.EncodeSnapshot([]patterns.Record{value})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPackageInputAndCountLimits(t *testing.T) {
	empty := `{"schema_version":1,"patterns":[]}`
	exact := []byte(empty + strings.Repeat(" ", patterns.MaxPackageBytes-len(empty)))
	if got, err := patterns.DecodePackage(exact); err != nil || len(got) != 0 {
		t.Fatal("exact 1MiB input was rejected", err)
	}
	if got, err := patterns.DecodePackage(append(exact, ' ')); err == nil || got != nil {
		t.Fatal("input above 1MiB accepted", err)
	}
	values := make([]patterns.Record, 0, patterns.MaxRecords+1)
	for i := 0; i <= patterns.MaxRecords; i++ {
		values = append(values, steadyRecord(t, fmt.Sprintf("id%d", i), "title"))
	}
	if _, err := patterns.EncodeSnapshot(values[:patterns.MaxRecords]); err != nil {
		t.Fatal("exact max record count rejected", err)
	}
	if got, err := patterns.EncodeSnapshot(values); err == nil || got != nil {
		t.Fatal("too many records accepted", err)
	}
	items := make([]map[string]interface{}, len(values))
	for i, record := range values {
		items[i] = map[string]interface{}{"id": record.ID, "title": record.Title, "effect": map[string]interface{}{"kind": "steady", "color": "#000000"}}
	}
	input, err := json.Marshal(map[string]interface{}{"schema_version": 1, "patterns": items})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := patterns.DecodePackage(input); err == nil || got != nil {
		t.Fatal("too many authoring records accepted", err)
	}
}

func recordsWithSteps(t *testing.T, total int) []patterns.Record {
	t.Helper()
	values := make([]patterns.Record, 10)
	remaining := total - len(values)
	for i := range values {
		count := 1
		if remaining > 1023 {
			count += 1023
			remaining -= 1023
		} else {
			count += remaining
			remaining = 0
		}
		steps := make([]control.Step, count)
		for j := range steps {
			steps[j].Color = "#112233"
		}
		value, err := patterns.NewRecord(fmt.Sprintf("id%d", i), "a", patterns.Source{Program: &control.Program{Steps: steps}})
		if err != nil {
			t.Fatal(err)
		}
		values[i] = value
	}
	return values
}

func TestPackageCanonicalExportLimitAndCompileExpansion(t *testing.T) {
	// Search fixture size using only the public API, then fill the remaining
	// bytes through title length. This checks the exact 1MiB boundary without
	// duplicating the canonical writer's per-field byte counts.
	low, high := 10, 10*control.MaxProgramSteps
	for low < high {
		middle := (low + high + 1) / 2
		if _, err := patterns.EncodeSnapshot(recordsWithSteps(t, middle)); err != nil {
			high = middle - 1
		} else {
			low = middle
		}
	}
	values := recordsWithSteps(t, low)
	data, err := patterns.EncodeSnapshot(values)
	if err != nil {
		t.Fatal(err)
	}
	gap := patterns.MaxCanonicalBytes - len(data)
	if gap < 0 || gap >= 255 {
		t.Fatalf("boundary fixture gap exceeds one legal title: %d", gap)
	}
	values[0], err = patterns.NewRecord(values[0].ID, "a"+strings.Repeat("x", gap), values[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	data, err = patterns.EncodeSnapshot(values)
	if err != nil || len(data) != patterns.MaxCanonicalBytes {
		t.Fatalf("exact canonical limit: %d %v", len(data), err)
	}
	if got, err := patterns.DecodePackage(data); err != nil || len(got) != len(values) {
		t.Fatal("exact max snapshot did not roundtrip", err)
	}
	values[0], err = patterns.NewRecord(values[0].ID, values[0].Title+"x", values[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := patterns.EncodeSnapshot(values); err == nil || got != nil {
		t.Fatal("one canonical byte over limit accepted", err)
	}
	// A much smaller authoring package may expand because defaults and the
	// frozen compiled snapshot are included. Reject it before returning records.
	steps := make([]map[string]string, 1024)
	for i := range steps {
		steps[i] = map[string]string{"color": "#112233"}
	}
	items := make([]map[string]interface{}, 16)
	for i := range items {
		items[i] = map[string]interface{}{"id": fmt.Sprintf("expanded%d", i), "title": "x", "program": map[string]interface{}{"steps": steps}}
	}
	input, err := json.Marshal(map[string]interface{}{"schema_version": 1, "patterns": items})
	if err != nil || len(input) >= patterns.MaxPackageBytes {
		t.Fatalf("bad authoring limit fixture: %d %v", len(input), err)
	}
	if got, err := patterns.DecodePackage(input); err == nil || got != nil || !strings.Contains(err.Error(), "canonical snapshot") {
		t.Fatal("bounded input bypassed expanded canonical size limit", err)
	}
}
