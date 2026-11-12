package daemonclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSequenceInputAuthoringAcceptsColorInputsAndRetainsSource(t *testing.T) {
	for _, color := range []string{`"#abc"`, `"red"`, `"@ready"`, `{"space":"rgb8","r":0,"g":128,"b":255}`, `{"space":"hsv","h_deg":120.500000,"s_pct":100,"v_pct":100}`} {
		source := `{ "timing": "serial", "steps": [{"targets":[{"led":0,"color":` + color + `}]}] }`
		input, err := DecodeSequenceInput([]byte(`{"sequence":` + source + `}`))
		if err != nil || string(input.Sequence) != source {
			t.Fatalf("client lost author source %s: %s, %v", color, input.Sequence, err)
		}
		// Marshaling the request may compact whitespace but must not reserialize
		// JSON numbers, string escapes or resolve mutable color references.
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), color) {
			t.Fatalf("client changed color spelling or reference: %s", encoded)
		}
	}
}

func TestSequenceInputAuthoringStillRejectsAmbiguousInput(t *testing.T) {
	for _, source := range []string{
		`{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red","co\u006cor":"blue"}]}]}`,
		`{"timing":"serial","steps":[{"targets":[{"led":0,"color":{"space":"rgb8","r":1.0,"g":0,"b":0}}]}]}`,
		`{"timing":"serial","steps":[{"targets":[{"led":0,"color":"@Bad"}]}]}`,
		`{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red"}],"fade_ms":1e1}]}`,
	} {
		if _, err := DecodeSequenceInput([]byte(`{"sequence":` + source + `}`)); err == nil {
			t.Fatalf("client accepted invalid source %s", source)
		}
	}
}
