package lightaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
)

func mustCompile(t *testing.T, input string) Result {
	t.Helper()
	r, err := CompileJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if r.Contract != Contract || r.SchemaVersion != 1 || r.ProgramCompileContract != control.CompileContract {
		t.Fatalf("unexpected contract: %#v", r)
	}
	preview, err := control.Preview(&r.Program, nil)
	if err != nil || !reflect.DeepEqual(r.Program, preview.Program) || r.NominalDurationMS != preview.NominalDurationMS || r.TotalSteps != preview.TotalSteps || !reflect.DeepEqual(r.RequiredCapabilities, preview.RequiredCapabilities) {
		t.Fatalf("result does not match authoritative Program preview: %#v %v", r, err)
	}
	return r
}

func TestBasicActionGoldens(t *testing.T) {
	for _, test := range []struct {
		input    string
		step     control.Step
		finish   string
		duration int64
	}{
		{`{"kind":"set","color":"green"}`, control.Step{Color: "#008000"}, "hold", 10},
		{`{"kind":"off","led":2}`, control.Step{LED: 2, Color: "#000000"}, "hold", 10},
		{`{"kind":"fade","color":"#abc","led":1,"duration_ms":300}`, control.Step{LED: 1, Color: "#aabbcc", FadeMS: 300}, "hold", 310},
		{`{"kind":"set","color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50},"on_finish":"off"}`, control.Step{Color: "#008000"}, "off", 10},
	} {
		t.Run(test.input, func(t *testing.T) {
			r := mustCompile(t, test.input)
			want := control.Program{Steps: []control.Step{test.step}, Repeat: 1, OnFinish: test.finish}
			if !reflect.DeepEqual(r.Program, want) || r.NominalDurationMS != test.duration || r.TotalSteps != 1 {
				t.Fatalf("got %#v", r)
			}
		})
	}
}

func TestBlinkPulseAndAlternateGoldens(t *testing.T) {
	for _, test := range []struct {
		input    string
		program  control.Program
		duration int64
	}{
		{`{"kind":"blink","color":"lime","on_ms":200,"off_ms":300,"cycles":3}`, control.Program{Steps: []control.Step{{Color: "#00ff00", HoldMS: 190}, {Color: "#000000", HoldMS: 290}}, Repeat: 3, OnFinish: "off"}, 1500},
		{`{"kind":"pulse","color":"lime","cycles":3}`, control.Program{Steps: []control.Step{{Color: "#000000"}, {Color: "#00ff00", FadeMS: 60}, {Color: "#000000", FadeMS: 240, HoldMS: 700}}, Repeat: 3, OnFinish: "off"}, 3090},
		{`{"kind":"alternate","cycles":3}`, control.Program{Steps: []control.Step{{LED: 1, Color: "#ff0000"}, {LED: 2, Color: "#0000ff", HoldMS: 980}, {LED: 1, Color: "#0000ff"}, {LED: 2, Color: "#ff0000", HoldMS: 980}}, Repeat: 3, OnFinish: "off"}, 6000},
		{`{"kind":"alternate","colors":["#abc",{"space":"rgb8","r":1,"g":2,"b":3}],"leds":[2,1],"period_ms":41}`, control.Program{Steps: []control.Step{{LED: 2, Color: "#aabbcc"}, {LED: 1, Color: "#010203"}, {LED: 2, Color: "#010203"}, {LED: 1, Color: "#aabbcc", HoldMS: 1}}, Repeat: 1, OnFinish: "off"}, 41},
	} {
		t.Run(test.input, func(t *testing.T) {
			r := mustCompile(t, test.input)
			if !reflect.DeepEqual(r.Program, test.program) || r.NominalDurationMS != test.duration {
				t.Fatalf("got %#v", r)
			}
		})
	}
}

func TestBreatheFixedCosineGolden(t *testing.T) {
	r := mustCompile(t, `{"kind":"breathe","color":"white","cycles":2}`)
	// Independent hand-calculated RGB8 vector for the frozen cosine16 weights.
	levels := []int{0, 10, 37, 79, 128, 176, 218, 245, 255, 245, 218, 176, 128, 79, 37, 10, 0}
	holds := []int{4, 4, 5, 4, 4, 5, 4, 5, 4, 4, 5, 4, 4, 5, 4, 5}
	if len(r.Program.Steps) != 17 || r.NominalDurationMS != 4000 || r.TotalSteps != 34 {
		t.Fatalf("breathe totals: %#v", r)
	}
	for i, s := range r.Program.Steps {
		if s.Color != fmt.Sprintf("#%02x%02x%02x", levels[i], levels[i], levels[i]) {
			t.Errorf("sample %d = %s", i, s.Color)
		}
		if i > 0 && (s.FadeMS != 110 || s.HoldMS != holds[i-1]) {
			t.Errorf("sample %d timing = %#v", i, s)
		}
	}
	if *r.Action.PeriodMS != 2000 {
		t.Fatal("normalized period missing")
	}
}

