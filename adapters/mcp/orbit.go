package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const orbitLightContract = "nb1-light-action/experimental-v2"
const frameCompileContract = "nb1-frame-program/experimental-v1"

// Independent wire descriptions only. No core, SDK, HID or CLI dependency.
type orbitActionWire struct {
	Kind     string            `json:"kind"`
	Colors   []json.RawMessage `json:"colors,omitempty"`
	LEDs     []int             `json:"leds,omitempty"`
	PeriodMS *int              `json:"period_ms,omitempty"`
	Cycles   *int              `json:"cycles,omitempty"`
	Segments *int              `json:"segments,omitempty"`
	OnFinish string            `json:"on_finish,omitempty"`
}
type frameTargetWire struct {
	LED   int    `json:"led"`
	Color string `json:"color"`
}
type frameWire struct {
	Targets    []frameTargetWire `json:"targets"`
	FadeMS     int               `json:"fade_ms"`
	IntervalMS int               `json:"interval_ms"`
}
type frameProgramWire struct {
	Contract string            `json:"contract"`
	Initial  []frameTargetWire `json:"initial"`
	Frames   []frameWire       `json:"frames"`
	Repeat   int               `json:"repeat"`
	OnFinish string            `json:"on_finish"`
}
type frameExecutionWire struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}
type frameDeviceWire struct {
	Serial     string `json:"serial"`
	Connection string `json:"connection"`
}
type framePreviewWire struct {
	Contract             string             `json:"contract"`
	Program              frameProgramWire   `json:"program"`
	TotalFrames          int                `json:"total_frames"`
	StoredTargetWrites   int                `json:"stored_target_writes"`
	PlannedTargetWrites  int                `json:"planned_target_writes"`
	CycleDurationMS      int64              `json:"cycle_duration_ms"`
	NominalInitialMS     int64              `json:"nominal_initial_ms"`
	NominalTailMS        int64              `json:"nominal_tail_ms"`
	NominalDurationMS    int64              `json:"nominal_duration_ms"`
	TimingBasis          string             `json:"timing_basis"`
	RequiredCapabilities []string           `json:"required_capabilities"`
	Execution            frameExecutionWire `json:"execution"`
	Device               *frameDeviceWire   `json:"device,omitempty"`
}
type orbitResultWire struct {
	SchemaVersion          int              `json:"schema_version"`
	Contract               string           `json:"contract"`
	ProgramCompileContract string           `json:"program_compile_contract"`
	RecipeID               string           `json:"recipe_id"`
	Action                 orbitActionWire  `json:"action"`
	Program                frameProgramWire `json:"program"`
	NominalDurationMS      int64            `json:"nominal_duration_ms"`
	TotalFrames            int              `json:"total_frames"`
	PlannedTargetWrites    int              `json:"planned_target_writes"`
	RequiredCapabilities   []string         `json:"required_capabilities"`
}
type orbitPreviewWire struct {
	Result  orbitResultWire  `json:"result"`
	Preview framePreviewWire `json:"preview"`
}
type frameJobWire struct {
	ID                    string           `json:"id"`
	Device                string           `json:"device"`
	Status                string           `json:"status"`
	Program               frameProgramWire `json:"program"`
	CompletedFrames       int              `json:"completed_frames"`
	TotalFrames           int              `json:"total_frames"`
	CompletedTargetWrites int              `json:"completed_target_writes"`
	PlannedTargetWrites   int              `json:"planned_target_writes"`
	CreatedAt             time.Time        `json:"created_at"`
	StartedAt             *time.Time       `json:"started_at,omitempty"`
	FinishedAt            *time.Time       `json:"finished_at,omitempty"`
	Error                 string           `json:"error,omitempty"`
}

func validFrameSerial(serial string) bool {
	if len(serial) != 8 {
		return false
	}
	_, err := hex.DecodeString(serial)
	return err == nil
}

