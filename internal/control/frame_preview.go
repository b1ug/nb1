package control

type frameFacts struct {
	totalFrames, storedTargetWrites, plannedTargetWrites int64
	cycle, initial, tail, duration                       int64
}

const frameMaxInt64 = int64(^uint64(0) >> 1)

func checkedFrameAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > frameMaxInt64-b {
		return 0, invalidFrame("nonnegative integer addition overflow")
	}
	return a + b, nil
}

func checkedFrameMultiply(a, b int64) (int64, error) {
	if a < 0 || b < 0 || (b != 0 && a > frameMaxInt64/b) {
		return 0, invalidFrame("nonnegative integer multiplication overflow")
	}
	return a * b, nil
}

// frameProgramFacts operates on structurally validated programs. Keep all
// accumulated counts and durations in checked int64 before converting counts.
func frameProgramFacts(p FrameProgram) (frameFacts, error) {
	var f frameFacts
	var err error
	if f.totalFrames, err = checkedFrameMultiply(int64(len(p.Frames)), int64(p.Repeat)); err != nil {
		return frameFacts{}, err
	}
	var cycleWrites int64
	for _, frame := range p.Frames {
		if f.cycle, err = checkedFrameAdd(f.cycle, int64(frame.IntervalMS)); err != nil {
			return frameFacts{}, err
		}
		if cycleWrites, err = checkedFrameAdd(cycleWrites, int64(len(frame.Targets))); err != nil {
			return frameFacts{}, err
		}
	}
	if f.storedTargetWrites, err = checkedFrameAdd(int64(len(p.Initial)), cycleWrites); err != nil {
		return frameFacts{}, err
	}
	if f.plannedTargetWrites, err = checkedFrameMultiply(cycleWrites, int64(p.Repeat)); err != nil {
		return frameFacts{}, err
	}
	if f.plannedTargetWrites, err = checkedFrameAdd(f.plannedTargetWrites, int64(len(p.Initial))); err != nil {
		return frameFacts{}, err
	}
	if f.initial, err = checkedFrameMultiply(int64(len(p.Initial)), FrameUpdateAllowanceMS); err != nil {
		return frameFacts{}, err
	}
	last := p.Frames[len(p.Frames)-1]
	tail := int64(len(last.Targets)-1)*FrameWriteSpacingMS + int64(last.FadeMS) + FrameUpdateAllowanceMS - int64(last.IntervalMS)
	if tail > 0 {
		f.tail = tail
	}
	if f.duration, err = checkedFrameMultiply(f.cycle, int64(p.Repeat)); err != nil {
		return frameFacts{}, err
	}
	if f.duration, err = checkedFrameAdd(f.duration, f.initial); err != nil {
		return frameFacts{}, err
	}
	if f.duration, err = checkedFrameAdd(f.duration, f.tail); err != nil {
		return frameFacts{}, err
	}
	return f, nil
}

// PreviewFrameProgram describes a validated experimental schedule only. It does not
// select an Engine or device, so execution availability remains unchecked.
// Actual tail waiting must use last native Set return times, not this nominal
// arithmetic. Native I/O, timer lateness, readback and cleanup add wall time.
func PreviewFrameProgram(input FrameProgram) (FramePreview, error) {
	p, err := NormalizeFrameProgram(input)
	if err != nil {
		return FramePreview{}, err
	}
	f, err := frameProgramFacts(p)
	if err != nil {
		return FramePreview{}, err
	}
	required := []string{"set", "raw-rgb", FrameExecutorCapability}
	if p.OnFinish == "hold" {
		required = append(required, "hold")
	}
	return FramePreview{
		Contract: FrameContract, Program: p,
		TotalFrames: int(f.totalFrames), StoredTargetWrites: int(f.storedTargetWrites), PlannedTargetWrites: int(f.plannedTargetWrites),
		CycleDurationMS: f.cycle, NominalInitialMS: f.initial, NominalTailMS: f.tail, NominalDurationMS: f.duration,
		TimingBasis: "host-nominal-excludes-native-io", RequiredCapabilities: required,
		Execution: FrameExecutionAvailability{Available: false, Reason: "execution_not_checked"},
	}, nil
}
