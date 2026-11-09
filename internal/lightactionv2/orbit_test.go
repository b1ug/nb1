package lightactionv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightaction"
)

func compile(t *testing.T, data string) Result {
	t.Helper()
	result, err := CompileJSON([]byte(data))
	if err != nil {
		t.Fatalf("compile %s: %v", data, err)
	}
	return result
}

func TestOrbitDefaultsAndExplicitRoundTrip(t *testing.T) {
	got := compile(t, `{"kind":"orbit"}`)
	want := Action{Kind: "orbit", Colors: []string{"#ff0000", "#0000ff"}, LEDs: []int{1, 2}, PeriodMS: 3000, Cycles: 1, Segments: 4, OnFinish: "off"}
	if !reflect.DeepEqual(got.Action, want) || got.SchemaVersion != 1 || got.Contract != Contract || got.ProgramCompileContract != control.FrameContract || got.RecipeID != "orbit-paired-linear-4/experimental-v1" {
		t.Fatalf("unexpected defaults/contract: %#v", got)
	}
	if got.TotalFrames != 4 || got.PlannedTargetWrites != 10 || got.NominalDurationMS != 3040 {
		t.Fatalf("initial or tail accounting: %#v", got)
	}
	if got.Program.Repeat != 1 || len(got.Program.Initial) != 2 || got.Program.OnFinish != "off" {
		t.Fatalf("unexpected program: %#v", got.Program)
	}
	encoded, _ := json.Marshal(got.Action)
	repeated, err := CompileJSON(encoded)
	if err != nil || !reflect.DeepEqual(got, repeated) {
		t.Fatalf("explicit canonical action changed result: %#v %v", repeated, err)
	}
	envelope := append(append([]byte(`{"contract":"`+Contract+`","action":`), encoded...), '}')
	wrapped, err := CompileRequest(envelope)
	if err != nil || !reflect.DeepEqual(got, wrapped) {
		t.Fatalf("envelope differs: %#v %v", wrapped, err)
	}
	if _, err := control.NormalizeFrameProgram(got.Program); err != nil {
		t.Fatalf("generated program rejected: %v", err)
	}
}

