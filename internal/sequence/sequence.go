// Package sequence defines the validated host playback model for nb1.
package sequence

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	TimingSerial         = "serial"
	TimingInterval       = "interval"
	MaxExecutedSteps     = 1024
	MaxNominalDurationMS = 24 * 60 * 60 * 1000
	MaxFadeMS            = 600000
	FadeQuantumMS        = 10
	MaxHoldMS            = 3600000
	MinIntervalMS        = 50
	MaxIntervalMS        = 600000
	UpdateAllowanceMS    = 10
	TargetWriteSpacingMS = 10
	MaxInputBytes        = 1 << 20
)

var ErrInvalid = errors.New("invalid sequence")

type Sequence struct {
	Timing   string   `json:"timing"`
	Initial  []Target `json:"initial,omitempty"`
	Steps    []Step   `json:"steps"`
	Repeat   int      `json:"repeat"`
	OnFinish string   `json:"on_finish"`
}

type Step struct {
	Targets    []Target `json:"targets"`
	FadeMS     int      `json:"fade_ms"`
	HoldMS     *int     `json:"hold_ms,omitempty"`
	IntervalMS *int     `json:"interval_ms,omitempty"`
}

type Target struct {
	LED   int    `json:"led"`
	Color string `json:"color"`
}

// Normalize validates the playback plan and returns an independent copy with
// defaults and canonical RGB values. Zero Repeat and empty OnFinish represent
// omitted defaults in Go. Decode distinguishes omission from explicit JSON.
func Normalize(input Sequence) (Sequence, error) {
	interval := input.Timing == TimingInterval
	if !interval && input.Timing != TimingSerial {
		return Sequence{}, invalid("timing must be serial or interval")
	}
	if len(input.Steps) == 0 || len(input.Steps) > MaxExecutedSteps {
		return Sequence{}, invalid("steps must contain 1 to %d steps", MaxExecutedSteps)
	}
	if input.Repeat == 0 {
		input.Repeat = 1
	}
	if input.Repeat < 1 || input.Repeat > MaxExecutedSteps/len(input.Steps) {
		return Sequence{}, invalid("repeat must be positive and cannot exceed %d executed steps", MaxExecutedSteps)
	}
	if input.OnFinish == "" {
		input.OnFinish = "off"
	}
	if input.OnFinish != "off" && input.OnFinish != "hold" {
		return Sequence{}, invalid("on_finish must be off or hold")
	}
	result := input
	if interval {
		var err error
		result.Initial, err = normalizeTargets(input.Initial, true, "initial")
		if err != nil {
			return Sequence{}, err
		}
	} else if input.Initial != nil {
		return Sequence{}, invalid("initial is only allowed for interval timing")
	}
	result.Steps = make([]Step, len(input.Steps))
	for i, step := range input.Steps {
		normalized, err := normalizeStep(step, result.Initial, i)
		if err != nil {
			return Sequence{}, err
		}
		result.Steps[i] = normalized
	}
	if result.NominalDurationMS() > MaxNominalDurationMS {
		return Sequence{}, invalid("nominal duration exceeds 24 hours")
	}
	return result, nil
}

func normalizeStep(step Step, initial []Target, index int) (Step, error) {
	path := fmt.Sprintf("steps[%d]", index)
	interval := initial != nil
	targets, err := normalizeTargets(step.Targets, interval, path+".targets")
	if err != nil {
		return Step{}, err
	}
	step.Targets = targets
	if step.FadeMS < 0 || step.FadeMS > MaxFadeMS || step.FadeMS%FadeQuantumMS != 0 {
		return Step{}, invalid("%s.fade_ms must be 0 to %d in %d ms units", path, MaxFadeMS, FadeQuantumMS)
	}
	if !interval {
		if step.IntervalMS != nil {
			return Step{}, invalid("%s.interval_ms is only allowed for interval timing", path)
		}
		hold := milliseconds(step.HoldMS)
		if hold < 0 || hold > MaxHoldMS {
			return Step{}, invalid("%s.hold_ms must be 0 to %d", path, MaxHoldMS)
		}
		step.HoldMS = &hold
		return step, nil
	}
	if step.HoldMS != nil {
		return Step{}, invalid("%s.hold_ms is only allowed for serial timing", path)
	}
	if step.IntervalMS == nil {
		return Step{}, invalid("%s.interval_ms is required for interval timing", path)
	}
	spacing := *step.IntervalMS
	if spacing < MinIntervalMS || spacing > MaxIntervalMS {
		return Step{}, invalid("%s.interval_ms must be %d to %d", path, MinIntervalMS, MaxIntervalMS)
	}
	if step.FadeMS > spacing {
		return Step{}, invalid("%s.fade_ms cannot exceed interval_ms", path)
	}
	if len(targets) != len(initial) {
		return Step{}, invalid("%s.targets must match the initial LED count and order", path)
	}
	for i, target := range targets {
		if target.LED != initial[i].LED {
			return Step{}, invalid("%s.targets must match the initial LED count and order", path)
		}
	}
	step.IntervalMS = &spacing
	return step, nil
}

func normalizeTargets(input []Target, physical bool, path string) ([]Target, error) {
	minLED := 0
	if physical {
		minLED = 1
		if len(input) < 1 || len(input) > 2 {
			return nil, invalid("%s must contain one or two physical targets", path)
		}
	} else if len(input) != 1 {
		return nil, invalid("%s must contain exactly one target for serial timing", path)
	}
	result := make([]Target, len(input))
	var seen [3]bool
	for i, target := range input {
		if target.LED < minLED || target.LED > 2 {
			return nil, invalid("%s[%d].led must be %d to 2", path, i, minLED)
		}
		if seen[target.LED] {
			return nil, invalid("%s[%d].led repeats an earlier target", path, i)
		}
		seen[target.LED] = true
		if len(target.Color) != 7 || target.Color[0] != '#' {
			return nil, invalid("%s[%d].color must be #RRGGBB", path, i)
		}
		if _, err := hex.DecodeString(target.Color[1:]); err != nil {
			return nil, invalid("%s[%d].color must be #RRGGBB", path, i)
		}
		target.Color = strings.ToLower(target.Color)
		result[i] = target
	}
	return result, nil
}

// NominalDurationMS gives the planned duration of a normalized sequence,
// including the device update allowance. Native I/O and final cleanup take
// additional time. An interval sequence applies its initial targets once and
// waits for its last target to settle beyond the final interval if necessary.
func (s Sequence) NominalDurationMS() int64 {
	var cycle int64
	if s.Timing == TimingSerial {
		for _, step := range s.Steps {
			cycle += int64(step.FadeMS) + int64(milliseconds(step.HoldMS)) + UpdateAllowanceMS
		}
		return cycle * int64(s.Repeat)
	}
	if len(s.Steps) == 0 {
		return 0
	}
	for _, step := range s.Steps {
		cycle += int64(milliseconds(step.IntervalMS))
	}
	initial := int64(len(s.Initial)) * UpdateAllowanceMS
	last := s.Steps[len(s.Steps)-1]
	tail := int64(len(last.Targets)-1)*TargetWriteSpacingMS + int64(last.FadeMS) + UpdateAllowanceMS - int64(milliseconds(last.IntervalMS))
	if tail < 0 {
		tail = 0
	}
	return initial + cycle*int64(s.Repeat) + tail
}

func milliseconds(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func invalid(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
