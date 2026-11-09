package colorinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func resolveBoth(t *testing.T, input []byte) Result {
	t.Helper()
	before := append([]byte(nil), input...)
	r, err := ResolveJSON(input)
	if err != nil {
		t.Fatalf("ResolveJSON(%s): %v", input, err)
	}
	envelope := append(append([]byte(`{"color":`), input...), '}')
	wrapped, err := ResolveRequestJSON(envelope)
	if err != nil || wrapped != r {
		t.Fatalf("envelope mismatch: %#v %v; bare=%#v", wrapped, err, r)
	}
	if err := ValidateResult(r); err != nil {
		t.Fatalf("own result rejected: %#v %v", r, err)
	}
	if !bytes.Equal(input, before) {
		t.Fatal("resolver mutated input bytes")
	}
	return r
}

func TestPublicGoldenInputs(t *testing.T) {
	for _, c := range []struct {
		input, kind, want string
		rgb               RGB8
	}{
		{`"#12AbEF"`, "hex6", "#12abef", RGB8{18, 171, 239}},
		{`"#0f8"`, "hex3", "#00ff88", RGB8{0, 255, 136}},
		{`"green"`, "name", "#008000", RGB8{0, 128, 0}},
		{`" LIME\t"`, "name", "#00ff00", RGB8{0, 255, 0}},
		{`"\t\r\n\u000b\f ReD \f\u000b\n\r\t"`, "name", "#ff0000", RGB8{255, 0, 0}},
		{`"\u0023\u0030f8"`, "hex3", "#00ff88", RGB8{0, 255, 136}},
		{`"\u0067reen"`, "name", "#008000", RGB8{0, 128, 0}},
		{`{"space":"rgb8","r":0,"g":255,"b":136}`, "rgb8", "#00ff88", RGB8{0, 255, 136}},
		{`{"b":136,"\u0072":0,"s\u0070ace":"rgb8","g":255}`, "rgb8", "#00ff88", RGB8{0, 255, 136}},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`, "hsv", "#008000", RGB8{0, 128, 0}},
		{`{"v_pct":100.000000,"s_pct":100,"h_deg":360,"space":"hsv"}`, "hsv", "#ff0000", RGB8{255, 0, 0}},
	} {
		t.Run(c.kind+"_"+c.want+"_"+strconv.Itoa(len(c.input)), func(t *testing.T) {
			r := resolveBoth(t, []byte(c.input))
			palette := ""
			if c.kind == "name" {
				palette = Palette
			}
			want := Result{1, Contract, c.kind, c.want, c.rgb, palette}
			if r != want {
				t.Fatalf("got %#v want %#v", r, want)
			}
		})
	}
	// Exact output includes zero channels, while palette is omitted for hex.
	r := resolveBoth(t, []byte(`"#000"`))
	wire, _ := json.Marshal(r)
	if string(wire) != `{"schema_version":1,"color_contract":"nb1-color-input/v1","input_kind":"hex3","canonical_color":"#000000","rgb8":{"r":0,"g":0,"b":0}}` {
		t.Fatal(string(wire))
	}
}

func TestFrozenW3CPaletteComplete(t *testing.T) {
	data, err := os.ReadFile("testdata/css-named-2026.tsv")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != paletteDataSHA256 || paletteDataSHA256 != "521686f82ac6c649de3f22589783ac86e2bfeb0c1858fd953f59efc4e1d2fd09" {
		t.Fatal("frozen palette data changed")
	}
	if paletteSourceURL != "https://www.w3.org/TR/2026/CRD-css-color-4-20261103/#named-colors" || paletteSourceSHA256 != "b66ad2af540a7550fd34e0d95f8dce3f49b9d39e255fdfed56f0ec1de9c78000" {
		t.Fatal("source provenance changed")
	}
	rows := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(rows) != 148 || len(namedColors) != 148 {
		t.Fatalf("palette cardinality %d / %d", len(rows), len(namedColors))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 5 || seen[fields[0]] {
			t.Fatal("bad or duplicated source row")
		}
		seen[fields[0]] = true
		name, want := fields[0], fields[1]
		t.Run(name, func(t *testing.T) {
			var rgb RGB8
			values := []*int{&rgb.R, &rgb.G, &rgb.B}
			for i, dest := range values {
				n, err := strconv.Atoi(fields[i+2])
				if err != nil {
					t.Fatal(err)
				}
				*dest = n
			}
			if fmt.Sprintf("#%02x%02x%02x", rgb.R, rgb.G, rgb.B) != want {
				t.Fatal("source hex/decimal disagree")
			}
			for _, spelling := range []string{name, strings.ToUpper(name), " \t" + name + "\r\n"} {
				b, _ := json.Marshal(spelling)
				r := resolveBoth(t, b)
				if r.InputKind != "name" || r.Palette != Palette || r.CanonicalColor != want || r.RGB8 != rgb {
					t.Fatalf("%s -> %#v", name, r)
				}
			}
		})
	}
	for _, pair := range [][2]string{{"aqua", "cyan"}, {"fuchsia", "magenta"}, {"gray", "grey"}, {"darkgray", "darkgrey"}, {"dimgray", "dimgrey"}, {"lightgray", "lightgrey"}, {"slategray", "slategrey"}, {"darkslategray", "darkslategrey"}, {"lightslategray", "lightslategrey"}} {
		a, _ := ResolveJSON([]byte(strconv.Quote(pair[0])))
		b, _ := ResolveJSON([]byte(strconv.Quote(pair[1])))
		if a != b {
			t.Fatalf("CSS alias changed: %v", pair)
		}
	}
}

func TestHSVExactGoldenSectorsAndHalfValues(t *testing.T) {
	data, err := os.ReadFile("testdata/hsv-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Input, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 37 {
		t.Fatal("goldens incomplete")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			r := resolveBoth(t, []byte(c.Input))
			if r.InputKind != "hsv" || r.CanonicalColor != c.Want {
				t.Fatalf("got %#v want %s", r, c.Want)
			}
		})
	}
}

func TestRejectBareGrammar(t *testing.T) {
	cases := []string{
		``, ` `, `null`, `true`, `0`, `[]`, `["red"]`, `{}`, `{"color":"red"}`, `""`, `" "`,
		`"transparent"`, `"currentColor"`, `"Canvas"`, `"random"`, `"red blue"`, `"rgb(255,0,0)"`, `"hsl(0,100%,50%)"`, `"hsb(0,100,100)"`, `"var(--red)"`,
		`"\u00a0red"`, `"red\u00a0"`, `"\u212A"`, `"\ud800"`, `"red\udfff"`, `"\ud83d\udd34"`, `"red\u0000"`,
		`"#ff"`, `"#ffff"`, `"#ffffffff"`, `"#12345g"`, `"#12g"`, `"ff0000"`,
		`{"space":"RGB8","r":0,"g":0,"b":0}`, `{"space":" rgb8 ","r":0,"g":0,"b":0}`,
		`{"Space":"rgb8","r":0,"g":0,"b":0}`, `{"space":"rgb8","R":0,"g":0,"b":0}`,
		`{"space":"rgb8","r":0,"g":0}`, `{"r":0,"g":0,"b":0}`, `{"space":"rgb8","r":0,"g":0,"b":0,"a":255}`,
		`{"space":"rgb8","r":1,"\u0072":2,"g":0,"b":0}`, `{"space":"rgb8","s\u0070ace":"hsv","r":0,"g":0,"b":0}`,
		`{"space":"rgb8","r":-0,"g":0,"b":0}`, `{"space":"rgb8","r":1.0,"g":0,"b":0}`, `{"space":"rgb8","r":1e0,"g":0,"b":0}`,
		`{"space":"rgb8","r":01,"g":0,"b":0}`, `{"space":"rgb8","r":256,"g":0,"b":0}`, `{"space":"rgb8","r":-1,"g":0,"b":0}`,
		`{"space":"rgb8","r":"1","g":0,"b":0}`, `{"space":"rgb8","r":true,"g":0,"b":0}`, `{"space":"rgb8","r":null,"g":0,"b":0}`,
		`{"space":null,"r":0,"g":0,"b":0}`, `{"space":"rgb8","r":{},"g":0,"b":0}`, `{"space":"rgb8","r":[],"g":0,"b":0}`,
		`{"space":"rgb8","h_deg":0,"s_pct":0,"v_pct":0}`, `{"space":"hsv","r":0,"g":0,"b":0}`,
		`{"space":"hsv","h_deg":-0,"s_pct":0,"v_pct":0}`, `{"space":"hsv","h_deg":-0.000000,"s_pct":0,"v_pct":0}`,
		`{"space":"hsv","h_deg":1e0,"s_pct":0,"v_pct":0}`, `{"space":"hsv","h_deg":1.0000000,"s_pct":0,"v_pct":0}`,
		`{"space":"hsv","h_deg":360.000001,"s_pct":0,"v_pct":0}`, `{"space":"hsv","h_deg":0,"s_pct":100.000001,"v_pct":0}`,
		`{"space":"hsv","h_deg":0,"s_pct":0,"v_pct":100.000001}`, `{"space":"hsv","h_deg":NaN,"s_pct":0,"v_pct":0}`,
		`{"space":"hsv","h_deg":0,"s_pct":Infinity,"v_pct":0}`, `{"space":"hsv","h_deg":0,"s_pct":0,"v_pct":"50"}`,
		`{"space":"hsv","h_deg":0,"s_pct":0,"v_pct":0,"alpha":1}`, `{"space":"hsl","h_deg":0,"s_pct":0,"v_pct":0}`,
		`"red" null`, `"red" {}`, `{"space":"rgb8","r":0,"g":0,"b":0}[]`,
		"\x0b\"red\"", "\u00a0\"red\"", "\"red\"\u00a0", "\xef\xbb\xbf\"red\"", "\"\xff\"",
		`{"space":"hsv","h_deg":` + strings.Repeat("9", 3000) + `,"s_pct":0,"v_pct":0}`,
		`{"space":"rgb8","r":` + strings.Repeat("9", 3000) + `,"g":0,"b":0}`,
		`{"space":"rgb8","r":` + strings.Repeat("[", 1000) + strings.Repeat("]", 1000) + `,"g":0,"b":0}`,
	}
	for i, input := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			r, err := ResolveJSON([]byte(input))
			if err == nil || r != (Result{}) {
				t.Fatalf("accepted invalid input: %q -> %#v / %v", input, r, err)
			}
		})
	}
}

func TestRequestEnvelopeAndByteBounds(t *testing.T) {
	for i, input := range []string{`"red"`, `[]`, `null`, `{}`, `{"Color":"red"}`, `{"color":"red","color":"blue"}`, `{"color":"red","\u0063olor":"blue"}`, `{"color":null}`, `{"color":["red"]}`, `{"color":{"color":"red"}}`, `{"color":"red","serial":"fixture"}`, `{"color":"red"} {}`} {
		t.Run("bad_envelope_"+strconv.Itoa(i), func(t *testing.T) {
			r, err := ResolveRequestJSON([]byte(input))
			if err == nil || r != (Result{}) {
				t.Fatalf("invalid envelope accepted: %s", input)
			}
		})
	}
	for _, c := range []struct {
		name, body string
		fn         func([]byte) (Result, error)
	}{{"bare", `"red"`, ResolveJSON}, {"envelope", `{"color":"red"}`, ResolveRequestJSON}} {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(strings.Repeat(" ", MaxRequestBytes-len(c.body)) + c.body)
			if _, err := c.fn(data); err != nil {
				t.Fatal("4096 bytes rejected", err)
			}
			data = append(data, ' ')
			if r, err := c.fn(data); err == nil || r != (Result{}) {
				t.Fatal("4097 bytes accepted")
			}
		})
	}
	exact, _ := json.Marshal(strings.Repeat(" ", 61) + "red")
	if _, err := ResolveJSON(exact); err != nil {
		t.Fatal("64 decoded bytes rejected", err)
	}
	tooLong, _ := json.Marshal(strings.Repeat(" ", 62) + "red")
	if _, err := ResolveJSON(tooLong); err == nil {
		t.Fatal("65 decoded bytes accepted after trimming")
	}
	if r, err := ResolveRequestJSON([]byte(`{"\u0063olor":"red"}`)); err != nil || r.CanonicalColor != "#ff0000" {
		t.Fatal("valid escaped envelope key rejected", r, err)
	}
}

func TestUnknownFieldErrorsQuoteControlCharacters(t *testing.T) {
	_, err := ResolveJSON([]byte(`{"\u001b[31m":0}`))
	if err == nil || strings.ContainsRune(err.Error(), '\x1b') || !strings.Contains(err.Error(), `\x1b`) {
		t.Fatalf("unknown field error exposed raw terminal control or lost quoted context: %v", err)
	}
}

func TestValidateResultBoundaries(t *testing.T) {
	good, err := ResolveJSON([]byte(`"green"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Result){
		func(r *Result) { r.SchemaVersion = 0 }, func(r *Result) { r.ColorContract = "future" }, func(r *Result) { r.InputKind = "hsl" }, func(r *Result) { r.Palette = "legacy" }, func(r *Result) { r.CanonicalColor = "#00800F" }, func(r *Result) { r.CanonicalColor = "#008001" }, func(r *Result) { r.RGB8.R = -1 }, func(r *Result) { r.RGB8.G = 256 }, func(r *Result) { r.RGB8.B = 1 }, func(r *Result) { r.InputKind = "rgb8" },
	} {
		r := good
		change(&r)
		if ValidateResult(r) == nil {
			t.Fatalf("invalid result accepted: %#v", r)
		}
	}
	hexResult, _ := ResolveJSON([]byte(`"#008000"`))
	hexResult.Palette = Palette
	if ValidateResult(hexResult) == nil {
		t.Fatal("non-name palette accepted")
	}
	if ValidateResult(Result{}) == nil {
		t.Fatal("zero result accepted")
	}
}

func TestConcurrentResolutionHasNoMutation(t *testing.T) {
	inputs := [][]byte{[]byte(`"green"`), []byte(`"#12AbEF"`), []byte(`{"space":"hsv","h_deg":29.999999,"s_pct":50.000001,"v_pct":66.666667}`)}
	wants := make([]Result, len(inputs))
	for i, input := range inputs {
		var err error
		wants[i], err = ResolveJSON(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				for j, input := range inputs {
					got, err := ResolveJSON(input)
					if err != nil || got != wants[j] {
						errors <- fmt.Errorf("concurrent result changed: %#v / %v", got, err)
						return
					}
					got.RGB8.R = 255
					got.Palette = "changed"
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	for i, input := range inputs {
		got, err := ResolveJSON(input)
		if err != nil || got != wants[i] {
			t.Fatal("resolution mutated frozen palette/input")
		}
	}
}