func TestOrbitMatchesRecordedHardwareExperiment(t *testing.T) {
	// These data-only fixtures are copied from the zero-retry, 30fps physical
	// experiments of 2026-11-10. They contain no camera images or runtime IDs.
	for _, segments := range []int{4, 8} {
		t.Run(fmt.Sprint(segments), func(t *testing.T) {
			data, err := os.ReadFile(fmt.Sprintf("testdata/recorded-paired%d.json", segments))
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Initial []string `json:"initial"`
				Frames  []struct {
					Colors     []string `json:"colors"`
					FadeMS     int      `json:"fade_ms"`
					IntervalMS int      `json:"interval_ms"`
				} `json:"frames"`
				Repeat int `json:"repeat"`
				GapMS  int `json:"gap_ms"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			got := compile(t, fmt.Sprintf(`{"kind":"orbit","segments":%d,"cycles":3}`, segments))
			if fixture.Repeat != got.Program.Repeat || fixture.GapMS != control.FrameWriteSpacingMS || len(fixture.Frames) != len(got.Program.Frames) {
				t.Fatal("experiment geometry no longer matches compiler")
			}
			for i, color := range fixture.Initial {
				if got.Program.Initial[i] != (control.FrameTarget{LED: i + 1, Color: color}) {
					t.Fatalf("initial %d differs: %#v", i, got.Program.Initial)
				}
			}
			for i, frame := range fixture.Frames {
				actual := got.Program.Frames[i]
				if frame.FadeMS != actual.FadeMS || frame.IntervalMS != actual.IntervalMS {
					t.Fatalf("frame %d timing differs: %#v", i, actual)
				}
				for j, color := range frame.Colors {
					if actual.Targets[j] != (control.FrameTarget{LED: j + 1, Color: color}) {
						t.Fatalf("frame %d target %d differs: %#v", i, j, actual.Targets)
					}
				}
			}
		})
	}
}

func TestOrbitColorFormsAndLEDOrder(t *testing.T) {
	want := compile(t, `{"kind":"orbit","colors":["#ff0000","#0000ff"]}`)
	for _, colors := range []string{
		`["RED","#00F"]`,
		`[{"space":"rgb8","r":255,"g":0,"b":0},{"space":"hsv","h_deg":240,"s_pct":100,"v_pct":100}]`,
		`["\u0072ed"," BLUE "]`,
	} {
		got := compile(t, `{"kind":"orbit","colors":`+colors+`}`)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("color forms differ: %s", colors)
		}
	}
	reversed := compile(t, `{"kind":"orbit","leds":[2,1],"on_finish":"hold"}`)
	if reversed.Program.Initial[0].LED != 2 || reversed.Program.Initial[1].LED != 1 || reversed.Program.OnFinish != "hold" {
		t.Fatalf("LED order/hold lost: %#v", reversed.Program)
	}
	for i, frame := range reversed.Program.Frames {
		if frame.Targets[0].LED != 2 || frame.Targets[1].LED != 1 || frame.Targets[0].Color != want.Program.Frames[i].Targets[0].Color {
			t.Fatal("logical side order was changed")
		}
	}
}

func TestOrbitPeriodsAndFrameBudgets(t *testing.T) {
	for _, segments := range []int{4, 8} {
		for _, period := range []int{segments * 50, segments*50 + 1, 2001, 3001, segments * 600000} {
			got := compile(t, fmt.Sprintf(`{"kind":"orbit","segments":%d,"period_ms":%d}`, segments, period))
			sum := 0
			for _, frame := range got.Program.Frames {
				sum += frame.IntervalMS
				if frame.FadeMS%10 != 0 || frame.FadeMS < frame.IntervalMS || frame.FadeMS-frame.IntervalMS > 9 {
					t.Fatalf("non-covering fade: %#v", frame)
				}
			}
			if sum != period {
				t.Fatalf("period %d compiled to %d", period, sum)
			}
		}
		limit := 1024 / segments
		got := compile(t, fmt.Sprintf(`{"kind":"orbit","segments":%d,"cycles":%d}`, segments, limit))
		if got.TotalFrames != 1024 || got.PlannedTargetWrites != 2050 {
			t.Fatal("executed budget wrong")
		}
		for _, input := range []string{
			fmt.Sprintf(`{"kind":"orbit","segments":%d,"cycles":%d}`, segments, limit+1),
			fmt.Sprintf(`{"kind":"orbit","segments":%d,"period_ms":%d}`, segments, segments*50-1),
			fmt.Sprintf(`{"kind":"orbit","segments":%d,"period_ms":%d}`, segments, segments*600000+1),
			fmt.Sprintf(`{"kind":"orbit","segments":%d,"period_ms":%d,"cycles":%d}`, segments, segments*600000, limit),
		} {
			if _, err := CompileJSON([]byte(input)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted budget overflow: %s %v", input, err)
			}
		}
	}
	// 24 hours includes one initial pair and the actual nominal final tail.
	if _, err := CompileJSON([]byte(`{"kind":"orbit","segments":4,"period_ms":864000,"cycles":100}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal("a full day plus initialization/tail was accepted")
	}
	got := compile(t, `{"kind":"orbit","segments":4,"period_ms":863999,"cycles":100}`)
	if got.NominalDurationMS != 86399940 {
		t.Fatalf("near-day budget changed: %d", got.NominalDurationMS)
	}
}

func TestOrbitStrictBadInputs(t *testing.T) {
	for i, input := range []string{
		``, `null`, `[]`, `{}`, `{"kind":"alternate"}`, `{"Kind":"orbit"}`,
		`{"kind":"orbit","kind":"orbit"}`, `{"kind":"orbit","k\u0069nd":"orbit"}`,
		`{"kind":"orbit","extra":1}`, `{"kind":"orbit","transition":"orbit"}`,
		`{"kind":"orbit","segments":4.0}`, `{"kind":"orbit","segments":4e0}`, `{"kind":"orbit","segments":3}`,
		`{"kind":"orbit","period_ms":-0}`, `{"kind":"orbit","period_ms":"3000"}`, `{"kind":"orbit","period_ms":null}`,
		`{"kind":"orbit","period_ms":9999999999999999999999999999999999}`,
		`{"kind":"orbit","cycles":0}`, `{"kind":"orbit","cycles":-1}`, `{"kind":"orbit","cycles":null}`,
		`{"kind":"orbit","on_finish":""}`, `{"kind":"orbit","on_finish":null}`, `{"kind":"orbit","on_finish":"restart"}`,
		`{"kind":"orbit","leds":null}`, `{"kind":"orbit","leds":[1,1]}`, `{"kind":"orbit","leds":[0,2]}`,
		`{"kind":"orbit","leds":[1]}`, `{"kind":"orbit","leds":[1,2,3]}`, `{"kind":"orbit","leds":[1.0,2]}`,
		`{"kind":"orbit","colors":null}`, `{"kind":"orbit","colors":[]}`, `{"kind":"orbit","colors":["red"]}`,
		`{"kind":"orbit","colors":["red","blue","green"]}`, `{"kind":"orbit","colors":[null,"blue"]}`,
		`{"kind":"orbit","colors":["\ud800","blue"]}`, `{"kind":"orbit","colors":["red",{}]}`,
		`{"kind":"orbit","colors":[{"space":"rgb8","r":255,"r":255,"g":0,"b":0},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"rgb8","r":255,"g":0,"b":0,"extra":true},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"hsv","h_deg":0,"s_pct":100,"v_pct":100,"space":"hsv"},"blue"]}`,
		`{"kind":"orbit","colors":["red",{"space":"rgb8","r":0,"g":0,"b":256}]}`,
		`{"kind":"orbit"} {}`, `{"kind":"orbit"}[]`, "{\"kind\":\"orbit\",\"x\":\"\xff\"}",
		`{"kind":"orbit","extra":` + strings.Repeat("[", 34) + strings.Repeat("]", 34) + `}`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got, err := CompileJSON([]byte(input))
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(got, Result{}) {
				t.Fatalf("accepted ambiguous input: %s -> %#v %v", input, got, err)
			}
		})
	}
	valid := `{"kind":"orbit"}`
	if _, err := CompileJSON([]byte(valid + strings.Repeat(" ", MaxRequestBytes-len(valid)))); err != nil {
		t.Fatalf("exact bound failed: %v", err)
	}
	if _, err := CompileJSON([]byte(valid + strings.Repeat(" ", MaxRequestBytes-len(valid)+1))); !errors.Is(err, ErrInvalid) {
		t.Fatal("byte bound bypassed")
	}
}

