package control

import "fmt"

// Effect is a finite, high-level request compiled into an ordinary Program.
// Optional pointers distinguish omitted defaults from explicitly invalid zero
// values or fields belonging to another effect kind. JSON entry points must
// independently reject explicit null and unknown fields.
type Effect struct {
	Kind     string  `json:"kind"`
	Color    string  `json:"color"`
	LED      int     `json:"led"`
	FadeMS   *int    `json:"fade_ms,omitempty"`
	Cycles   *int    `json:"cycles,omitempty"`
	PeriodMS *int    `json:"period_ms,omitempty"`
	OnFinish *string `json:"on_finish,omitempty"`
}

// CompileEffect is the authoritative high-level compiler. Its result is an
// ordinary normalized Program: queueing, receipts, execution and cleanup retain
// their existing semantics. This function performs no I/O or device discovery.
// Pulse periods include the executor's three 10ms update allowances per cycle;
// native I/O and preflight/finish cleanup add time beyond that nominal period.
func CompileEffect(effect Effect) (Program, error) {
	var program Program
	switch effect.Kind {
	case "steady":
		if effect.Cycles != nil || effect.PeriodMS != nil || effect.OnFinish != nil {
			return Program{}, fmt.Errorf("%w: steady permits only kind, color, led and fade_ms", ErrInvalid)
		}
		fade := 0
		if effect.FadeMS != nil {
			fade = *effect.FadeMS
		}
		program = Program{Steps: []Step{{LED: effect.LED, Color: effect.Color, FadeMS: fade}}, Repeat: 1, OnFinish: "hold"}
	case "pulse":
		if effect.FadeMS != nil {
			return Program{}, fmt.Errorf("%w: pulse does not permit fade_ms", ErrInvalid)
		}
		cycles, period, finish := 1, 1000, "off"
		if effect.Cycles != nil {
			cycles = *effect.Cycles
		}
		if effect.PeriodMS != nil {
			period = *effect.PeriodMS
		}
		if effect.OnFinish != nil {
			finish = *effect.OnFinish
		}
		if cycles < 1 || cycles > MaxProgramRepeat {
			return Program{}, fmt.Errorf("%w: pulse cycles must be 1–10000", ErrInvalid)
		}
		if period < MinPulsePeriodMS || period > MaxPulsePeriodMS || period%FadeQuantumMS != 0 {
			return Program{}, fmt.Errorf("%w: pulse period_ms must be 60–1200000 in 10ms units", ErrInvalid)
		}
		if finish != "off" && finish != "hold" {
			return Program{}, fmt.Errorf("%w: pulse on_finish must be off or hold", ErrInvalid)
		}
		up := (period / 20) * 10
		down := period - up
		program = Program{Steps: []Step{
			{LED: effect.LED, Color: "#000000"},
			{LED: effect.LED, Color: effect.Color, FadeMS: up - 20},
			{LED: effect.LED, Color: "#000000", FadeMS: down - 10},
		}, Repeat: cycles, OnFinish: finish}
	default:
		return Program{}, fmt.Errorf("%w: effect kind must be steady or pulse", ErrInvalid)
	}
	// Normalize owns RGB/LED/fade validation and the complete 24-hour nominal
	// duration budget. Repeat stays compact instead of expanding cycles.
	normalized, err := Normalize(program)
	if err != nil {
		return Program{}, fmt.Errorf("compile %s effect: %w", effect.Kind, err)
	}
	return normalized, nil
}
