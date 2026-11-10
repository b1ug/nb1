package devicebackend

import (
	"context"
	"fmt"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type nativeLamp interface {
	GetDeviceInfo() *gid.DeviceInfo
	ReadPatternLineRaw(uint) (b1.RawPatternLine, error)
	ReadPlaystateRaw() (b1.RawPlaystate, error)
}

// InspectNative uses the already-owned SDK handle. No Stop, Open, close, retry,
// storage, library import or playback operation is part of this path.
func (b *backend) InspectNative(ctx context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
	if err := request.Validate(); err != nil {
		return nativeinspection.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return nativeinspection.Result{}, err
	}
	// The frozen v1 diagnostic stream has no R operation/inspection phase. Do
	// not silently perform unrecorded calls while claiming complete coverage.
	if b.trace != nil {
		return nativeinspection.Result{}, fmt.Errorf("%w: native inspection requires a diagnostic contract upgrade when transport tracing is enabled", nativeinspection.ErrUnsupported)
	}
	lamp, ok := b.dev.(nativeLamp)
	if !ok {
		return nativeinspection.Result{}, nativeinspection.ErrUnsupported
	}
	metadata := lamp.GetDeviceInfo()
	if metadata == nil || metadata.SerialNumber != b.info.Serial {
		return nativeinspection.Result{}, nativeinspection.ErrUnsupported
	}
	profile, err := nativeReadProfile(metadata, b.info.Firmware)
	if err != nil {
		return nativeinspection.Result{}, err
	}
	reader := &nativeSDKReader{lamp: lamp, metrics: b.metrics}
	return nativeinspection.Collect(ctx, reader, request, nativeinspection.Identity{Serial: b.info.Serial, Product: b.info.Product, Firmware: b.info.Firmware, FirmwareSource: "cached-on-open"}, profile)
}

// nativeReadProfile preserves the SDK's FW205 reference separately. An audited
// FW204 read interpretation is not a modification of that frozen SDK fact and
// is not proof of the connected production image or physical qualification.
func nativeReadProfile(metadata *gid.DeviceInfo, firmware int) (nativeinspection.Profile, error) {
	capabilities := b1.IdentifyPatternCapabilities(metadata, firmware)
	if capabilities.Identity.Model == b1.ProfileModelUnknown || capabilities.Storage.RAMLines.Knowledge != b1.PatternKnowledgeKnown || capabilities.Storage.RAMLines.Value == nil {
		return nativeinspection.Profile{}, nativeinspection.ErrUnsupported
	}
	ram := *capabilities.Storage.RAMLines.Value
	if ram < 1 || ram > 32 {
		return nativeinspection.Profile{}, nativeinspection.ErrUnsupported
	}
	out := nativeinspection.Profile{SDKModel: capabilities.Identity.Model, SDKReferenceFirmware: capabilities.ReferenceFirmwareVersion, SDKApplicability: capabilities.Applicability, RAMLines: &ram, OperationQualification: "unverified", ReadReference: nativeinspection.ReadReference{Firmware: firmware, Applicability: "unresolved", Sources: []string{}, PlaybackEndEncoding: "unknown"}}
	source, end := "", "unknown"
	switch {
	case capabilities.Identity.Model == b1.ProfileModelMK2 && firmware == 204:
		source = "https://github.com/todbot/blink1/blob/f4bf437fa016b2975675fecc2ad18fd04c955fa8/hardware/firmware_mk2/main.c"
		end = "exclusive"
	case capabilities.Identity.Model == b1.ProfileModelMK2 && firmware == 205:
		source = "https://github.com/todbot/blink1/blob/101abe3599c661c4dd4d1204b4d50fb592293143/hardware/firmware_mk2/main.c"
		end = "exclusive"
	case capabilities.Identity.Model == b1.ProfileModelMK3 && firmware == 304:
		source = "https://github.com/todbot/blink1mk3/blob/ccf04e26726a4a41ef9689653ae12baa73dbc456/firmware/firmware-v30x/main.c"
		end = "inclusive"
	}
	readCaps := devicesequence.Capabilities{Model: capabilities.Identity.Model, Firmware: firmware, Applicability: devicesequence.Unresolved, RAMLines: &ram, Sources: []string{}}
	if source != "" {
		out.ReadReference = nativeinspection.ReadReference{Firmware: firmware, Applicability: "firmware-matched", Sources: []string{source}, RowsDecodable: true, PlaybackEndEncoding: end}
		readCaps.ReferenceFirmware, readCaps.Applicability, readCaps.Sources = firmware, devicesequence.FirmwareMatched, []string{source}
	}
	out.PlaybackProfile = devicesequence.PlaybackProfile{Capabilities: readCaps, EndEncoding: end}
	return out.Clone(), nil
}

type nativeSDKReader struct {
	lamp    nativeLamp
	metrics *retryTracker
}

func rawReply(value b1.RawFeatureResponse) nativeinspection.Reply {
	return nativeinspection.Reply{RequestedReportID: value.RequestedReportID, BufferLength: value.BufferLength, ReadAttempted: value.ReadAttempted, ReturnedLength: value.ReturnedLength, LengthValid: value.LengthValid, Bytes: value.Bytes()}
}

func (r *nativeSDKReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	var value b1.RawPatternLine
	err := singleNativeQuery(r.metrics, "query_pattern_line_raw", func() error { var err error; value, err = r.lamp.ReadPatternLineRaw(uint(position)); return err })
	return nativeinspection.LineRead{Position: int(value.Position), RGB8: value.RGB8, DurationTicks: value.DurationTicks, LEDCode: value.LEDCode, Reply: rawReply(value.Response)}, err
}

func (r *nativeSDKReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	var value b1.RawPlaystate
	err := singleNativeQuery(r.metrics, "query_playback_raw", func() error { var err error; value, err = r.lamp.ReadPlaystateRaw(); return err })
	return nativeinspection.PlaybackRead{Fields: devicesequence.PlaybackFields{Mode: value.Mode, Start: value.Start, End: value.End, Count: value.Count, NextPosition: value.NextPosition}, Reply: rawReply(value.Response)}, err
}

// These raw methods deliberately do not use retryMK2. Each query is counted
// once even for an eligible Darwin error, retaining its partial observation.
func singleNativeQuery(tracker *retryTracker, kind string, query func() error) error {
	if tracker != nil {
		tracker.mu.Lock()
		tracker.data.Operations++
		tracker.data.Attempts++
		if tracker.operations == nil {
			tracker.operations = map[string]*control.TransportMetric{}
		}
		if tracker.operations[kind] == nil {
			tracker.operations[kind] = &control.TransportMetric{Serial: tracker.data.Serial, Operation: kind}
		}
		tracker.operations[kind].Calls++
		tracker.operations[kind].Attempts++
		tracker.mu.Unlock()
	}
	err := query()
	if err != nil && tracker != nil {
		tracker.mu.Lock()
		tracker.data.FailedOperations++
		tracker.data.LastFailure = fmt.Sprintf("%s: %v", kind, err)
		tracker.operations[kind].Failures++
		tracker.operations[kind].LastError = err.Error()
		tracker.mu.Unlock()
	}
	return err
}

var _ control.NativeInspectionBackend = (*backend)(nil)
