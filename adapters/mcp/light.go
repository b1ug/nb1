package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const lightContract = "nb1-light-action/v1"
const maxLightArgumentBytes = 65536

type lightPreviewArgs struct {
	Contract string          `json:"contract"`
	Action   json.RawMessage `json:"action"`
	Serial   *string         `json:"serial,omitempty"`
}
type lightRunArgs struct {
	Contract                 string          `json:"contract"`
	Action                   json.RawMessage `json:"action"`
	Serial                   string          `json:"serial"`
	Policy                   string          `json:"policy,omitempty"`
	RequestID                string          `json:"request_id"`
	ExpectedDaemonInstanceID string          `json:"expected_daemon_instance_id"`
}
type lightSubmission struct {
	Contract                 string          `json:"contract"`
	Action                   json.RawMessage `json:"action"`
	Policy                   string          `json:"policy"`
	RequestID                string          `json:"request_id"`
	ExpectedDaemonInstanceID string          `json:"expected_daemon_instance_id"`
}

// This is a wire validator, not an effect compiler. Raw color values retain
// their original JSON until the daemon performs name/HSV conversion.
type lightActionWire struct {
	Kind       string            `json:"kind"`
	LED        *int              `json:"led,omitempty"`
	Color      json.RawMessage   `json:"color,omitempty"`
	Colors     []json.RawMessage `json:"colors,omitempty"`
	LEDs       []int             `json:"leds,omitempty"`
	DurationMS *int              `json:"duration_ms,omitempty"`
	OnMS       *int              `json:"on_ms,omitempty"`
	OffMS      *int              `json:"off_ms,omitempty"`
	PeriodMS   *int              `json:"period_ms,omitempty"`
	AttackMS   *int              `json:"attack_ms,omitempty"`
	DecayMS    *int              `json:"decay_ms,omitempty"`
	RestMS     *int              `json:"rest_ms,omitempty"`
	Cycles     *int              `json:"cycles,omitempty"`
	OnFinish   string            `json:"on_finish,omitempty"`
}
type lightResult struct {
	SchemaVersion          int             `json:"schema_version"`
	Contract               string          `json:"contract"`
	ProgramCompileContract string          `json:"program_compile_contract"`
	Action                 lightActionWire `json:"action"`
	Program                previewProgram  `json:"program"`
	NominalDurationMS      int64           `json:"nominal_duration_ms"`
	TotalSteps             int             `json:"total_steps"`
	RequiredCapabilities   []string        `json:"required_capabilities"`
}
type lightPreviewEnvelope struct {
	Result  lightResult   `json:"result"`
	Preview previewResult `json:"preview"`
}

