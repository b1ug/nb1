package control

// FrameContract is an experimental authoring contract with an opt-in
// FrameService executor. The legacy Service and its capability catalog remain
// unchanged. Physical qualification precedes any stable contract release.
const FrameContract = "nb1-frame-program/experimental-v1"

const (
	FrameExecutorCapability       = "frame-program-experimental-v1"
	MaxFrameProgramFrames         = 1024
	MaxFrameProgramRepeat         = 10000
	MaxFrameProgramExecutedFrames = 1024
	MaxFrameProgramTargetWrites   = 2050 // two initial + two per executed frame
	MaxFrameProgramTargets        = 2
	MinFrameIntervalMS            = 50
	MaxFrameIntervalMS            = 600000
	MaxFrameFadeMS                = 600000
	FrameFadeQuantumMS            = 10
	FrameWriteSpacingMS           = 10
	FrameUpdateAllowanceMS        = 10
	MaxFrameProgramBytes          = 1 << 20
	MaxFrameNominalDurationMS     = int64(86400000)
)

// FrameTarget is an exact raw RGB target for one physical LED. Ordered targets
// would be sent sequentially; this type does not promise an atomic USB write.
type FrameTarget struct {
	LED   int    `json:"led"`
	Color string `json:"color"`
}

// Frame describes overlapping device fades followed by one host interval.
// The executor must wait only the remainder of IntervalMS after writes,
// not FadeMS separately for each target. Device qualification remains separate
// from these host-side scheduling semantics.
type Frame struct {
	Targets    []FrameTarget `json:"targets"`
	FadeMS     int           `json:"fade_ms"`
	IntervalMS int           `json:"interval_ms"`
}

// FrameProgram is distinct from the unchanged sequential Program contract.
// Initial zero-time targets apply once; Repeat repeats Frames only. The final
// device fade must settle before OnFinish; native I/O is not included in nominal
// preview duration and cannot be cancelled by a host timer.
type FrameProgram struct {
	Contract string        `json:"contract"`
	Initial  []FrameTarget `json:"initial"`
	Frames   []Frame       `json:"frames"`
	Repeat   int           `json:"repeat"`
	OnFinish string        `json:"on_finish"`
}

// FrameExecutionAvailability prevents a pure preview from claiming a working
// frame executor or any physical device qualification.
type FrameExecutionAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type FramePreview struct {
	Contract             string                     `json:"contract"`
	Program              FrameProgram               `json:"program"`
	TotalFrames          int                        `json:"total_frames"`
	StoredTargetWrites   int                        `json:"stored_target_writes"`
	PlannedTargetWrites  int                        `json:"planned_target_writes"`
	CycleDurationMS      int64                      `json:"cycle_duration_ms"`
	NominalInitialMS     int64                      `json:"nominal_initial_ms"`
	NominalTailMS        int64                      `json:"nominal_tail_ms"`
	NominalDurationMS    int64                      `json:"nominal_duration_ms"`
	TimingBasis          string                     `json:"timing_basis"`
	RequiredCapabilities []string                   `json:"required_capabilities"`
	Execution            FrameExecutionAvailability `json:"execution"`
}
