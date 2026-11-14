package control

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func invalidFrame(format string, args ...interface{}) error {
	return fmt.Errorf("%w: frame program: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// NormalizeFrameProgram validates and deeply copies a pure experimental frame
// description. It never discovers devices, enables capabilities or submits work.
func NormalizeFrameProgram(input FrameProgram) (FrameProgram, error) {
	if input.Contract != FrameContract {
		return FrameProgram{}, invalidFrame("contract must be %q", FrameContract)
	}
	if len(input.Initial) < 1 || len(input.Initial) > MaxFrameProgramTargets {
		return FrameProgram{}, invalidFrame("initial requires 1–2 targets")
	}
	if len(input.Frames) < 1 || len(input.Frames) > MaxFrameProgramFrames {
		return FrameProgram{}, invalidFrame("requires 1–1024 frames")
	}
	if input.Repeat == 0 {
		input.Repeat = 1
	}
	if input.Repeat < 1 || input.Repeat > MaxFrameProgramRepeat {
		return FrameProgram{}, invalidFrame("repeat must be 1–10000")
	}
	if input.OnFinish == "" {
		input.OnFinish = "off"
	}
	if input.OnFinish != "off" && input.OnFinish != "hold" {
		return FrameProgram{}, invalidFrame("on_finish must be off or hold")
	}
	if err := validateFrameTargets(input.Initial, nil); err != nil {
		return FrameProgram{}, invalidFrame("initial: %v", err)
	}
	for i, frame := range input.Frames {
		if err := validateFrameTargets(frame.Targets, input.Initial); err != nil {
			return FrameProgram{}, invalidFrame("frame %d: %v", i, err)
		}
		if frame.IntervalMS < MinFrameIntervalMS || frame.IntervalMS > MaxFrameIntervalMS {
			return FrameProgram{}, invalidFrame("frame %d interval_ms must be 50–600000", i)
		}
		if frame.FadeMS < 0 || frame.FadeMS > MaxFrameFadeMS || frame.FadeMS%FrameFadeQuantumMS != 0 {
			return FrameProgram{}, invalidFrame("frame %d fade_ms must be 0–600000 in 10ms units", i)
		}
		// Interval has already been bounded; this rounding cannot overflow int,
		// including on 32-bit platforms. No multi-phase trailing fades in v1.
		maxFade := ((frame.IntervalMS + FrameFadeQuantumMS - 1) / FrameFadeQuantumMS) * FrameFadeQuantumMS
		if frame.FadeMS > maxFade {
			return FrameProgram{}, invalidFrame("frame %d fade_ms exceeds rounded interval_ms", i)
		}
	}
	facts, err := frameProgramFacts(input)
	if err != nil {
		return FrameProgram{}, err
	}
	if facts.totalFrames > MaxFrameProgramExecutedFrames || facts.plannedTargetWrites > MaxFrameProgramTargetWrites || facts.storedTargetWrites > MaxFrameProgramTargetWrites {
		return FrameProgram{}, invalidFrame("executed frames or target writes exceed the bounded budget")
	}
	if facts.duration > MaxFrameNominalDurationMS {
		return FrameProgram{}, invalidFrame("nominal duration exceeds 24 hours")
	}
	output := cloneFrameProgram(input)
	for i := range output.Initial {
		output.Initial[i].Color = strings.ToLower(output.Initial[i].Color)
	}
	for i := range output.Frames {
		for j := range output.Frames[i].Targets {
			output.Frames[i].Targets[j].Color = strings.ToLower(output.Frames[i].Targets[j].Color)
		}
	}
	return output, nil
}

func validateFrameTargets(targets, initial []FrameTarget) error {
	if len(targets) < 1 || len(targets) > MaxFrameProgramTargets {
		return fmt.Errorf("requires 1–2 targets")
	}
	if initial != nil && len(targets) != len(initial) {
		return fmt.Errorf("targets must retain the initial LED count and order")
	}
	seen := [3]bool{}
	for i, target := range targets {
		if target.LED < 1 || target.LED > 2 || seen[target.LED] {
			return fmt.Errorf("targets require distinct physical LEDs 1 or 2")
		}
		seen[target.LED] = true
		if initial != nil && target.LED != initial[i].LED {
			return fmt.Errorf("targets must retain the initial LED count and order")
		}
		if len(target.Color) != 7 || target.Color[0] != '#' {
			return fmt.Errorf("target %d color must be #RRGGBB", i)
		}
		if _, err := hex.DecodeString(target.Color[1:]); err != nil {
			return fmt.Errorf("target %d color must be #RRGGBB", i)
		}
	}
	return nil
}

func cloneFrameProgram(input FrameProgram) FrameProgram {
	input.Initial = append([]FrameTarget(nil), input.Initial...)
	input.Frames = append([]Frame(nil), input.Frames...)
	for i := range input.Frames {
		input.Frames[i].Targets = append([]FrameTarget(nil), input.Frames[i].Targets...)
	}
	return input
}
