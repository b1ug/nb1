package lightaction

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/control"
)

func object(data []byte) (map[string]json.RawMessage, error) {
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return nil, invalid("input must be valid UTF-8 and at most %d bytes", MaxRequestBytes)
	}
	if !boundedDepth(data) {
		return nil, invalid("JSON nesting exceeds 32 levels")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid("one JSON object is required")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, invalid("invalid object key")
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return nil, invalid("duplicate or invalid field %q", key)
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, invalid("field %q must contain a non-null JSON value", key)
		}
		fields[key] = bytes.TrimSpace(raw)
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid("invalid object end")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid("trailing JSON is not accepted")
	}
	return fields, nil
}

// The JSON decoder remains responsible for syntax. This preliminary scan only
// bounds nesting before Decode materializes a RawMessage, respecting strings.
func boundedDepth(data []byte) bool {
	depth, quoted, escaped := 0, false, false
	for _, c := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 32 {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

func allowed(fields map[string]json.RawMessage, names ...string) error {
	for key := range fields {
		found := false
		for _, name := range names {
			found = found || key == name
		}
		if !found {
			return invalid("unknown or inapplicable field %q", key)
		}
	}
	return nil
}

func stringValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", invalid("a string is required")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", invalid("invalid string")
	}
	return value, nil
}

func number(fields map[string]json.RawMessage, name string, fallback, min, max int) (int, error) {
	raw, exists := fields[name]
	if !exists {
		if fallback < min {
			return 0, invalid("%s is required", name)
		}
		return fallback, nil
	}
	if len(raw) == 0 || len(raw) > 10 || len(raw) > 1 && raw[0] == '0' {
		return 0, invalid("%s requires an ordinary integer", name)
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, invalid("%s requires an ordinary nonnegative integer", name)
		}
	}
	value, err := strconv.Atoi(string(raw))
	if err != nil || value < min || value > max {
		return 0, invalid("%s must be within %d..%d", name, min, max)
	}
	return value, nil
}

func decodeAction(data []byte) (NormalizedAction, error) {
	fields, err := object(data)
	if err != nil {
		return NormalizedAction{}, err
	}
	kind, err := stringValue(fields["kind"])
	if err != nil {
		return NormalizedAction{}, invalid("kind is required")
	}
	common := []string{"kind", "on_finish"}
	names := append([]string{}, common...)
	switch kind {
	case "set":
		names = append(names, "led", "color")
	case "off":
		names = append(names, "led")
	case "fade":
		names = append(names, "led", "color", "duration_ms")
	case "blink":
		names = append(names, "led", "color", "on_ms", "off_ms", "cycles")
	case "breathe":
		names = append(names, "led", "color", "period_ms", "cycles")
	case "pulse":
		names = append(names, "led", "color", "attack_ms", "decay_ms", "rest_ms", "cycles")
	case "alternate":
		names = append(names, "colors", "leds", "period_ms", "cycles", "transition")
	case "rainbow":
		names = append(names, "led", "period_ms", "cycles")
	default:
		return NormalizedAction{}, invalid("unsupported kind %q", kind)
	}
	if err := allowed(fields, names...); err != nil {
		return NormalizedAction{}, err
	}
	a := NormalizedAction{Kind: kind, OnFinish: "off"}
	basic := kind == "set" || kind == "off" || kind == "fade"
	if basic {
		a.OnFinish = "hold"
	}
	if raw, exists := fields["on_finish"]; exists {
		a.OnFinish, err = stringValue(raw)
		if err != nil || a.OnFinish != "hold" && a.OnFinish != "off" {
			return a, invalid("on_finish must be hold or off")
		}
	}
	if kind != "alternate" {
		led, err := number(fields, "led", 0, 0, 2)
		if err != nil {
			return a, err
		}
		a.LED = integer(led)
	}
	if !basic {
		cycles, err := number(fields, "cycles", 1, 1, control.MaxProgramRepeat)
		if err != nil {
			return a, err
		}
		a.Cycles = integer(cycles)
	}
	if kind != "off" && kind != "rainbow" && kind != "alternate" {
		color, err := colorinput.ResolveJSON(fields["color"])
		if err != nil {
			return a, invalid("color: %v", err)
		}
		a.Color = color.CanonicalColor
	}
	read := func(name string, fallback, min, max int) (*int, error) {
		value, err := number(fields, name, fallback, min, max)
		return integer(value), err
	}
	switch kind {
	case "fade":
		a.DurationMS, err = read("duration_ms", -1, 10, control.MaxFadeMS)
		if err == nil && *a.DurationMS%control.FadeQuantumMS != 0 {
			err = invalid("duration_ms must use 10ms units")
		}
	case "blink":
		a.OnMS, err = read("on_ms", 250, 10, control.MaxHoldMS)
		if err == nil {
			a.OffMS, err = read("off_ms", 250, 10, control.MaxHoldMS)
		}
	case "breathe":
		a.PeriodMS, err = read("period_ms", 2000, 330, control.MaxPulsePeriodMS)
	case "pulse":
		a.AttackMS, err = read("attack_ms", 60, 10, control.MaxFadeMS)
		if err == nil {
			a.DecayMS, err = read("decay_ms", 240, 10, control.MaxFadeMS)
		}
		if err == nil && (*a.AttackMS%control.FadeQuantumMS != 0 || *a.DecayMS%control.FadeQuantumMS != 0) {
			err = invalid("attack_ms and decay_ms must use 10ms units")
		}
		if err == nil {
			a.RestMS, err = read("rest_ms", 700, 0, control.MaxHoldMS)
		}
	case "alternate":
		minimum := 40
		if raw, exists := fields["transition"]; exists {
			a.Transition, err = stringValue(raw)
			if err != nil {
				return a, err
			}
			switch a.Transition {
			case "cut":
			case "linear", "sine", "orbit":
				minimum = flowMinimumPeriodMS
			default:
				return a, invalid("transition must be cut, linear, sine or orbit")
			}
		}
		a.PeriodMS, err = read("period_ms", 2000, minimum, control.MaxPulsePeriodMS)
		if err == nil {
			a.Colors, err = pairColors(fields["colors"])
		}
		if err == nil {
			a.LEDs, err = pairLEDs(fields["leds"])
		}
	case "rainbow":
		a.PeriodMS, err = read("period_ms", 3000, 130, control.MaxPulsePeriodMS)
	}
	return a, err
}

func pairColors(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return []string{"#ff0000", "#0000ff"}, nil
	}
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) != 2 {
		return nil, invalid("colors requires exactly two ColorInput values")
	}
	result := make([]string, 0, 2)
	for _, value := range values {
		color, err := colorinput.ResolveJSON(value)
		if err != nil {
			return nil, invalid("colors: %v", err)
		}
		result = append(result, color.CanonicalColor)
	}
	return result, nil
}

func pairLEDs(raw json.RawMessage) ([]int, error) {
	if raw == nil {
		return []int{1, 2}, nil
	}
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) != 2 {
		return nil, invalid("leds requires [1,2] or [2,1]")
	}
	result := make([]int, 0, 2)
	for _, value := range values {
		led, err := number(map[string]json.RawMessage{"led": value}, "led", -1, 1, 2)
		if err != nil {
			return nil, err
		}
		result = append(result, led)
	}
	if result[0] == result[1] {
		return nil, invalid("leds must select each physical LED once")
	}
	return result, nil
}
