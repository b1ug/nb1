package lightactionv2

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/jsoninput"
)

type actionInput struct {
	Kind     *string   `json:"kind"`
	Colors   *[]string `json:"colors"`
	LEDs     *[]int    `json:"leds"`
	PeriodMS *int      `json:"period_ms"`
	Cycles   *int      `json:"cycles"`
	Segments *int      `json:"segments"`
	OnFinish *string   `json:"on_finish"`
}

// CompileRequest requires the new envelope exactly; a v1 request is not
// silently upgraded and an unknown action is never converted to Orbit.
func CompileRequest(data []byte) (Result, error) {
	fields, err := rawObject(data)
	if err != nil {
		return Result{}, err
	}
	if len(fields) != 2 || fields["contract"] == nil || fields["action"] == nil {
		return Result{}, invalid("request requires exactly contract and action")
	}
	var contract string
	if err := json.Unmarshal(fields["contract"], &contract); err != nil || contract != Contract {
		return Result{}, invalid("request requires contract %q", Contract)
	}
	return CompileJSON(fields["action"])
}

func decodeAction(data []byte) (Action, error) {
	fields, err := rawObject(data)
	if err != nil {
		return Action{}, err
	}
	// jsoninput validates the typed action. Its reflection checker does not
	// accept a string/object union in json.RawMessage, so resolve each color
	// using the strict, versioned color parser first. All other original JSON
	// values (including numeric spelling) are preserved for typed validation.
	if raw, present := fields["colors"]; present {
		var values []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || len(values) != 2 {
			return Action{}, invalid("colors requires exactly two ColorInput values")
		}
		colors := make([]string, 2)
		for i, value := range values {
			result, err := colorinput.ResolveJSON(value)
			if err != nil {
				return Action{}, invalid("colors[%d]: %v", i, err)
			}
			colors[i] = result.CanonicalColor
		}
		fields["colors"], _ = json.Marshal(colors)
	}
	checked, err := json.Marshal(fields)
	if err != nil {
		return Action{}, invalid("invalid action: %v", err)
	}
	var wire actionInput
	if err := jsoninput.DecodeObject(checked, &wire); err != nil {
		return Action{}, invalid("%v", err)
	}
	if wire.Kind == nil || *wire.Kind != "orbit" {
		return Action{}, invalid("kind must be orbit")
	}
	a := Action{Kind: "orbit", Colors: []string{"#ff0000", "#0000ff"}, LEDs: []int{1, 2}, PeriodMS: 3000, Cycles: 1, Segments: 4, OnFinish: "off"}
	if wire.Colors != nil {
		a.Colors = *wire.Colors
	}
	if wire.LEDs != nil {
		a.LEDs = *wire.LEDs
	}
	if wire.PeriodMS != nil {
		a.PeriodMS = *wire.PeriodMS
	}
	if wire.Cycles != nil {
		a.Cycles = *wire.Cycles
	}
	if wire.Segments != nil {
		a.Segments = *wire.Segments
	}
	if wire.OnFinish != nil {
		a.OnFinish = *wire.OnFinish
	}
	if len(a.LEDs) != 2 || !(a.LEDs[0] == 1 && a.LEDs[1] == 2 || a.LEDs[0] == 2 && a.LEDs[1] == 1) {
		return Action{}, invalid("leds must be [1,2] or [2,1]")
	}
	if a.Segments != 4 && a.Segments != 8 {
		return Action{}, invalid("segments must be 4 or 8")
	}
	if a.PeriodMS < a.Segments*control.MinFrameIntervalMS || int64(a.PeriodMS) > int64(a.Segments)*int64(control.MaxFrameIntervalMS) {
		return Action{}, invalid("period_ms requires each segment to be within %d..%dms", control.MinFrameIntervalMS, control.MaxFrameIntervalMS)
	}
	if a.Cycles < 1 || a.Cycles > control.MaxFrameProgramExecutedFrames/a.Segments {
		return Action{}, invalid("cycles must be within 1..%d for %d segments", control.MaxFrameProgramExecutedFrames/a.Segments, a.Segments)
	}
	if a.OnFinish != "off" && a.OnFinish != "hold" {
		return Action{}, invalid("on_finish must be off or hold")
	}
	return a, nil
}

// rawObject preserves values for the nested color grammar while bounding the
// input before materialization. Typed jsoninput validation follows resolution.
// Duplicate keys and null are rejected before a map could discard ambiguity.
func rawObject(data []byte) (map[string]json.RawMessage, error) {
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
