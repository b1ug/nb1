package sequence

import (
	"fmt"

	"github.com/b1ug/nb1/internal/jsoninput"
)

type sequenceInput struct {
	Timing   string        `json:"timing"`
	Initial  []targetInput `json:"initial"`
	Steps    []stepInput   `json:"steps"`
	Repeat   *int          `json:"repeat"`
	OnFinish *string       `json:"on_finish"`
}

type stepInput struct {
	Targets    []targetInput `json:"targets"`
	FadeMS     int           `json:"fade_ms"`
	HoldMS     *int          `json:"hold_ms"`
	IntervalMS *int          `json:"interval_ms"`
}

type targetInput struct {
	LED   *int   `json:"led"`
	Color string `json:"color"`
}

// Decode accepts one strict Sequence JSON object and returns its normalized
// playback plan. A target's LED selector is required, including for LED 0.
func Decode(data []byte) (Sequence, error) {
	if len(data) > MaxInputBytes {
		return Sequence{}, invalid("input exceeds 1 MiB")
	}
	if !jsoninput.ValidUnicode(data) {
		return Sequence{}, invalid("input must contain valid Unicode")
	}
	var input sequenceInput
	if err := jsoninput.DecodeObject(data, &input); err != nil {
		return Sequence{}, invalid("%v", err)
	}
	result := Sequence{Timing: input.Timing}
	if input.Repeat != nil {
		if *input.Repeat < 1 {
			return Sequence{}, invalid("repeat must be positive when specified")
		}
		result.Repeat = *input.Repeat
	}
	if input.OnFinish != nil {
		if *input.OnFinish == "" {
			return Sequence{}, invalid("on_finish must be off or hold when specified")
		}
		result.OnFinish = *input.OnFinish
	}
	if len(input.Steps) == 0 || len(input.Steps) > MaxExecutedSteps {
		return Sequence{}, invalid("steps must contain 1 to %d steps", MaxExecutedSteps)
	}
	if input.Initial != nil {
		initial, err := decodeTargets(input.Initial, "initial")
		if err != nil {
			return Sequence{}, err
		}
		result.Initial = initial
	}
	result.Steps = make([]Step, len(input.Steps))
	for i, step := range input.Steps {
		targets, err := decodeTargets(step.Targets, fmt.Sprintf("steps[%d].targets", i))
		if err != nil {
			return Sequence{}, err
		}
		result.Steps[i] = Step{Targets: targets, FadeMS: step.FadeMS, HoldMS: step.HoldMS, IntervalMS: step.IntervalMS}
	}
	return Normalize(result)
}

func decodeTargets(input []targetInput, path string) ([]Target, error) {
	if len(input) > 2 {
		return nil, invalid("%s cannot contain more than 2 targets", path)
	}
	result := make([]Target, len(input))
	for i, target := range input {
		if target.LED == nil {
			return nil, invalid("%s[%d].led is required", path, i)
		}
		result[i] = Target{LED: *target.LED, Color: target.Color}
	}
	return result, nil
}
