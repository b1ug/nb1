// Package colorinput resolves a bounded, versioned color grammar to raw RGB.
// It performs no I/O, device discovery, gamma correction or light control.
package colorinput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/colorkit"
)

const (
	Contract        = "nb1-color-input/v1"
	Palette         = "css-named-2026"
	MaxRequestBytes = 4096
)

type RGB8 struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

type Result struct {
	SchemaVersion  int    `json:"schema_version"`
	ColorContract  string `json:"color_contract"`
	InputKind      string `json:"input_kind"`
	CanonicalColor string `json:"canonical_color"`
	RGB8           RGB8   `json:"rgb8"`
	Palette        string `json:"palette,omitempty"`
}

// ResolveJSON accepts one bare ColorInput, not an HTTP envelope.
func ResolveJSON(data []byte) (Result, error) { return resolveDocument(data, false) }

// ResolveRequestJSON accepts exactly {"color": ColorInput}.
func ResolveRequestJSON(data []byte) (Result, error) { return resolveDocument(data, true) }

func resolveDocument(data []byte, envelope bool) (Result, error) {
	if len(data) > MaxRequestBytes {
		return Result{}, invalid("input exceeds 4096 bytes")
	}
	if !utf8.Valid(data) {
		return Result{}, invalid("input must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var result Result
	var err error
	if envelope {
		result, err = request(d)
	} else {
		result, err = color(d)
	}
	if err != nil {
		return Result{}, err
	}
	if _, err = d.Token(); err != io.EOF {
		return Result{}, invalid("exactly one JSON value is required")
	}
	return result, nil
}

func request(d *json.Decoder) (Result, error) {
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return Result{}, invalid("request must be an object containing color")
	}
	seen := false
	var result Result
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return Result{}, invalid("invalid request field")
		}
		if key != "color" || seen {
			return Result{}, invalid("request permits exactly one color field")
		}
		seen = true
		result, err = color(d)
		if err != nil {
			return Result{}, err
		}
	}
	close, err := d.Token()
	if err != nil || close != json.Delim('}') || !seen {
		return Result{}, invalid("request requires color")
	}
	return result, nil
}

func color(d *json.Decoder) (Result, error) {
	token, err := d.Token()
	if err != nil {
		return Result{}, invalid("invalid JSON color")
	}
	switch value := token.(type) {
	case string:
		return text(value)
	case json.Delim:
		if value == '{' {
			return object(d)
		}
	}
	return Result{}, invalid("color must be a string, rgb8 object or hsv object")
}

func text(value string) (Result, error) {
	// The bound applies before trimming, to the entire decoded input string.
	if len(value) > 64 {
		return Result{}, invalid("color string exceeds 64 decoded bytes")
	}
	value = strings.Trim(value, " \t\r\n\v\f")
	for i := range value {
		if value[i] >= utf8.RuneSelf {
			return Result{}, invalid("color string must contain only supported ASCII syntax")
		}
	}
	rgb, err := colorkit.Parse(value)
	if err != nil {
		if strings.HasPrefix(value, "#") {
			if len(value) == 4 || len(value) == 7 {
				return Result{}, invalid("invalid hex digits")
			}
			return Result{}, invalid("hex color requires three or six digits")
		}
		return Result{}, invalid("unsupported color name or syntax")
	}
	kind := "name"
	if strings.HasPrefix(value, "#") {
		kind = "hex6"
		if len(value) == 4 {
			kind = "hex3"
		}
	}
	return resultFromCore(kind, rgb), nil
}

