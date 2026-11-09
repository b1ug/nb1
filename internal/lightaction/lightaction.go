// Package lightaction compiles a versioned authoring language into the existing
// finite Program contract. It performs no I/O, device discovery or execution.
package lightaction

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/control"
)

const Contract = "nb1-light-action/v1"
const MaxRequestBytes = 65536

const expectedProgramContract = "nb1-program-effect/v1"
const expectedColorContract = "nb1-color-input/v1"

var ErrInvalid = errors.New("invalid light action")

// NormalizedAction includes only fields applicable to this kind. Colors are
// raw canonical RGB8, and defaults are explicit. Pointers preserve valid zeros.
type NormalizedAction struct {
	Kind       string   `json:"kind"`
	LED        *int     `json:"led,omitempty"`
	Color      string   `json:"color,omitempty"`
	Colors     []string `json:"colors,omitempty"`
	LEDs       []int    `json:"leds,omitempty"`
	DurationMS *int     `json:"duration_ms,omitempty"`
	OnMS       *int     `json:"on_ms,omitempty"`
	OffMS      *int     `json:"off_ms,omitempty"`
	PeriodMS   *int     `json:"period_ms,omitempty"`
	AttackMS   *int     `json:"attack_ms,omitempty"`
	DecayMS    *int     `json:"decay_ms,omitempty"`
	RestMS     *int     `json:"rest_ms,omitempty"`
	Cycles     *int     `json:"cycles,omitempty"`
	OnFinish   string   `json:"on_finish"`
}

type Result struct {
	SchemaVersion          int              `json:"schema_version"`
	Contract               string           `json:"contract"`
	ProgramCompileContract string           `json:"program_compile_contract"`
	Action                 NormalizedAction `json:"action"`
	Program                control.Program  `json:"program"`
	NominalDurationMS      int64            `json:"nominal_duration_ms"`
	TotalSteps             int              `json:"total_steps"`
	RequiredCapabilities   []string         `json:"required_capabilities"`
}

// CompileJSON accepts one bare action. It does not accept a Program, legacy
// Effect or the contract/action envelope; those remain distinct input shapes.
func CompileJSON(data []byte) (Result, error) {
	if control.CompileContract != expectedProgramContract || colorinput.Contract != expectedColorContract || control.StepOverheadMS != 10 || control.FadeQuantumMS != 10 {
		return Result{}, invalid("the version-bound v1 program/color compiler is unavailable")
	}
	action, err := decodeAction(data)
	if err != nil {
		return Result{}, err
	}
	program := build(action)
	preview, err := control.Preview(&program, nil)
	if err != nil {
		return Result{}, invalid("compiled program: %v", err)
	}
	// This authoring contract bounds executed steps as well as stored steps.
	// The underlying legacy Program repeat semantics are not modified.
	if preview.TotalSteps > control.MaxProgramSteps {
		return Result{}, invalid("action exceeds %d executed steps", control.MaxProgramSteps)
	}
	return Result{SchemaVersion: 1, Contract: Contract, ProgramCompileContract: control.CompileContract,
		Action: action, Program: preview.Program, NominalDurationMS: preview.NominalDurationMS,
		TotalSteps: preview.TotalSteps, RequiredCapabilities: preview.RequiredCapabilities}, nil
}

// CompileRequest requires exactly {"contract":"nb1-light-action/v1","action":{...}}.
func CompileRequest(data []byte) (Result, error) {
	fields, err := object(data)
	if err != nil {
		return Result{}, err
	}
	if err := allowed(fields, "contract", "action"); err != nil {
		return Result{}, err
	}
	version, err := stringValue(fields["contract"])
	if err != nil || version != Contract || fields["action"] == nil {
		return Result{}, invalid("request requires contract %q and action", Contract)
	}
	return CompileJSON(fields["action"])
}

// cosine16 is a fixed raw-RGB intensity LUT: 0 -> 1 -> 0. Its 16 linear
// firmware transitions approximate a smooth cosine envelope. No float, gamma
// or optical calibration is hidden in this contract.
var cosine16 = [...]int{0, 381, 1464, 3087, 5000, 6913, 8536, 9619, 10000, 9619, 8536, 6913, 5000, 3087, 1464, 381, 0}

func build(a NormalizedAction) control.Program {
	p := control.Program{Repeat: 1, OnFinish: a.OnFinish}
	led := 0
	if a.LED != nil {
		led = *a.LED
	}
	if a.Cycles != nil {
		p.Repeat = *a.Cycles
	}
	switch a.Kind {
	case "set":
		p.Steps = []control.Step{{LED: led, Color: a.Color}}
	case "off":
		p.Steps = []control.Step{{LED: led, Color: "#000000"}}
	case "fade":
		p.Steps = []control.Step{{LED: led, Color: a.Color, FadeMS: *a.DurationMS}}
	case "blink":
		p.Steps = []control.Step{{LED: led, Color: a.Color, HoldMS: *a.OnMS - control.StepOverheadMS}, {LED: led, Color: "#000000", HoldMS: *a.OffMS - control.StepOverheadMS}}
	case "breathe":
		p.Steps = []control.Step{{LED: led, Color: "#000000"}}
		for i := 1; i < len(cosine16); i++ {
			p.Steps = append(p.Steps, transition(led, scaled(a.Color, cosine16[i]), *a.PeriodMS-control.StepOverheadMS, i, 16))
		}
	case "pulse":
		p.Steps = []control.Step{{LED: led, Color: "#000000"}, {LED: led, Color: a.Color, FadeMS: *a.AttackMS}, {LED: led, Color: "#000000", FadeMS: *a.DecayMS, HoldMS: *a.RestMS}}
	case "alternate":
		first := *a.PeriodMS / 2
		second := *a.PeriodMS - first
		p.Steps = []control.Step{
			{LED: a.LEDs[0], Color: a.Colors[0]},
			{LED: a.LEDs[1], Color: a.Colors[1], HoldMS: first - 2*control.StepOverheadMS},
			{LED: a.LEDs[0], Color: a.Colors[1]},
			{LED: a.LEDs[1], Color: a.Colors[0], HoldMS: second - 2*control.StepOverheadMS},
		}
	case "rainbow":
		colors := [...]string{"#ff0000", "#ffff00", "#00ff00", "#00ffff", "#0000ff", "#ff00ff", "#ff0000"}
		p.Steps = []control.Step{{LED: led, Color: colors[0]}}
		for i := 1; i < len(colors); i++ {
			p.Steps = append(p.Steps, transition(led, colors[i], *a.PeriodMS-control.StepOverheadMS, i, 6))
		}
	}
	return p
}

// Transition slots sum exactly to the requested period (after the initial
// 10ms marker). Quantize only the hardware fade; preserve the remainder as hold.
func transition(led int, color string, duration, index, count int) control.Step {
	slot := duration*index/count - duration*(index-1)/count
	fade := (slot - control.StepOverheadMS) / control.FadeQuantumMS * control.FadeQuantumMS
	return control.Step{LED: led, Color: color, FadeMS: fade, HoldMS: slot - control.StepOverheadMS - fade}
}

func scaled(color string, weight int) string {
	rgb, _ := hex.DecodeString(color[1:]) // decodeAction already resolved exact Hex6.
	for i, value := range rgb {
		rgb[i] = byte((int(value)*weight + 5000) / 10000)
	}
	return "#" + hex.EncodeToString(rgb)
}

func integer(value int) *int { return &value }
func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
