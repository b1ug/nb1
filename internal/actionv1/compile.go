// Package actionv1 compiles a v1 light action to a host sequence.
package actionv1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/sequence"
)

const (
	maxActionBytes = sequence.MaxInputBytes
	black          = "#000000"
)

// FieldError identifies a field relative to the action object.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func invalid(field, message string, args ...interface{}) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(message, args...)}
}

var kindFields = map[string][]string{
	"set":       {"led", "color"},
	"off":       {"led"},
	"fade":      {"led", "color", "duration_ms"},
	"blink":     {"led", "color", "on_ms", "off_ms", "cycles", "on_finish"},
	"breathe":   {"led", "color", "period_ms", "cycles", "on_finish"},
	"pulse":     {"led", "color", "attack_ms", "decay_ms", "rest_ms", "cycles", "on_finish"},
	"alternate": {"leds", "colors", "period_ms", "cycles", "on_finish"},
	"orbit":     {"leds", "colors", "period_ms", "cycles", "on_finish"},
	"rainbow":   {"led", "period_ms", "cycles", "on_finish"},
}

// Compile accepts a single bare Action object. The optional resolver receives
// a validated local color name without its leading @ and returns exact Hex6.
func Compile(raw []byte, resolve func(string) (string, error)) (sequence.Sequence, error) {
	if len(raw) > maxActionBytes || !jsoninput.ValidUnicode(raw) {
		return sequence.Sequence{}, invalid("", "action must be valid UTF-8 and at most %d bytes", maxActionBytes)
	}
	var fields map[string]json.RawMessage
	if err := jsoninput.DecodeObject(raw, &fields); err != nil {
		return sequence.Sequence{}, invalid("", "%v", err)
	}
	var kind string
	if err := json.Unmarshal(fields["kind"], &kind); err != nil || kind == "" {
		return sequence.Sequence{}, invalid("kind", "a kind string is required")
	}
	allowed, known := kindFields[kind]
	if !known {
		return sequence.Sequence{}, invalid("kind", "unknown action %q", kind)
	}
	for key := range fields {
		if key != "kind" && !includes(allowed, key) {
			return sequence.Sequence{}, invalid(key, "field is not accepted for %s", kind)
		}
	}

	result := sequence.Sequence{Timing: sequence.TimingSerial, Repeat: 1, OnFinish: "hold"}
	if includes(allowed, "cycles") {
		cycles, err := integer(fields, "cycles", 1, 1, sequence.MaxExecutedSteps)
		if err != nil {
			return sequence.Sequence{}, err
		}
		result.Repeat, result.OnFinish = cycles, "off"
		if raw, present := fields["on_finish"]; present {
			if err := json.Unmarshal(raw, &result.OnFinish); err != nil || result.OnFinish != "off" && result.OnFinish != "hold" {
				return sequence.Sequence{}, invalid("on_finish", "must be off or hold")
			}
		}
	}
	led := 0
	if includes(allowed, "led") {
		var err error
		led, err = integer(fields, "led", 0, 0, 2)
		if err != nil {
			return sequence.Sequence{}, err
		}
	}
	var color string
	if includes(allowed, "color") {
		var err error
		color, err = ResolveColor(fields["color"], resolve)
		if err != nil {
			return sequence.Sequence{}, invalid("color", "%v", err)
		}
	}

	steps, err := compileSteps(kind, fields, led, color, resolve, &result)
	if err != nil {
		return sequence.Sequence{}, err
	}
	if result.Repeat > sequence.MaxExecutedSteps/len(steps) {
		return sequence.Sequence{}, invalid("cycles", "compiled action exceeds %d executed steps", sequence.MaxExecutedSteps)
	}
	result.Steps = steps
	if result.NominalDurationMS() > sequence.MaxNominalDurationMS {
		return sequence.Sequence{}, invalid("cycles", "compiled action exceeds 24 hours")
	}
	return sequence.Normalize(result)
}

func includes(values []string, value string) bool {
	for _, entry := range values {
		if entry == value {
			return true
		}
	}
	return false
}

func integer(fields map[string]json.RawMessage, name string, fallback, min, max int) (int, error) {
	raw, present := fields[name]
	if !present {
		if fallback < min {
			return 0, invalid(name, "field is required")
		}
		return fallback, nil
	}
	var value int
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' || json.Unmarshal(raw, &value) != nil {
		return 0, invalid(name, "must be a nonnegative integer")
	}
	if value < min || value > max {
		return 0, invalid(name, "must be within %d–%d", min, max)
	}
	return value, nil
}

func fadeTime(fields map[string]json.RawMessage, name string, fallback int) (int, error) {
	value, err := integer(fields, name, fallback, sequence.FadeQuantumMS, sequence.MaxFadeMS)
	if err == nil && value%sequence.FadeQuantumMS != 0 {
		return 0, invalid(name, "must use %d ms units", sequence.FadeQuantumMS)
	}
	return value, err
}

// ResolveColor normalizes a v1 ColorInput without touching a device.
func ResolveColor(raw []byte, resolve func(string) (string, error)) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("color is required")
	}
	if len(raw) > colorinput.MaxRequestBytes || !jsoninput.ValidUnicode(raw) {
		return "", fmt.Errorf("color must be valid UTF-8 and at most %d bytes", colorinput.MaxRequestBytes)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && strings.HasPrefix(text, "@") {
		name := text[1:]
		if !validAlias(name) {
			return "", fmt.Errorf("color alias must begin with a lowercase ASCII letter and contain 1–32 letters, digits or hyphens")
		}
		if resolve == nil {
			return "", fmt.Errorf("unknown color alias %q", text)
		}
		color, err := resolve(name)
		if err != nil {
			return "", err
		}
		if len(color) != 7 || color[0] != '#' {
			return "", fmt.Errorf("color alias %q must resolve to exact Hex6", text)
		}
		raw, _ = json.Marshal(color)
	}
	result, err := colorinput.ResolveJSON(raw)
	if err != nil {
		return "", err
	}
	return result.CanonicalColor, nil
}

func validAlias(name string) bool {
	if len(name) < 1 || len(name) > 32 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func pair(fields map[string]json.RawMessage, resolve func(string) (string, error)) ([2]sequence.Target, error) {
	var values []json.RawMessage
	if json.Unmarshal(fields["colors"], &values) != nil || len(values) != 2 {
		return [2]sequence.Target{}, invalid("colors", "exactly two colors are required")
	}
	leds := []int{1, 2}
	if raw, present := fields["leds"]; present {
		if json.Unmarshal(raw, &leds) != nil || len(leds) != 2 ||
			!(leds[0] == 1 && leds[1] == 2 || leds[0] == 2 && leds[1] == 1) {
			return [2]sequence.Target{}, invalid("leds", "must be [1,2] or [2,1]")
		}
	}
	var targets [2]sequence.Target
	for i, value := range values {
		color, err := ResolveColor(value, resolve)
		if err != nil {
			return targets, invalid(fmt.Sprintf("colors[%d]", i), "%v", err)
		}
		targets[i] = sequence.Target{LED: leds[i], Color: color}
	}
	return targets, nil
}
