package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPatternAuthorCanonicalP1Vectors(t *testing.T) {
	data, err := os.ReadFile("testdata/pattern-author-v2-canonical-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCommit string `json:"source_commit"`
		Vectors      []struct {
			Label, Input, Canonical, Domain, Digest string
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SourceCommit != "0fe12e90876e81db9ff2d090e33285596b9d89b2" || len(fixture.Vectors) != 14 {
		t.Fatal("canonical oracle identity or coverage changed", fixture.SourceCommit, len(fixture.Vectors))
	}
	for _, vector := range fixture.Vectors {
		t.Run(vector.Label, func(t *testing.T) {
			value, err := parsePatternAuthorValue([]byte(vector.Input), 1<<20, 32)
			if err != nil {
				t.Fatal(err)
			}
			got, err := canonicalPatternAuthorValue(value, 1<<20)
			if err != nil || string(got) != vector.Canonical {
				t.Fatalf("canonical bytes differ: %s, %v; want %s", got, err, vector.Canonical)
			}
			raw, err := canonicalPatternAuthorRaw([]byte(vector.Input), 1<<20, 32)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatal("raw helper changed the canonical domain", err)
			}
			second, err := canonicalPatternAuthorRaw(got, 1<<20, 32)
			if err != nil || !bytes.Equal(got, second) {
				t.Fatal("canonicalization is not idempotent", err)
			}
			if vector.Digest != "" {
				digest := sha256.Sum256(append([]byte(vector.Domain+"\n"), got...))
				if actual := fmt.Sprintf("sha256:%x", digest); actual != vector.Digest {
					t.Fatal("P1 complete-payload digest differs", actual, vector.Digest)
				}
			}
		})
	}
}

func TestPatternAuthorParsePreservesNumericLexemes(t *testing.T) {
	input := []byte(`{"cycles":1.0,"fraction":12.340000,"exact":9007199254740993,"max":9223372036854775807}`)
	original := append([]byte(nil), input...)
	value, err := parsePatternAuthorValue(input, len(input), 32)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatal("object AST not preserved", value)
	}
	for key, want := range map[string]string{"cycles": "1.0", "fraction": "12.340000", "exact": "9007199254740993", "max": "9223372036854775807"} {
		got, ok := object[key].(json.Number)
		if !ok || string(got) != want {
			t.Fatal("numeric lexeme was repaired or rounded", key, object[key], want)
		}
	}
	got, err := canonicalPatternAuthorValue(object, 1<<20)
	if err != nil || string(got) != `{"cycles":1,"exact":9007199254740993,"fraction":12.34,"max":9223372036854775807}` {
		t.Fatal(string(got), err)
	}
	if object["cycles"] != json.Number("1.0") || object["fraction"] != json.Number("12.340000") || !bytes.Equal(input, original) {
		t.Fatal("canonical writer mutated raw input or parsed lexemes")
	}
}

