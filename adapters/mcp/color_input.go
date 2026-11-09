package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxColorRequestBytes = 4096
const colorInputContract = "nb1-color-input/v1"
const colorNamePalette = "css-named-2026"

// RawMessage retains the caller's number lexemes through HTTP marshalling.
// This module owns only wire validation, never a palette or HSV converter.
type resolveColorArgs struct {
	Color json.RawMessage `json:"color"`
}

type colorRGB8 struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

type colorResolution struct {
	SchemaVersion  int       `json:"schema_version"`
	ColorContract  string    `json:"color_contract"`
	InputKind      string    `json:"input_kind"`
	CanonicalColor string    `json:"canonical_color"`
	RGB8           colorRGB8 `json:"rgb8"`
	Palette        string    `json:"palette,omitempty"`
}

func resolveColorSchema() map[string]any {
	rgb := objectSchema(map[string]any{
		"space": map[string]any{"type": "string", "enum": []string{"rgb8"}},
		"r":     map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
		"g":     map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
		"b":     map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
	}, "space", "r", "g", "b")
	hsv := objectSchema(map[string]any{
		"space": map[string]any{"type": "string", "enum": []string{"hsv"}},
		"h_deg": map[string]any{"type": "number", "minimum": 0, "maximum": 360},
		"s_pct": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
		"v_pct": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
	}, "space", "h_deg", "s_pct", "v_pct")
	return objectSchema(map[string]any{"color": map[string]any{
		"oneOf":       []any{map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, rgb, hsv},
		"description": "Independent color notation only; does not change existing v1 Program/Effect color fields. String ≤64 decoded bytes; final lexical/semantic validation belongs to the daemon.",
	}}, "color")
}

func resolveColorOutputSchema() map[string]any {
	rgb := objectSchema(map[string]any{
		"r": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
		"g": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
		"b": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
	}, "r", "g", "b")
	data := objectSchema(map[string]any{
		"schema_version":  map[string]any{"type": "integer", "const": 1},
		"color_contract":  map[string]any{"type": "string", "enum": []string{colorInputContract}},
		"input_kind":      map[string]any{"type": "string", "enum": []string{"hex6", "hex3", "name", "rgb8", "hsv"}},
		"canonical_color": map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"},
		"rgb8":            rgb, "palette": map[string]any{"type": "string", "enum": []string{colorNamePalette}},
	}, "schema_version", "color_contract", "input_kind", "canonical_color", "rgb8")
	data["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"input_kind": map[string]any{"const": "name"}}, "required": []string{"palette"}},
		map[string]any{"properties": map[string]any{"input_kind": map[string]any{"enum": []string{"hex6", "hex3", "rgb8", "hsv"}}}, "not": map[string]any{"required": []string{"palette"}}},
	}
	problem := objectSchema(map[string]any{
		"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"},
		"http_status": map[string]any{"type": "integer"}, "outcome_unknown": map[string]any{"type": "boolean", "const": false},
	}, "code", "message")
	schema := objectSchema(map[string]any{
		"kind":     map[string]any{"type": "string", "enum": []string{"color_resolution", "error"}},
		"accepted": map[string]any{"type": "boolean", "const": false}, "data": data, "error": problem,
	}, "kind", "accepted")
	schema["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "color_resolution"}}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}},
		map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "error"}}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}},
	}
	return schema
}

func resolveColor(ctx context.Context, d *daemon, original json.RawMessage) (*mcp.CallToolResult, output, error) {
	if len(original) == 0 || len(original) > maxColorRequestBytes {
		return invalid("one color envelope of at most 4096 bytes is required")
	}
	if err := validateRawArguments(original); err != nil {
		return invalid(err.Error())
	}
	var args resolveColorArgs
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return invalid(err.Error())
	}
	if _, _, err := colorInputBinding(args.Color); err != nil {
		return invalid(err.Error())
	}
	return result(d.requestSoftware(ctx, http.MethodPost, "/v1/colors/resolve", "color_resolution", args))
}

// Determine the original input branch and bind unambiguous hex values. Name
// lookup, HSV math and numeric lexical semantics are left to the daemon.
func colorInputBinding(raw json.RawMessage) (kind, expectedHex string, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", "", errors.New("color is required")
	}
	switch raw[0] {
	case '"':
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", "", err
		}
		if len(value) > 64 {
			return "", "", errors.New("color string exceeds 64 decoded bytes")
		}
		value = strings.Trim(value, " \t\r\n\v\f")
		if value == "" {
			return "", "", errors.New("color string is empty")
		}
		if !strings.HasPrefix(value, "#") {
			return "name", "", nil
		}
		digits := value[1:]
		switch len(digits) {
		case 6:
			kind = "hex6"
		case 3:
			kind = "hex3"
			digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
		default:
			return "", "", errors.New("hex color requires three or six digits")
		}
		if _, err := hex.DecodeString(digits); err != nil {
			return "", "", errors.New("hex color has invalid digits")
		}
		return kind, "#" + strings.ToLower(digits), nil
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return "", "", err
		}
		var space string
		if err := json.Unmarshal(fields["space"], &space); err != nil || space != "rgb8" && space != "hsv" {
			return "", "", errors.New("color object requires rgb8 or hsv space")
		}
		return space, "", nil
	default:
		return "", "", errors.New("color must be a string or closed rgb8/hsv object")
	}
}

func validateColorResponse(data []byte, input any) (any, error) {
	args, ok := input.(resolveColorArgs)
	if !ok {
		return nil, errors.New("missing original color input binding")
	}
	kind, expectedHex, err := colorInputBinding(args.Color)
	if err != nil {
		return nil, err
	}
	var value colorResolution
	if err := decodeSoftwareWire(data, &value); err != nil {
		return nil, err
	}
	if value.SchemaVersion != 1 || value.ColorContract != colorInputContract || value.InputKind != kind {
		return nil, errors.New("daemon color contract or original input kind mismatch")
	}
	if value.RGB8.R < 0 || value.RGB8.R > 255 || value.RGB8.G < 0 || value.RGB8.G > 255 || value.RGB8.B < 0 || value.RGB8.B > 255 || value.CanonicalColor != fmt.Sprintf("#%02x%02x%02x", value.RGB8.R, value.RGB8.G, value.RGB8.B) {
		return nil, errors.New("daemon canonical color and RGB8 are not consistent")
	}
	if kind == "name" && value.Palette != colorNamePalette || kind != "name" && value.Palette != "" {
		return nil, errors.New("daemon color palette does not match input kind")
	}
	if expectedHex != "" && value.CanonicalColor != expectedHex {
		return nil, errors.New("daemon color changed the explicitly supplied hex value")
	}
	// Self-consistency and input-kind/hex binding are not source authentication:
	// for names/HSV the configured local daemon remains the resolver authority.
	return value, nil
}
