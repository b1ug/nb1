package control

import (
	"fmt"
	"reflect"
)

// CompileContract identifies the unchanged Program/Effect semantics shared by
// execution and software preview. It is not a hardware conformance claim.
const CompileContract = "nb1-program-effect/v1"
const (
	MaxProgramSteps            = 1024
	MaxProgramRepeat           = 10000
	MaxFadeMS                  = 600000
	FadeQuantumMS              = 10
	MaxHoldMS                  = 3600000
	MaxNominalDurationMS int64 = 86400000
	StepOverheadMS             = 10
	MinPulsePeriodMS           = 60
	MaxPulsePeriodMS           = 1200000
	MaxRequestBytes            = 1 << 20
)

type ProgramLimits struct {
	MaxSteps             int   `json:"max_steps"`
	MaxRepeat            int   `json:"max_repeat"`
	MaxFadeMS            int   `json:"max_fade_ms"`
	FadeQuantumMS        int   `json:"fade_quantum_ms"`
	MaxHoldMS            int   `json:"max_hold_ms"`
	MaxNominalDurationMS int64 `json:"max_nominal_duration_ms"`
	StepOverheadMS       int   `json:"step_overhead_ms"`
}
type EffectLimits struct {
	MinPulsePeriodMS int `json:"min_pulse_period_ms"`
	MaxPulsePeriodMS int `json:"max_pulse_period_ms"`
	MaxPulseCycles   int `json:"max_pulse_cycles"`
}
type CapabilityCatalog struct {
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
	ProgramLimits         ProgramLimits `json:"program_limits"`
	EffectLimits          EffectLimits  `json:"effect_limits"`
	MaxRequestBytes       int           `json:"max_request_bytes"`
}

// Capabilities describes software compilation. Configured device snapshots
// separately describe enabled backend capabilities; this performs no I/O.
func Capabilities() CapabilityCatalog {
	return CapabilityCatalog{SchemaVersion: 1, APIVersion: "v1", CompileContract: CompileContract,
		SupportedInputs: []string{"program", "effect"}, Effects: []string{"steady", "pulse"}, Policies: []string{"replace", "queue", "reject"}, ColorModel: "raw-rgb", LEDSelectors: []int{0, 1, 2}, PreviewPath: "/v1/programs/preview",
		ProgramLimits: ProgramLimits{MaxProgramSteps, MaxProgramRepeat, MaxFadeMS, FadeQuantumMS, MaxHoldMS, MaxNominalDurationMS, StepOverheadMS}, EffectLimits: EffectLimits{MinPulsePeriodMS, MaxPulsePeriodMS, MaxProgramRepeat}, MaxRequestBytes: MaxRequestBytes}
}

func ValidateCapabilityCatalog(c CapabilityCatalog) error {
	expected := Capabilities()
	if c.SchemaVersion != 1 || c.APIVersion != "v1" || c.CompileContract != CompileContract || c.ColorModel != "raw-rgb" || c.PreviewPath != expected.PreviewPath || !reflect.DeepEqual(c.LEDSelectors, expected.LEDSelectors) || c.ProgramLimits != expected.ProgramLimits || c.EffectLimits != expected.EffectLimits || c.MaxRequestBytes < 1 || c.MaxRequestBytes > MaxRequestBytes {
		return fmt.Errorf("invalid or unsupported compiler capability catalog")
	}
	for _, pair := range []struct{ got, required []string }{{c.SupportedInputs, expected.SupportedInputs}, {c.Effects, expected.Effects}, {c.Policies, expected.Policies}} {
		seen := make(map[string]bool)
		for _, value := range pair.got {
			if value == "" || seen[value] {
				return fmt.Errorf("invalid capability names")
			}
			seen[value] = true
		}
		for _, value := range pair.required {
			if !seen[value] {
				return fmt.Errorf("required compiler capability %q missing", value)
			}
		}
	}
	return nil
}

type PreviewValidation struct {
	Program             string   `json:"program"`
	Device              string   `json:"device"`
	Reason              string   `json:"reason,omitempty"`
	MissingCapabilities []string `json:"missing_capabilities,omitempty"`
}
type PreviewDevice struct {
	Serial       string   `json:"serial"`
	Connection   string   `json:"connection,omitempty"`
	Capabilities []string `json:"capabilities"`
}
type PreviewResult struct {
	SchemaVersion        int               `json:"schema_version"`
	CompileContract      string            `json:"compile_contract"`
	InputKind            string            `json:"input_kind"`
	Program              Program           `json:"program"`
	NominalDurationMS    int64             `json:"nominal_duration_ms"`
	TotalSteps           int               `json:"total_steps"`
	RequiredCapabilities []string          `json:"required_capabilities"`
	Validation           PreviewValidation `json:"validation"`
	Device               *PreviewDevice    `json:"device,omitempty"`
}

