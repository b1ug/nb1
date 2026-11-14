package actionv1

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/sequence"
)

func compileForTest(t *testing.T, raw string) sequence.Sequence {
	t.Helper()
	result, err := Compile([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBasicActionsHoldTheirSelectedTargets(t *testing.T) {
	for _, tc := range []struct {
		input, color string
		led, fade    int
	}{
		{`{"kind":"set","color":"#0F0"}`, "#00ff00", 0, 0},
		{`{"kind":"off","led":1}`, "#000000", 1, 0},
		{`{"kind":"fade","led":2,"color":"lime","duration_ms":600}`, "#00ff00", 2, 600},
	} {
		result := compileForTest(t, tc.input)
		if result.Timing != sequence.TimingSerial || result.OnFinish != "hold" || result.Repeat != 1 || len(result.Steps) != 1 {
			t.Fatalf("basic action changed lifetime or timing: %+v", result)
		}
		step := result.Steps[0]
		if !reflect.DeepEqual(step.Targets, []sequence.Target{{LED: tc.led, Color: tc.color}}) || step.FadeMS != tc.fade || step.HoldMS == nil || *step.HoldMS != 0 {
			t.Fatalf("action %s compiled incorrectly: %+v", tc.input, step)
		}
	}
}

func TestSerialEffectRecipesAndTiming(t *testing.T) {
	for _, tc := range []struct {
		name, input      string
		colors           []string
		duration, repeat int
	}{
		{"blink", `{"kind":"blink","color":"red","cycles":2}`, []string{"#ff0000", "#000000"}, 1000, 2},
		{"breathe", `{"kind":"breathe","color":"red","period_ms":333}`, []string{
			"#000000", "#0a0000", "#250000", "#4f0000", "#800000", "#b00000", "#da0000", "#f50000", "#ff0000",
			"#f50000", "#da0000", "#b00000", "#800000", "#4f0000", "#250000", "#0a0000", "#000000",
		}, 333, 1},
		{"pulse", `{"kind":"pulse","color":"red"}`, []string{"#000000", "#ff0000", "#000000"}, 1030, 1},
		{"alternate", `{"kind":"alternate","colors":["red","blue"],"leds":[2,1],"period_ms":41}`, []string{"#ff0000", "#0000ff", "#0000ff", "#ff0000"}, 41, 1},
		{"rainbow", `{"kind":"rainbow"}`, []string{"#ff0000", "#ffa500", "#ffff00", "#00ff00", "#0000ff", "#4b0082", "#8f00ff", "#ff0000"}, 3000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := compileForTest(t, tc.input)
			if result.Timing != sequence.TimingSerial || result.OnFinish != "off" || result.Repeat != tc.repeat || result.NominalDurationMS() != int64(tc.duration) {
				t.Fatalf("effect lifetime or nominal time changed: %+v, %d ms", result, result.NominalDurationMS())
			}
			var colors []string
			for _, step := range result.Steps {
				colors = append(colors, step.Targets[0].Color)
			}
			if !reflect.DeepEqual(colors, tc.colors) {
				t.Fatalf("effect color progression = %v, want %v", colors, tc.colors)
			}
			if tc.name == "alternate" {
				for i, led := range []int{2, 1, 2, 1} {
					if result.Steps[i].Targets[0].LED != led || result.Steps[i].FadeMS != 0 {
						t.Fatalf("alternate must hard-cut and preserve LED order: %+v", result.Steps)
					}
				}
			}
		})
	}
}

func TestOrbitKeepsFourPairedFadesAndAnExactPeriod(t *testing.T) {
	result := compileForTest(t, `{"kind":"orbit","colors":["red","blue"],"leds":[2,1],"period_ms":3003,"cycles":2,"on_finish":"hold"}`)
	if result.Timing != sequence.TimingInterval || result.Repeat != 2 || result.OnFinish != "hold" || len(result.Steps) != 4 {
		t.Fatalf("orbit must compile to four interval steps: %+v", result)
	}
	if !reflect.DeepEqual(result.Initial, []sequence.Target{{LED: 2, Color: "#990000"}, {LED: 1, Color: "#000099"}}) {
		t.Fatalf("orbit initial = %v", result.Initial)
	}
	colors := [][2]string{{"#800080", "#1a001a"}, {"#000099", "#990000"}, {"#1a001a", "#800080"}, {"#990000", "#000099"}}
	for i, step := range result.Steps {
		want := []sequence.Target{{LED: 2, Color: colors[i][0]}, {LED: 1, Color: colors[i][1]}}
		interval := []int{750, 751, 751, 751}[i]
		if !reflect.DeepEqual(step.Targets, want) || step.FadeMS != 750 || step.HoldMS != nil || step.IntervalMS == nil || *step.IntervalMS != interval {
			t.Fatalf("orbit step %d = %+v, want ordered %v and a 750 ms fade within %d ms", i, step, want, interval)
		}
	}
}

func TestResolveColorUsesExactRGBAndLocalAliases(t *testing.T) {
	input := `{"kind":"orbit","colors":["@status-ok",{"space":"hsv","h_deg":240,"s_pct":100,"v_pct":100}]}`
	result, err := Compile([]byte(input), func(name string) (string, error) {
		if name != "status-ok" {
			t.Fatalf("resolver received %q", name)
		}
		return "#00FF00", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []sequence.Target{{LED: 1, Color: "#009900"}, {LED: 2, Color: "#000099"}}; !reflect.DeepEqual(result.Initial, want) {
		t.Fatalf("unexpected resolved orbit colors: %v", result.Initial)
	}
	for _, input := range []string{`"#0f0"`, `"lime"`, `{"space":"rgb8","r":0,"g":255,"b":0}`, `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}`} {
		if got, err := ResolveColor([]byte(input), nil); got != "#00ff00" || err != nil {
			t.Errorf("resolve %s = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{`"@status-ok"`, `"@Upper"`, `"@9lead"`, `"@a_b"`, `"@` + strings.Repeat("a", 33) + `"`} {
		if got, err := ResolveColor([]byte(input), nil); got != "" || err == nil {
			t.Errorf("unavailable or invalid alias accepted: %s", input)
		}
	}
	if _, err := ResolveColor([]byte(`"@status-ok"`), func(string) (string, error) { return "lime", nil }); err == nil {
		t.Fatal("a local alias must resolve to exact Hex6")
	}
}

func TestCompileRejectsAmbiguousOrUnsupportedInputs(t *testing.T) {
	for _, tc := range []struct{ input, field string }{
		{`{"kind":"swap","colors":["red","blue"]}`, "kind"},
		{`{"kind":"steady","color":"red"}`, "kind"},
		{`{"kind":"spin","colors":["red","blue"]}`, "kind"},
		{`{"kind":"switch","colors":["red","blue"]}`, "kind"},
		{`{"kind":"alternate","colors":["red","blue"],"transition":"orbit"}`, "transition"},
		{`{"kind":"orbit","colors":["red","blue"],"segments":4}`, "segments"},
		{`{"kind":"rainbow","colors":["red","blue"]}`, "colors"},
		{`{"kind":"set","color":"red","on_finish":"hold"}`, "on_finish"},
		{`{"kind":"off","color":"black"}`, "color"},
		{`{"kind":"fade","color":"red"}`, "duration_ms"},
		{`{"kind":"fade","color":"red","duration_ms":19}`, "duration_ms"},
		{`{"kind":"fade","color":"red","duration_ms":1e2}`, "duration_ms"},
		{`{"kind":"set","color":"red","led":-0}`, "led"},
		{`{"kind":"set","color":"@unknown"}`, "color"},
		{`{"kind":"orbit"}`, "colors"},
		{`{"kind":"alternate","colors":["red"]}`, "colors"},
		{`{"kind":"alternate","colors":["red","blue"],"leds":[1,1]}`, "leds"},
		{`{"kind":"orbit","colors":["red","no-such-color"]}`, "colors[1]"},
		{`{"kind":"orbit","colors":["red","blue"],"period_ms":199}`, "period_ms"},
		{`{"kind":"alternate","colors":["red","blue"],"period_ms":39}`, "period_ms"},
		{`{"kind":"blink","color":"red","on_ms":9}`, "on_ms"},
		{`{"kind":"breathe","color":"red","period_ms":329}`, "period_ms"},
		{`{"kind":"rainbow","period_ms":149}`, "period_ms"},
		{`{"kind":"rainbow","cycles":129}`, "cycles"},
		{`{"kind":"set","color":null}`, ""},
		{`{"kind":"set","color":"blue","color":"red"}`, ""},
		{`{"kind":"set","color":{"space":"rgb8","r":0,"g":0,"b":0,"b":255}}`, ""},
		{`{"kind":"off"} {"kind":"off"}`, ""},
		{`{"kind":"set","color":"\uD800"}`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			_, err := Compile([]byte(tc.input), nil)
			var field *FieldError
			if !errors.As(err, &field) || field.Field != tc.field {
				t.Fatalf("expected error at %q, got %v", tc.field, err)
			}
		})
	}
}

func TestCompileAppliesSequenceDurationLimit(t *testing.T) {
	input := fmt.Sprintf(`{"kind":"pulse","color":"red","rest_ms":%d,"cycles":24}`, sequence.MaxHoldMS)
	_, err := Compile([]byte(input), nil)
	var field *FieldError
	if !errors.As(err, &field) || field.Field != "cycles" {
		t.Fatal("action exceeding the Sequence duration limit must identify cycles", err)
	}
}
