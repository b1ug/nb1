// Package lightactionv2 compiles experimental high-level actions into explicit
// frame programs. It does not discover devices, submit work, or perform I/O.
package lightactionv2

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/control"
)

const Contract = "nb1-light-action/experimental-v2"
const MaxRequestBytes = 65536

const expectedFrameContract = "nb1-frame-program/experimental-v1"
const expectedColorContract = "nb1-color-input/v1"

var ErrInvalid = errors.New("invalid experimental light action")

// Action makes every default explicit. Colors are canonical raw RGB8; a
// segment is one pair of target fades, not two sequential completed fades.
type Action struct {
	Kind     string   `json:"kind"`
	Colors   []string `json:"colors"`
	LEDs     []int    `json:"leds"`
	PeriodMS int      `json:"period_ms"`
	Cycles   int      `json:"cycles"`
	Segments int      `json:"segments"`
	OnFinish string   `json:"on_finish"`
}

type Result struct {
	SchemaVersion          int                  `json:"schema_version"`
	Contract               string               `json:"contract"`
	ProgramCompileContract string               `json:"program_compile_contract"`
	RecipeID               string               `json:"recipe_id"`
	Action                 Action               `json:"action"`
	Program                control.FrameProgram `json:"program"`
	NominalDurationMS      int64                `json:"nominal_duration_ms"`
	TotalFrames            int                  `json:"total_frames"`
	PlannedTargetWrites    int                  `json:"planned_target_writes"`
	RequiredCapabilities   []string             `json:"required_capabilities"`
}

// CompileJSON accepts one bare Orbit action and never submits the result.
// The explicit frame contract must be understood by the eventual executor.
func CompileJSON(data []byte) (Result, error) {
	if control.FrameContract != expectedFrameContract || colorinput.Contract != expectedColorContract || control.FrameFadeQuantumMS != 10 || control.FrameWriteSpacingMS != 10 || control.FrameUpdateAllowanceMS != 10 {
		return Result{}, invalid("the version-bound frame/color compiler is unavailable")
	}
	action, err := decodeAction(data)
	if err != nil {
		return Result{}, err
	}
	preview, err := control.PreviewFrameProgram(buildOrbit(action))
	if err != nil {
		return Result{}, invalid("compiled frame program: %v", err)
	}
	return Result{
		SchemaVersion: 1, Contract: Contract, ProgramCompileContract: control.FrameContract,
		RecipeID: fmt.Sprintf("orbit-paired-linear-%d/experimental-v1", action.Segments),
		Action:   action, Program: preview.Program, NominalDurationMS: preview.NominalDurationMS,
		TotalFrames: preview.TotalFrames, PlannedTargetWrites: preview.PlannedTargetWrites,
		RequiredCapabilities: preview.RequiredCapabilities,
	}, nil
}

// These fixed samples are the old 32-phase Orbit recipe at phases
// 0,4,8,...,32. Four segments use every second sample. The new recipe changes
// execution geometry: the device connects these targets with long linear
// fades, without implicit gamma or floating-point/platform variation.
var orbitCosine8 = [...]int{0, 1464, 5000, 8536, 10000, 8536, 5000, 1464, 0}
var orbitSine8 = [...]int{0, 7071, 10000, 7071, 0, -7071, -10000, -7071, 0}

func buildOrbit(a Action) control.FrameProgram {
	first, _ := hex.DecodeString(a.Colors[0][1:])
	second, _ := hex.DecodeString(a.Colors[1][1:])
	initial := orbitColors(first, second, 0)
	program := control.FrameProgram{Contract: control.FrameContract,
		Initial: []control.FrameTarget{{LED: a.LEDs[0], Color: initial[0]}, {LED: a.LEDs[1], Color: initial[1]}},
		Frames:  make([]control.Frame, a.Segments), Repeat: a.Cycles, OnFinish: a.OnFinish}
	for n := 1; n <= a.Segments; n++ {
		phase := n * 8 / a.Segments
		colors := orbitColors(first, second, phase)
		// Use int64 before multiplying so the bounds stay safe on 32-bit Go.
		interval := int(int64(n)*int64(a.PeriodMS)/int64(a.Segments) - int64(n-1)*int64(a.PeriodMS)/int64(a.Segments))
		fade := (interval + control.FrameFadeQuantumMS - 1) / control.FrameFadeQuantumMS * control.FrameFadeQuantumMS
		program.Frames[n-1] = control.Frame{
			Targets: []control.FrameTarget{{LED: a.LEDs[0], Color: colors[0]}, {LED: a.LEDs[1], Color: colors[1]}},
			FadeMS:  fade, IntervalMS: interval,
		}
	}
	return program
}

func orbitColors(first, second []byte, phase int) [2]string {
	weight := orbitCosine8[phase]
	brightnessFirst := 30000 + 2*orbitSine8[phase]
	brightnessSecond := 30000 - 2*orbitSine8[phase]
	return [2]string{mix(first, second, weight, brightnessFirst), mix(second, first, weight, brightnessSecond)}
}

func mix(first, second []byte, weight, brightness int) string {
	var rgb [3]byte
	const denominator = int64(10000) * 50000
	for i := range rgb {
		mixed := int64(first[i])*int64(10000-weight) + int64(second[i])*int64(weight)
		rgb[i] = byte((mixed*int64(brightness) + denominator/2) / denominator)
	}
	return "#" + hex.EncodeToString(rgb[:])
}

func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