// Preview shares the actual compiler and normalizer with job execution. It
// neither discovers devices nor creates jobs, receipts or ownership leases.
func Preview(program *Program, effect *Effect) (PreviewResult, error) {
	if (program == nil) == (effect == nil) {
		return PreviewResult{}, fmt.Errorf("%w: exactly one of program or effect is required", ErrInvalid)
	}
	var p Program
	var err error
	kind := "program"
	if effect != nil {
		kind = "effect"
		p, err = CompileEffect(*effect)
	} else {
		p, err = Normalize(*program)
	}
	if err != nil {
		return PreviewResult{}, err
	}
	var duration int64
	for _, s := range p.Steps {
		duration += int64(s.FadeMS) + int64(s.HoldMS) + StepOverheadMS
	}
	required := []string{"set", "program", "raw-rgb"}
	if p.OnFinish == "hold" {
		required = append(required, "hold")
	}
	return PreviewResult{SchemaVersion: 1, CompileContract: CompileContract, InputKind: kind, Program: p, NominalDurationMS: duration * int64(p.Repeat), TotalSteps: len(p.Steps) * p.Repeat, RequiredCapabilities: required, Validation: PreviewValidation{Program: "valid", Device: "not_requested"}}, nil
}

// WithDeviceSnapshot checks advertised names only, not hardware execution or
// admission. Offline/unknown snapshots cannot establish support. No I/O occurs.
func WithDeviceSnapshot(result PreviewResult, serial string, snapshot *DeviceState) PreviewResult {
	result.Program = cloneProgram(result.Program)
	result.RequiredCapabilities = append([]string{}, result.RequiredCapabilities...)
	result.Validation = PreviewValidation{Program: "valid", Device: "unknown", Reason: "not_configured"}
	result.Device = &PreviewDevice{Serial: serial, Capabilities: []string{}}
	if snapshot == nil {
		return result
	}
	result.Device.Connection = snapshot.Connection
	result.Device.Capabilities = append([]string{}, snapshot.Capabilities...)
	if snapshot.Serial != serial {
		result.Device.Connection = ""
		result.Device.Capabilities = []string{}
		return result
	}
	if snapshot.Connection != "online" {
		result.Validation.Reason = "not_online"
		return result
	}
	if len(snapshot.Capabilities) == 0 {
		result.Validation.Reason = "capabilities_unavailable"
		return result
	}
	enabled := make(map[string]bool)
	for _, value := range snapshot.Capabilities {
		enabled[value] = true
	}
	for _, value := range result.RequiredCapabilities {
		if !enabled[value] {
			result.Validation.MissingCapabilities = append(result.Validation.MissingCapabilities, value)
		}
	}
	if len(result.Validation.MissingCapabilities) > 0 {
		result.Validation.Device = "unsupported"
		result.Validation.Reason = "capabilities_missing"
	} else {
		result.Validation.Device = "supported"
		result.Validation.Reason = ""
	}
	return result
}

func ValidDeviceSerial(serial string) bool {
	if len(serial) == 0 || len(serial) > 128 {
		return false
	}
	for _, c := range serial {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ValidatePreviewResult checks a received v1 result, not the truth of remote
// hardware claims. The known compiler contract, arithmetic, canonical Program
// and requested snapshot identity must agree before a client reports success.
func ValidatePreviewResult(r PreviewResult, inputKind string, serial *string) error {
	if r.SchemaVersion != 1 || r.CompileContract != CompileContract || r.InputKind != inputKind || (inputKind != "program" && inputKind != "effect") {
		return fmt.Errorf("invalid or unsupported preview contract")
	}
	expected, err := Preview(&r.Program, nil)
	if err != nil || !reflect.DeepEqual(r.Program, expected.Program) || r.NominalDurationMS != expected.NominalDurationMS || r.TotalSteps != expected.TotalSteps || !reflect.DeepEqual(r.RequiredCapabilities, expected.RequiredCapabilities) {
		return fmt.Errorf("invalid normalized program or preview totals")
	}
	if serial != nil {
		if !ValidDeviceSerial(*serial) || r.Device == nil || r.Device.Serial != *serial {
			return fmt.Errorf("preview device does not match requested serial")
		}
		var snapshot *DeviceState
		if r.Device.Connection != "" {
			snapshot = &DeviceState{DeviceInfo: DeviceInfo{Serial: *serial, Capabilities: r.Device.Capabilities}, Connection: r.Device.Connection}
		}
		expected = WithDeviceSnapshot(expected, *serial, snapshot)
		if !reflect.DeepEqual(r.Device, expected.Device) {
			return fmt.Errorf("invalid preview device snapshot")
		}
	} else if r.Device != nil {
		return fmt.Errorf("unexpected preview device snapshot")
	}
	if !reflect.DeepEqual(r.Validation, expected.Validation) {
		return fmt.Errorf("invalid preview validation status")
	}
	return nil
}
