// Package devicesequence models the draft device-native row language. It only
// parses and inspects caller-owned data; it has no transport or execution path.
package devicesequence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/colorinput"
)

const (
	Contract          = "nb1-device-sequence/draft-1"
	ColorSpace        = "device-rgb8"
	MaxBytes          = 65536
	MaxLines          = 32
	TickMS            = 10
	WireMaxDurationMS = 65535 * TickMS
)

var ErrInvalid = errors.New("invalid device sequence")

type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type InputError struct{ Issue }

func (e *InputError) Error() string            { return e.Code + " at " + e.Path + ": " + e.Message }
func (e *InputError) Unwrap() error            { return ErrInvalid }
func invalid(code, path, message string) error { return &InputError{Issue{code, path, message}} }

// LED uses wire codes internally but spells all as a word in the author format.
type LED byte

func (led LED) MarshalJSON() ([]byte, error) {
	if led == 0 {
		return []byte(`"all"`), nil
	}
	if led == 1 || led == 2 {
		return []byte(strconv.Itoa(int(led))), nil
	}
	return nil, invalid("unsupported_led", "led", "Only all, LED1 and LED2 are authorable.")
}

type Line struct {
	LED        LED    `json:"led"`
	Color      string `json:"color"`
	DurationMS int    `json:"duration_ms"`
}

type Sequence struct {
	Contract   string `json:"contract"`
	ColorSpace string `json:"color_space"`
	Lines      []Line `json:"lines"`
}

type Compiled struct {
	Sequence Sequence `json:"sequence"`
	Digest   string   `json:"sequence_digest"`
	// Exact input bytes are caller-owned; normalization never overwrites them.
	OriginalJSON json.RawMessage `json:"-"`
}

// CompileJSON accepts only the independent author sequence. Target memory,
// repeat, startup, initial, on_finish and separate fade/hold are not its fields.
func CompileJSON(data []byte) (Compiled, error) {
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return Compiled{}, invalid("invalid_document", "$", "Input must be valid UTF-8 and at most 65536 bytes.")
	}
	fields, err := object(data, "$", "contract", "color_space", "lines")
	if err != nil {
		return Compiled{}, err
	}
	if !literal(fields["contract"], Contract) || !literal(fields["color_space"], ColorSpace) {
		return Compiled{}, invalid("unsupported_contract", "$", "The draft contract and device-rgb8 color space are required.")
	}
	var rawLines []json.RawMessage
	if err := json.Unmarshal(fields["lines"], &rawLines); err != nil || len(rawLines) == 0 || len(rawLines) > MaxLines {
		return Compiled{}, invalid("line_count_out_of_range", "lines", "A native sequence requires 1 to 32 lines.")
	}
	sequence := Sequence{Contract: Contract, ColorSpace: ColorSpace, Lines: make([]Line, 0, len(rawLines))}
	for i, raw := range rawLines {
		path := fmt.Sprintf("lines[%d]", i)
		line, err := object(raw, path, "led", "color", "duration_ms")
		if err != nil {
			return Compiled{}, err
		}
		var led LED
		switch string(bytes.TrimSpace(line["led"])) {
		case "1":
			led = 1
		case "2":
			led = 2
		default:
			if !literal(line["led"], "all") {
				return Compiled{}, invalid("unsupported_led", path+".led", "Use all, 1 or 2; one row cannot contain two different colors.")
			}
		}
		duration, err := integer(line["duration_ms"], path+".duration_ms", 1, WireMaxDurationMS)
		if err != nil {
			return Compiled{}, err
		}
		if duration%TickMS != 0 {
			return Compiled{}, invalid("time_quantization", path+".duration_ms", "Native durations must be exact multiples of 10 milliseconds.")
		}
		color, err := colorinput.ResolveJSON(line["color"])
		if err != nil {
			return Compiled{}, invalid("invalid_color", path+".color", "Color must satisfy nb1-color-input/v1.")
		}
		sequence.Lines = append(sequence.Lines, Line{led, color.CanonicalColor, duration})
	}
	canonical, err := json.Marshal(sequence)
	if err != nil {
		return Compiled{}, err
	}
	return Compiled{Sequence: sequence, Digest: digest(canonical), OriginalJSON: append(json.RawMessage(nil), data...)}, nil
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}
func colorRGB(color string) ([]byte, error)   { return hex.DecodeString(color[1:]) }
func linePath(index int, field string) string { return fmt.Sprintf("lines[%d].%s", index, field) }
func literal(raw []byte, expected string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func integer(raw []byte, path string, min, max int) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 10 {
		return 0, invalid("invalid_integer", path, "An ordinary nonnegative integer is required.")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, invalid("invalid_integer", path, "Fractions, exponents, signs and strings are not accepted.")
		}
	}
	value, err := strconv.Atoi(string(raw))
	if err != nil || value < min || value > max {
		return 0, invalid("integer_out_of_range", path, fmt.Sprintf("Value must be within %d..%d.", min, max))
	}
	return value, nil
}

// object checks this shallow author grammar without introducing a generic
// dependency on host Pattern storage or device control. The input size bounds
// the preliminary scan; nested color objects are checked by colorinput itself.
func object(data []byte, path string, required ...string) (map[string]json.RawMessage, error) {
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
		if c == '"' {
			quoted = true
		} else if c == '{' || c == '[' {
			depth++
			if depth > 16 {
				return nil, invalid("nesting_limit", path, "JSON nesting exceeds 16 levels.")
			}
		} else if c == '}' || c == ']' {
			depth--
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid("object_required", path, "An object is required.")
	}
	fields := make(map[string]json.RawMessage, len(required))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, invalid("invalid_document", path, "Invalid object key.")
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalid("invalid_document", path, "Object keys must be text.")
		}
		if _, exists := fields[key]; exists {
			return nil, invalid("duplicate_field", path+"."+key, "Duplicate fields are not accepted.")
		}
		known := false
		for _, name := range required {
			known = known || key == name
		}
		if !known {
			return nil, invalid("unknown_field", path+"."+key, "This field is not part of a device-native sequence.")
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, invalid("invalid_document", path+"."+key, "A non-null JSON value is required.")
		}
		fields[key] = raw
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid("invalid_document", path, "Invalid object end.")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid("trailing_input", path, "Exactly one JSON value is required.")
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, invalid("missing_field", path+"."+key, "The field is required.")
		}
	}
	return fields, nil
}
