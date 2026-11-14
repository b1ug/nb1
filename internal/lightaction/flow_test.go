package lightaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
)

func flowPairAt(t *testing.T, r Result, phase int) [2]control.Step {
	t.Helper()
	index := 0
	if phase > 0 {
		index = 2 + (phase-1)*2
	}
	return [2]control.Step{r.Program.Steps[index], r.Program.Steps[index+1]}
}

func TestFlowLandmarkColorGoldens(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		colors map[int][2]string
	}{
		{"linear", map[int][2]string{0: {"#ff0000", "#0000ff"}, 1: {"#ef0010", "#1000ef"}, 4: {"#bf0040", "#4000bf"}, 8: {"#800080", "#800080"}, 16: {"#0000ff", "#ff0000"}, 24: {"#800080", "#800080"}, 28: {"#bf0040", "#4000bf"}, 32: {"#ff0000", "#0000ff"}}},
		{"sine", map[int][2]string{0: {"#ff0000", "#0000ff"}, 1: {"#fd0002", "#0200fd"}, 4: {"#da0025", "#2500da"}, 8: {"#800080", "#800080"}, 16: {"#0000ff", "#ff0000"}, 24: {"#800080", "#800080"}, 28: {"#da0025", "#2500da"}, 32: {"#ff0000", "#0000ff"}}},
		{"orbit", map[int][2]string{0: {"#990000", "#000099"}, 1: {"#ab0002", "#010084"}, 4: {"#c00021", "#0c0045"}, 8: {"#800080", "#1a001a"}, 16: {"#000099", "#990000"}, 24: {"#1a001a", "#800080"}, 28: {"#45000c", "#2100c0"}, 32: {"#990000", "#000099"}}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r := mustCompile(t, fmt.Sprintf(`{"kind":"alternate","transition":%q,"on_finish":"hold"}`, tc.mode))
			if len(r.Program.Steps) != 66 || r.TotalSteps != 66 || r.NominalDurationMS != 2000 || r.Action.Transition != tc.mode {
				t.Fatal(r)
			}
			for phase, want := range tc.colors {
				pair := flowPairAt(t, r, phase)
				got := [2]string{pair[0].Color, pair[1].Color}
				if got != want {
					t.Errorf("phase%d got%v want%v", phase, got, want)
				}
			}
		})
	}
}

func TestFlowLUTMatchesIndependentAnalyticPhases(t *testing.T) {
	for phase := 0; phase <= 32; phase++ {
		theta := 2 * math.Pi * float64(phase) / 32
		wantW := int(math.Round((1 - math.Cos(theta)) * 5000))
		wantS := int(math.Round(math.Sin(theta) * 10000))
		if flowCosine32[phase] != wantW || flowSine32[phase] != wantS {
			t.Fatalf("phase%d weights %d/%d want%d/%d", phase, flowCosine32[phase], flowSine32[phase], wantW, wantS)
		}
	}
}

func TestFlowOrbitRoundsOnlyAfterMixAndBrightness(t *testing.T) {
	// At phase1: blue=53*.0096=.5088, brightness=.67804. One rounding
	// gives0; rounding the mixture first would give round(1*.67804)=1.
	r := mustCompile(t, `{"kind":"alternate","transition":"orbit","colors":["#000000",{"space":"rgb8","r":0,"g":0,"b":53}]}`)
	if pair := flowPairAt(t, r, 1); pair[0].Color != "#000000" || pair[1].Color != "#00001b" {
		t.Fatal(pair)
	}
}

func TestFlowPeriodQuantumCyclesAndClosedBoundary(t *testing.T) {
	for _, mode := range []string{"linear", "sine", "orbit"} {
		for _, period := range []int{1300, 1301, 1999, 2000, 2001, 1199999, 1200000} {
			for _, cycles := range []int{1, 15} {
				r := mustCompile(t, fmt.Sprintf(`{"kind":"alternate","transition":%q,"period_ms":%d,"cycles":%d}`, mode, period, cycles))
				var sum int64
				for i, step := range r.Program.Steps {
					sum += int64(step.FadeMS + step.HoldMS + control.StepOverheadMS)
					if step.LED != 1+i%2 {
						t.Fatal("LED order changed", i, step)
					}
					if i < 2 {
						if step.FadeMS != 0 || step.HoldMS != 0 {
							t.Fatal("initial pair is not immediate", step)
						}
					} else if step.FadeMS < 10 || step.FadeMS%10 != 0 || step.HoldMS < 0 || step.HoldMS > 9 {
						t.Fatal("invalid sequential slot", i, step)
					}
				}
				if sum != int64(period) || r.NominalDurationMS != int64(period*cycles) || r.TotalSteps != 66*cycles || r.Program.Repeat != cycles {
					t.Fatal("period/count mismatch", sum, r)
				}
				first, last := flowPairAt(t, r, 0), flowPairAt(t, r, 32)
				if first[0].Color != last[0].Color || first[1].Color != last[1].Color {
					t.Fatal("repeat boundary changes targets", first, last)
				}
			}
		}
	}
	r := mustCompile(t, `{"kind":"alternate","transition":"sine","period_ms":1301}`)
	for i, s := range r.Program.Steps[2:] {
		wantHold := 0
		if i == 63 {
			wantHold = 1
		}
		if s.FadeMS != 10 || s.HoldMS != wantHold {
			t.Fatal("1301ms distribution", i, s)
		}
	}
}