func TestOrbitEnvelopeVersionAndOldContractIsolation(t *testing.T) {
	for _, body := range []string{
		`{"kind":"orbit"}`, `{"contract":"` + Contract + `"}`, `{"contract":"` + Contract + `","action":null}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"orbit"}}`,
		`{"contract":"` + Contract + `","action":{"kind":"orbit"},"serial":"20005601"}`,
		`{"contract":"` + Contract + `","action":{"kind":"orbit"},"action":{"kind":"orbit"}}`,
		`{"contract":"` + Contract + `","action":{"kind":"orbit","cycles":null}}`,
	} {
		if _, err := CompileRequest([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted bad envelope: %s", body)
		}
	}
	if _, err := lightaction.CompileJSON([]byte(`{"kind":"orbit"}`)); err == nil {
		t.Fatal("new standalone action leaked into v1")
	}
	old, err := lightaction.CompileJSON([]byte(`{"kind":"alternate","transition":"orbit","period_ms":3000,"cycles":3}`))
	if err != nil || len(old.Program.Steps) != 66 || old.ProgramCompileContract != "nb1-program-effect/v1" {
		t.Fatalf("old sequential program changed: %#v %v", old, err)
	}
}

func TestOrbitCompileIsDetachedAndConcurrent(t *testing.T) {
	baseline := compile(t, `{"kind":"orbit","segments":8}`)
	changed := compile(t, `{"kind":"orbit","segments":8}`)
	changed.Action.LEDs[0] = 99
	changed.Action.Colors[0] = "#abcdef"
	changed.Program.Initial[0].Color = "#abcdef"
	changed.Program.Frames[0].Targets[0].Color = "#abcdef"
	if !reflect.DeepEqual(baseline, compile(t, `{"kind":"orbit","segments":8}`)) {
		t.Fatal("result shares mutable compiler state")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := CompileJSON([]byte(`{"kind":"orbit","segments":8}`))
			if err != nil || !reflect.DeepEqual(got, baseline) {
				t.Errorf("concurrent result differs: %v", err)
			}
		}()
	}
	wg.Wait()
}
