package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const compileContract = "nb1-program-effect/v1"

// Independent wire types: the adapter imports neither the core nor USB stack.
// It verifies a daemon's result; it does not compile effects a second time.
type previewArgs struct {
	Program *program `json:"program,omitempty"`
	Effect  *effect  `json:"effect,omitempty"`
	Serial  *string  `json:"serial,omitempty"`
}

type previewProgram struct {
	Steps    []step `json:"steps"`
	Repeat   int    `json:"repeat"`
	OnFinish string `json:"on_finish"`
}

type previewDevice struct {
	Serial       string   `json:"serial"`
	Connection   string   `json:"connection,omitempty"`
	Capabilities []string `json:"capabilities"`
}

type previewValidation struct {
	Program             string   `json:"program"`
	Device              string   `json:"device"`
	Reason              string   `json:"reason,omitempty"`
	MissingCapabilities []string `json:"missing_capabilities,omitempty"`
}

type previewResult struct {
	SchemaVersion        int               `json:"schema_version"`
	CompileContract      string            `json:"compile_contract"`
	InputKind            string            `json:"input_kind"`
	Program              previewProgram    `json:"program"`
	NominalDurationMS    int64             `json:"nominal_duration_ms"`
	TotalSteps           int               `json:"total_steps"`
	RequiredCapabilities []string          `json:"required_capabilities"`
	Validation           previewValidation `json:"validation"`
	Device               *previewDevice    `json:"device,omitempty"`
}

type programLimits struct {
	MaxSteps             int `json:"max_steps"`
	MaxRepeat            int `json:"max_repeat"`
	MaxFadeMS            int `json:"max_fade_ms"`
	FadeQuantumMS        int `json:"fade_quantum_ms"`
	MaxHoldMS            int `json:"max_hold_ms"`
	MaxNominalDurationMS int `json:"max_nominal_duration_ms"`
	StepOverheadMS       int `json:"step_overhead_ms"`
}

type effectLimits struct {
	MinPulsePeriodMS int `json:"min_pulse_period_ms"`
	MaxPulsePeriodMS int `json:"max_pulse_period_ms"`
	MaxPulseCycles   int `json:"max_pulse_cycles"`
}

type capabilityCatalog struct {
	SchemaVersion         int           `json:"schema_version"`
	APIVersion            string        `json:"api_version"`
	CompileContract       string        `json:"compile_contract"`
	SupportedInputs       []string      `json:"supported_inputs"`
	Effects               []string      `json:"effects"`
	Policies              []string      `json:"policies"`
	ColorModel            string        `json:"color_model"`
	LEDSelectors          []int         `json:"led_selectors"`
	PreviewPath           string        `json:"preview_path"`
	PatternLibrary        bool          `json:"pattern_library"`
	HardwarePatternUpload bool          `json:"hardware_pattern_upload"`
	ProgramLimits         programLimits `json:"program_limits"`
	EffectLimits          effectLimits  `json:"effect_limits"`
	MaxRequestBytes       int           `json:"max_request_bytes"`
}

func expectedCatalog() capabilityCatalog {
	return capabilityCatalog{
		SchemaVersion: 1, APIVersion: "v1", CompileContract: compileContract,
		SupportedInputs: []string{"program", "effect"}, Effects: []string{"steady", "pulse"},
		Policies: []string{"replace", "queue", "reject"}, ColorModel: "raw-rgb", LEDSelectors: []int{0, 1, 2},
		PreviewPath: "/v1/programs/preview", ProgramLimits: programLimits{1024, 10000, 600000, 10, 3600000, 86400000, 10},
		EffectLimits: effectLimits{60, 1200000, 10000}, MaxRequestBytes: 1 << 20,
	}
}

func previewSchema() map[string]any {
	schema := objectSchema(map[string]any{"program": programSchema(), "effect": effectSchema(), "serial": tokenSchema()})
	schema["oneOf"] = []any{
		map[string]any{"required": []string{"program"}, "not": map[string]any{"required": []string{"effect"}}},
		map[string]any{"required": []string{"effect"}, "not": map[string]any{"required": []string{"program"}}},
	}
	return schema
}

func preview(ctx context.Context, d *daemon, args previewArgs) (*mcp.CallToolResult, output, error) {
	if (args.Program == nil) == (args.Effect == nil) {
		return invalid("provide exactly one of program or effect")
	}
	if args.Serial != nil && !validToken(*args.Serial) {
		return invalid("invalid serial")
	}
	if args.Program != nil {
		p, err := normalizeProgram(*args.Program)
		if err != nil {
			return invalid(err.Error())
		}
		args.Program = &p
	} else {
		e, err := normalizeEffectStructure(*args.Effect)
		if err != nil {
			return invalid(err.Error())
		}
		args.Effect = &e
	}
	return result(d.requestSoftware(ctx, http.MethodPost, "/v1/programs/preview", "program_preview", args))
}

func listDevicesWithCapabilities(ctx context.Context, d *daemon, prepare, include bool) output {
	out := listDevices(ctx, d, prepare)
	if out.Error != nil || !include {
		return out
	}
	catalog := d.requestSoftware(ctx, http.MethodGet, "/v1/capabilities", "capabilities", nil)
	if catalog.Error != nil {
		// Preparing a ticket does not admit a job. Catalog failures must not be
		// reclassified as unknown job outcomes by the receipt write helpers.
		catalog.RequestTicket = out.RequestTicket
		return catalog
	}
	out.Data = map[string]any{"devices": out.Data, "capabilities": catalog.Data}
	return out
}

