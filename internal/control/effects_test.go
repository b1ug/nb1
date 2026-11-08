package control

import (
	"errors"
	"reflect"
	"testing"
)

func effectInt(value int) *int          { return &value }
func effectString(value string) *string { return &value }

func pulseGolden(led, upFade, downFade, cycles int, finish string) Program {
	return Program{Steps: []Step{
		{LED: led, Color: "#000000"},
		{LED: led, Color: "#ffaa00", FadeMS: upFade},
		{LED: led, Color: "#000000", FadeMS: downFade},
	}, Repeat: cycles, OnFinish: finish}
}

func TestCompileEffectGolden(t *testing.T) {
	for _, test := range []struct {
		name   string
		effect Effect
		want   Program
	}{
		{"steady_defaults", Effect{Kind: "steady", Color: "#FFAA00"}, Program{Steps: []Step{{Color: "#ffaa00"}}, Repeat: 1, OnFinish: "hold"}},
		{"steady_fade_led", Effect{Kind: "steady", Color: "#FFAA00", LED: 2, FadeMS: effectInt(300)}, Program{Steps: []Step{{LED: 2, Color: "#ffaa00", FadeMS: 300}}, Repeat: 1, OnFinish: "hold"}},
		{"steady_explicit_zero", Effect{Kind: "steady", Color: "#FFAA00", FadeMS: effectInt(0)}, Program{Steps: []Step{{Color: "#ffaa00"}}, Repeat: 1, OnFinish: "hold"}},
		{"pulse_defaults", Effect{Kind: "pulse", Color: "#FFAA00"}, pulseGolden(0, 480, 490, 1, "off")},
		{"pulse_800", Effect{Kind: "pulse", Color: "#FFAA00", Cycles: effectInt(3), PeriodMS: effectInt(800)}, pulseGolden(0, 380, 390, 3, "off")},
		{"pulse_odd_half", Effect{Kind: "pulse", Color: "#FFAA00", PeriodMS: effectInt(810)}, pulseGolden(0, 380, 400, 1, "off")},
		{"pulse_minimum", Effect{Kind: "pulse", Color: "#FFAA00", PeriodMS: effectInt(60)}, pulseGolden(0, 10, 20, 1, "off")},
		{"pulse_short_odd_half", Effect{Kind: "pulse", Color: "#FFAA00", PeriodMS: effectInt(70)}, pulseGolden(0, 10, 30, 1, "off")},
		{"pulse_hold_led", Effect{Kind: "pulse", Color: "#FFAA00", LED: 1, PeriodMS: effectInt(800), OnFinish: effectString("hold")}, pulseGolden(1, 380, 390, 1, "hold")},
		{"pulse_maximum_period", Effect{Kind: "pulse", Color: "#FFAA00", PeriodMS: effectInt(1200000)}, pulseGolden(0, 599980, 599990, 1, "off")},
		{"pulse_exact_24h", Effect{Kind: "pulse", Color: "#FFAA00", Cycles: effectInt(72), PeriodMS: effectInt(1200000)}, pulseGolden(0, 599980, 599990, 72, "off")},
		{"pulse_10000_cycles_exact_24h", Effect{Kind: "pulse", Color: "#FFAA00", Cycles: effectInt(10000), PeriodMS: effectInt(8640)}, pulseGolden(0, 4300, 4310, 10000, "off")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := CompileEffect(test.effect)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("compiled program=%+v; want %+v", got, test.want)
			}
			if test.effect.Kind == "pulse" {
				period := 1000
				if test.effect.PeriodMS != nil {
					period = *test.effect.PeriodMS
				}
				var nominal int64
				for _, step := range got.Steps {
					nominal += int64(step.FadeMS) + int64(step.HoldMS) + 10
				}
				if len(got.Steps) != 3 || nominal != int64(period) {
					t.Fatalf("pulse was expanded or period excluded ticks: len=%d nominal=%d requested=%d", len(got.Steps), nominal, period)
				}
			}
			// Caller mutation must not affect another compilation or the DTO.
			got.Steps[0].Color = "#ffffff"
			again, err := CompileEffect(test.effect)
			if err != nil || !reflect.DeepEqual(again, test.want) {
				t.Fatalf("compilation shared mutable storage: %+v err=%v", again, err)
			}
		})
	}
}

