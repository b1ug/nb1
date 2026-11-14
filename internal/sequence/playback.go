package sequence

// PlaybackContract versions the backend-neutral timing descriptors produced by
// Plan. A plan contains no timers, device handles, USB reports or runtime state.
const PlaybackContract = "nb1-sequence-playback/v1"

// PlaybackPlan expands a finite Sequence into ordered writes and waits. The
// nominal offsets exclude I/O time and timer lateness: they are for previews,
// never deadlines that an executor should catch up with a burst of writes.
//
// The owner must stop autonomous device playback before Initial, serialize all
// device access, and apply Finish after the final wait. Cancellation and error
// handling belong to that owner: in particular an unknown USB write outcome
// must not trigger automatic retries or additional cleanup writes. A plan does
// not assert that I/O, cleanup, readback or optical output succeeded.
type PlaybackPlan struct {
	Contract          string           `json:"contract"`
	Timing            string           `json:"timing"`
	Initial           []PlaybackTarget `json:"initial"`
	Steps             []PlaybackStep   `json:"steps"`
	Finish            []PlaybackTarget `json:"finish"`
	NominalDurationMS int64            `json:"nominal_duration_ms"`
	TotalSteps        int              `json:"total_steps"`
	TotalTargetWrites int              `json:"total_target_writes"`
	OnFinish          string           `json:"on_finish"`
}

// PlaybackTarget is one ordered device fade. WaitBeforeMS starts when the
// previous target's write returns; WaitAfterMS starts when this write returns.
// Initial targets use WaitAfterMS, including the final initial target. Step
// targets use WaitBeforeMS to preserve spacing between physical LED writes.
type PlaybackTarget struct {
	LED          int    `json:"led"`
	Color        string `json:"color"`
	FadeMS       int    `json:"fade_ms"`
	WaitBeforeMS int    `json:"wait_before_ms"`
	WaitAfterMS  int    `json:"wait_after_ms"`
}

// PlaybackStep is one executed Step, with repetition already expanded. Indices
// are zero-based. Serial execution waits WaitAfterMS after the target returns.
//
// Interval execution starts a fresh DeadlineMS budget at the actual start of
// this Step. Every target write must begin and return strictly before that
// deadline; then the executor waits until the deadline. A late write fails the
// run. After a late timer wake-up, an executor may start a fresh interval or fail
// stopped; it must never shorten intervals to catch up a nominal timeline.
//
// Only the final interval Step has SettleAfterMS: after its interval, also wait
// until the final target's actual write return plus SettleAfterMS. This wait can
// overlap the interval. Check cancellation between every write and wait; an
// in-flight native/USB write cannot be cancelled by these descriptors.
type PlaybackStep struct {
	Index           int              `json:"index"`
	RepeatIndex     int              `json:"repeat_index"`
	StepIndex       int              `json:"step_index"`
	NominalOffsetMS int64            `json:"nominal_offset_ms"`
	Targets         []PlaybackTarget `json:"targets"`
	WaitAfterMS     int              `json:"wait_after_ms"`
	DeadlineMS      int              `json:"deadline_ms"`
	SettleAfterMS   int              `json:"settle_after_ms"`
}

// Plan validates input before producing any descriptors. It does not modify or
// retain mutable fields of input. Sequence limits bound the result to 1024
// Steps and at most 2051 target writes (two initial, two per Step, one off),
// excluding the owner's preflight. Initial is applied only once. Nominal timing
// matches Sequence previews and excludes Finish, device I/O and timer lateness.
func Plan(input Sequence) (PlaybackPlan, error) {
	normalized, err := Normalize(input)
	if err != nil {
		return PlaybackPlan{}, err
	}
	plan := PlaybackPlan{
		Contract: PlaybackContract, Timing: normalized.Timing,
		Initial:           make([]PlaybackTarget, 0, len(normalized.Initial)),
		Steps:             make([]PlaybackStep, 0, len(normalized.Steps)*normalized.Repeat),
		Finish:            make([]PlaybackTarget, 0, 1),
		NominalDurationMS: normalized.NominalDurationMS(),
		TotalSteps:        len(normalized.Steps) * normalized.Repeat,
		OnFinish:          normalized.OnFinish,
	}
	if normalized.OnFinish == "off" {
		plan.Finish = append(plan.Finish, PlaybackTarget{
			LED: 0, Color: "#000000", WaitAfterMS: UpdateAllowanceMS,
		})
	}
	var offset int64
	for _, target := range normalized.Initial {
		plan.Initial = append(plan.Initial, PlaybackTarget{
			LED: target.LED, Color: target.Color, WaitAfterMS: UpdateAllowanceMS,
		})
		offset += UpdateAllowanceMS
	}
	plan.TotalTargetWrites = len(plan.Initial) + len(plan.Finish)
	for repeat := 0; repeat < normalized.Repeat; repeat++ {
		for index, step := range normalized.Steps {
			entry := PlaybackStep{
				Index: len(plan.Steps), RepeatIndex: repeat, StepIndex: index,
				NominalOffsetMS: offset, Targets: make([]PlaybackTarget, len(step.Targets)),
			}
			for i, target := range step.Targets {
				entry.Targets[i] = PlaybackTarget{LED: target.LED, Color: target.Color, FadeMS: step.FadeMS}
				if i > 0 {
					entry.Targets[i].WaitBeforeMS = TargetWriteSpacingMS
				}
			}
			if normalized.Timing == TimingSerial {
				entry.WaitAfterMS = step.FadeMS + *step.HoldMS + UpdateAllowanceMS
				offset += int64(entry.WaitAfterMS)
			} else {
				entry.DeadlineMS = *step.IntervalMS
				offset += int64(entry.DeadlineMS)
				if entry.Index == plan.TotalSteps-1 {
					entry.SettleAfterMS = step.FadeMS + UpdateAllowanceMS
				}
			}
			plan.TotalTargetWrites += len(entry.Targets)
			plan.Steps = append(plan.Steps, entry)
		}
	}
	return plan, nil
}