func object(d *json.Decoder) (Result, error) {
	fields := make(map[string]json.Token)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return Result{}, invalid("invalid color object field")
		}
		key, ok := token.(string)
		if !ok {
			return Result{}, invalid("color field name must be a string")
		}
		switch key {
		case "space", "r", "g", "b", "h_deg", "s_pct", "v_pct":
		default:
			return Result{}, invalid(fmt.Sprintf("unknown color field: %q", key))
		}
		if _, exists := fields[key]; exists {
			return Result{}, invalid(fmt.Sprintf("duplicate color field: %q", key))
		}
		value, err := d.Token()
		if err != nil {
			return Result{}, invalid("invalid color field value")
		}
		if key == "space" {
			if _, ok := value.(string); !ok {
				return Result{}, invalid("space must be a string")
			}
		} else if _, ok := value.(json.Number); !ok {
			return Result{}, invalid("color channels must be JSON numbers, without nested values")
		}
		fields[key] = value
	}
	close, err := d.Token()
	if err != nil || close != json.Delim('}') {
		return Result{}, invalid("invalid color object")
	}
	if len(fields) != 4 {
		return Result{}, invalid("color object requires exactly four fields")
	}
	space, ok := fields["space"].(string)
	if !ok {
		return Result{}, invalid("color object requires space")
	}
	switch space {
	case "rgb8":
		var rgb RGB8
		for i, key := range []string{"r", "g", "b"} {
			n, ok := fields[key].(json.Number)
			if !ok {
				return Result{}, invalid("rgb8 requires r, g and b")
			}
			v, err := integer(n.String())
			if err != nil {
				return Result{}, err
			}
			switch i {
			case 0:
				rgb.R = v
			case 1:
				rgb.G = v
			case 2:
				rgb.B = v
			}
		}
		return result("rgb8", rgb), nil
	case "hsv":
		return hsv(fields)
	default:
		return Result{}, invalid("unsupported color space")
	}
}

func integer(s string) (int, error) {
	if !integerSyntax(s) {
		return 0, invalid("rgb8 channels require ordinary nonnegative JSON integers")
	}
	v, err := strconv.Atoi(s)
	if err != nil || v > 255 {
		return 0, invalid("rgb8 channels must be 0-255")
	}
	return v, nil
}

// integerSyntax is the rgb8 wire grammar: no signs, fractions, exponent,
// leading zero, or more than three digits. HSV token syntax is owned by colorkit.
func integerSyntax(s string) bool {
	if s == "" || len(s) > 3 || len(s) > 1 && s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resultFromCore is the single boundary between shared opaque colors and the
// nb1 JSON object DTO. colorkit.RGB8 marshals as a Hex string, so it must not
// replace RGB8's {r,g,b} wire representation.
func resultFromCore(kind string, rgb colorkit.RGB8) Result {
	r := result(kind, RGB8{R: int(rgb.R), G: int(rgb.G), B: int(rgb.B)})
	if kind == "name" {
		r.Palette = Palette
	}
	return r
}

func result(kind string, rgb RGB8) Result {
	return Result{SchemaVersion: 1, ColorContract: Contract, InputKind: kind, CanonicalColor: fmt.Sprintf("#%02x%02x%02x", rgb.R, rgb.G, rgb.B), RGB8: rgb}
}

// ValidateResult checks the versioned result's structure and canonical RGB.
// It is not a proof that a remote resolver received or honored an input.
func ValidateResult(r Result) error {
	if r.SchemaVersion != 1 || r.ColorContract != Contract {
		return invalid("unsupported result contract")
	}
	if r.RGB8.R < 0 || r.RGB8.R > 255 || r.RGB8.G < 0 || r.RGB8.G > 255 || r.RGB8.B < 0 || r.RGB8.B > 255 || r.CanonicalColor != result("", r.RGB8).CanonicalColor {
		return invalid("result hex and RGB8 must be canonical and agree")
	}
	switch r.InputKind {
	case "hex6", "hex3", "rgb8", "hsv":
		if r.Palette != "" {
			return invalid("palette is permitted only for name input")
		}
	case "name":
		if r.Palette != Palette {
			return invalid("name input requires the frozen palette")
		}
	default:
		return invalid("unsupported result input_kind")
	}
	return nil
}

func invalid(message string) error { return fmt.Errorf("invalid color: %s", message) }