func TestPatternAuthorJSONRejectsAmbiguousOrUnsupportedInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"root-null", []byte(`null`)}, {"nested-null", []byte(`{"a":[null]}`)},
		{"duplicate", []byte(`{"a":1,"a":2}`)},
		{"escaped-duplicate", []byte(`{"a":1,"\u0061":2}`)},
		{"nested-duplicate", []byte(`{"a":{"x":1,"x":2}}`)},
		{"unicode-key", []byte(`{"灯":1}`)},
		{"escaped-unicode-key", []byte(`{"\u00e9":1}`)},
		{"unknown-action-unicode-key", []byte(`{"source":{"contract":"future/v99","action":{"灯":1}}}`)},
		{"negative", []byte(`-1`)}, {"negative-zero", []byte(`-0`)}, {"negative-fraction", []byte(`[-0.00]`)},
		{"plus", []byte(`+1`)}, {"exponent", []byte(`1e0`)}, {"uppercase-exponent", []byte(`1E+0`)},
		{"int64-overflow", []byte(`9223372036854775808`)},
		{"decimal-int64-overflow", []byte(`9223372036854775808.0`)},
		{"fraction-precision", []byte(`0.1234567`)}, {"missing-fraction", []byte(`0.`)},
		{"missing-whole", []byte(`.1`)}, {"leading-zero", []byte(`01`)},
		{"high-surrogate", []byte(`{"s":"\ud800"}`)},
		{"low-surrogate", []byte(`{"s":"\udfff"}`)},
		{"reversed-pair", []byte(`{"s":"\udc00\ud800"}`)},
		{"pair-followed-by-high", []byte(`{"s":"\ud83d\udca1\ud800"}`)},
		{"high-then-text", []byte(`{"s":"\ud800x"}`)},
		{"high-then-other-escape", []byte(`{"s":"\ud800\n"}`)},
		{"invalid-unicode-hex", []byte(`{"s":"\uZZZZ"}`)},
		{"invalid-escape", []byte(`{"s":"\x"}`)},
		{"raw-control", []byte{'"', 0, '"'}}, {"invalid-utf8", []byte{'"', 0xff, '"'}},
		{"unterminated-string", []byte(`"text`)}, {"unterminated-escape", []byte{'"', '\\'}},
		{"unterminated-object", []byte(`{"a":1`)}, {"unterminated-array", []byte(`[1`)},
		{"trailing-object", []byte(`{} {}`)}, {"trailing-scalar", []byte(`0 true`)},
		{"utf8-bom", []byte("\xef\xbb\xbf{}")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := parsePatternAuthorValue(test.data, 1<<20, 32)
			if err == nil || value != nil {
				t.Fatal("invalid JSON produced a partial/accepted value", value, err)
			}
			canonical, err := canonicalPatternAuthorRaw(test.data, 1<<20, 32)
			if err == nil || canonical != nil {
				t.Fatal("invalid JSON produced canonical bytes", canonical, err)
			}
		})
	}
}

func TestPatternAuthorJSONByteAndDepthBounds(t *testing.T) {
	t.Run("exact-input-budget", func(t *testing.T) {
		input := []byte(" {\"a\":1} \n")
		if _, err := parsePatternAuthorValue(input, len(input), 32); err != nil {
			t.Fatal(err)
		}
		if value, err := parsePatternAuthorValue(input, len(input)-1, 32); err == nil || value != nil {
			t.Fatal("input whitespace bypassed byte budget", value, err)
		}
	})
	t.Run("invalid-bounds", func(t *testing.T) {
		for _, pair := range [][2]int{{0, 32}, {-1, 32}, {1024, -1}} {
			if value, err := parsePatternAuthorValue([]byte(`{}`), pair[0], pair[1]); err == nil || value != nil {
				t.Fatal(pair, value, err)
			}
		}
	})
	t.Run("depth-zero", func(t *testing.T) {
		for _, input := range []string{`[]`, `{}`, `"灯"`, `0`, `true`} {
			if _, err := parsePatternAuthorValue([]byte(input), 1024, 0); err != nil {
				t.Fatal(input, err)
			}
		}
		if value, err := parsePatternAuthorValue([]byte(`[0]`), 1024, 0); err == nil || value != nil {
			t.Fatal("nested child exceeded zero-depth budget", value, err)
		}
	})
	t.Run("p1-depth-boundary", func(t *testing.T) {
		for depth := 32; depth <= 33; depth++ {
			input := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
			value, err := parsePatternAuthorValue([]byte(input), 1024, 32)
			if depth == 32 && err != nil || depth == 33 && (err == nil || value != nil) {
				t.Fatal(depth, err)
			}
		}
	})
}

func TestPatternAuthorCanonicalRejectsNonASTValues(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"nil", nil}, {"float64-even-integer", float64(1)}, {"float32", float32(1)},
		{"go-int", int(1)}, {"raw-json", json.RawMessage(`1`)}, {"go-struct", struct{ A int }{1}},
		{"nil-object", map[string]any(nil)}, {"nil-array", []any(nil)},
		{"nested-nil", map[string]any{"a": nil}},
		{"unicode-key", map[string]any{"灯": json.Number("1")}},
		{"invalid-key-utf8", map[string]any{string([]byte{0xff}): true}},
		{"invalid-string-utf8", string([]byte{0xff})},
		{"manual-exponent", json.Number("1e0")}, {"manual-negative-zero", json.Number("-0")},
		{"manual-leading-zero", json.Number("01")}, {"manual-overflow", json.Number("9223372036854775808")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := canonicalPatternAuthorValue(test.value, 1024)
			if err == nil || data != nil {
				t.Fatal("non-AST/coerced value accepted", data, err)
			}
		})
	}
}

