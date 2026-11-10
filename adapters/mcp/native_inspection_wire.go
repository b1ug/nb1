package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const nativeInspectionContract = "nb1-native-inspection/experimental-v1"
const nativeInspectionLayout = "gid-standard-report-id-slot/v1"
const maxNativeInspectionResponse = 64 << 10
const maxNativeInspectionArguments = 4096

// These are HTTP observation shapes only. No SDK, hardware provider, author
// parser or effect compiler is imported into this independent MCP module.
type nativeRangeWire struct {
	StartLine int `json:"start_line"`
	LineCount int `json:"line_count"`
}
type nativeErrorWire struct {
	Code                       string  `json:"code"`
	Message                    string  `json:"message"`
	MessageTruncated           bool    `json:"message_truncated"`
	MessageUTF8Replaced        bool    `json:"message_utf8_replaced"`
	NativePlatform             *string `json:"native_platform"`
	NativeCode                 *string `json:"native_code"`
	NativePlatformTruncated    bool    `json:"native_platform_truncated"`
	NativePlatformUTF8Replaced bool    `json:"native_platform_utf8_replaced"`
}
type nativeReportWire struct {
	Layout            string `json:"layout"`
	RequestedReportID byte   `json:"requested_report_id"`
	BufferLength      int    `json:"buffer_length"`
	ReadAttempted     bool   `json:"read_attempted"`
	ReturnedLength    int    `json:"returned_length"`
	LengthValid       bool   `json:"length_valid"`
	PrefixTrusted     bool   `json:"prefix_trusted"`
	RawHex            string `json:"raw_hex"`
	UnknownTailHex    string `json:"unknown_tail_hex"`
}
type nativeIssueWire struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
type nativeReferenceWire struct {
	Firmware            int      `json:"firmware"`
	Applicability       string   `json:"applicability"`
	Sources             []string `json:"sources"`
	RowsDecodable       bool     `json:"rows_decodable"`
	PlaybackEndEncoding string   `json:"playback_end_encoding"`
}
type nativeProfileWire struct {
	SDKModel               string              `json:"sdk_model"`
	SDKReferenceFirmware   *int                `json:"sdk_reference_firmware"`
	SDKApplicability       string              `json:"sdk_applicability"`
	RAMLines               *int                `json:"ram_lines"`
	ReadReference          nativeReferenceWire `json:"read_reference"`
	OperationQualification string              `json:"operation_qualification"`
}
type nativeRowWire struct {
	Position      int              `json:"position"`
	RGB8          *[3]byte         `json:"rgb8"`
	DurationTicks *uint16          `json:"duration_ticks"`
	LEDCode       *byte            `json:"led_code"`
	Report        nativeReportWire `json:"report"`
	Error         *nativeErrorWire `json:"error"`
}
type nativeLineWire struct {
	LED        json.RawMessage `json:"led"`
	Color      string          `json:"color"`
	DurationMS int             `json:"duration_ms"`
}
type nativeSequenceWire struct {
	Contract   string           `json:"contract"`
	ColorSpace string           `json:"color_space"`
	Lines      []nativeLineWire `json:"lines"`
}
type nativeRAMWire struct {
	RequestedRange  nativeRangeWire     `json:"requested_range"`
	Rows            []nativeRowWire     `json:"rows"`
	UnreadPositions []int               `json:"unread_positions"`
	Sequence        *nativeSequenceWire `json:"sequence"`
	SequenceDigest  *string             `json:"sequence_digest"`
	DecodingStatus  string              `json:"decoding_status"`
	Issues          []nativeIssueWire   `json:"issues"`
	Deployable      bool                `json:"deployable"`
}
type nativePlaybackFieldsWire struct {
	Mode         *byte `json:"mode"`
	Start        *byte `json:"start"`
	End          *byte `json:"end"`
	Count        *byte `json:"count"`
	NextPosition *byte `json:"next_position"`
}
type nativePlaybackWire struct {
	Fields          nativePlaybackFieldsWire `json:"raw_fields"`
	Report          nativeReportWire         `json:"report"`
	ConfiguredRange *nativeRangeWire         `json:"configured_range"`
	DecodingStatus  string                   `json:"decoding_status"`
	ProfileDigest   string                   `json:"profile_digest"`
	Issues          []nativeIssueWire        `json:"issues"`
	Deployable      bool                     `json:"deployable"`
	Error           *nativeErrorWire         `json:"error"`
}
type nativeIdentityWire struct {
	Serial         string `json:"serial"`
	Product        string `json:"product"`
	Firmware       int    `json:"firmware"`
	FirmwareSource string `json:"firmware_source"`
}
type nativeInspectionWire struct {
	Contract         string              `json:"contract"`
	Kind             string              `json:"kind"`
	DaemonInstanceID string              `json:"daemon_instance_id"`
	DeviceSessionID  string              `json:"device_session_id"`
	Device           nativeIdentityWire  `json:"device"`
	Source           string              `json:"source"`
	Consistency      string              `json:"consistency"`
	StartedAt        string              `json:"started_at"`
	FinishedAt       string              `json:"finished_at"`
	DurationNS       int64               `json:"duration_ns"`
	Status           string              `json:"status"`
	Complete         bool                `json:"complete"`
	Profile          nativeProfileWire   `json:"profile"`
	RAM              *nativeRAMWire      `json:"ram"`
	Playback         *nativePlaybackWire `json:"playback"`
	Error            *nativeErrorWire    `json:"error"`
}

