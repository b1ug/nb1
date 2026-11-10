package devicesequence

import "encoding/json"

const (
	RAM             = "ram"
	Flash           = "flash"
	FirmwareMatched = "firmware-matched"
	ModelOnly       = "model-only"
	Unresolved      = "unresolved"
	Unknown         = "unknown"
)

// Capabilities is trusted, server-supplied reference data, never an author JSON
// field. An SDK/provider adapter will supply it; this package has no hardware
// database, device resolver or qualification registry. Sources remain evidence,
// not proof of the actual firmware image or permission to write.
type Capabilities struct {
	Model             string `json:"model"`
	Firmware          int    `json:"firmware"`
	ReferenceFirmware int    `json:"reference_firmware"`
	Applicability     string `json:"applicability"`
	RAMLines          *int   `json:"ram_lines"`
	FlashLines        *int   `json:"flash_lines"`
	UsableFlashLines  *int   `json:"usable_flash_lines"`
	// Nil means the firmware fader's applicable duration bound is unresolved.
	MaxDurationMS *int     `json:"max_duration_ms"`
	Sources       []string `json:"sources"`
}

type Range struct {
	StartLine int `json:"start_line"`
	LineCount int `json:"line_count"`
}

type EncodedRow struct {
	Position      int     `json:"position"`
	LEDCode       byte    `json:"led_code"`
	RGB8          [3]byte `json:"rgb8"`
	DurationTicks uint16  `json:"duration_ticks"`
}

type Placement struct {
	Contract               string       `json:"contract"`
	Target                 string       `json:"target"`
	SequenceDigest         string       `json:"sequence_digest"`
	ProfileDigest          string       `json:"profile_digest"`
	RequiredLines          int          `json:"required_lines"`
	AvailableLines         *int         `json:"available_lines"`
	PhysicalFlashLines     *int         `json:"physical_flash_lines"`
	EncodingStatus         string       `json:"encoding_status"`
	CapacityStatus         string       `json:"capacity_status"`
	FirmwareApplicability  string       `json:"firmware_applicability"`
	OperationQualification string       `json:"operation_qualification"`
	Deployable             bool         `json:"deployable"`
	Range                  *Range       `json:"range"`
	Rows                   []EncodedRow `json:"rows"`
	Issues                 []Issue      `json:"issues"`
}

// PlanPlacementJSON inspects addressability, encoding and target capacity. It
// does not plan a Flash commit or a playback range. It never emits HID reports.
// No hardware qualification registry/executor exists yet, so Deployable remains
// false even when the known encoding, capacity and reference firmware agree.
func PlanPlacementJSON(data []byte, target string, start int, caps Capabilities) (Placement, error) {
	compiled, err := CompileJSON(data)
	if err != nil {
		return Placement{}, err
	}
	if err := validateCapabilities(caps); err != nil {
		return Placement{}, err
	}
	if target != RAM && target != Flash {
		return Placement{}, invalid("invalid_target", "target", "Choose ram or flash explicitly; no automatic fallback is performed.")
	}
	profileJSON, err := json.Marshal(caps)
	if err != nil {
		return Placement{}, err
	}
	result := Placement{Contract: Contract, Target: target, SequenceDigest: compiled.Digest, ProfileDigest: digest(profileJSON), RequiredLines: len(compiled.Sequence.Lines), PhysicalFlashLines: copyInt(caps.FlashLines), EncodingStatus: "exact", CapacityStatus: Unknown, FirmwareApplicability: caps.Applicability, OperationQualification: "unverified", Rows: []EncodedRow{}, Issues: []Issue{}}
	available := caps.RAMLines
	if target == Flash {
		available = caps.UsableFlashLines
	}
	result.AvailableLines = copyInt(available)
	validAddress := start >= 0 && start < MaxLines && len(compiled.Sequence.Lines) <= MaxLines-start
	if !validAddress {
		result.Issues = append(result.Issues, Issue{"range_out_of_bounds", "start_line", "Every absolute position must be within 0..31; firmware may alias invalid positions to line0."})
	}
	if available == nil {
		result.Issues = append(result.Issues, Issue{"capacity_unknown", "target", "The usable range for this target has not been established."})
	} else if !validAddress || start > *available || len(compiled.Sequence.Lines) > *available-start {
		result.CapacityStatus = "exceeded"
		result.Issues = append(result.Issues, Issue{"pattern_capacity_exceeded", "lines", "The complete sequence does not fit the requested usable target range."})
	} else {
		result.CapacityStatus = "fits"
	}
	if validAddress {
		result.Range = &Range{StartLine: start, LineCount: len(compiled.Sequence.Lines)}
		for i, line := range compiled.Sequence.Lines {
			// Color is already canonical Hex6 and no gamma path is involved.
			var rgb [3]byte
			color, _ := colorRGB(line.Color)
			copy(rgb[:], color)
			result.Rows = append(result.Rows, EncodedRow{start + i, byte(line.LED), rgb, uint16(line.DurationMS / TickMS)})
		}
	}
	for i, line := range compiled.Sequence.Lines {
		if caps.MaxDurationMS != nil && line.DurationMS > *caps.MaxDurationMS {
			result.Issues = append(result.Issues, Issue{"firmware_duration_exceeded", linePath(i, "duration_ms"), "The encoded duration exceeds the supplied applicable firmware bound."})
		}
	}
	if caps.MaxDurationMS == nil {
		result.Issues = append(result.Issues, Issue{"firmware_duration_unverified", "lines", "Wire width alone does not establish the firmware fader's usable time range."})
	}
	if caps.Applicability != FirmwareMatched {
		result.Issues = append(result.Issues, Issue{"firmware_applicability_unresolved", "profile", "The reference profile is not matched to the supplied firmware version."})
	}
	result.Issues = append(result.Issues, Issue{"operation_unverified", "qualification", "This offline model does not establish device operation qualification or authorize execution."})
	if target == Flash {
		result.Issues = append(result.Issues, Issue{"persistence_plan_required", "target", "A Flash commit needs a separate complete affected-image plan, including collateral data."})
	}
	return result, nil
}