func TestPatternAuthorCanonicalOutputBudgetAndCycles(t *testing.T) {
	t.Run("c0-expansion", func(t *testing.T) {
		value, err := parsePatternAuthorValue([]byte(`"\n"`), 4, 32)
		if err != nil {
			t.Fatal(err)
		}
		for budget := 0; budget <= 8; budget++ {
			got, err := canonicalPatternAuthorValue(value, budget)
			if budget < 8 && (err == nil || got != nil) || budget == 8 && (err != nil || string(got) != `"\u000a"`) {
				t.Fatal(budget, string(got), err)
			}
		}
		if got, err := canonicalPatternAuthorRaw([]byte(`"\n"`), 4, 32); err == nil || got != nil {
			t.Fatal("canonical expansion bypassed shared byte budget", got, err)
		}
	})
	t.Run("unicode-byte-budget", func(t *testing.T) {
		got, err := canonicalPatternAuthorValue("中", 5)
		if err != nil || string(got) != `"中"` {
			t.Fatal(string(got), err)
		}
		if got, err := canonicalPatternAuthorValue("中", 4); err == nil || got != nil {
			t.Fatal("Unicode budget counted runes rather than bytes", got, err)
		}
	})
	t.Run("array-and-object-overhead", func(t *testing.T) {
		for _, raw := range []string{`[]`, `{}`, `[0,1]`, `{"a":0,"b":1}`} {
			value, err := parsePatternAuthorValue([]byte(raw), 1024, 32)
			if err != nil {
				t.Fatal(err)
			}
			got, err := canonicalPatternAuthorValue(value, len(raw))
			if err != nil || string(got) != raw {
				t.Fatal(raw, string(got), err)
			}
			if got, err := canonicalPatternAuthorValue(value, len(raw)-1); err == nil || got != nil {
				t.Fatal("structural overhead bypassed budget", raw, got, err)
			}
		}
	})
	t.Run("incremental-append-limit", func(t *testing.T) {
		writer := patternAuthorJSONWriter{limit: 5}
		if err := writer.raw("abc"); err != nil {
			t.Fatal(err)
		}
		if err := writer.raw("def"); err == nil || string(writer.data) != "abc" || len(writer.data) > writer.limit {
			t.Fatal("failed append allocated/published over budget", writer.data, err)
		}
	})
	t.Run("cycle-and-overdeep-manual-ast", func(t *testing.T) {
		cycle := map[string]any{}
		cycle["a"] = cycle
		if got, err := canonicalPatternAuthorValue(cycle, 1024); err == nil || got != nil {
			t.Fatal("manual AST cycle did not stop", got, err)
		}
		var value any = json.Number("0")
		for index := 0; index < 33; index++ {
			value = []any{value}
		}
		if got, err := canonicalPatternAuthorValue(value, 1024); err == nil || got != nil {
			t.Fatal("manual AST exceeded P1 depth", got, err)
		}
	})
}

func TestPatternAuthorCanonicalUnicodeAndInputAreUnchanged(t *testing.T) {
	input := []byte("{\"b\":\"é\",\"a\":\"é\",\"escaped\":\"\\ud83d\\udca1\",\"replacement\":\"�\"}")
	copyInput := append([]byte(nil), input...)
	value, err := parsePatternAuthorValue(input, 1024, 32)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(value)
	canonical, err := canonicalPatternAuthorValue(value, 1024)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(value)
	if !bytes.Equal(before, after) || !bytes.Equal(input, copyInput) {
		t.Fatal("writer changed input byte slice or AST")
	}
	parsed, err := parsePatternAuthorValue(canonical, 1024, 32)
	if err != nil {
		t.Fatal(err)
	}
	object := parsed.(map[string]any)
	if reflect.DeepEqual(object["a"], object["b"]) || object["escaped"] != "💡" || object["replacement"] != "�" {
		t.Fatal("Unicode meaning was normalized or repaired", object)
	}
}
