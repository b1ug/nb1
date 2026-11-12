package sequence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func colorAuthorSequence(color string) string {
	return `{"timing":"serial","steps":[{"targets":[{"led":0,"color":` + color + `}]}]}`
}

func TestDecodeAuthoringResolvesColorInputsToCanonicalPlayback(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{`"#abc"`, "#aabbcc"},
		{`"#AbCdEf"`, "#abcdef"},
		{`"  RED  "`, "#ff0000"},
		{`"rebeccapurple"`, "#663399"},
		{`{"space":"rgb8","r":1,"g":127,"b":255}`, "#017fff"},
		{`{"space":"hsv","h_deg":360,"s_pct":100,"v_pct":100}`, "#ff0000"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			raw := []byte(colorAuthorSequence(test.raw))
			before := string(raw)
			plan, err := DecodeAuthoring(raw, nil)
			if err != nil || plan.Steps[0].Targets[0].Color != test.want {
				t.Fatalf("authoring = %+v, %v", plan, err)
			}
			if string(raw) != before {
				t.Fatal("author source changed")
			}
			canonical, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(canonical); err != nil {
				t.Fatalf("authoring did not produce canonical playback: %v", err)
			}
		})
	}
	if _, err := Decode([]byte(colorAuthorSequence(`"red"`))); !errors.Is(err, ErrInvalid) {
		t.Fatal("frozen-plan decoder accepted unresolved name", err)
	}
	if _, err := Normalize(Sequence{Timing: TimingSerial, Steps: []Step{{Targets: []Target{{LED: 0, Color: "red"}}}}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("runtime accepted unresolved color", err)
	}
}

func TestDecodeAuthoringInitialTargetsAndRawResolver(t *testing.T) {
	rawColor := `{"space":"hsv","h_deg":120.500000,"s_pct":100,"v_pct":100}`
	input := `{"timing":"interval","initial":[{"led":2,"color":"@ready"},{"led":1,"color":"red"}],"steps":[{"targets":[{"led":2,"color":` + rawColor + `},{"led":1,"color":"#abc"}],"interval_ms":100}]}`
	var seen []string
	plan, err := DecodeAuthoring([]byte(input), func(raw json.RawMessage) (string, error) {
		seen = append(seen, string(raw))
		return "#123456", nil
	})
	if err != nil || plan.Initial[0].LED != 2 || plan.Initial[1].LED != 1 || len(seen) != 4 || seen[2] != rawColor {
		t.Fatalf("resolver lost source/order: %+v %v %v", plan, seen, err)
	}
	if _, err := DecodeAuthoring([]byte(colorAuthorSequence(`"red"`)), func(json.RawMessage) (string, error) { return "red", nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal("resolver bypassed canonical validation", err)
	}
}

func TestDecodeAuthoringKeepsStrictJSONAndColorGrammar(t *testing.T) {
	for _, raw := range []string{
		`"transparent"`, `"currentColor"`, `"rgb(1,2,3)"`, `"#abcd"`, `"#abcdef00"`, `"@missing"`,
		`{"space":"rgb8","r":1.0,"g":0,"b":0}`,
		`{"space":"hsv","h_deg":1e2,"s_pct":100,"v_pct":100}`,
		`{"space":"hsb","h_deg":120,"s_pct":100,"v_pct":100}`,
		`{"space":"rgb8","r":1,"\u0072":2,"g":0,"b":0}`,
	} {
		if _, err := DecodeAuthoring([]byte(colorAuthorSequence(raw)), nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid color %s: %v", raw, err)
		}
	}
	base := colorAuthorSequence(`"red"`)
	for _, raw := range []string{
		strings.Replace(base, `"led":0`, `"led":0,"l\u0065d":1`, 1),
		strings.Replace(base, `"color":"red"`, `"color":null`, 1),
		strings.Replace(base, `"led":0`, `"led":1e0`, 1),
		strings.Replace(base, `"led":0`, `"led":1.0`, 1),
		strings.Replace(base, `"targets":[`, `"fade_ms":1e1,"targets":[`, 1),
	} {
		calls := 0
		_, err := DecodeAuthoring([]byte(raw), func(json.RawMessage) (string, error) { calls++; return "#ff0000", nil })
		if !errors.Is(err, ErrInvalid) || calls != 0 {
			t.Errorf("invalid input reached resolver %s: %v, calls=%d", raw, err, calls)
		}
	}
}
