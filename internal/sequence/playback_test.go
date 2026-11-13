package sequence

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestPlanSerialWaitsFromWriteReturnAndExpandsRepeat(t *testing.T) {
	input, err := Decode([]byte(`{"timing":"serial","steps":[{"targets":[{"led":1,"color":"#FF0000"}],"fade_ms":20,"hold_ms":30},{"targets":[{"led":0,"color":"#0000FF"}]}],"repeat":2,"on_finish":"hold"}`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Contract != PlaybackContract || plan.TotalSteps != 4 || plan.TotalTargetWrites != 4 || plan.NominalDurationMS != 140 {
		t.Fatalf("unexpected plan facts: %+v", plan)
	}
	if len(plan.Initial) != 0 || len(plan.Finish) != 0 || plan.OnFinish != "hold" {
		t.Fatalf("hold plan must not add initial or cleanup writes: %+v", plan)
	}
	for i, want := range []struct {
		offset int64
		wait   int
		led    int
		color  string
		fade   int
	}{{0, 60, 1, "#ff0000", 20}, {60, 10, 0, "#0000ff", 0}, {70, 60, 1, "#ff0000", 20}, {130, 10, 0, "#0000ff", 0}} {
		step := plan.Steps[i]
		if step.Index != i || step.RepeatIndex != i/2 || step.StepIndex != i%2 || step.NominalOffsetMS != want.offset || step.WaitAfterMS != want.wait || step.DeadlineMS != 0 || step.SettleAfterMS != 0 {
			t.Errorf("serial step %d = %+v", i, step)
		}
		if len(step.Targets) != 1 || step.Targets[0] != (PlaybackTarget{LED: want.led, Color: want.color, FadeMS: want.fade}) {
			t.Errorf("serial target %d = %+v", i, step.Targets)
		}
	}
}

func TestPlanIntervalInitialOnceOrderedWritesAndOnlyFinalSettle(t *testing.T) {
	input, err := Decode([]byte(`{"timing":"interval","initial":[{"led":2,"color":"#FF0000"},{"led":1,"color":"#0000FF"}],"steps":[{"targets":[{"led":2,"color":"#0000ff"},{"led":1,"color":"#ff0000"}],"fade_ms":50,"interval_ms":50},{"targets":[{"led":2,"color":"#ff0000"},{"led":1,"color":"#0000ff"}],"fade_ms":90,"interval_ms":100}],"repeat":2}`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalSteps != 4 || plan.TotalTargetWrites != 11 || plan.NominalDurationMS != 330 {
		t.Fatalf("unexpected interval facts: %+v", plan)
	}
	if !reflect.DeepEqual(plan.Initial, []PlaybackTarget{
		{LED: 2, Color: "#ff0000", WaitAfterMS: 10},
		{LED: 1, Color: "#0000ff", WaitAfterMS: 10},
	}) {
		t.Fatalf("initial targets must retain order and wait after the last write too: %+v", plan.Initial)
	}
	for i, offset := range []int64{20, 70, 170, 220} {
		step := plan.Steps[i]
		deadline, fade, settle := 50, 50, 0
		if i%2 == 1 {
			deadline, fade = 100, 90
		}
		if i == 3 {
			settle = 100
		}
		if step.Index != i || step.RepeatIndex != i/2 || step.StepIndex != i%2 || step.NominalOffsetMS != offset || step.WaitAfterMS != 0 || step.DeadlineMS != deadline || step.SettleAfterMS != settle {
			t.Errorf("interval step %d = %+v", i, step)
		}
		if len(step.Targets) != 2 || step.Targets[0].LED != 2 || step.Targets[1].LED != 1 || step.Targets[0].WaitBeforeMS != 0 || step.Targets[1].WaitBeforeMS != 10 || step.Targets[0].FadeMS != fade || step.Targets[1].FadeMS != fade || step.Targets[0].WaitAfterMS != 0 || step.Targets[1].WaitAfterMS != 0 {
			t.Errorf("interval target ordering/spacing %d = %+v", i, step.Targets)
		}
	}
	// The final settle deadline is relative to the actual last write return;
	// there is no extra serial wait for every fade or at repetition boundaries.
	if !reflect.DeepEqual(plan.Finish, []PlaybackTarget{{LED: 0, Color: "#000000", WaitAfterMS: 10}}) || plan.OnFinish != "off" {
		t.Fatalf("normal off cleanup = %+v", plan.Finish)
	}
}

func TestPlanOneLEDIntervalTailAndOffAreSeparate(t *testing.T) {
	ms := 50
	plan, err := Plan(Sequence{
		Timing: TimingInterval, Initial: []Target{{LED: 1, Color: "#000000"}},
		Steps: []Step{{Targets: []Target{{LED: 1, Color: "#ffffff"}}, FadeMS: 50, IntervalMS: &ms}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 10 initial + 50 interval + 10 settle, excluding the final off update wait.
	if plan.NominalDurationMS != 70 || plan.Steps[0].SettleAfterMS != 60 || plan.TotalTargetWrites != 3 || plan.Finish[0].WaitAfterMS != 10 {
		t.Fatalf("single-LED interval plan = %+v", plan)
	}
}

func TestPlanIsIndependentOfInputAndRepeatedTargets(t *testing.T) {
	input, err := Decode([]byte(intervalJSON))
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	plan.Initial[0].Color = "#123456"
	plan.Steps[0].Targets[0].Color = "#abcdef"
	plan.Finish[0].Color = "#fedcba"
	if plan.Steps[2].Targets[0].Color != "#0000ff" {
		t.Fatal("expanded repetitions share mutable targets")
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("Plan modified or retained mutable input: before %s after %s", before, after)
	}
	input.Initial[1].Color = "#123456"
	input.Steps[1].Targets[1].Color = "#123456"
	if plan.Initial[1].Color != "#0000ff" || plan.Steps[1].Targets[1].Color != "#0000ff" {
		t.Fatal("plan aliases input targets")
	}
}

func TestPlanValidatesBeforeReturningAndHasBoundedWrites(t *testing.T) {
	ms := 50
	input := Sequence{
		Timing:  TimingInterval,
		Initial: []Target{{LED: 1, Color: "#000000"}, {LED: 2, Color: "#000000"}},
		Steps:   []Step{{Targets: []Target{{LED: 1, Color: "#ffffff"}, {LED: 2, Color: "#ffffff"}}, FadeMS: 50, IntervalMS: &ms}},
		Repeat:  MaxExecutedSteps,
	}
	plan, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != MaxExecutedSteps || plan.TotalTargetWrites != 2051 || plan.NominalDurationMS != 51240 {
		t.Fatalf("largest interval plan facts = %+v", plan)
	}
	input.Repeat++
	invalid, err := Plan(input)
	if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(invalid, PlaybackPlan{}) {
		t.Fatalf("invalid repeat returned partial plan: %+v, %v", invalid, err)
	}
	input.Repeat = 1
	input.Steps[0].FadeMS = 60
	if _, err := Plan(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("interval fade above deadline accepted: %v", err)
	}
}

func TestPlanJSONUsesExplicitArraysAndCurrentContract(t *testing.T) {
	input, err := Decode([]byte(`{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#ff0000"}]}],"on_finish":"hold"}`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"contract":"nb1-sequence-playback/v1","timing":"serial","initial":[],"steps":[{"index":0,"repeat_index":0,"step_index":0,"nominal_offset_ms":0,"targets":[{"led":0,"color":"#ff0000","fade_ms":0,"wait_before_ms":0,"wait_after_ms":0}],"wait_after_ms":10,"deadline_ms":0,"settle_after_ms":0}],"finish":[],"nominal_duration_ms":10,"total_steps":1,"total_target_writes":1,"on_finish":"hold"}`
	if string(encoded) != want {
		t.Fatalf("plan wire format = %s, want %s", encoded, want)
	}
}