func TestCompileEffectRejectsInvalidIntent(t *testing.T) {
	for _, test := range []struct {
		name   string
		effect Effect
	}{
		{"missing_kind", Effect{Color: "#ffaa00"}},
		{"kind_case", Effect{Kind: "Pulse", Color: "#ffaa00"}},
		{"unknown_kind", Effect{Kind: "script", Color: "#ffaa00"}},
		{"steady_cycles_zero", Effect{Kind: "steady", Color: "#ffaa00", Cycles: effectInt(0)}},
		{"steady_period_zero", Effect{Kind: "steady", Color: "#ffaa00", PeriodMS: effectInt(0)}},
		{"steady_finish_hold", Effect{Kind: "steady", Color: "#ffaa00", OnFinish: effectString("hold")}},
		{"pulse_fade_zero", Effect{Kind: "pulse", Color: "#ffaa00", FadeMS: effectInt(0)}},
		{"cycles_zero", Effect{Kind: "pulse", Color: "#ffaa00", Cycles: effectInt(0)}},
		{"cycles_negative", Effect{Kind: "pulse", Color: "#ffaa00", Cycles: effectInt(-1)}},
		{"cycles_too_large", Effect{Kind: "pulse", Color: "#ffaa00", Cycles: effectInt(10001)}},
		{"period_zero", Effect{Kind: "pulse", Color: "#ffaa00", PeriodMS: effectInt(0)}},
		{"period_negative", Effect{Kind: "pulse", Color: "#ffaa00", PeriodMS: effectInt(-10)}},
		{"period_below_minimum", Effect{Kind: "pulse", Color: "#ffaa00", PeriodMS: effectInt(50)}},
		{"period_not_quantized", Effect{Kind: "pulse", Color: "#ffaa00", PeriodMS: effectInt(61)}},
		{"period_above_maximum", Effect{Kind: "pulse", Color: "#ffaa00", PeriodMS: effectInt(1200010)}},
		{"finish_empty", Effect{Kind: "pulse", Color: "#ffaa00", OnFinish: effectString("")}},
		{"finish_unknown", Effect{Kind: "pulse", Color: "#ffaa00", OnFinish: effectString("repeat")}},
		{"fade_negative", Effect{Kind: "steady", Color: "#ffaa00", FadeMS: effectInt(-10)}},
		{"fade_not_quantized", Effect{Kind: "steady", Color: "#ffaa00", FadeMS: effectInt(1)}},
		{"fade_above_maximum", Effect{Kind: "steady", Color: "#ffaa00", FadeMS: effectInt(600010)}},
		{"negative_led", Effect{Kind: "pulse", Color: "#ffaa00", LED: -1}},
		{"unknown_led", Effect{Kind: "steady", Color: "#ffaa00", LED: 3}},
		{"wrapped_led", Effect{Kind: "pulse", Color: "#ffaa00", LED: 256}},
		{"missing_color", Effect{Kind: "pulse"}},
		{"named_color", Effect{Kind: "steady", Color: "red"}},
		{"invalid_hex", Effect{Kind: "pulse", Color: "#zz9900"}},
		{"24h_exceeded_by_cycle", Effect{Kind: "pulse", Color: "#ffaa00", Cycles: effectInt(73), PeriodMS: effectInt(1200000)}},
		{"24h_exceeded_by_period", Effect{Kind: "pulse", Color: "#ffaa00", Cycles: effectInt(10000), PeriodMS: effectInt(8650)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			program, err := CompileEffect(test.effect)
			if !errors.Is(err, ErrInvalid) || len(program.Steps) != 0 {
				t.Fatalf("invalid effect produced program=%+v err=%v", program, err)
			}
		})
	}
}

func TestCompiledPulseExecutesFromBlackAndSharesRawReceipt(t *testing.T) {
	b := newEngineBackend("a")
	b.led1, b.led2 = "#ffffff", "#123456"
	e, _ := receiptTestEngine(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	program, err := CompileEffect(Effect{Kind: "pulse", Color: "#FFAA00", LED: 1, PeriodMS: effectInt(60), OnFinish: effectString("hold")})
	if err != nil {
		t.Fatal(err)
	}
	ticket := receiptTicket(e, 1)
	accepted := submitReceipt(t, e, program, "replace", ticket)
	awaitEngineSignal(t, gate.entered)
	raw := pulseGolden(1, 10, 20, 1, "hold")
	if replay := submitReceipt(t, e, raw, "replace", ticket); !replay.Replayed || replay.Job.ID != accepted.Job.ID {
		t.Fatalf("effect and equivalent raw program did not share one job: %+v", replay)
	}
	gate.open()
	awaitEngineJob(t, e, accepted.Job.ID, "succeeded")
	calls, led1, led2, _, _, _ := b.snapshot()
	if colors := engineSetColors(calls); !reflect.DeepEqual(colors, []string{"#000000", "#ffaa00", "#000000"}) {
		t.Fatalf("pulse did not begin/end at black or was replayed: %v", colors)
	}
	if led1 != "#000000" || led2 != "#123456" {
		t.Fatalf("hold did not retain final selected black/other LED: %s/%s", led1, led2)
	}
}
