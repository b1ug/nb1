package playback

import (
	"testing"
	"time"
)

func TestPolicyDefaultsAndModes(t *testing.T) {
	policy, err := Defaults().Normalize()
	if err != nil || policy.Mode != ModeAlways || policy.HistoryLimit != DefaultHistoryLimit || policy.Timezone != "local" {
		t.Fatalf("defaults=%+v err=%v", policy, err)
	}
	for _, test := range []struct {
		mode string
		want bool
	}{
		{ModeAlways, true}, {ModeNever, false},
	} {
		policy.Mode = test.mode
		allowed, _ := policy.Allows(time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC))
		if allowed != test.want {
			t.Errorf("mode %s allowed=%t want %t", test.mode, allowed, test.want)
		}
	}
}

func TestWeeklyWindowsAndExplicitOvernight(t *testing.T) {
	policy := Policy{Mode: ModeWeekly, Timezone: "UTC", HistoryLimit: 10, Weekly: map[string]Window{
		"mon": {Start: "08:00", End: "20:00"},
		"fri": {Start: "22:00", End: "02:00", Overnight: true},
	}}
	var err error
	policy, err = policy.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"start included", time.Date(2026, time.January, 5, 8, 0, 0, 0, time.UTC), true}, // Monday
		{"end excluded", time.Date(2026, time.January, 5, 20, 0, 0, 0, time.UTC), false},
		{"unlisted day closed", time.Date(2026, time.January, 6, 12, 0, 0, 0, time.UTC), false},
		{"overnight start", time.Date(2026, time.January, 9, 23, 0, 0, 0, time.UTC), true}, // Friday
		{"overnight next day", time.Date(2026, time.January, 10, 1, 59, 0, 0, time.UTC), true},
		{"overnight end excluded", time.Date(2026, time.January, 10, 2, 0, 0, 0, time.UTC), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, _ := policy.Allows(test.at)
			if allowed != test.want {
				t.Fatalf("at %s allowed=%t want %t", test.at, allowed, test.want)
			}
		})
	}
}

func TestPolicyValidationRejectsAmbiguousWindows(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy Policy
	}{
		{"unknown mode", Policy{Mode: "sometimes"}},
		{"unknown weekday", Policy{Mode: ModeWeekly, Weekly: map[string]Window{"monday": {Start: "08:00", End: "20:00"}}}},
		{"invalid clock", Policy{Mode: ModeWeekly, Weekly: map[string]Window{"mon": {Start: "8am", End: "20:00"}}}},
		{"implicit overnight", Policy{Mode: ModeWeekly, Weekly: map[string]Window{"mon": {Start: "22:00", End: "02:00"}}}},
		{"wrong overnight direction", Policy{Mode: ModeWeekly, Weekly: map[string]Window{"mon": {Start: "08:00", End: "20:00", Overnight: true}}}},
		{"empty interval", Policy{Mode: ModeWeekly, Weekly: map[string]Window{"mon": {Start: "08:00", End: "08:00"}}}},
		{"invalid timezone", Policy{Mode: ModeWeekly, Timezone: "Mars/Olympus"}},
		{"invalid history limit", Policy{Mode: ModeAlways, HistoryLimit: MaxHistoryLimit + 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.policy.Normalize(); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}