func normalizedOrbitStructure(raw json.RawMessage) (orbitActionWire, error) {
	var a orbitActionWire
	if err := decodeSoftwareWire(raw, &a); err != nil {
		return a, err
	}
	if err := validateLightUnicodeEscapes(raw); err != nil {
		return a, err
	}
	if a.Kind != "orbit" {
		return a, errors.New("experimental-v2 supports only kind orbit")
	}
	var err error
	a.Segments, err = lightNumber(a.Segments, 4, 4, 8, 1)
	if err != nil || *a.Segments != 4 && *a.Segments != 8 {
		return a, errors.New("Orbit segments must be 4 or 8")
	}
	a.PeriodMS, err = lightNumber(a.PeriodMS, 3000, *a.Segments*50, *a.Segments*600000, 1)
	if err != nil {
		return a, err
	}
	a.Cycles, err = lightNumber(a.Cycles, 1, 1, 1024 / *a.Segments, 1)
	if err != nil {
		return a, err
	}
	if a.OnFinish == "" {
		a.OnFinish = "off"
	}
	if a.OnFinish != "off" && a.OnFinish != "hold" {
		return a, errors.New("Orbit on_finish must be off or hold")
	}
	if a.LEDs == nil {
		a.LEDs = []int{1, 2}
	}
	if len(a.LEDs) != 2 || !(a.LEDs[0] == 1 && a.LEDs[1] == 2 || a.LEDs[0] == 2 && a.LEDs[1] == 1) {
		return a, errors.New("Orbit leds must be [1,2] or [2,1]")
	}
	if a.Colors == nil {
		a.Colors = []json.RawMessage{json.RawMessage(`"#ff0000"`), json.RawMessage(`"#0000ff"`)}
	}
	if len(a.Colors) != 2 {
		return a, errors.New("Orbit requires exactly two colors")
	}
	for _, color := range a.Colors {
		if err := validateOrbitColor(color); err != nil {
			return a, err
		}
	}
	// The daemon remains the compiler. Bound the declared nominal schedule,
	// without generating any targets or a second copy of its curve.
	last := *a.PeriodMS - *a.PeriodMS*(*a.Segments-1) / *a.Segments
	tail := int64(20 + ((last+9)/10)*10 - last)
	if int64(*a.PeriodMS)*int64(*a.Cycles)+20+tail > 86400000 {
		return a, errors.New("Orbit nominal duration exceeds 24 hours")
	}
	return a, nil
}

