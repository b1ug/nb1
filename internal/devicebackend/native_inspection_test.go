package devicebackend

import (
	"context"
	"errors"
	"testing"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

func nativeMetadata() *gid.DeviceInfo {
	return &gid.DeviceInfo{VendorID: 0x27b8, ProductID: 0x01ed, VersionNumber: 2, SerialNumber: "20000001", Product: "blink(1) mk2"}
}

type nativeLampSpy struct {
	*testLamp
	metadata      *gid.DeviceInfo
	metadataCalls int
	positions     []uint
	playbackCalls int
	err           error
}

func (f *nativeLampSpy) GetDeviceInfo() *gid.DeviceInfo { f.metadataCalls++; return f.metadata }
func (f *nativeLampSpy) Close()                         { f.calls = append(f.calls, "close") }
func (f *nativeLampSpy) ReadRGB(led b1.LEDIndex) (byte, byte, byte, error) {
	f.calls = append(f.calls, "legacy_rgb")
	return f.testLamp.ReadRGB(led)
}
func (f *nativeLampSpy) ReadPlaystate() (b1.DevicePatternState, error) {
	f.calls = append(f.calls, "legacy_playstate")
	return f.testLamp.ReadPlaystate()
}
func (f *nativeLampSpy) ReadPatternLineRaw(position uint) (b1.RawPatternLine, error) {
	f.positions = append(f.positions, position)
	return b1.RawPatternLine{Position: position, Response: b1.RawFeatureResponse{RequestedReportID: 1, BufferLength: 9}}, f.err
}
func (f *nativeLampSpy) ReadPlaystateRaw() (b1.RawPlaystate, error) {
	f.playbackCalls++
	return b1.RawPlaystate{Response: b1.RawFeatureResponse{RequestedReportID: 1, BufferLength: 9}}, f.err
}

func TestNativeReadProfileKeepsSDKAndReadEvidenceSeparate(t *testing.T) {
	for _, tc := range []struct {
		fw   int
		sdk  string
		read string
		end  string
	}{{204, "model-only", "firmware-matched", "exclusive"}, {205, "firmware-matched", "firmware-matched", "exclusive"}, {206, "unresolved", "unresolved", "unknown"}, {203, "model-only", "unresolved", "unknown"}} {
		profile, err := nativeReadProfile(nativeMetadata(), tc.fw)
		if err != nil || profile.SDKApplicability != tc.sdk || profile.ReadReference.Applicability != tc.read || profile.ReadReference.PlaybackEndEncoding != tc.end || profile.SDKReferenceFirmware == nil || *profile.SDKReferenceFirmware != 205 || profile.OperationQualification != "unverified" {
			t.Fatalf("fw%d %+v %v", tc.fw, profile, err)
		}
		if profile.ReadReference.RowsDecodable != (tc.read == "firmware-matched") {
			t.Fatal(profile)
		}
	}
	mk3 := nativeMetadata()
	mk3.SerialNumber, mk3.Product, mk3.VersionNumber = "30000001", "blink(1) mk3", 0x0101
	profile, err := nativeReadProfile(mk3, 304)
	if err != nil || profile.ReadReference.PlaybackEndEncoding != "inclusive" || *profile.RAMLines != 32 {
		t.Fatal(profile, err)
	}
	// This pure profile does not expand the existing driver's MK2-only Open.
	profile.ReadReference.Sources[0] = "modified"
	*profile.RAMLines = 1
	fresh, err := nativeReadProfile(mk3, 304)
	if err != nil || *fresh.RAMLines != 32 || fresh.ReadReference.Sources[0] == "modified" {
		t.Fatal(fresh, err)
	}
	for _, metadata := range []*gid.DeviceInfo{nil, {}, {VendorID: 0x27b8, ProductID: 0x01ed, VersionNumber: 3, SerialNumber: "20000001", Product: "blink(1) mk2"}} {
		if _, err := nativeReadProfile(metadata, 204); !errors.Is(err, nativeinspection.ErrUnsupported) {
			t.Fatal(err)
		}
	}
}

func TestNativeSDKAdapterDoesOneQueryAndPreservesNativeError(t *testing.T) {
	for _, kind := range []string{nativeinspection.RAM, nativeinspection.Playback} {
		cause := errors.New("synthetic native cause")
		transportErr := &gid.NativeError{Platform: "darwin", Code: 0xe00002bc, Err: cause}
		spy := &nativeLampSpy{testLamp: &testLamp{}, metadata: nativeMetadata(), err: transportErr}
		metrics := &retryTracker{data: RetryMetrics{Serial: "20000001"}}
		b := &backend{dev: spy, info: control.DeviceInfo{Serial: "20000001", Product: "blink(1) mk2", Firmware: 204}, metrics: metrics}
		request := nativeinspection.Request{Kind: kind}
		operation := "query_playback_raw"
		if kind == nativeinspection.RAM {
			request.Range = &devicesequence.Range{StartLine: 31, LineCount: 1}
			operation = "query_pattern_line_raw"
		}
		out, err := b.InspectNative(context.Background(), request)
		var got *gid.NativeError
		if !errors.Is(err, cause) || !errors.As(err, &got) || got != transportErr || out.Contract != nativeinspection.Contract || out.Complete || out.Error == nil || *out.Error.NativeCode != "3758097084" {
			t.Fatalf("%+v %v", out, err)
		}
		if len(spy.testLamp.calls) != 0 || spy.metadataCalls != 1 || len(spy.positions)+spy.playbackCalls != 1 {
			t.Fatalf("unexpected calls: %+v", spy)
		}
		if metrics.data.Attempts != 1 || metrics.data.Retries != 0 || metrics.data.FailedOperations != 1 || metrics.operations[operation].Failures != 1 {
			t.Fatal(metrics.data, metrics.operations)
		}
		if kind == nativeinspection.RAM && spy.positions[0] != 31 {
			t.Fatal(spy.positions)
		}
		out.DaemonInstanceID, out.DeviceSessionID = "daemon_fixture", "session_1"
		if err := nativeinspection.ValidateResult(out, "20000001", request); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeReadPreflightAndTraceGuardDoNotQueryOrStop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*backend, *nativeLampSpy)
		request nativeinspection.Request
		cancel  bool
	}{
		{"trace", func(b *backend, _ *nativeLampSpy) { b.trace = &backendTrace{} }, nativeinspection.Request{Kind: nativeinspection.Playback}, false},
		{"missing-metadata", func(_ *backend, f *nativeLampSpy) { f.metadata = nil }, nativeinspection.Request{Kind: nativeinspection.Playback}, false},
		{"conflicting-serial", func(_ *backend, f *nativeLampSpy) { f.metadata.SerialNumber = "20000002" }, nativeinspection.Request{Kind: nativeinspection.Playback}, false},
		{"invalid-range", func(_ *backend, _ *nativeLampSpy) {}, nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 32, LineCount: 1}}, false},
		{"canceled", func(_ *backend, _ *nativeLampSpy) {}, nativeinspection.Request{Kind: nativeinspection.Playback}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &nativeLampSpy{testLamp: &testLamp{}, metadata: nativeMetadata()}
			b := &backend{dev: f, info: control.DeviceInfo{Serial: "20000001", Firmware: 204}}
			tc.mutate(b, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			out, err := b.InspectNative(ctx, tc.request)
			if err == nil || out.Contract != "" || len(f.positions) != 0 || f.playbackCalls != 0 || len(f.calls) != 0 {
				t.Fatalf("%+v %v %+v", out, err, f)
			}
		})
	}
	legacy := &backend{dev: &testLamp{}}
	if _, err := legacy.InspectNative(context.Background(), nativeinspection.Request{Kind: nativeinspection.Playback}); !errors.Is(err, nativeinspection.ErrUnsupported) {
		t.Fatal(err)
	}
}