func TestFlowLEDOrderColorsAndNormalizedRoundTrip(t *testing.T) {
	for _, mode := range []string{"linear", "sine", "orbit"} {
		r := mustCompile(t, fmt.Sprintf(`{"kind":"alternate","transition":%q}`, mode))
		swap := mustCompile(t, fmt.Sprintf(`{"kind":"alternate","transition":%q,"leds":[2,1]}`, mode))
		colors := mustCompile(t, fmt.Sprintf(`{"kind":"alternate","transition":%q,"colors":["blue","red"]}`, mode))
		for i, s := range r.Program.Steps {
			if swap.Program.Steps[i].LED != 3-s.LED || swap.Program.Steps[i].Color != s.Color || swap.Program.Steps[i].FadeMS != s.FadeMS || swap.Program.Steps[i].HoldMS != s.HoldMS {
				t.Fatal("LED order altered temporal/color intent", i)
			}
			want := "#" + s.Color[5:7] + s.Color[3:5] + s.Color[1:3]
			if colors.Program.Steps[i].Color != want {
				t.Fatal("swapping source colors changed brightness focus", i, colors.Program.Steps[i].Color, want)
			}
		}
		data, err := json.Marshal(r.Action)
		if err != nil {
			t.Fatal(err)
		}
		again := mustCompile(t, string(data))
		if !reflect.DeepEqual(r, again) {
			t.Fatal("normalized roundtrip changed")
		}
		r.Program.Steps[0].Color = "changed"
		r.Action.Colors[0] = "changed"
		if fresh := mustCompile(t, string(data)); !reflect.DeepEqual(fresh, again) {
			t.Fatal("flow results alias mutable memory")
		}
	}
}

func TestFlowCutPreservesOriginalProgramAndDefaults(t *testing.T) {
	for _, input := range []string{`{"kind":"alternate"}`, `{"kind":"alternate","period_ms":41,"leds":[2,1],"colors":["#abc","blue"]}`} {
		legacy := mustCompile(t, input)
		explicit := mustCompile(t, strings.TrimSuffix(input, "}")+`,"transition":"cut"}`)
		if !reflect.DeepEqual(legacy.Program, explicit.Program) || legacy.NominalDurationMS != explicit.NominalDurationMS || len(explicit.Program.Steps) != 4 || explicit.Action.Transition != "cut" {
			t.Fatal("cut changed legacy behavior")
		}
		explicit.Action.Transition = ""
		if !reflect.DeepEqual(legacy, explicit) {
			t.Fatal("cut changed non-transition normalized fields")
		}
	}
}

func TestFlowInvalidBoundaryIsStrictAndReturnsNoResult(t *testing.T) {
	cases := []string{`{"kind":"alternate","transition":""}`, `{"kind":"alternate","transition":"ease"}`, `{"kind":"alternate","transition":"Orbit"}`, `{"kind":"alternate","transition":null}`, `{"kind":"alternate","transition":1}`, `{"kind":"alternate","transition":"sine","transit\u0069on":"orbit"}`, `{"kind":"alternate","transition":"orbit","period_ms":1299}`, `{"kind":"alternate","transition":"linear","period_ms":1200001}`, `{"kind":"alternate","transition":"sine","period_ms":1300.0}`, `{"kind":"alternate","transition":"sine","period_ms":13e2}`, `{"kind":"alternate","transition":"orbit","cycles":16}`, `{"kind":"alternate","transition":"orbit","gamma":2}`}
	for _, valid := range []string{`{"kind":"set","color":"red"}`, `{"kind":"off"}`, `{"kind":"fade","color":"red","duration_ms":10}`, `{"kind":"blink","color":"red"}`, `{"kind":"breathe","color":"red"}`, `{"kind":"pulse","color":"red"}`, `{"kind":"rainbow"}`} {
		cases = append(cases, strings.TrimSuffix(valid, "}")+`,"transition":"orbit"}`)
	}
	for _, input := range cases {
		result, err := CompileJSON([]byte(input))
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(result, Result{}) {
			t.Fatalf("invalid input accepted %s: %+v %v", input, result, err)
		}
	}
}

func TestFlowConcurrentCompilation(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r, err := CompileJSON([]byte(`{"kind":"alternate","transition":"orbit","period_ms":1301,"cycles":15}`))
				if err != nil || r.NominalDurationMS != 19515 || r.TotalSteps != 990 {
					t.Error(r, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