// Round-trip equality enforces exact case, required fields and omission rules,
// in addition to the raw duplicate/null/depth guard and unknown-field decoder.
// The daemon is not allowed to substitute defaults or aliases in its response.
func decodeSoftwareWire(data []byte, target any) error {
	if err := validateRawArguments(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	decode := func(b []byte) (any, error) {
		var v any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		err := d.Decode(&v)
		return v, err
	}
	got, err := decode(data)
	if err != nil {
		return err
	}
	want, err := decode(canonical)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("daemon software response has missing, noncanonical or aliased fields")
	}
	return nil
}

func validateSoftwareResponse(data []byte, kind string, input any) (any, error) {
	switch kind {
	case "capabilities":
		var catalog capabilityCatalog
		if err := decodeSoftwareWire(data, &catalog); err != nil {
			return nil, err
		}
		if !validCatalog(catalog) {
			return nil, errors.New("daemon capability catalog does not match the supported v1 compiler contract")
		}
		return catalog, nil
	case "program_preview":
		args, ok := input.(previewArgs)
		if !ok {
			return nil, errors.New("missing original preview binding")
		}
		var p previewResult
		if err := decodeSoftwareWire(data, &p); err != nil {
			return nil, err
		}
		if err := validatePreviewResult(p, args); err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, errors.New("unknown software response kind")
	}
}

func validCatalog(c capabilityCatalog) bool {
	e := expectedCatalog()
	if c.SchemaVersion != e.SchemaVersion || c.APIVersion != e.APIVersion || c.CompileContract != e.CompileContract || c.ColorModel != e.ColorModel || c.PreviewPath != e.PreviewPath || !reflect.DeepEqual(c.LEDSelectors, e.LEDSelectors) || c.ProgramLimits != e.ProgramLimits || c.EffectLimits != e.EffectLimits || c.MaxRequestBytes < 1 || c.MaxRequestBytes > e.MaxRequestBytes {
		return false
	}
	for _, pair := range []struct{ got, required []string }{{c.SupportedInputs, e.SupportedInputs}, {c.Effects, e.Effects}, {c.Policies, e.Policies}} {
		seen := map[string]bool{}
		for _, name := range pair.got {
			if name == "" || seen[name] {
				return false
			}
			seen[name] = true
		}
		for _, name := range pair.required {
			if !seen[name] {
				return false
			}
		}
	}
	// Future advertised names/flags are metadata, not permission to expose or
	// invoke additional tools. Known v1 limits and semantics remain fixed.
	return true
}

func validatePreviewResult(p previewResult, args previewArgs) error {
	kind := "effect"
	if args.Program != nil {
		kind = "program"
	}
	if p.SchemaVersion != 1 || p.CompileContract != compileContract || p.InputKind != kind || p.Validation.Program != "valid" {
		return errors.New("daemon preview contract or input kind mismatch")
	}
	raw := program{Steps: p.Program.Steps, Repeat: p.Program.Repeat, OnFinish: p.Program.OnFinish}
	normalized, err := normalizeProgram(raw)
	if err != nil || !reflect.DeepEqual(raw, normalized) {
		return errors.New("daemon preview does not contain a canonical valid program")
	}
	if args.Program != nil && !reflect.DeepEqual(raw, *args.Program) {
		return errors.New("daemon preview changed the original normalized program")
	}
	var nominal int64
	for _, s := range raw.Steps {
		nominal += int64(s.FadeMS) + int64(s.HoldMS) + 10
	}
	nominal *= int64(raw.Repeat)
	if p.NominalDurationMS != nominal || p.TotalSteps != len(raw.Steps)*raw.Repeat {
		return errors.New("daemon preview duration or step arithmetic mismatch")
	}
	required := []string{"set", "program", "raw-rgb"}
	if raw.OnFinish == "hold" {
		required = append(required, "hold")
	}
	if !reflect.DeepEqual(p.RequiredCapabilities, required) {
		return errors.New("daemon preview required capabilities mismatch")
	}
	want := previewValidation{Program: "valid", Device: "not_requested"}
	if args.Serial == nil {
		if p.Device != nil {
			return errors.New("daemon preview supplied an unrequested target")
		}
	} else {
		if p.Device == nil || p.Device.Serial != *args.Serial || !validToken(p.Device.Serial) {
			return errors.New("daemon preview did not bind the requested target")
		}
		seen := map[string]bool{}
		for _, c := range p.Device.Capabilities {
			seen[c] = true
		}
		want.Device = "unknown"
		switch p.Device.Connection {
		case "":
			if len(seen) != 0 {
				return errors.New("unconfigured target cannot advertise capabilities")
			}
			want.Reason = "not_configured"
		case "online":
			if len(p.Device.Capabilities) == 0 {
				want.Reason = "capabilities_unavailable"
			} else {
				for _, c := range required {
					if !seen[c] {
						want.MissingCapabilities = append(want.MissingCapabilities, c)
					}
				}
				if len(want.MissingCapabilities) != 0 {
					want.Device, want.Reason = "unsupported", "capabilities_missing"
				} else {
					want.Device = "supported"
				}
			}
		default:
			want.Reason = "not_online"
		}
	}
	if !reflect.DeepEqual(p.Validation, want) {
		return errors.New("daemon preview target validation disagrees with its snapshot")
	}
	return nil
}