func TestRainbowHueLoopGolden(t *testing.T) {
	r := mustCompile(t, `{"kind":"rainbow"}`)
	colors := []string{"#ff0000", "#ffff00", "#00ff00", "#00ffff", "#0000ff", "#ff00ff", "#ff0000"}
	holds := []int{8, 8, 9, 8, 8, 9}
	if len(r.Program.Steps) != 7 || r.NominalDurationMS != 3000 {
		t.Fatal(r)
	}
	for i, s := range r.Program.Steps {
		if s.Color != colors[i] {
			t.Errorf("hue %d: %s", i, s.Color)
		}
		if i > 0 && (s.FadeMS != 480 || s.HoldMS != holds[i-1]) {
			t.Errorf("phase %d: %#v", i, s)
		}
	}
}

func TestNormalizedActionsRoundTripAndIndependentResults(t *testing.T) {
	for _, input := range []string{`{"kind":"off"}`, `{"kind":"set","color":"red"}`, `{"kind":"fade","color":"red","duration_ms":10}`, `{"kind":"blink","color":"#123456"}`, `{"kind":"breathe","color":"#123456"}`, `{"kind":"pulse","color":"#123456","rest_ms":0}`, `{"kind":"alternate"}`, `{"kind":"rainbow"}`} {
		r := mustCompile(t, input)
		data, err := json.Marshal(r.Action)
		if err != nil {
			t.Fatal(err)
		}
		other := mustCompile(t, string(data))
		if !reflect.DeepEqual(r, other) {
			t.Fatalf("normalized roundtrip differs: %s", data)
		}
		r.Program.Steps[0].Color = "#ffffff"
		if r.Action.Color != "" && r.Action.Color == r.Program.Steps[0].Color {
			t.Fatal("fixture lacks independent color")
		}
		if r.Action.Cycles != nil {
			*r.Action.Cycles = 999
		}
		if r.Action.LEDs != nil {
			r.Action.LEDs[0] = 99
		}
		if r.Action.Colors != nil {
			r.Action.Colors[0] = "bad"
		}
		r.RequiredCapabilities[0] = "bad"
		fresh := mustCompile(t, input)
		if !reflect.DeepEqual(fresh, other) {
			t.Fatal("result aliases later compilation")
		}
	}
}

func TestInvalidActionsAreStrictAndReturnNoResult(t *testing.T) {
	cases := []string{
		``, `null`, `[]`, `{}`, `{"kind":null}`, `{"kind":"OFF"}`, `{"kind":"spin"}`, `{"kind":"sequence","actions":[]}`,
		`{"kind":"off","color":"red"}`, `{"kind":"off","duration_ms":0}`, `{"kind":"set"}`, `{"kind":"set","color":null}`,
		`{"kind":"set","color":"transparent"}`, `{"kind":"set","color":{"space":"rgb8","r":1.0,"g":2,"b":3}}`,
		`{"kind":"set","color":{"space":"hsv","h_deg":120,"h_deg":60,"s_pct":100,"v_pct":100}}`,
		`{"kind":"off","kind":"set","color":"red"}`, `{"kind":"off","k\u0069nd":"off"}`, `{"Kind":"off"}`, `{"kind":"off"}{}`,
		`{"kind":"off","led":-0}`, `{"kind":"off","led":1.0}`, `{"kind":"off","led":1e0}`, `{"kind":"off","led":3}`, `{"kind":"off","led":null}`,
		`{"kind":"fade","color":"red"}`, `{"kind":"fade","color":"red","duration_ms":11}`, `{"kind":"fade","color":"red","duration_ms":0}`,
		`{"kind":"breathe","color":"red","period_ms":329}`, `{"kind":"breathe","color":"red","attack_ms":10}`,
		`{"kind":"pulse","color":"red","period_ms":1000}`, `{"kind":"pulse","color":"red","attack_ms":11}`, `{"kind":"pulse","color":"red","rest_ms":-0}`,
		`{"kind":"blink","color":"red","on_ms":9}`, `{"kind":"blink","color":"red","cycles":0}`, `{"kind":"blink","color":"red","cycles":10001}`,
		`{"kind":"alternate","led":0}`, `{"kind":"alternate","leds":[1,1]}`, `{"kind":"alternate","leds":[0,1]}`, `{"kind":"alternate","leds":[1.0,2]}`,
		`{"kind":"alternate","colors":["red"]}`, `{"kind":"alternate","colors":["red",null]}`, `{"kind":"alternate","period_ms":39}`,
		`{"kind":"rainbow","color":"red"}`, `{"kind":"rainbow","period_ms":129}`, `{"kind":"off","on_finish":"keep"}`,
		`{"kind":"set","color":"\ud800"}`, `{"kind":"off","extra":` + strings.Repeat(`[`, 33) + `0` + strings.Repeat(`]`, 33) + `}`,
	}
	for i, input := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r, err := CompileJSON([]byte(input))
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(r, Result{}) {
				t.Fatalf("accepted %s: %#v %v", input, r, err)
			}
		})
	}
	for _, data := range [][]byte{append([]byte(`{"kind":"off","extra":"`), 0xff, '"', '}'), []byte(strings.Repeat(" ", MaxRequestBytes) + `{"kind":"off"}`)} {
		if _, err := CompileJSON(data); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid UTF8/size accepted")
		}
	}
}

