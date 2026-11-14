package nativeinspection

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/devicesequence"
)

const (
	LiveNative = "live-native"
	NonAtomic  = "sequential-best-effort-not-atomic"
)

type nativeCarrier interface {
	NativePlatform() string
	NativeCode() uint64
}

func boundedText(value string, maximum int) (string, bool, bool) {
	replaced := !utf8.ValidString(value)
	value = strings.ToValidUTF8(value, "\ufffd")
	truncated := len(value) > maximum
	if truncated {
		value = value[:maximum]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value, truncated, replaced
}

func errorInfo(code string, err error) *ErrorInfo {
	if err == nil {
		return nil
	}
	message, truncated, replaced := boundedText(err.Error(), 1024)
	out := &ErrorInfo{Code: code, Message: message, MessageTruncated: truncated, MessageUTF8Replaced: replaced}
	var carrier nativeCarrier
	if errors.As(err, &carrier) {
		platform, cut, replaced := boundedText(carrier.NativePlatform(), 64)
		value := strconv.FormatUint(carrier.NativeCode(), 10)
		out.NativePlatform, out.NativeCode, out.NativePlatformTruncated = &platform, &value, cut
		out.NativePlatformUTF8Replaced = replaced
	}
	return out
}

// report preserves only a consistent reported prefix. A valid prefix with an
// error remains available. An invalid count never authenticates buffer bytes.
func report(reply Reply, prefix int) (Report, []byte, error) {
	out := Report{Layout: "unknown", RequestedReportID: reply.RequestedReportID, BufferLength: reply.BufferLength, ReadAttempted: reply.ReadAttempted, ReturnedLength: reply.ReturnedLength, LengthValid: reply.LengthValid}
	if reply.RequestedReportID == 1 && reply.BufferLength == 9 {
		out.Layout = devicesequence.GIDStandardLayout
	}
	if !reply.ReadAttempted {
		if reply.ReturnedLength != 0 || reply.LengthValid || len(reply.Bytes) != 0 {
			return out, nil, fmt.Errorf("inconsistent unattempted response")
		}
		return out, nil, io.ErrUnexpectedEOF
	}
	validCount := reply.BufferLength >= 0 && reply.BufferLength <= devicesequence.MaxObservedReportBytes && reply.ReturnedLength >= 0 && reply.ReturnedLength <= reply.BufferLength
	if !reply.LengthValid || !validCount || reply.ReturnedLength != len(reply.Bytes) {
		return out, nil, fmt.Errorf("invalid or inconsistent reported response length")
	}
	data := append([]byte(nil), reply.Bytes...)
	out.PrefixTrusted = true
	out.RawHex = hex.EncodeToString(data)
	if out.Layout != devicesequence.GIDStandardLayout {
		out.UnknownTailHex = out.RawHex
		return out, data, fmt.Errorf("unrecognized response buffer layout")
	}
	if len(data) > prefix {
		out.UnknownTailHex = hex.EncodeToString(data[prefix:])
	}
	if len(data) < prefix {
		return out, data, io.ErrUnexpectedEOF
	}
	return out, data, nil
}

func preferReadError(readErr, formatErr error) error {
	if readErr != nil {
		return readErr
	}
	return formatErr
}

func lineFields(data []byte) (*[3]byte, *uint16, *byte) {
	var rgb *[3]byte
	var ticks *uint16
	var led *byte
	if len(data) >= 5 {
		value := [3]byte{data[2], data[3], data[4]}
		rgb = &value
	}
	if len(data) >= 7 {
		value := uint16(data[5])<<8 | uint16(data[6])
		ticks = &value
	}
	if len(data) >= 8 {
		value := data[7]
		led = &value
	}
	return rgb, ticks, led
}

func playbackFields(data []byte) devicesequence.PlaybackFields {
	out := devicesequence.PlaybackFields{}
	for index, field := range []**byte{&out.Mode, &out.Start, &out.End, &out.Count, &out.NextPosition} {
		if len(data) > index+2 {
			value := data[index+2]
			*field = &value
		}
	}
	return out
}

// Collect is called by the owner of an already-open reader. It neither opens
// nor closes it. It stops a batch on its first error and never retries or
// manufactures missing rows. Cancellation is checked between synchronous calls.
func Collect(ctx context.Context, reader Reader, request Request, identity Identity, profile Profile) (Result, error) {
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if reader == nil || profile.RAMLines == nil || *profile.RAMLines < 1 || *profile.RAMLines > 32 {
		return Result{}, ErrUnsupported
	}
	if !validProfile(profile, identity.Firmware) {
		return Result{}, ErrInvalid
	}
	request, profile = request.Clone(), profile.Clone()
	if request.Kind == RAM && request.Range.StartLine+request.Range.LineCount > *profile.RAMLines {
		return Result{}, ErrInvalid
	}
	start := time.Now()
	out := Result{Contract: Contract, Kind: request.Kind, Device: identity, Source: LiveNative, Consistency: NonAtomic, StartedAt: start.UTC(), Status: "failed", Profile: profile}
	var collectErr error
	if request.Kind == RAM {
		collectErr = collectRAM(ctx, reader, request, &out)
	} else {
		collectErr = collectPlayback(ctx, reader, &out)
	}
	finish := time.Now()
	out.FinishedAt, out.DurationNS = finish.UTC(), finish.Sub(start).Nanoseconds()
	if out.Complete {
		out.Status = "complete"
	} else {
		hasBytes := false
		if out.RAM != nil {
			for _, row := range out.RAM.Rows {
				hasBytes = hasBytes || row.Report.RawHex != "" || row.Error == nil
			}
		}
		if out.Playback != nil {
			hasBytes = out.Playback.Report.RawHex != ""
		}
		if hasBytes {
			out.Status = "partial"
		}
		if collectErr == nil {
			collectErr = fmt.Errorf("native observation was incomplete")
		}
		code := "query_failed"
		if errors.Is(collectErr, context.Canceled) || errors.Is(collectErr, context.DeadlineExceeded) {
			code = "collection_cancelled"
		}
		out.Error = errorInfo(code, collectErr)
	}
	return out, collectErr
}

func collectRAM(ctx context.Context, reader Reader, request Request, out *Result) error {
	ram := &RAMResult{RequestedRange: *request.Range, Rows: []Row{}, UnreadPositions: []int{}, DecodingStatus: "incomplete", Issues: []devicesequence.Issue{}}
	out.RAM = ram
	snapshot := devicesequence.Snapshot{RequestedRange: *request.Range, Complete: true, Rows: []devicesequence.RowObservation{}}
	var collectErr error
	for position := request.Range.StartLine; position < request.Range.StartLine+request.Range.LineCount; position++ {
		if err := ctx.Err(); err != nil {
			collectErr = err
			break
		}
		value, readErr := reader.ReadLine(position)
		raw, data, shapeErr := report(value.Reply, 8)
		if value.Position != position {
			shapeErr = fmt.Errorf("reader returned an unexpected requested position")
		}
		callErr := preferReadError(readErr, shapeErr)
		row := Row{Position: position, Report: raw, Error: errorInfo("query_failed", callErr)}
		if raw.Layout == devicesequence.GIDStandardLayout && raw.PrefixTrusted {
			row.RGB8, row.DurationTicks, row.LEDCode = lineFields(data)
		}
		ram.Rows = append(ram.Rows, row)
		errorMarker := ""
		if callErr != nil {
			errorMarker = "read_failed"
			snapshot.Complete = false
		}
		snapshot.Rows = append(snapshot.Rows, devicesequence.RowObservation{Position: position, Layout: raw.Layout, ReadAttempted: raw.ReadAttempted, ReturnedLength: raw.ReturnedLength, Response: data, ReadError: errorMarker})
		if callErr != nil {
			collectErr = fmt.Errorf("inspect RAM line %d: %w", position, callErr)
			break
		}
	}
	for position := request.Range.StartLine + len(ram.Rows); position < request.Range.StartLine+request.Range.LineCount; position++ {
		ram.UnreadPositions = append(ram.UnreadPositions, position)
	}
	snapshot.Complete = snapshot.Complete && len(ram.Rows) == request.Range.LineCount && collectErr == nil
	out.Complete = snapshot.Complete
	if !out.Profile.ReadReference.RowsDecodable {
		ram.DecodingStatus = "profile-unresolved"
		if !out.Complete {
			ram.DecodingStatus = "incomplete"
		}
		ram.Issues = append(ram.Issues, devicesequence.Issue{Code: "row_semantics_unresolved", Path: "profile.read_reference", Message: "Raw fields remain available; this provider has not established a row interpretation."})
		return collectErr
	}
	inspection, err := devicesequence.InspectSnapshot(snapshot)
	if err != nil {
		ram.DecodingStatus = "interpretation-failed"
		ram.Issues = append(ram.Issues, devicesequence.Issue{Code: "interpretation_failed", Path: "ram", Message: "Raw observations are retained independently from interpretation."})
		return collectErr
	}
	ram.Sequence, ram.SequenceDigest, ram.DecodingStatus, ram.Issues = inspection.Sequence, inspection.SequenceDigest, inspection.DecodingStatus, inspection.Issues
	return collectErr
}

func collectPlayback(ctx context.Context, reader Reader, out *Result) error {
	playback := &PlaybackResult{Report: Report{Layout: "unknown"}, DecodingStatus: "incomplete", Issues: []devicesequence.Issue{}}
	out.Playback = playback
	if err := ctx.Err(); err != nil {
		playback.Error = errorInfo("collection_cancelled", err)
		return err
	}
	value, readErr := reader.ReadPlayback()
	raw, data, shapeErr := report(value.Reply, 7)
	callErr := preferReadError(readErr, shapeErr)
	playback.Report, playback.Error = raw, errorInfo("query_failed", callErr)
	if raw.Layout == devicesequence.GIDStandardLayout && raw.PrefixTrusted {
		playback.Fields = playbackFields(data)
	}
	out.Complete = callErr == nil
	errorMarker := ""
	if callErr != nil {
		errorMarker = "read_failed"
	}
	inspection, err := devicesequence.InspectPlayback(devicesequence.PlaybackObservation{Layout: raw.Layout, ReadAttempted: raw.ReadAttempted, ReturnedLength: raw.ReturnedLength, Response: data, ReadError: errorMarker}, out.Profile.PlaybackProfile)
	if err != nil {
		playback.DecodingStatus = "interpretation-failed"
		playback.Issues = append(playback.Issues, devicesequence.Issue{Code: "interpretation_failed", Path: "profile", Message: "Raw observations are retained independently from interpretation."})
	} else {
		playback.ConfiguredRange, playback.DecodingStatus, playback.ProfileDigest, playback.Issues = inspection.ConfiguredRange, inspection.DecodingStatus, inspection.ProfileDigest, inspection.Issues
	}
	return callErr
}
