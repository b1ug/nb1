package control

import (
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/jsoninput"
)

// Pointers distinguish required zero fade from a missing field, and omitted
// defaults from explicit invalid zero repeat or empty on_finish values.
type frameTargetInput struct {
	LED   *int    `json:"led"`
	Color *string `json:"color"`
}
type frameInput struct {
	Targets    []frameTargetInput `json:"targets"`
	FadeMS     *int               `json:"fade_ms"`
	IntervalMS *int               `json:"interval_ms"`
}
type frameProgramInput struct {
	Contract *string            `json:"contract"`
	Initial  []frameTargetInput `json:"initial"`
	Frames   []frameInput       `json:"frames"`
	Repeat   *int               `json:"repeat"`
	OnFinish *string            `json:"on_finish"`
}

// DecodeFrameProgram accepts one strict JSON description and returns a fresh
// normalized copy. It performs no I/O and is not registered with any transport.
func DecodeFrameProgram(data []byte) (FrameProgram, error) {
	if len(data) > MaxFrameProgramBytes {
		return FrameProgram{}, invalidFrame("input exceeds 1 MiB")
	}
	if !utf8.Valid(data) {
		return FrameProgram{}, invalidFrame("input must be valid UTF-8")
	}
	var wire frameProgramInput
	if err := jsoninput.DecodeObject(data, &wire); err != nil {
		return FrameProgram{}, invalidFrame("%v", err)
	}
	if wire.Contract == nil {
		return FrameProgram{}, invalidFrame("contract is required")
	}
	if wire.Repeat != nil && *wire.Repeat == 0 {
		return FrameProgram{}, invalidFrame("explicit repeat must be positive")
	}
	if wire.OnFinish != nil && *wire.OnFinish == "" {
		return FrameProgram{}, invalidFrame("explicit on_finish must be off or hold")
	}
	// Bound slices before converting the wire representation to public types.
	if len(wire.Initial) < 1 || len(wire.Initial) > MaxFrameProgramTargets || len(wire.Frames) < 1 || len(wire.Frames) > MaxFrameProgramFrames {
		return FrameProgram{}, invalidFrame("invalid initial target or stored frame count")
	}
	result := FrameProgram{Contract: *wire.Contract}
	if wire.Repeat != nil {
		result.Repeat = *wire.Repeat
	}
	if wire.OnFinish != nil {
		result.OnFinish = *wire.OnFinish
	}
	var err error
	if result.Initial, err = decodeFrameTargets(wire.Initial); err != nil {
		return FrameProgram{}, err
	}
	result.Frames = make([]Frame, len(wire.Frames))
	for i, item := range wire.Frames {
		if item.FadeMS == nil || item.IntervalMS == nil {
			return FrameProgram{}, invalidFrame("frame %d requires fade_ms and interval_ms", i)
		}
		targets, err := decodeFrameTargets(item.Targets)
		if err != nil {
			return FrameProgram{}, err
		}
		result.Frames[i] = Frame{Targets: targets, FadeMS: *item.FadeMS, IntervalMS: *item.IntervalMS}
	}
	return NormalizeFrameProgram(result)
}

func decodeFrameTargets(input []frameTargetInput) ([]FrameTarget, error) {
	if len(input) < 1 || len(input) > MaxFrameProgramTargets {
		return nil, invalidFrame("requires 1–2 targets")
	}
	result := make([]FrameTarget, len(input))
	for i, item := range input {
		if item.LED == nil || item.Color == nil {
			return nil, invalidFrame("target %d requires led and color", i)
		}
		result[i] = FrameTarget{LED: *item.LED, Color: *item.Color}
	}
	return result, nil
}
