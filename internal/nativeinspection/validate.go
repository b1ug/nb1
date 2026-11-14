package nativeinspection

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/devicesequence"
)

const MaxResponseBytes = 64 << 10

func invalidResult(field string) error {
	return fmt.Errorf("invalid native inspection response: %s", field)
}

func ascii(value string, max int) bool {
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

func validError(value *ErrorInfo) bool {
	if value == nil {
		return true
	}
	if !ascii(value.Code, 64) || len(value.Message) > 1024 || !utf8.ValidString(value.Message) {
		return false
	}
	if (value.NativePlatform == nil) != (value.NativeCode == nil) {
		return false
	}
	if value.NativePlatform != nil {
		if len(*value.NativePlatform) > 64 || !utf8.ValidString(*value.NativePlatform) {
			return false
		}
		code, err := strconv.ParseUint(*value.NativeCode, 10, 64)
		if err != nil || strconv.FormatUint(code, 10) != *value.NativeCode {
			return false
		}
	} else if value.NativePlatformTruncated || value.NativePlatformUTF8Replaced {
		return false
	}
	return true
}

func validIssues(values []devicesequence.Issue) bool {
	if values == nil || len(values) > 128 {
		return false
	}
	for _, issue := range values {
		if !ascii(issue.Code, 128) || len(issue.Path) > 256 || len(issue.Message) > 2048 || !utf8.ValidString(issue.Path) || !utf8.ValidString(issue.Message) {
			return false
		}
	}
	return true
}

func validateReport(value Report, prefix int) ([]byte, error) {
	if value.Layout != "unknown" && value.Layout != devicesequence.GIDStandardLayout {
		return nil, invalidResult("report.layout")
	}
	if value.Layout == devicesequence.GIDStandardLayout && (value.RequestedReportID != 1 || value.BufferLength != 9) {
		return nil, invalidResult("report standard layout")
	}
	if !value.PrefixTrusted {
		if value.RawHex != "" || value.UnknownTailHex != "" {
			return nil, invalidResult("untrusted prefix has bytes")
		}
		return nil, nil
	}
	if !value.ReadAttempted || !value.LengthValid || value.BufferLength < 0 || value.BufferLength > devicesequence.MaxObservedReportBytes || value.ReturnedLength < 0 || value.ReturnedLength > value.BufferLength || len(value.RawHex) != value.ReturnedLength*2 || value.RawHex != strings.ToLower(value.RawHex) {
		return nil, invalidResult("trusted prefix length")
	}
	data, err := hex.DecodeString(value.RawHex)
	if err != nil {
		return nil, invalidResult("raw_hex")
	}
	wantTail := ""
	if value.Layout == "unknown" {
		wantTail = value.RawHex
	} else if len(data) > prefix {
		wantTail = hex.EncodeToString(data[prefix:])
	}
	if wantTail != value.UnknownTailHex {
		return nil, invalidResult("unknown_tail_hex")
	}
	return data, nil
}

func validProfile(value Profile, firmware int) bool {
	if !ascii(value.SDKModel, 64) || value.RAMLines == nil || *value.RAMLines < 1 || *value.RAMLines > 32 || value.OperationQualification != "unverified" {
		return false
	}
	if value.SDKReferenceFirmware != nil && (*value.SDKReferenceFirmware < 1 || *value.SDKReferenceFirmware > 65535) {
		return false
	}
	switch value.SDKApplicability {
	case "unknown", "unresolved", "model-only", "firmware-matched":
	default:
		return false
	}
	ref := value.ReadReference
	switch ref.Applicability {
	case "unknown", "unresolved", "model-only", "firmware-matched":
	default:
		return false
	}
	if ref.PlaybackEndEncoding != "unknown" && ref.PlaybackEndEncoding != "exclusive" && ref.PlaybackEndEncoding != "inclusive" {
		return false
	}
	if ref.Sources == nil || len(ref.Sources) > 16 {
		return false
	}
	for _, source := range ref.Sources {
		if len(source) == 0 || len(source) > 2048 || !utf8.ValidString(source) {
			return false
		}
	}
	if ref.Applicability == "firmware-matched" && (ref.Firmware != firmware || firmware < 1 || len(ref.Sources) == 0) {
		return false
	}
	if ref.RowsDecodable && ref.Applicability != "firmware-matched" {
		return false
	}
	return true
}

// ValidateResult verifies the public envelope independently from private SDK
// types. It is not authentication of a real device, firmware or source evidence.
func ValidateResult(value Result, serial string, request Request) error {
	if request.Validate() != nil || len(serial) != 8 {
		return invalidResult("request")
	}
	if _, err := hex.DecodeString(serial); err != nil {
		return invalidResult("serial")
	}
	if value.Contract != Contract || value.Kind != request.Kind || value.Device.Serial != serial || value.Device.FirmwareSource != "cached-on-open" || value.Source != LiveNative || value.Consistency != NonAtomic {
		return invalidResult("identity or contract")
	}
	if !ascii(value.DaemonInstanceID, 128) || !ascii(value.DeviceSessionID, 128) || value.StartedAt.IsZero() || value.FinishedAt.IsZero() || value.DurationNS < 0 {
		return invalidResult("identity or time")
	}
	if len(value.Device.Product) > 512 || !utf8.ValidString(value.Device.Product) || value.Device.Firmware < 1 || value.Device.Firmware > 65535 || !validProfile(value.Profile, value.Device.Firmware) || !validError(value.Error) {
		return invalidResult("profile or error")
	}
	if value.Status != "complete" && value.Status != "partial" && value.Status != "failed" {
		return invalidResult("status")
	}
	if value.Complete != (value.Status == "complete") || value.Complete != (value.Error == nil) {
		return invalidResult("completeness or error")
	}
	if request.Kind == RAM {
		return validateRAM(value, request)
	}
	return validatePlayback(value)
}

func validateRAM(value Result, request Request) error {
	ram := value.RAM
	if ram == nil || value.Playback != nil || ram.RequestedRange != *request.Range || ram.Rows == nil || ram.UnreadPositions == nil || len(ram.Rows) > request.Range.LineCount || !validIssues(ram.Issues) || ram.Deployable {
		return invalidResult("ram envelope")
	}
	if len(ram.UnreadPositions) != request.Range.LineCount-len(ram.Rows) {
		return invalidResult("unread positions")
	}
	for index, position := range ram.UnreadPositions {
		if position != request.Range.StartLine+len(ram.Rows)+index {
			return invalidResult("unread position order")
		}
	}
	snapshot := devicesequence.Snapshot{RequestedRange: *request.Range, Complete: value.Complete, Rows: []devicesequence.RowObservation{}}
	hasBytes := false
	for index, row := range ram.Rows {
		if row.Position != request.Range.StartLine+index || !validError(row.Error) || row.Error != nil && (index != len(ram.Rows)-1 || value.Complete) {
			return invalidResult("row order or error")
		}
		data, err := validateReport(row.Report, 8)
		if err != nil {
			return err
		}
		var rgb *[3]byte
		var ticks *uint16
		var led *byte
		if row.Report.PrefixTrusted && row.Report.Layout == devicesequence.GIDStandardLayout {
			rgb, ticks, led = lineFields(data)
		}
		if !reflect.DeepEqual(rgb, row.RGB8) || !reflect.DeepEqual(ticks, row.DurationTicks) || !reflect.DeepEqual(led, row.LEDCode) {
			return invalidResult("raw row fields")
		}
		full := row.Report.PrefixTrusted && row.Report.Layout == devicesequence.GIDStandardLayout && len(data) >= 8
		if row.Error == nil && !full {
			return invalidResult("incomplete row without error")
		}
		hasBytes = hasBytes || row.Report.RawHex != "" || row.Error == nil
		marker := ""
		if row.Error != nil {
			marker = "read_failed"
		}
		snapshot.Rows = append(snapshot.Rows, devicesequence.RowObservation{Position: row.Position, Layout: row.Report.Layout, ReadAttempted: row.Report.ReadAttempted, ReturnedLength: row.Report.ReturnedLength, Response: data, ReadError: marker})
	}
	if value.Complete && (len(ram.Rows) != request.Range.LineCount || len(ram.UnreadPositions) != 0) {
		return invalidResult("complete range")
	}
	if !value.Complete && (value.Status == "partial") != hasBytes {
		return invalidResult("partial status")
	}
	if !value.Profile.ReadReference.RowsDecodable {
		want := "profile-unresolved"
		if !value.Complete {
			want = "incomplete"
		}
		if ram.Sequence != nil || ram.SequenceDigest != nil || ram.DecodingStatus != want {
			return invalidResult("unresolved row interpretation")
		}
		return nil
	}
	inspection, err := devicesequence.InspectSnapshot(snapshot)
	if err != nil {
		return invalidResult("row interpretation")
	}
	if !reflect.DeepEqual(ram.Sequence, inspection.Sequence) || !reflect.DeepEqual(ram.SequenceDigest, inspection.SequenceDigest) || ram.DecodingStatus != inspection.DecodingStatus {
		return invalidResult("sequence fields or digest")
	}
	return nil
}

func validatePlayback(value Result) error {
	playback := value.Playback
	if playback == nil || value.RAM != nil || !validError(playback.Error) || !validIssues(playback.Issues) || playback.Deployable {
		return invalidResult("playback envelope")
	}
	switch playback.DecodingStatus {
	case "incomplete", "profile-unresolved", "unrepresentable", "reference-fields-interpreted", "interpretation-failed":
	default:
		return invalidResult("playback decoding status")
	}
	if (playback.ConfiguredRange != nil) != (playback.DecodingStatus == "reference-fields-interpreted") {
		return invalidResult("playback range status")
	}
	data, err := validateReport(playback.Report, 7)
	if err != nil {
		return err
	}
	fields := devicesequence.PlaybackFields{}
	if playback.Report.PrefixTrusted && playback.Report.Layout == devicesequence.GIDStandardLayout {
		fields = playbackFields(data)
	}
	if !reflect.DeepEqual(fields, playback.Fields) {
		return invalidResult("raw playback fields")
	}
	if value.Complete {
		if playback.Error != nil || !playback.Report.PrefixTrusted || playback.Report.Layout != devicesequence.GIDStandardLayout || len(data) < 7 {
			return invalidResult("complete playback")
		}
	} else if playback.Error == nil || (value.Status == "partial") != (playback.Report.RawHex != "") {
		return invalidResult("playback error or partial status")
	}
	if playback.ConfiguredRange != nil {
		ref := value.Profile.ReadReference
		if !value.Complete || ref.Applicability != "firmware-matched" || ref.PlaybackEndEncoding == "unknown" || fields.Mode == nil || *fields.Mode > 3 {
			return invalidResult("unqualified playback interpretation")
		}
		end := int(*fields.End)
		if ref.PlaybackEndEncoding == "inclusive" {
			end++
		}
		want := devicesequence.Range{StartLine: int(*fields.Start), LineCount: end - int(*fields.Start)}
		if want.LineCount < 1 || want.StartLine < 0 || end > *value.Profile.RAMLines || want != *playback.ConfiguredRange || playback.DecodingStatus != "reference-fields-interpreted" {
			return invalidResult("configured range")
		}
	}
	if playback.ProfileDigest != "" {
		if len(playback.ProfileDigest) != 71 || !strings.HasPrefix(playback.ProfileDigest, "sha256:") {
			return invalidResult("profile digest")
		}
		if _, err := hex.DecodeString(playback.ProfileDigest[7:]); err != nil {
			return invalidResult("profile digest")
		}
	}
	return nil
}

// MarshalResult is a bounded final wire check, useful for HTTP and fixtures.
func MarshalResult(value Result, serial string, request Request) ([]byte, error) {
	if err := ValidateResult(value, serial, request); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxResponseBytes {
		return nil, invalidResult("response byte limit")
	}
	return data, nil
}