type Repeat struct {
	Mode  string `json:"mode"`
	Count *int   `json:"count,omitempty"`
}

type PlaybackPlan struct {
	ProfileDigest string `json:"profile_digest"`
	Range         Range  `json:"range"`
	Repeat        Repeat `json:"repeat"`
	// These are fields for an eventual adapter, not a ready-to-send report.
	InclusiveEnd  *int    `json:"inclusive_end"`
	WireCount     *byte   `json:"wire_count"`
	Representable bool    `json:"representable"`
	Deployable    bool    `json:"deployable"`
	Issues        []Issue `json:"issues"`
}

// PlanPlayback is separate from placement: uploading line0 is valid, while
// playing line0 alone is ambiguous in the historic p end=0 protocol.
func PlanPlayback(r Range, repeat Repeat, caps Capabilities) (PlaybackPlan, error) {
	if err := validateCapabilities(caps); err != nil {
		return PlaybackPlan{}, err
	}
	profileJSON, err := json.Marshal(caps)
	if err != nil {
		return PlaybackPlan{}, err
	}
	out := PlaybackPlan{ProfileDigest: digest(profileJSON), Range: r, Repeat: Repeat{repeat.Mode, copyInt(repeat.Count)}, Issues: []Issue{}}
	valid := validRange(r)
	if !valid {
		out.Issues = append(out.Issues, Issue{"range_out_of_bounds", "range", "Playback must select 1..32 lines entirely within positions0..31."})
	}
	if caps.RAMLines == nil {
		out.Issues = append(out.Issues, Issue{"capacity_unknown", "range", "RAM capacity is unresolved."})
	} else if valid && (r.StartLine > *caps.RAMLines || r.LineCount > *caps.RAMLines-r.StartLine) {
		out.Issues = append(out.Issues, Issue{"pattern_capacity_exceeded", "range", "Playback exceeds the known RAM range."})
	}
	if valid && r.StartLine == 0 && r.LineCount == 1 {
		out.Issues = append(out.Issues, Issue{"single_line_zero_range_ambiguous", "range", "Protocol end0 selects the full capacity; choose an explicit different placement, without silent relocation."})
	}
	var count byte
	switch repeat.Mode {
	case "forever":
		if repeat.Count != nil {
			out.Issues = append(out.Issues, Issue{"invalid_repeat", "repeat.count", "Forever does not accept a count."})
		}
	case "count":
		if repeat.Count == nil || *repeat.Count < 1 || *repeat.Count > 255 {
			out.Issues = append(out.Issues, Issue{"invalid_repeat", "repeat.count", "A finite count must be within 1..255."})
		} else {
			count = byte(*repeat.Count)
		}
	default:
		out.Issues = append(out.Issues, Issue{"invalid_repeat", "repeat.mode", "Choose count or forever explicitly."})
	}
	out.Representable = len(out.Issues) == 0
	if out.Representable {
		end := r.StartLine + r.LineCount - 1
		out.InclusiveEnd, out.WireCount = &end, &count
	}
	if caps.Applicability != FirmwareMatched {
		out.Issues = append(out.Issues, Issue{"firmware_applicability_unresolved", "profile", "Firmware applicability is unresolved."})
	}
	out.Issues = append(out.Issues, Issue{"operation_unverified", "qualification", "No hardware playback qualification is established by this pure plan."})
	return out, nil
}

func validateCapabilities(c Capabilities) error {
	for _, n := range []*int{c.RAMLines, c.FlashLines, c.UsableFlashLines} {
		if n != nil && (*n < 0 || *n > MaxLines) {
			return invalid("invalid_profile", "profile", "The draft only models capacities within0..32.")
		}
	}
	if c.UsableFlashLines != nil && (c.FlashLines == nil || *c.UsableFlashLines > *c.FlashLines) {
		return invalid("invalid_profile", "profile", "Usable Flash capacity needs a compatible physical capacity.")
	}
	if c.MaxDurationMS != nil && (*c.MaxDurationMS < TickMS || *c.MaxDurationMS > WireMaxDurationMS || *c.MaxDurationMS%TickMS != 0) {
		return invalid("invalid_profile", "profile", "The firmware time bound must be positive, tick-aligned and within the wire range.")
	}
	switch c.Applicability {
	case FirmwareMatched, ModelOnly, Unresolved, Unknown:
	default:
		return invalid("invalid_profile", "profile", "An explicit applicability status is required.")
	}
	if c.Applicability == FirmwareMatched && (c.Firmware < 1 || c.ReferenceFirmware != c.Firmware || len(c.Sources) == 0) {
		return invalid("invalid_profile", "profile", "Matched applicability requires the same nonzero reference version and source evidence.")
	}
	return nil
}

func validRange(r Range) bool {
	return r.StartLine >= 0 && r.StartLine < MaxLines && r.LineCount > 0 && r.LineCount <= MaxLines-r.StartLine
}
func copyInt(n *int) *int {
	if n == nil {
		return nil
	}
	v := *n
	return &v
}
