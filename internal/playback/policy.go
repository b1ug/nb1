// Package playback defines the daemon's persistent playback window policy.
// Runtime overrides are deliberately kept by the daemon Engine instead.
package playback

import (
	"fmt"
	"time"
	_ "time/tzdata"
)

const (
	ModeAlways = "always"
	ModeWeekly = "weekly"
	ModeNever  = "never"

	DefaultHistoryLimit = 100
	MaxHistoryLimit     = 10000
)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// Window is one local-time allow window for one weekday. Overnight must be
// explicitly true when End is on the following day.
type Window struct {
	Start     string `json:"start" yaml:"start"`
	End       string `json:"end" yaml:"end"`
	Overnight bool   `json:"overnight,omitempty" yaml:"overnight,omitempty"`
}

// Policy is immutable after Engine startup. Weekly days omitted from the map
// are closed. A local timezone follows the host's configured local zone.
type Policy struct {
	Mode         string            `json:"mode" yaml:"mode"`
	Timezone     string            `json:"timezone" yaml:"timezone"`
	Weekly       map[string]Window `json:"weekly,omitempty" yaml:"weekly,omitempty"`
	HistoryLimit int               `json:"history_limit" yaml:"history_limit"`
}

func Defaults() Policy {
	return Policy{Mode: ModeAlways, Timezone: "local", Weekly: map[string]Window{}, HistoryLimit: DefaultHistoryLimit}
}

func (p Policy) Normalize() (Policy, error) {
	if p.Mode == "" {
		p.Mode = ModeAlways
	}
	if p.Timezone == "" {
		p.Timezone = "local"
	}
	if p.HistoryLimit == 0 {
		p.HistoryLimit = DefaultHistoryLimit
	}
	if p.Weekly == nil {
		p.Weekly = map[string]Window{}
	}
	if p.Mode != ModeAlways && p.Mode != ModeWeekly && p.Mode != ModeNever {
		return Policy{}, fmt.Errorf("playback.mode must be always, weekly or never")
	}
	if p.HistoryLimit < 1 || p.HistoryLimit > MaxHistoryLimit {
		return Policy{}, fmt.Errorf("playback.history_limit must be between 1 and %d", MaxHistoryLimit)
	}
	if p.Timezone != "local" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return Policy{}, fmt.Errorf("playback.timezone must be local or a valid IANA timezone: %w", err)
		}
	}
	for day, window := range p.Weekly {
		if _, ok := weekdays[day]; !ok {
			return Policy{}, fmt.Errorf("playback.weekly.%s is not a weekday (use mon, tue, wed, thu, fri, sat or sun)", day)
		}
		start, err := parseClock(window.Start)
		if err != nil {
			return Policy{}, fmt.Errorf("playback.weekly.%s.start: %w", day, err)
		}
		end, err := parseClock(window.End)
		if err != nil {
			return Policy{}, fmt.Errorf("playback.weekly.%s.end: %w", day, err)
		}
		if window.Overnight {
			if end >= start {
				return Policy{}, fmt.Errorf("playback.weekly.%s overnight window must end before its start", day)
			}
		} else if end <= start {
			return Policy{}, fmt.Errorf("playback.weekly.%s end must be after start; set overnight: true to cross midnight", day)
		}
	}
	return p, nil
}

func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, fmt.Errorf("must use 24-hour HH:MM format")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func (p Policy) location() *time.Location {
	if p.Timezone == "local" || p.Timezone == "" {
		return time.Local
	}
	location, err := time.LoadLocation(p.Timezone)
	if err != nil { // Normalize rejects this; keep evaluation safe for zero-value callers.
		return time.Local
	}
	return location
}

// Allows reports whether a new playback request is within the persistent
// policy. Override precedence is applied by the owning Engine.
func (p Policy) Allows(at time.Time) (bool, string) {
	if p.Mode == "" || p.Mode == ModeAlways {
		return true, "mode_always"
	}
	if p.Mode == ModeNever {
		return false, "mode_never"
	}
	local := at.In(p.location())
	minute := local.Hour()*60 + local.Minute()
	if window, ok := p.Weekly[weekdayName(local.Weekday())]; ok {
		start, _ := parseClock(window.Start)
		end, _ := parseClock(window.End)
		if minute >= start && (window.Overnight || minute < end) {
			return true, "weekly_window"
		}
	}
	// The same local clock time may not exist on the previous calendar day.
	previousDay := (local.Weekday() + 6) % 7
	if window, ok := p.Weekly[weekdayName(previousDay)]; ok && window.Overnight {
		end, _ := parseClock(window.End)
		if minute < end {
			return true, "weekly_overnight_window"
		}
	}
	return false, "outside_weekly_window"
}

func weekdayName(day time.Weekday) string {
	for name, value := range weekdays {
		if value == day {
			return name
		}
	}
	return ""
}