func nativeWireError(field string) error {
	return fmt.Errorf("invalid native inspection response: %s", field)
}

// Unlike existing argument decoders, observations have explicit nullable
// pointers. Require every field before typed decoding can fill missing zeros.
func decodeNativeWire(data []byte, target any) error {
	if len(data) == 0 || len(data) > maxNativeInspectionResponse || !utf8.Valid(data) {
		return nativeWireError("byte limit or UTF-8")
	}
	if err := patternAuthorCheckStrings(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := nativeWireValue(d, reflect.TypeOf(target).Elem(), 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return nativeWireError("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func nativeWireValue(d *json.Decoder, typ reflect.Type, depth int) error {
	if depth > 32 {
		return nativeWireError("depth")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if typ.Kind() == reflect.Ptr {
			return nil
		}
		return nativeWireError("null non-nullable field")
	}
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(json.RawMessage{}) {
		if _, ok := token.(json.Delim); ok {
			return nativeWireError("LED scalar")
		}
		return nil
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			if typ.Kind() != reflect.Struct {
				return nativeWireError("object type")
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
			}
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := t.(string)
				field, known := fields[name]
				if !ok || !known || seen[name] {
					return nativeWireError("duplicate, unknown or noncanonical field")
				}
				seen[name] = true
				if err := nativeWireValue(d, field, depth+1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') || len(seen) != len(fields) {
				return nativeWireError("missing explicit field")
			}
			return nil
		case '[':
			if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
				return nativeWireError("array type")
			}
			count := 0
			for d.More() {
				if err := nativeWireValue(d, typ.Elem(), depth+1); err != nil {
					return err
				}
				count++
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') || typ.Kind() == reflect.Array && count != typ.Len() {
				return nativeWireError("array length")
			}
			return nil
		}
		return nativeWireError("delimiter")
	}
	switch typ.Kind() {
	case reflect.String:
		if _, ok := token.(string); !ok {
			return nativeWireError("string type")
		}
	case reflect.Bool:
		if _, ok := token.(bool); !ok {
			return nativeWireError("boolean type")
		}
	case reflect.Int, reflect.Int64, reflect.Uint8, reflect.Uint16:
		n, ok := token.(json.Number)
		if !ok || !nativeIntegerLexeme(string(n)) {
			return nativeWireError("ordinary integer required")
		}
	default:
		return nativeWireError("scalar type")
	}
	return nil
}

func nativeIntegerLexeme(value string) bool {
	if value == "" || value == "-0" {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func nativeASCII(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, c := range []byte(value) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func nativeDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}
func nativeErrorValid(value *nativeErrorWire) bool {
	if value == nil {
		return true
	}
	if !nativeASCII(value.Code, 64) || len(value.Message) > 1024 || !utf8.ValidString(value.Message) || (value.NativePlatform == nil) != (value.NativeCode == nil) {
		return false
	}
	if value.NativePlatform == nil {
		return !value.NativePlatformTruncated && !value.NativePlatformUTF8Replaced
	}
	if len(*value.NativePlatform) > 64 || !utf8.ValidString(*value.NativePlatform) {
		return false
	}
	n, err := strconv.ParseUint(*value.NativeCode, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == *value.NativeCode
}
func nativeIssuesValid(values []nativeIssueWire) bool {
	if values == nil || len(values) > 128 {
		return false
	}
	for _, v := range values {
		if !nativeASCII(v.Code, 128) || len(v.Path) > 256 || len(v.Message) > 2048 {
			return false
		}
	}
	return true
}
func nativeApplicability(value string) bool {
	return value == "unknown" || value == "unresolved" || value == "model-only" || value == "firmware-matched"
}
func nativeProfileValid(value nativeProfileWire, firmware int) bool {
	if !nativeASCII(value.SDKModel, 64) || value.RAMLines == nil || *value.RAMLines < 1 || *value.RAMLines > 32 || !nativeApplicability(value.SDKApplicability) || value.OperationQualification != "unverified" {
		return false
	}
	if value.SDKReferenceFirmware != nil && (*value.SDKReferenceFirmware < 1 || *value.SDKReferenceFirmware > 65535) {
		return false
	}
	r := value.ReadReference
	if !nativeApplicability(r.Applicability) || r.Sources == nil || len(r.Sources) > 16 || r.PlaybackEndEncoding != "unknown" && r.PlaybackEndEncoding != "exclusive" && r.PlaybackEndEncoding != "inclusive" {
		return false
	}
	for _, s := range r.Sources {
		if len(s) == 0 || len(s) > 2048 {
			return false
		}
	}
	return !(r.Applicability == "firmware-matched" && (r.Firmware != firmware || firmware < 1 || len(r.Sources) == 0)) && !(r.RowsDecodable && r.Applicability != "firmware-matched")
}

func nativeReportData(value nativeReportWire, prefix int) ([]byte, error) {
	if value.Layout != nativeInspectionLayout && value.Layout != "unknown" {
		return nil, nativeWireError("report layout")
	}
	if value.Layout == nativeInspectionLayout && (value.RequestedReportID != 1 || value.BufferLength != 9) {
		return nil, nativeWireError("standard report")
	}
	if !value.PrefixTrusted {
		if value.RawHex != "" || value.UnknownTailHex != "" {
			return nil, nativeWireError("untrusted bytes")
		}
		return nil, nil // Invalid n and other original diagnostics remain intact.
	}
	if !value.ReadAttempted || !value.LengthValid || value.BufferLength < 0 || value.BufferLength > 256 || value.ReturnedLength < 0 || value.ReturnedLength > value.BufferLength || len(value.RawHex) != value.ReturnedLength*2 || strings.ToLower(value.RawHex) != value.RawHex {
		return nil, nativeWireError("trusted prefix length")
	}
	data, err := hex.DecodeString(value.RawHex)
	if err != nil {
		return nil, err
	}
	tail := ""
	if value.Layout == "unknown" {
		tail = value.RawHex
	} else if len(data) > prefix {
		tail = hex.EncodeToString(data[prefix:])
	}
	if tail != value.UnknownTailHex {
		return nil, nativeWireError("unknown tail")
	}
	return data, nil
}

func decodeNativeInspectionResponse(data []byte, serial, kind string, startLine, lineCount int) (nativeInspectionWire, error) {
	var out nativeInspectionWire
	if err := decodeNativeWire(data, &out); err != nil {
		return out, err
	}
	if !nativeSerial(serial) || out.Contract != nativeInspectionContract || out.Kind != kind || out.Device.Serial != serial || out.Device.FirmwareSource != "cached-on-open" || out.Source != "live-native" || out.Consistency != "sequential-best-effort-not-atomic" {
		return out, nativeWireError("request identity or contract")
	}
	if !nativeASCII(out.DaemonInstanceID, 128) || !nativeASCII(out.DeviceSessionID, 128) || len(out.Device.Product) > 512 || out.Device.Firmware < 1 || out.Device.Firmware > 65535 || out.DurationNS < 0 {
		return out, nativeWireError("identity or duration")
	}
	for _, stamp := range []string{out.StartedAt, out.FinishedAt} {
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || parsed.IsZero() {
			return out, nativeWireError("timestamp")
		}
	}
	if !nativeProfileValid(out.Profile, out.Device.Firmware) || !nativeErrorValid(out.Error) || out.Status != "complete" && out.Status != "partial" && out.Status != "failed" || out.Complete != (out.Status == "complete") || out.Complete != (out.Error == nil) {
		return out, nativeWireError("status, profile or error")
	}
	var err error
	switch kind {
	case "ram":
		err = validateNativeRAM(out, startLine, lineCount)
	case "playback":
		err = validateNativePlayback(out)
	default:
		err = nativeWireError("kind")
	}
	return out, err
}

func validateNativeRAM(value nativeInspectionWire, start, count int) error {
	r := value.RAM
	if start < 0 || start >= 32 || count < 1 || count > 32-start || r == nil || value.Playback != nil || r.RequestedRange != (nativeRangeWire{start, count}) || r.Rows == nil || r.UnreadPositions == nil || len(r.Rows) > count || len(r.UnreadPositions) != count-len(r.Rows) || !nativeIssuesValid(r.Issues) || r.Deployable {
		return nativeWireError("RAM envelope or range")
	}
	for i, pos := range r.UnreadPositions {
		if pos != start+len(r.Rows)+i {
			return nativeWireError("unread positions")
		}
	}
	hasBytes, representable := false, value.Complete
	for i, row := range r.Rows {
		if row.Position != start+i || !nativeErrorValid(row.Error) || row.Error != nil && (i != len(r.Rows)-1 || value.Complete) {
			return nativeWireError("row order or error")
		}
		data, err := nativeReportData(row.Report, 8)
		if err != nil {
			return err
		}
		var rgb *[3]byte
		var ticks *uint16
		var led *byte
		if row.Report.PrefixTrusted && row.Report.Layout == nativeInspectionLayout {
			if len(data) >= 5 {
				v := [3]byte{data[2], data[3], data[4]}
				rgb = &v
			}
			if len(data) >= 7 {
				v := uint16(data[5])<<8 | uint16(data[6])
				ticks = &v
			}
			if len(data) >= 8 {
				v := data[7]
				led = &v
			}
		}
		if !reflect.DeepEqual(rgb, row.RGB8) || !reflect.DeepEqual(ticks, row.DurationTicks) || !reflect.DeepEqual(led, row.LEDCode) {
			return nativeWireError("raw row fields")
		}
		full := row.Report.PrefixTrusted && row.Report.Layout == nativeInspectionLayout && len(data) >= 8
		if row.Error == nil && !full {
			return nativeWireError("short row without error")
		}
		hasBytes = hasBytes || len(data) != 0 || row.Error == nil
		representable = representable && full && ticks != nil && *ticks > 0 && led != nil && *led <= 2
	}
	if value.Complete && (len(r.Rows) != count || len(r.UnreadPositions) != 0) || !value.Complete && (value.Status == "partial") != hasBytes {
		return nativeWireError("RAM completeness")
	}
	if !value.Complete || !value.Profile.ReadReference.RowsDecodable || !representable {
		want := "unrepresentable"
		if !value.Profile.ReadReference.RowsDecodable {
			want = "profile-unresolved"
		}
		if !value.Complete {
			want = "incomplete"
		}
		if r.Sequence != nil || r.SequenceDigest != nil || r.DecodingStatus != want {
			return nativeWireError("undecodable sequence")
		}
		return nil
	}
	if r.Sequence == nil || r.SequenceDigest == nil || !nativeDigest(*r.SequenceDigest) || r.DecodingStatus != "exact-known-fields" || r.Sequence.Contract != "nb1-device-sequence/draft-1" || r.Sequence.ColorSpace != "device-rgb8" || len(r.Sequence.Lines) != count {
		return nativeWireError("sequence shape")
	}
	// Integrity of an already decoded observation, not author compilation. No
	// colors, recipes or device execution are generated or substituted here.
	type canonicalLine struct {
		LED        any    `json:"led"`
		Color      string `json:"color"`
		DurationMS int    `json:"duration_ms"`
	}
	canonical := struct {
		Contract   string          `json:"contract"`
		ColorSpace string          `json:"color_space"`
		Lines      []canonicalLine `json:"lines"`
	}{r.Sequence.Contract, r.Sequence.ColorSpace, []canonicalLine{}}
	for i, line := range r.Sequence.Lines {
		row := r.Rows[i]
		var led any = int(*row.LEDCode)
		if *row.LEDCode == 0 {
			led = "all"
		}
		wantLED, _ := json.Marshal(led)
		var suppliedLED any
		decoder := json.NewDecoder(bytes.NewReader(line.LED))
		decoder.UseNumber()
		if decoder.Decode(&suppliedLED) != nil {
			return nativeWireError("sequence LED")
		}
		actualLED, _ := json.Marshal(suppliedLED)
		color := "#" + hex.EncodeToString(row.RGB8[:])
		if !bytes.Equal(wantLED, actualLED) || line.Color != color || line.DurationMS != int(*row.DurationTicks)*10 {
			return nativeWireError("sequence/raw binding")
		}
		canonical.Lines = append(canonical.Lines, canonicalLine{led, line.Color, line.DurationMS})
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	if *r.SequenceDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		return nativeWireError("sequence digest")
	}
	return nil
}

func validateNativePlayback(value nativeInspectionWire) error {
	p := value.Playback
	if p == nil || value.RAM != nil || p.Deployable || !nativeErrorValid(p.Error) || !nativeIssuesValid(p.Issues) {
		return nativeWireError("playback envelope")
	}
	switch p.DecodingStatus {
	case "incomplete", "profile-unresolved", "unrepresentable", "reference-fields-interpreted", "interpretation-failed":
	default:
		return nativeWireError("playback status")
	}
	if (p.ConfiguredRange != nil) != (p.DecodingStatus == "reference-fields-interpreted") {
		return nativeWireError("range/status")
	}
	data, err := nativeReportData(p.Report, 7)
	if err != nil {
		return err
	}
	fields := nativePlaybackFieldsWire{}
	if p.Report.PrefixTrusted && p.Report.Layout == nativeInspectionLayout {
		for i, field := range []**byte{&fields.Mode, &fields.Start, &fields.End, &fields.Count, &fields.NextPosition} {
			if len(data) > i+2 {
				v := data[i+2]
				*field = &v
			}
		}
	}
	if !reflect.DeepEqual(fields, p.Fields) {
		return nativeWireError("raw playback fields")
	}
	if value.Complete {
		if p.Error != nil || !p.Report.PrefixTrusted || p.Report.Layout != nativeInspectionLayout || len(data) < 7 {
			return nativeWireError("complete playback")
		}
	} else if p.Error == nil || (value.Status == "partial") != (p.Report.RawHex != "") {
		return nativeWireError("partial playback")
	}
	if p.ConfiguredRange != nil {
		ref := value.Profile.ReadReference
		if !value.Complete || ref.Applicability != "firmware-matched" || ref.PlaybackEndEncoding == "unknown" || fields.Mode == nil || *fields.Mode > 3 {
			return nativeWireError("unqualified playback range")
		}
		end := int(*fields.End)
		if ref.PlaybackEndEncoding == "inclusive" {
			end++
		}
		want := nativeRangeWire{int(*fields.Start), end - int(*fields.Start)}
		if want.LineCount < 1 || want.StartLine >= 32 || end > *value.Profile.RAMLines || want != *p.ConfiguredRange {
			return nativeWireError("configured range")
		}
	}
	if p.ProfileDigest != "" && !nativeDigest(p.ProfileDigest) {
		return nativeWireError("profile digest")
	}
	return nil
}