func lightActionSchema() map[string]any {
	color := resolveColorSchema()["properties"].(map[string]any)["color"]
	integer := func(min, max, fallback int) map[string]any {
		return map[string]any{"type": "integer", "minimum": min, "maximum": max, "default": fallback}
	}
	variants := make([]any, 0, 8)
	for _, kind := range []string{"set", "off", "fade", "blink", "breathe", "pulse", "alternate", "rainbow"} {
		finish := "off"
		if kind == "set" || kind == "off" || kind == "fade" {
			finish = "hold"
		}
		p := map[string]any{"kind": map[string]any{"type": "string", "const": kind}, "on_finish": map[string]any{"type": "string", "enum": []string{"off", "hold"}, "default": finish}}
		required := []string{"kind"}
		if kind != "alternate" {
			p["led"] = map[string]any{"type": "integer", "enum": []int{0, 1, 2}, "default": 0}
		}
		if kind != "set" && kind != "off" && kind != "fade" {
			p["cycles"] = integer(1, 10000, 1)
		}
		if kind != "off" && kind != "alternate" && kind != "rainbow" {
			p["color"] = color
			required = append(required, "color")
		}
		switch kind {
		case "fade":
			p["duration_ms"] = map[string]any{"type": "integer", "minimum": 10, "maximum": 600000, "multipleOf": 10}
			required = append(required, "duration_ms")
		case "blink":
			p["on_ms"], p["off_ms"] = integer(10, 3600000, 250), integer(10, 3600000, 250)
		case "breathe":
			p["period_ms"] = integer(330, 1200000, 2000)
		case "pulse":
			attack, decay := integer(10, 600000, 60), integer(10, 600000, 240)
			attack["multipleOf"], decay["multipleOf"] = 10, 10
			p["attack_ms"], p["decay_ms"], p["rest_ms"] = attack, decay, integer(0, 3600000, 700)
		case "alternate":
			p["period_ms"] = integer(40, 1200000, 2000)
			p["colors"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": color, "default": []string{"red", "blue"}}
			p["leds"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "uniqueItems": true, "items": map[string]any{"type": "integer", "enum": []int{1, 2}}, "default": []int{1, 2}}
		case "rainbow":
			p["period_ms"] = integer(130, 1200000, 3000)
		}
		variants = append(variants, objectSchema(p, required...))
	}
	return map[string]any{"oneOf": variants, "description": "One action in the unified light namespace. breathe is a symmetric cosine16 envelope; new pulse is fast attack, decay, rest. All actions are finite; daemon enforces <=1024 executed steps/24h. Legacy run_program.effect.pulse is unchanged."}
}

func lightPreviewSchema() map[string]any {
	return objectSchema(map[string]any{"contract": map[string]any{"type": "string", "const": lightContract}, "action": lightActionSchema(), "serial": tokenSchema()}, "contract", "action")
}
func lightRunSchema() map[string]any {
	return objectSchema(map[string]any{"contract": map[string]any{"type": "string", "const": lightContract}, "action": lightActionSchema(), "serial": tokenSchema(), "policy": map[string]any{"type": "string", "enum": []string{"reject"}, "default": "reject"}, "request_id": requestIDSchema(), "expected_daemon_instance_id": instanceIDSchema()}, "contract", "action", "serial", "request_id", "expected_daemon_instance_id")
}

func lightPreviewOutputSchema() map[string]any {
	hex := map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"}
	action := lightActionSchema()
	for _, branch := range action["oneOf"].([]any) {
		variant := branch.(map[string]any)
		props := variant["properties"].(map[string]any)
		if _, ok := props["color"]; ok {
			props["color"] = hex
		}
		if _, ok := props["colors"]; ok {
			props["colors"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": hex}
		}
		required := make([]string, 0, len(props))
		for key := range props {
			required = append(required, key)
		}
		sort.Strings(required)
		variant["required"] = required
	}
	p := programSchema()
	pp := p["properties"].(map[string]any)
	p["required"] = []string{"steps", "repeat", "on_finish"}
	pp["repeat"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}
	s := pp["steps"].(map[string]any)["items"].(map[string]any)
	s["required"] = []string{"led", "color", "fade_ms", "hold_ms"}
	s["properties"].(map[string]any)["color"] = hex
	positive := map[string]any{"type": "integer", "minimum": 1}
	stringsArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	result := objectSchema(map[string]any{"schema_version": map[string]any{"const": 1}, "contract": map[string]any{"const": lightContract}, "program_compile_contract": map[string]any{"const": compileContract}, "action": action, "program": p, "nominal_duration_ms": positive, "total_steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 1024}, "required_capabilities": stringsArray}, "schema_version", "contract", "program_compile_contract", "action", "program", "nominal_duration_ms", "total_steps", "required_capabilities")
	validation := objectSchema(map[string]any{"program": map[string]any{"const": "valid"}, "device": map[string]any{"enum": []string{"not_requested", "unknown", "supported", "unsupported"}}, "reason": map[string]any{"type": "string"}, "missing_capabilities": stringsArray}, "program", "device")
	device := objectSchema(map[string]any{"serial": tokenSchema(), "connection": map[string]any{"type": "string"}, "capabilities": stringsArray}, "serial", "capabilities")
	preview := objectSchema(map[string]any{"schema_version": map[string]any{"const": 1}, "compile_contract": map[string]any{"const": compileContract}, "input_kind": map[string]any{"const": "program"}, "program": p, "nominal_duration_ms": positive, "total_steps": positive, "required_capabilities": stringsArray, "validation": validation, "device": device}, "schema_version", "compile_contract", "input_kind", "program", "nominal_duration_ms", "total_steps", "required_capabilities", "validation")
	problem := objectSchema(map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "http_status": map[string]any{"type": "integer"}, "outcome_unknown": map[string]any{"const": false}}, "code", "message")
	out := objectSchema(map[string]any{"kind": map[string]any{"enum": []string{"light_preview", "error"}}, "accepted": map[string]any{"const": false}, "data": objectSchema(map[string]any{"result": result, "preview": preview}, "result", "preview"), "error": problem}, "kind", "accepted")
	out["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "light_preview"}}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}}, map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "error"}}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}}}
	return out
}

func readLightArguments(raw json.RawMessage, out any) error {
	if len(raw) == 0 || len(raw) > maxLightArgumentBytes || !utf8.Valid(raw) {
		return errors.New("light arguments require a valid UTF-8 object of at most 65536 bytes")
	}
	return decodeSoftwareWire(raw, out)
}

func validateLightErrorResponse(data []byte) error {
	var problem struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := decodeSoftwareWire(data, &problem); err != nil {
		return err
	}
	if problem.Error.Code == "" || problem.Error.Message == "" {
		return errors.New("light error responses require a nonempty code and message")
	}
	return nil
}

// encoding/json replaces an unpaired UTF-16 escape with U+FFFD. Reject that
// lossy conversion on the new response contract before using an error code to
// decide whether a keyed write was definitely rejected. JSON syntax has
// already been checked, so every escape here has the required source bytes.
func validateLightUnicodeEscapes(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		if i+1 >= len(data) {
			return errors.New("incomplete JSON escape")
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			return errors.New("incomplete Unicode escape")
		}
		unit, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil {
			return errors.New("invalid Unicode escape")
		}
		switch {
		case unit >= 0xdc00 && unit <= 0xdfff:
			return errors.New("unpaired low Unicode surrogate")
		case unit >= 0xd800 && unit <= 0xdbff:
			if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
				return errors.New("unpaired high Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("unpaired high Unicode surrogate")
			}
			i += 11
		default:
			i += 5
		}
	}
	return nil
}

func previewLight(ctx context.Context, d *daemon, original json.RawMessage) (*mcp.CallToolResult, output, error) {
	var args lightPreviewArgs
	if err := readLightArguments(original, &args); err != nil {
		return invalid(err.Error())
	}
	if err := validateLightInput(args); err != nil {
		return invalid(err.Error())
	}
	return result(d.requestSoftware(ctx, http.MethodPost, "/v1/light/preview", "light_preview", args))
}

func runLight(ctx context.Context, d *daemon, original json.RawMessage) (*mcp.CallToolResult, output, error) {
	var args lightRunArgs
	if err := readLightArguments(original, &args); err != nil {
		return invalid(err.Error())
	}
	if !validToken(args.Serial) || args.Policy != "" && args.Policy != "reject" {
		return invalid("run_light requires an explicit serial and only policy reject")
	}
	ticket, err := optionalTicket(args.RequestID, args.ExpectedDaemonInstanceID)
	if err != nil || ticket == nil {
		return invalid("run_light requires the complete original request ticket")
	}
	previewArgs := lightPreviewArgs{Contract: args.Contract, Action: args.Action, Serial: &args.Serial}
	if err := validateLightInput(previewArgs); err != nil {
		return invalid(err.Error())
	}
	// One read-only preflight, then at most one write. Never prepare/rebind a
	// ticket, compile effects locally, retry a write or fall back to another API.
	preflight := d.requestSoftware(ctx, http.MethodPost, "/v1/light/preview", "light_preview", previewArgs)
	if preflight.Error != nil {
		return result(preserveTicket(preflight, ticket))
	}
	prepared := preflight.Data.(lightPreviewEnvelope)
	if prepared.Preview.Validation.Device != "supported" {
		return result(preserveTicket(errorOutput("device_not_supported", "target preview did not confirm the selected device; no execution POST attempted", 0, false), ticket))
	}
	request := lightSubmission{args.Contract, args.Action, "reject", args.RequestID, args.ExpectedDaemonInstanceID}
	out := d.requestBound(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(args.Serial)+"/light", "light_job", request, &receiptBinding{serial: args.Serial, ticket: *ticket})
	out = preserveTicket(out, ticket)
	if out.Error == nil {
		if err := validateLightJob(out.Data, prepared); err != nil {
			out = preserveTicket(errorOutput("invalid_daemon_response", err.Error(), http.StatusAccepted, true), ticket)
		}
	}
	return result(out)
}

func validateLightInput(args lightPreviewArgs) error {
	if args.Contract != lightContract || args.Serial != nil && !validToken(*args.Serial) {
		return errors.New("unsupported light contract or invalid explicit serial")
	}
	_, err := normalizedLightStructure(args.Action)
	return err
}

func normalizedLightStructure(raw json.RawMessage) (lightActionWire, error) {
	var a lightActionWire
	if err := decodeSoftwareWire(raw, &a); err != nil {
		return a, err
	}
	names := []string{"kind", "on_finish"}
	var basic bool
	switch a.Kind {
	case "set", "off", "fade":
		basic = true
	case "blink", "breathe", "pulse", "alternate", "rainbow":
	default:
		return a, errors.New("unknown light kind")
	}
	if a.Kind != "alternate" {
		names = append(names, "led")
	}
	if a.Kind != "off" && a.Kind != "rainbow" && a.Kind != "alternate" {
		names = append(names, "color")
	}
	if !basic {
		names = append(names, "cycles")
	}
	switch a.Kind {
	case "fade":
		names = append(names, "duration_ms")
	case "blink":
		names = append(names, "on_ms", "off_ms")
	case "breathe", "rainbow":
		names = append(names, "period_ms")
	case "pulse":
		names = append(names, "attack_ms", "decay_ms", "rest_ms")
	case "alternate":
		names = append(names, "period_ms", "colors", "leds")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return a, err
	}
	for key := range fields {
		known := false
		for _, name := range names {
			known = known || key == name
		}
		if !known {
			return a, fmt.Errorf("inapplicable light field %q", key)
		}
	}
	if a.OnFinish == "" {
		a.OnFinish = "off"
		if basic {
			a.OnFinish = "hold"
		}
	}
	if a.OnFinish != "hold" && a.OnFinish != "off" {
		return a, errors.New("invalid light on_finish")
	}
	var err error
	if a.Kind != "alternate" {
		a.LED, err = lightNumber(a.LED, 0, 0, 2, 1)
		if err != nil {
			return a, err
		}
	}
	if !basic {
		a.Cycles, err = lightNumber(a.Cycles, 1, 1, 10000, 1)
		if err != nil {
			return a, err
		}
	}
	if a.Kind != "off" && a.Kind != "rainbow" && a.Kind != "alternate" {
		if len(a.Color) > maxColorRequestBytes {
			return a, errors.New("color exceeds 4096 bytes")
		}
		if _, _, err := colorInputBinding(a.Color); err != nil {
			return a, err
		}
	}
	switch a.Kind {
	case "fade":
		a.DurationMS, err = lightNumber(a.DurationMS, -1, 10, 600000, 10)
	case "blink":
		a.OnMS, err = lightNumber(a.OnMS, 250, 10, 3600000, 1)
		if err == nil {
			a.OffMS, err = lightNumber(a.OffMS, 250, 10, 3600000, 1)
		}
	case "breathe":
		a.PeriodMS, err = lightNumber(a.PeriodMS, 2000, 330, 1200000, 1)
	case "pulse":
		a.AttackMS, err = lightNumber(a.AttackMS, 60, 10, 600000, 10)
		if err == nil {
			a.DecayMS, err = lightNumber(a.DecayMS, 240, 10, 600000, 10)
		}
		if err == nil {
			a.RestMS, err = lightNumber(a.RestMS, 700, 0, 3600000, 1)
		}
	case "alternate":
		a.PeriodMS, err = lightNumber(a.PeriodMS, 2000, 40, 1200000, 1)
		if a.LEDs == nil {
			a.LEDs = []int{1, 2}
		}
		if len(a.LEDs) != 2 || a.LEDs[0]+a.LEDs[1] != 3 || a.LEDs[0] < 1 || a.LEDs[0] > 2 {
			return a, errors.New("leds must be [1,2] or [2,1]")
		}
		if a.Colors == nil {
			a.Colors = []json.RawMessage{json.RawMessage(`"#ff0000"`), json.RawMessage(`"#0000ff"`)}
		}
		if len(a.Colors) != 2 {
			return a, errors.New("alternate requires exactly two colors")
		}
		for _, color := range a.Colors {
			if len(color) > maxColorRequestBytes {
				return a, errors.New("color exceeds 4096 bytes")
			}
			if _, _, e := colorInputBinding(color); e != nil {
				return a, e
			}
		}
	case "rainbow":
		a.PeriodMS, err = lightNumber(a.PeriodMS, 3000, 130, 1200000, 1)
	}
	return a, err
}

func lightNumber(value *int, fallback, min, max, quantum int) (*int, error) {
	if value == nil {
		value = &fallback
	}
	if *value < min || *value > max || *value%quantum != 0 {
		return nil, errors.New("light number is missing, outside bounds, or not quantized")
	}
	return value, nil
}

func validateLightPreview(data []byte, input any) (any, error) {
	args, ok := input.(lightPreviewArgs)
	if !ok || args.Contract != lightContract {
		return nil, errors.New("missing original light preview input")
	}
	expected, err := normalizedLightStructure(args.Action)
	if err != nil {
		return nil, err
	}
	var envelope lightPreviewEnvelope
	if err := decodeSoftwareWire(data, &envelope); err != nil {
		return nil, err
	}
	r := envelope.Result
	if r.SchemaVersion != 1 || r.Contract != lightContract || r.ProgramCompileContract != compileContract || r.TotalSteps < 1 || r.TotalSteps > 1024 {
		return nil, errors.New("unsupported light response contract or limits")
	}
	if err := bindLightAction(expected, r.Action); err != nil {
		return nil, err
	}
	p := program{Steps: r.Program.Steps, Repeat: r.Program.Repeat, OnFinish: r.Program.OnFinish}
	if err := validatePreviewResult(envelope.Preview, previewArgs{Program: &p, Serial: args.Serial}); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(r.Program, envelope.Preview.Program) || r.TotalSteps != envelope.Preview.TotalSteps || r.NominalDurationMS != envelope.Preview.NominalDurationMS || !reflect.DeepEqual(r.RequiredCapabilities, envelope.Preview.RequiredCapabilities) {
		return nil, errors.New("light result and authoritative preview disagree")
	}
	if err := validateLightProgramShape(r); err != nil {
		return nil, err
	}
	return envelope, nil
}

// Check declared selectors, cycle arithmetic and color anchors. This verifies
// the public wire contract without generating an effect, duplicating its LUT,
// or performing name/HSV conversion in this independent adapter.
func validateLightProgramShape(r lightResult) error {
	a, p := r.Action, r.Program
	cycles, count, nominal := 1, 1, int64(10)
	if a.Cycles != nil {
		cycles = *a.Cycles
	}
	switch a.Kind {
	case "fade":
		nominal = int64(*a.DurationMS) + 10
	case "blink":
		count, nominal = 2, int64(*a.OnMS)+int64(*a.OffMS)
	case "breathe":
		count, nominal = 17, int64(*a.PeriodMS)
	case "pulse":
		count, nominal = 3, int64(*a.AttackMS)+int64(*a.DecayMS)+int64(*a.RestMS)+30
	case "alternate":
		count, nominal = 4, int64(*a.PeriodMS)
	case "rainbow":
		count, nominal = 7, int64(*a.PeriodMS)
	}
	if len(p.Steps) != count || p.Repeat != cycles || p.OnFinish != a.OnFinish || r.NominalDurationMS != nominal*int64(cycles) {
		return errors.New("light Program shape or duration disagrees with the declared action")
	}
	if a.Kind != "alternate" {
		for _, s := range p.Steps {
			if s.LED != *a.LED {
				return errors.New("light Program changed the requested LED")
			}
		}
	} else {
		for i, s := range p.Steps {
			if s.LED != a.LEDs[i%2] {
				return errors.New("alternate Program changed LED order")
			}
		}
	}
	color := func(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
	check := func(index int, want string) error {
		if p.Steps[index].Color != want {
			return errors.New("light Program color anchor disagrees with its normalized action")
		}
		return nil
	}
	timing := func(index, fade, hold int) bool {
		return p.Steps[index].FadeMS == fade && p.Steps[index].HoldMS == hold
	}
	switch a.Kind {
	case "set", "off":
		if !timing(0, 0, 0) {
			return errors.New("basic light timing changed")
		}
	case "fade":
		if !timing(0, *a.DurationMS, 0) {
			return errors.New("light fade duration changed")
		}
	case "blink":
		if !timing(0, 0, *a.OnMS-10) || !timing(1, 0, *a.OffMS-10) {
			return errors.New("light blink phase timing changed")
		}
	case "pulse":
		if !timing(0, 0, 0) || !timing(1, *a.AttackMS, 0) || !timing(2, *a.DecayMS, *a.RestMS) {
			return errors.New("light pulse phase timing changed")
		}
	case "alternate":
		half := *a.PeriodMS / 2
		if !timing(0, 0, 0) || !timing(1, 0, half-20) || !timing(2, 0, 0) || !timing(3, 0, *a.PeriodMS-half-20) {
			return errors.New("light alternate phase timing changed")
		}
	}
	switch a.Kind {
	case "set", "fade":
		return check(0, color(a.Color))
	case "off":
		return check(0, "#000000")
	case "blink":
		if err := check(0, color(a.Color)); err != nil {
			return err
		}
		return check(1, "#000000")
	case "breathe":
		if err := check(0, "#000000"); err != nil {
			return err
		}
		if err := check(8, color(a.Color)); err != nil {
			return err
		}
		return check(16, "#000000")
	case "pulse":
		if err := check(0, "#000000"); err != nil {
			return err
		}
		if err := check(1, color(a.Color)); err != nil {
			return err
		}
		return check(2, "#000000")
	case "alternate":
		for i, want := range []string{color(a.Colors[0]), color(a.Colors[1]), color(a.Colors[1]), color(a.Colors[0])} {
			if err := check(i, want); err != nil {
				return err
			}
		}
	}
	return nil
}

func bindLightAction(expected, actual lightActionWire) error {
	bindColor := func(input, output json.RawMessage) error {
		_, hex, err := colorInputBinding(input)
		if err != nil {
			return err
		}
		kind, got, err := colorInputBinding(output)
		if err != nil || kind != "hex6" || got == "" || !bytes.Equal(output, json.RawMessage(strconvQuote(got))) || hex != "" && hex != got {
			return errors.New("light normalized color is noncanonical or changes an explicit hex input")
		}
		return nil
	}
	if len(expected.Color) > 0 {
		if err := bindColor(expected.Color, actual.Color); err != nil {
			return err
		}
	} else if len(actual.Color) > 0 {
		return errors.New("unexpected light color")
	}
	if len(expected.Colors) != len(actual.Colors) {
		return errors.New("light color count mismatch")
	}
	for i, color := range expected.Colors {
		if err := bindColor(color, actual.Colors[i]); err != nil {
			return err
		}
	}
	expected.Color, actual.Color = nil, nil
	expected.Colors, actual.Colors = nil, nil
	if !reflect.DeepEqual(expected, actual) {
		return errors.New("light action kind, selectors, timing or defaults changed")
	}
	return nil
}

func strconvQuote(value string) string { b, _ := json.Marshal(value); return string(b) }

func validateLightJob(value any, prepared lightPreviewEnvelope) error {
	job, ok := value.(map[string]any)
	if !ok {
		return errors.New("expected a light Job object")
	}
	data, err := json.Marshal(job["program"])
	if err != nil {
		return err
	}
	var p previewProgram
	if err := decodeSoftwareWire(data, &p); err != nil {
		return err
	}
	if !reflect.DeepEqual(p, prepared.Result.Program) {
		return errors.New("accepted light Job changed the authoritative preview Program; original outcome unknown")
	}
	count := func(key string) (int64, error) {
		n, ok := job[key].(json.Number)
		if !ok {
			return 0, errors.New("missing integer job step count")
		}
		return n.Int64()
	}
	total, err := count("total_steps")
	if err != nil || total != int64(prepared.Result.TotalSteps) {
		return errors.New("light Job total step count changed")
	}
	completed, err := count("completed_steps")
	if err != nil || completed < 0 || completed > total {
		return errors.New("invalid light Job completed step count")
	}
	return nil
}