func validateOrbitColor(raw json.RawMessage) error {
	if len(raw) > maxColorRequestBytes {
		return errors.New("Orbit color exceeds 4096 bytes")
	}
	kind, _, err := colorInputBinding(raw)
	if err != nil {
		return err
	}
	if kind != "rgb8" && kind != "hsv" {
		return nil // Names/hex are validated by the daemon's versioned resolver.
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 4 {
		return errors.New("color objects require exactly four fields")
	}
	keys, max := []string{"r", "g", "b"}, float64(255)
	if kind == "hsv" {
		keys, max = []string{"h_deg", "s_pct", "v_pct"}, 100
	}
	for i, key := range keys {
		s := string(fields[key])
		parts := strings.Split(s, ".")
		if len(parts) > 2 || len(parts[0]) < 1 || len(parts[0]) > 3 || len(parts[0]) > 1 && parts[0][0] == '0' || kind == "rgb8" && len(parts) != 1 || len(parts) == 2 && (len(parts[1]) < 1 || len(parts[1]) > 6) {
			return errors.New("color channels require ordinary bounded decimal syntax")
		}
		for _, part := range parts {
			for _, c := range part {
				if c < '0' || c > '9' {
					return errors.New("color channels require ordinary nonnegative decimals")
				}
			}
		}
		bound := max
		if kind == "hsv" && i == 0 {
			bound = 360
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || n < 0 || n > bound {
			return errors.New("color channel is outside its range")
		}
	}
	return nil
}

func validateOrbitInput(args lightPreviewArgs) error {
	if args.Contract != orbitLightContract || args.Serial != nil && !validFrameSerial(*args.Serial) {
		return errors.New("unsupported Orbit contract or invalid serial")
	}
	_, err := normalizedOrbitStructure(args.Action)
	return err
}

type frameFactsWire struct {
	frames, storedWrites, writes   int
	cycle, initial, tail, duration int64
}

func validateFrameProgram(p frameProgramWire) (frameFactsWire, error) {
	var facts frameFactsWire
	if p.Contract != frameCompileContract || len(p.Initial) < 1 || len(p.Initial) > 2 || len(p.Frames) < 1 || len(p.Frames) > 1024 || p.Repeat < 1 || p.Repeat > 10000 || p.OnFinish != "off" && p.OnFinish != "hold" {
		return facts, errors.New("invalid frame program contract or limits")
	}
	validateTargets := func(targets []frameTargetWire, initial []frameTargetWire) error {
		if len(targets) < 1 || len(targets) > 2 || initial != nil && len(targets) != len(initial) {
			return errors.New("invalid frame target count")
		}
		seen := [3]bool{}
		for i, target := range targets {
			if target.LED < 1 || target.LED > 2 || seen[target.LED] || initial != nil && target.LED != initial[i].LED {
				return errors.New("frame changed the declared LED order")
			}
			seen[target.LED] = true
			if len(target.Color) != 7 || target.Color[0] != '#' || target.Color != strings.ToLower(target.Color) {
				return errors.New("frame color must be canonical raw RGB6")
			}
			if _, err := hex.DecodeString(target.Color[1:]); err != nil {
				return errors.New("invalid frame RGB6")
			}
		}
		return nil
	}
	if err := validateTargets(p.Initial, nil); err != nil {
		return facts, err
	}
	facts.storedWrites = len(p.Initial)
	for _, frame := range p.Frames {
		if err := validateTargets(frame.Targets, p.Initial); err != nil {
			return facts, err
		}
		if frame.IntervalMS < 50 || frame.IntervalMS > 600000 || frame.FadeMS < 0 || frame.FadeMS > 600000 || frame.FadeMS%10 != 0 || frame.FadeMS > ((frame.IntervalMS+9)/10)*10 {
			return facts, errors.New("invalid frame fade or interval")
		}
		facts.cycle += int64(frame.IntervalMS)
		facts.storedWrites += len(frame.Targets)
	}
	facts.frames = len(p.Frames) * p.Repeat
	facts.writes = len(p.Initial) + (facts.storedWrites-len(p.Initial))*p.Repeat
	facts.initial = int64(len(p.Initial) * 10)
	last := p.Frames[len(p.Frames)-1]
	facts.tail = int64((len(last.Targets)-1)*10 + last.FadeMS + 10 - last.IntervalMS)
	if facts.tail < 0 {
		facts.tail = 0
	}
	facts.duration = facts.initial + facts.cycle*int64(p.Repeat) + facts.tail
	if facts.frames > 1024 || facts.writes > 2050 || facts.duration > 86400000 {
		return facts, errors.New("frame program exceeds executed frame, write or duration budget")
	}
	return facts, nil
}

func validateOrbitPreview(data []byte, input any) (any, error) {
	args, ok := input.(lightPreviewArgs)
	if !ok || args.Contract != orbitLightContract {
		return nil, errors.New("missing original Orbit preview input")
	}
	expected, err := normalizedOrbitStructure(args.Action)
	if err != nil {
		return nil, err
	}
	var value orbitPreviewWire
	if err := decodeSoftwareWire(data, &value); err != nil {
		return nil, err
	}
	r, p := value.Result, value.Preview
	if r.SchemaVersion != 1 || r.Contract != orbitLightContract || r.ProgramCompileContract != frameCompileContract || r.RecipeID != fmt.Sprintf("orbit-paired-linear-%d/experimental-v1", *expected.Segments) {
		return nil, errors.New("unsupported Orbit recipe or compile contract")
	}
	if err := bindOrbitAction(expected, r.Action); err != nil {
		return nil, err
	}
	facts, err := validateFrameProgram(r.Program)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(r.Program, p.Program) || p.Contract != frameCompileContract || r.TotalFrames != facts.frames || p.TotalFrames != facts.frames || r.PlannedTargetWrites != facts.writes || p.PlannedTargetWrites != facts.writes || p.StoredTargetWrites != facts.storedWrites || r.NominalDurationMS != facts.duration || p.NominalDurationMS != facts.duration || p.CycleDurationMS != facts.cycle || p.NominalInitialMS != facts.initial || p.NominalTailMS != facts.tail || p.TimingBasis != "host-nominal-excludes-native-io" {
		return nil, errors.New("Orbit result and frame preview disagree")
	}
	caps := []string{"set", "raw-rgb", "frame-program-experimental-v1"}
	if r.Program.OnFinish == "hold" {
		caps = append(caps, "hold")
	}
	if !reflect.DeepEqual(r.RequiredCapabilities, caps) || !reflect.DeepEqual(p.RequiredCapabilities, caps) {
		return nil, errors.New("Orbit required capabilities changed")
	}
	if p.Execution.Reason == "" || args.Serial == nil && (p.Device != nil || p.Execution.Available) || args.Serial != nil && (p.Device == nil || p.Device.Serial != *args.Serial || p.Device.Connection == "") || p.Execution.Available && (p.Device.Connection != "online" || p.Execution.Reason != "cached_profile_supported") {
		return nil, errors.New("Orbit preview device or execution check is not bound")
	}
	if err := validateOrbitGeometry(r.Action, r.Program, facts); err != nil {
		return nil, err
	}
	return value, nil
}

func bindOrbitAction(expected, actual orbitActionWire) error {
	if len(actual.Colors) != 2 {
		return errors.New("Orbit normalized colors missing")
	}
	for i, original := range expected.Colors {
		_, expectedHex, err := colorInputBinding(original)
		if err != nil {
			return err
		}
		kind, got, err := colorInputBinding(actual.Colors[i])
		if err != nil || kind != "hex6" || !bytes.Equal(actual.Colors[i], []byte(strconvQuote(got))) || expectedHex != "" && expectedHex != got {
			return errors.New("Orbit normalized color is noncanonical or changes explicit RGB input")
		}
	}
	expected.Colors, actual.Colors = nil, nil
	if !reflect.DeepEqual(expected, actual) {
		return errors.New("Orbit action changed selectors, timing or defaults")
	}
	return nil
}

func validateOrbitGeometry(a orbitActionWire, p frameProgramWire, facts frameFactsWire) error {
	if len(p.Initial) != 2 || len(p.Frames) != *a.Segments || p.Repeat != *a.Cycles || p.OnFinish != a.OnFinish || facts.cycle != int64(*a.PeriodMS) {
		return errors.New("Orbit frame geometry changed")
	}
	for i, target := range p.Initial {
		if target.LED != a.LEDs[i] || p.Frames[len(p.Frames)-1].Targets[i] != target || p.Frames[len(p.Frames)/2-1].Targets[i].Color != p.Initial[1-i].Color {
			return errors.New("Orbit LED selectors or closed-cycle color anchors changed")
		}
	}
	for i, frame := range p.Frames {
		interval := int(int64(i+1)*int64(*a.PeriodMS)/int64(*a.Segments) - int64(i)*int64(*a.PeriodMS)/int64(*a.Segments))
		if frame.IntervalMS != interval || frame.FadeMS != ((interval+9)/10)*10 {
			return errors.New("Orbit phase timing is not the advertised long-fade geometry")
		}
	}
	return nil
}

func runOrbit(ctx context.Context, d *daemon, args lightRunArgs, ticket *requestTicket) (*mcp.CallToolResult, output, error) {
	previewArgs := lightPreviewArgs{Contract: args.Contract, Action: args.Action, Serial: &args.Serial}
	if err := validateOrbitInput(previewArgs); err != nil {
		return invalid(err.Error())
	}
	preflight := d.requestSoftware(ctx, http.MethodPost, "/v2/light/preview", "orbit_preview", previewArgs)
	if preflight.Error != nil {
		return result(preserveTicket(preflight, ticket))
	}
	prepared := preflight.Data.(orbitPreviewWire)
	if !prepared.Preview.Execution.Available {
		return result(preserveTicket(errorOutput("device_not_supported", "Orbit preview did not confirm frame execution; no execution POST attempted", 0, false), ticket))
	}
	request := lightSubmission{args.Contract, args.Action, "reject", args.RequestID, args.ExpectedDaemonInstanceID}
	out := d.requestBound(ctx, http.MethodPost, "/v2/devices/"+url.PathEscape(args.Serial)+"/light", "frame_job", request, &receiptBinding{serial: args.Serial, ticket: *ticket})
	out = preserveTicket(out, ticket)
	if out.Error == nil {
		job, ok := out.Data.(frameJobWire)
		if !ok || !reflect.DeepEqual(job.Program, prepared.Result.Program) || job.TotalFrames != prepared.Result.TotalFrames || job.PlannedTargetWrites != prepared.Result.PlannedTargetWrites {
			return result(preserveTicket(errorOutput("invalid_daemon_response", "accepted Orbit Job changed its preview program or counts", http.StatusAccepted, true), ticket))
		}
	}
	return result(out)
}

func validateFrameJob(job frameJobWire) error {
	facts, err := validateFrameProgram(job.Program)
	if err != nil {
		return err
	}
	if !validLowerHexID(job.ID, "framejob_") || !validFrameSerial(job.Device) || job.CreatedAt.IsZero() || job.TotalFrames != facts.frames || job.PlannedTargetWrites != facts.writes || job.CompletedFrames < 0 || job.CompletedFrames > facts.frames || job.CompletedTargetWrites < 0 || job.CompletedTargetWrites > facts.writes {
		return errors.New("FrameJob identity, program or counts are invalid")
	}
	// Completed frames require all of their target writes. One subsequent
	// frame may be in flight, but no later frame runs ahead of that callback.
	targets := len(job.Program.Initial)
	minimumWrites := 0
	if job.CompletedFrames > 0 {
		minimumWrites = targets + job.CompletedFrames*targets
	}
	maximumWrites := targets + (job.CompletedFrames+1)*targets
	if maximumWrites > facts.writes {
		maximumWrites = facts.writes
	}
	if job.CompletedTargetWrites < minimumWrites || job.CompletedTargetWrites > maximumWrites {
		return errors.New("FrameJob write progress contradicts completed frames")
	}
	if job.StartedAt != nil && job.StartedAt.Before(job.CreatedAt) || job.FinishedAt != nil && job.FinishedAt.Before(job.CreatedAt) || job.StartedAt != nil && job.FinishedAt != nil && job.FinishedAt.Before(*job.StartedAt) {
		return errors.New("FrameJob timestamps are inconsistent")
	}
	switch job.Status {
	case "queued":
		if job.StartedAt != nil || job.FinishedAt != nil || job.CompletedFrames != 0 || job.CompletedTargetWrites != 0 {
			return errors.New("queued FrameJob already reports execution")
		}
	case "running", "cancelling":
		if job.StartedAt == nil || job.FinishedAt != nil {
			return errors.New("active FrameJob has invalid timestamps")
		}
	case "succeeded":
		if job.StartedAt == nil || job.FinishedAt == nil || job.CompletedFrames != facts.frames || job.CompletedTargetWrites != facts.writes || job.Error != "" {
			return errors.New("successful FrameJob is not complete")
		}
	case "failed", "cancelled":
		if job.FinishedAt == nil {
			return errors.New("terminal FrameJob lacks finished_at")
		}
	default:
		return errors.New("unknown FrameJob status")
	}
	return nil
}

func frameReceiptPath(binding receiptBinding) string {
	return "/v1/devices/" + url.PathEscape(binding.serial) + "/frame-requests/" + url.PathEscape(binding.ticket.RequestID) + "?expected_daemon_instance_id=" + url.QueryEscape(binding.ticket.ExpectedDaemonInstanceID)
}

func frameResponse(method, path string, status int, headers http.Header, data []byte, binding *receiptBinding) output {
	var job frameJobWire
	if err := decodeSoftwareWire(data, &job); err != nil {
		return errorOutput("invalid_daemon_response", err.Error(), status, method == http.MethodPost || binding != nil)
	}
	if err := validateFrameJob(job); err != nil {
		return errorOutput("invalid_daemon_response", err.Error(), status, method == http.MethodPost || binding != nil)
	}
	wantStatus := http.StatusOK
	if method == http.MethodPost {
		wantStatus = http.StatusAccepted
	}
	if status != wantStatus {
		return errorOutput("invalid_daemon_response", "FrameJob response status changed", status, method == http.MethodPost || binding != nil)
	}
	if binding != nil {
		ids := headers.Values("X-NB1-Daemon-Instance-ID")
		if len(ids) != 1 || ids[0] != binding.ticket.ExpectedDaemonInstanceID || job.Device != binding.serial {
			return errorOutput("invalid_daemon_response", "FrameJob receipt does not bind the original daemon and device", status, true)
		}
		if method == http.MethodPost {
			replayed, location := headers.Values("X-NB1-Replayed"), headers.Values("Location")
			if len(replayed) != 1 || replayed[0] != "true" && replayed[0] != "false" || len(location) != 1 || location[0] != frameReceiptPath(*binding) {
				return errorOutput("invalid_daemon_response", "FrameJob receipt metadata changed", status, true)
			}
		}
	} else {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/frame-jobs/"), "/cancel")
		if job.ID != id {
			return errorOutput("invalid_daemon_response", "FrameJob response changed the requested ID", status, method == http.MethodPost)
		}
	}
	out := output{Kind: "job", Data: job, Accepted: status == http.StatusAccepted}
	if job.Status == "queued" || job.Status == "running" || job.Status == "cancelling" {
		out.PollAfterMS = 200
	}
	return out
}
