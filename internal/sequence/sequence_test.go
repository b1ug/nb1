package sequence

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const serialJSON = `{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#AbCdEf"}]}]}`

const intervalJSON = `{"timing":"interval","initial":[{"led":2,"color":"#FF0000"},{"led":1,"color":"#0000FF"}],"steps":[{"targets":[{"led":2,"color":"#0000ff"},{"led":1,"color":"#ff0000"}],"fade_ms":1000,"interval_ms":1000},{"targets":[{"led":2,"color":"#ff0000"},{"led":1,"color":"#0000ff"}],"fade_ms":1000,"interval_ms":1000}],"repeat":2}`

func TestDecodeCanonicalSerial(t *testing.T) {
	s, err := Decode([]byte(serialJSON))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#abcdef"}],"fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"off"}`
	if string(canonical) != want {
		t.Fatalf("canonical Sequence = %s, want %s", canonical, want)
	}
	if got := s.NominalDurationMS(); got != 10 {
		t.Fatalf("zero-fade serial duration = %d, want one 10 ms update", got)
	}
	again, err := Decode(canonical)
	if err != nil || !reflect.DeepEqual(s, again) {
		t.Fatalf("canonical Sequence changed after decoding again: %+v, %v", again, err)
	}
}

func TestIntervalTimingKeepsTargetOrderAndAccountsForSettle(t *testing.T) {
	s, err := Decode([]byte(intervalJSON))
	if err != nil {
		t.Fatal(err)
	}
	if s.Initial[0] != (Target{LED: 2, Color: "#ff0000"}) || s.Steps[0].Targets[0].LED != 2 {
		t.Fatalf("initial or step targets were reordered: %+v", s)
	}
	// 20 ms initial once, four 1000 ms intervals, then 20 ms of final settle.
	if got := s.NominalDurationMS(); got != 4040 {
		t.Fatalf("interval duration = %d, want 4040", got)
	}
	if s.Steps[0].HoldMS != nil {
		t.Fatal("interval step has a hold value")
	}
}

func TestNormalizeDoesNotShareMutableFields(t *testing.T) {
	serial, err := Decode([]byte(serialJSON))
	if err != nil {
		t.Fatal(err)
	}
	interval, err := Decode([]byte(intervalJSON))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []Sequence{serial, interval} {
		output, err := Normalize(input)
		if err != nil {
			t.Fatal(err)
		}
		output.Steps[0].Targets[0].Color = "#000000"
		if output.Timing == TimingSerial {
			*output.Steps[0].HoldMS = 123
			if *input.Steps[0].HoldMS != 0 {
				t.Fatal("serial hold aliases the input")
			}
		} else {
			output.Initial[0].Color = "#000000"
			*output.Steps[0].IntervalMS = 50
			if input.Initial[0].Color != "#ff0000" || *input.Steps[0].IntervalMS != 1000 {
				t.Fatal("interval timing or initial targets alias the input")
			}
		}
		if input.Steps[0].Targets[0].Color == "#000000" {
			t.Fatal("step targets alias the input")
		}
	}
}

func TestDecodeRejectsAmbiguousOrInvalidInput(t *testing.T) {
	withSerial := func(field string) string { return strings.TrimSuffix(serialJSON, "}") + "," + field + "}" }
	withStep := func(field string) string { return strings.Replace(serialJSON, `}]}]`, `}],`+field+`}]`, 1) }
	for name, input := range map[string]string{
		"legacy frames":          withSerial(`"frames":[]`),
		"unknown target field":   strings.Replace(serialJSON, `"led":0`, `"led":0,"slot":0`, 1),
		"duplicate target field": strings.Replace(serialJSON, `"led":0`, `"led":0,"l\u0065d":1`, 1),
		"noncanonical key":       strings.Replace(serialJSON, `"timing"`, `"Timing"`, 1),
		"invalid surrogate":      strings.Replace(serialJSON, "#AbCdEf", `\ud800`, 1),
		"missing timing":         strings.Replace(serialJSON, `"timing":"serial",`, "", 1),
		"missing led":            strings.Replace(serialJSON, `"led":0,`, "", 1),
		"color name":             strings.Replace(serialJSON, "#AbCdEf", "red", 1),
		"explicit zero repeat":   withSerial(`"repeat":0`),
		"explicit empty finish":  withSerial(`"on_finish":""`),
		"explicit null":          withStep(`"hold_ms":null`),
		"fractional time":        withStep(`"fade_ms":10.5`),
		"exponent time":          withStep(`"fade_ms":1e1`),
		"fade quantum":           withStep(`"fade_ms":1`),
		"hold overflow":          withStep(`"hold_ms":3600001`),
		"initial in serial":      withSerial(`"initial":[]`),
		"interval in serial":     withStep(`"interval_ms":0`),
		"missing initial":        strings.Replace(intervalJSON, `"initial":[{"led":2,"color":"#FF0000"},{"led":1,"color":"#0000FF"}],`, "", 1),
		"duplicate physical LED": strings.Replace(intervalJSON, `"led":1`, `"led":2`, 1),
		"all LED in interval":    strings.Replace(intervalJSON, `"led":2`, `"led":0`, 1),
		"different LED order":    strings.Replace(intervalJSON, `{"led":2,"color":"#FF0000"},{"led":1,"color":"#0000FF"}`, `{"led":1,"color":"#0000FF"},{"led":2,"color":"#FF0000"}`, 1),
		"missing interval":       strings.Replace(intervalJSON, `,"interval_ms":1000`, "", 1),
		"hold in interval":       strings.Replace(intervalJSON, `"interval_ms":1000`, `"interval_ms":1000,"hold_ms":0`, 1),
		"interval below minimum": strings.Replace(intervalJSON, `"interval_ms":1000`, `"interval_ms":49`, 1),
		"fade exceeds interval":  strings.Replace(intervalJSON, `"interval_ms":1000`, `"interval_ms":999`, 1),
		"second JSON object":     serialJSON + `{}`,
		"oversized input":        strings.Repeat(" ", MaxInputBytes) + serialJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(input)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestExecutedStepsAndDurationBudgets(t *testing.T) {
	s, err := Decode([]byte(serialJSON))
	if err != nil {
		t.Fatal(err)
	}
	s.Repeat = 512
	s.Steps = append(s.Steps, s.Steps[0])
	if _, err := Normalize(s); err != nil {
		t.Fatalf("1024 executed steps should fit: %v", err)
	}
	s.Repeat++
	if _, err := Normalize(s); !errors.Is(err, ErrInvalid) {
		t.Fatalf("1026 executed steps error = %v, want ErrInvalid", err)
	}
	s.Steps = s.Steps[:1]
	s.Repeat = 120
	s.Steps[0].FadeMS = 600000
	*s.Steps[0].HoldMS = 119990
	normalized, err := Normalize(s)
	if err != nil || normalized.NominalDurationMS() != MaxNominalDurationMS {
		t.Fatalf("exact 24-hour duration rejected: %+v, %v", normalized, err)
	}
	*s.Steps[0].HoldMS++
	if _, err := Normalize(s); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duration above 24 hours error = %v, want ErrInvalid", err)
	}
}