func TestVersionEnvelopeAndColorFractions(t *testing.T) {
	bare := `{"kind":"set","color":{"space":"hsv","h_deg":120.5,"s_pct":50.5,"v_pct":50.5}}`
	want := mustCompile(t, bare)
	r, err := CompileRequest([]byte(`{"contract":"nb1-light-action/v1","action":` + bare + `}`))
	if err != nil || !reflect.DeepEqual(r, want) {
		t.Fatalf("envelope = %#v %v", r, err)
	}
	for _, raw := range []string{bare, `{"contract":"v2","action":{"kind":"off"}}`, `{"contract":"nb1-light-action/v1"}`, `{"contract":"nb1-light-action/v1","action":null}`, `{"contract":"nb1-light-action/v1","action":{"kind":"off"},"serial":"x"}`} {
		if _, err := CompileRequest([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid envelope accepted %s: %v", raw, err)
		}
	}
}

func TestPeriodBoundariesAndTotalBudgets(t *testing.T) {
	for _, kind := range []string{"breathe", "rainbow", "alternate"} {
		min := 330
		if kind == "rainbow" {
			min = 130
		}
		if kind == "alternate" {
			min = 40
		}
		for _, period := range []int{min, min + 1, 1999, 2000, 2001, control.MaxPulsePeriodMS} {
			color := ""
			if kind == "breathe" {
				color = `,"color":"#123456"`
			}
			r := mustCompile(t, fmt.Sprintf(`{"kind":%q,"period_ms":%d%s}`, kind, period, color))
			if r.NominalDurationMS != int64(period) {
				t.Fatalf("%s period%d = %d", kind, period, r.NominalDurationMS)
			}
			for _, s := range r.Program.Steps {
				if s.FadeMS < 0 || s.FadeMS%10 != 0 || s.HoldMS < 0 {
					t.Fatalf("invalid quantization %#v", s)
				}
			}
		}
	}
	mustCompile(t, `{"kind":"breathe","color":"red","cycles":60}`)
	mustCompile(t, `{"kind":"blink","color":"red","cycles":512}`)
	for _, input := range []string{`{"kind":"breathe","color":"red","cycles":61}`, `{"kind":"blink","color":"red","cycles":513}`, `{"kind":"pulse","color":"red","cycles":342}`, `{"kind":"blink","color":"red","on_ms":3600000,"off_ms":3600000,"cycles":13}`} {
		if _, err := CompileJSON([]byte(input)); !errors.Is(err, ErrInvalid) {
			t.Errorf("budget overflow accepted: %s", input)
		}
	}
}

func TestLegacyPulseSemanticsUnchanged(t *testing.T) {
	period := 1000
	legacy, err := control.CompileEffect(control.Effect{Kind: "pulse", Color: "#00ff00", PeriodMS: &period})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Steps[1].FadeMS != 480 || legacy.Steps[2].FadeMS != 490 || legacy.Steps[2].HoldMS != 0 {
		t.Fatal("legacy pulse changed")
	}
	now := mustCompile(t, `{"kind":"pulse","color":"lime"}`)
	if reflect.DeepEqual(now.Program, legacy) || now.Program.Steps[2].HoldMS != 700 {
		t.Fatal("new pulse does not have distinct attack/decay/rest")
	}
}

func TestConcurrentCompilation(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				r, err := CompileJSON([]byte(`{"kind":"breathe","color":"rebeccapurple","cycles":2}`))
				if err != nil || r.NominalDurationMS != 4000 {
					t.Errorf("concurrent compile: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
