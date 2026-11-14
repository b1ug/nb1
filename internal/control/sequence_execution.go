package control

import (
	"fmt"

	"github.com/b1ug/nb1/internal/sequence"
)

// SequenceRequiredCapabilities describes the existing execution path selected
// by a normalized sequence.
func SequenceRequiredCapabilities(input sequence.Sequence) []string {
	required := []string{"set", "program", "raw-rgb"}
	if input.Timing == sequence.TimingInterval {
		required = []string{"set", "raw-rgb", "mk2-basic"}
	}
	if input.OnFinish == "hold" {
		required = append(required, "hold")
	}
	return required
}

// CheckSequenceDevice checks the cached connection and profile. Admission and
// native execution still belong to the device worker.
func CheckSequenceDevice(input sequence.Sequence, state DeviceState) error {
	if state.Connection == "busy" {
		return ErrBusy
	}
	if state.Connection != "online" {
		return ErrOffline
	}
	available := make(map[string]bool, len(state.Capabilities))
	for _, capability := range state.Capabilities {
		available[capability] = true
	}
	for _, required := range SequenceRequiredCapabilities(input) {
		if !available[required] {
			return fmt.Errorf("%w: %s", ErrSequenceCapability, required)
		}
	}
	return nil
}

func prepareSequenceJob(prepare func() (sequence.Sequence, error)) (sequence.Sequence, *record, error) {
	if prepare == nil {
		return sequence.Sequence{}, nil, fmt.Errorf("%w: job requires a sequence", ErrInvalid)
	}
	input, err := prepare()
	if err != nil {
		return sequence.Sequence{}, nil, err
	}
	input, err = sequence.Normalize(input)
	if err != nil {
		return sequence.Sequence{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	candidate := &record{origin: originSequenceJob, cancel: make(chan struct{})}
	if input.Timing == sequence.TimingSerial {
		program := Program{Steps: make([]Step, len(input.Steps)), Repeat: input.Repeat, OnFinish: input.OnFinish}
		for i, step := range input.Steps {
			target := step.Targets[0]
			program.Steps[i] = Step{LED: target.LED, Color: target.Color, FadeMS: step.FadeMS, HoldMS: *step.HoldMS}
		}
		candidate.program = &program
		candidate.totalSteps = len(program.Steps) * program.Repeat
		return input, candidate, nil
	}
	frames := FrameProgram{Contract: FrameContract, Initial: sequenceFrameTargets(input.Initial),
		Frames: make([]Frame, len(input.Steps)), Repeat: input.Repeat, OnFinish: input.OnFinish}
	for i, step := range input.Steps {
		frames.Frames[i] = Frame{Targets: sequenceFrameTargets(step.Targets), FadeMS: step.FadeMS, IntervalMS: *step.IntervalMS}
	}
	facts, err := frameProgramFacts(frames)
	if err != nil {
		return sequence.Sequence{}, nil, err
	}
	candidate.kind = recordFrames
	candidate.frames = &frames
	candidate.totalFrames = int(facts.totalFrames)
	candidate.plannedTargetWrites = int(facts.plannedTargetWrites)
	return input, candidate, nil
}

func sequenceFrameTargets(input []sequence.Target) []FrameTarget {
	targets := make([]FrameTarget, len(input))
	for i, target := range input {
		targets[i] = FrameTarget{LED: target.LED, Color: target.Color}
	}
	return targets
}
