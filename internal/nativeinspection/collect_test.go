package nativeinspection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/devicesequence"
)

func fixtureProfile() Profile {
	ram, ref := 32, 205
	return Profile{SDKModel: "mk2", SDKReferenceFirmware: &ref, SDKApplicability: "model-only", RAMLines: &ram, OperationQualification: "unverified", ReadReference: ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture-not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"}, PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &ram, Sources: []string{"fixture-not-hardware"}}, EndEncoding: "exclusive"}}
}

type fakeReader struct {
	line          func(int) (LineRead, error)
	playback      func() (PlaybackRead, error)
	positions     []int
	playbackCalls int
}

func fixtureReply(command byte) Reply {
	return Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: []byte{1, command, 16, 32, 48, 0, 50, 1, 0xee}}
}
func (f *fakeReader) ReadLine(position int) (LineRead, error) {
	f.positions = append(f.positions, position)
	if f.line != nil {
		return f.line(position)
	}
	return LineRead{Position: position, Reply: fixtureReply('R')}, nil
}
func (f *fakeReader) ReadPlayback() (PlaybackRead, error) {
	f.playbackCalls++
	if f.playback != nil {
		return f.playback()
	}
	r := fixtureReply('S')
	r.Bytes = []byte{1, 'S', 1, 0, 2, 0, 1, 0xcc, 0xdd}
	return PlaybackRead{Reply: r}, nil
}
func collectFixture(t *testing.T, f Reader, request Request, profile Profile) (Result, error) {
	t.Helper()
	out, err := Collect(context.Background(), f, request, Identity{Serial: "20005601", Product: "blink(1) mk2", Firmware: 204, FirmwareSource: "cached-on-open"}, profile)
	out.DaemonInstanceID, out.DeviceSessionID = "daemon_fixture", "session_0000000000000001"
	return out, err
}
func ramRequest(start, count int) Request {
	return Request{Kind: RAM, Range: &devicesequence.Range{StartLine: start, LineCount: count}}
}

func TestRAMCollectionPreservesImageAndDoesNotClaimFlashOrRecipe(t *testing.T) {
	f := &fakeReader{}
	req := ramRequest(0, 32)
	out, err := collectFixture(t, f, req, fixtureProfile())
	if err != nil || !out.Complete || out.Status != "complete" || len(f.positions) != 32 || f.playbackCalls != 0 || len(out.RAM.Rows) != 32 || out.RAM.Sequence == nil || out.RAM.Deployable {
		t.Fatalf("%+v %v", out, err)
	}
	for i, p := range f.positions {
		if p != i {
			t.Fatal(f.positions)
		}
	}
	row := out.RAM.Rows[0]
	if *row.RGB8 != ([3]byte{16, 32, 48}) || *row.DurationTicks != 50 || *row.LEDCode != 1 || row.Report.RawHex != "0152102030003201ee" || row.Report.UnknownTailHex != "ee" {
		t.Fatal(row)
	}
	data, err := MarshalResult(out, "20005601", req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"read_flash"`)) || bytes.Contains(data, []byte(`"recipe"`)) || bytes.Contains(data, []byte(`"title"`)) || bytes.Contains(data, []byte(`"response":`)) {
		t.Fatal(string(data))
	}
	decoded, err := DecodeResult(data, "20005601", req)
	if err != nil || decoded.RAM.SequenceDigest == nil || *decoded.RAM.SequenceDigest != *out.RAM.SequenceDigest {
		t.Fatal(err)
	}
}

func TestRAMSequenceLEDUsesRequiredNumericChannels(t *testing.T) {
	request := ramRequest(0, 1)
	for _, led := range []byte{0, 1, 2} {
		t.Run(fmt.Sprint(led), func(t *testing.T) {
			reader := &fakeReader{line: func(position int) (LineRead, error) {
				reply := fixtureReply('R')
				reply.Bytes[7] = led
				return LineRead{Position: position, Reply: reply}, nil
			}}
			result, err := collectFixture(t, reader, request, fixtureProfile())
			if err != nil || result.RAM.Sequence == nil {
				t.Fatalf("known native LED rejected: %v", err)
			}
			data, err := MarshalResult(result, result.Device.Serial, request)
			if err != nil || !bytes.Contains(data, []byte(fmt.Sprintf(`"led":%d`, led))) {
				t.Fatalf("numeric LED missing from response: %s %v", data, err)
			}
			decoded, err := DecodeResult(data, result.Device.Serial, request)
			if err != nil || decoded.RAM.Sequence.Lines[0].LED != devicesequence.LED(led) || *decoded.RAM.Rows[0].LEDCode != led {
				t.Fatalf("numeric LED or raw observation changed: %v", err)
			}
			if led != 0 {
				return
			}
			for _, token := range []string{`"all"`, `"0"`, `"1"`, `"2"`, `0.0`, `1.0`, `1e0`, `-0`, `-1`, `3`, `null`} {
				malformed := bytes.Replace(data, []byte(`"led":0`), []byte(`"led":`+token), 1)
				if _, err := DecodeResult(malformed, result.Device.Serial, request); err == nil {
					t.Fatalf("accepted derived Sequence LED %s", token)
				}
			}
			missing := bytes.Replace(data, []byte(`"led":0,`), nil, 1)
			if _, err := DecodeResult(missing, result.Device.Serial, request); err == nil {
				t.Fatal("missing derived Sequence LED became a plausible all-output zero")
			}
		})
	}
}

func TestRawZeroUnknownAndAllLEDReadbackRemainInspectable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		led      byte
		ticks    byte
		sequence bool
	}{{"zero", 0, 0, false}, {"unknown", 0x81, 10, false}, {"all", 0, 50, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeReader{line: func(p int) (LineRead, error) {
				r := fixtureReply('R')
				r.Bytes[7], r.Bytes[6] = tc.led, tc.ticks
				return LineRead{Position: p, Reply: r}, nil
			}}
			req := ramRequest(0, 1)
			out, err := collectFixture(t, f, req, fixtureProfile())
			if err != nil || !out.Complete || (out.RAM.Sequence != nil) != tc.sequence || *out.RAM.Rows[0].LEDCode != tc.led {
				t.Fatalf("%+v %v", out, err)
			}
			data, err := MarshalResult(out, "20005601", req)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeResult(data, "20005601", req)
			if err != nil || !got.Complete {
				t.Fatal(string(data), err)
			}
		})
	}
}

type codedError struct{ cause error }

func (e codedError) Error() string          { return e.cause.Error() }
func (e codedError) Unwrap() error          { return e.cause }
func (e codedError) NativePlatform() string { return "darwin" }
func (e codedError) NativeCode() uint64     { return 0xe00002bc }

func TestPartialPrefixAndNativeCauseSurviveWithoutRetryOrZeroFill(t *testing.T) {
	cause := errors.New("fixture native cause")
	for n := 0; n <= 9; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := &fakeReader{line: func(p int) (LineRead, error) {
				r := fixtureReply('R')
				r.Bytes = r.Bytes[:n]
				r.ReturnedLength = n
				return LineRead{Position: p, Reply: r}, codedError{cause}
			}}
			req := ramRequest(3, 3)
			out, err := collectFixture(t, f, req, fixtureProfile())
			var carrier nativeCarrier
			if !errors.Is(err, cause) || !errors.As(err, &carrier) || len(f.positions) != 1 || out.Complete || out.RAM.Sequence != nil || len(out.RAM.UnreadPositions) != 2 || out.RAM.UnreadPositions[0] != 4 {
				t.Fatalf("%+v %v", out, err)
			}
			row := out.RAM.Rows[0]
			if len(row.Report.RawHex) != n*2 || row.Report.ReturnedLength != n || !row.Report.PrefixTrusted || row.Error == nil || out.Error.NativeCode == nil || *out.Error.NativeCode != "3758097084" {
				t.Fatalf("%+v", out)
			}
			if (row.RGB8 != nil) != (n >= 5) || (row.DurationTicks != nil) != (n >= 7) || (row.LEDCode != nil) != (n >= 8) {
				t.Fatal(row)
			}
			if err := ValidateResult(out, "20005601", req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMalformedResponsesDoNotFabricateTrustedBytes(t *testing.T) {
	for _, mutate := range []func(*Reply){
		func(r *Reply) { r.ReadAttempted = false; r.ReturnedLength = 0; r.LengthValid = false; r.Bytes = nil },
		func(r *Reply) { r.ReturnedLength = -1; r.LengthValid = false; r.Bytes = nil },
		func(r *Reply) { r.ReturnedLength = 10; r.LengthValid = false; r.Bytes = nil },
		func(r *Reply) { r.ReturnedLength = 0 },
		func(r *Reply) { r.LengthValid = false },
	} {
		f := &fakeReader{line: func(p int) (LineRead, error) {
			r := fixtureReply('R')
			mutate(&r)
			return LineRead{Position: p, Reply: r}, nil
		}}
		req := ramRequest(31, 1)
		out, err := collectFixture(t, f, req, fixtureProfile())
		if err == nil || out.Complete || out.Status != "failed" || out.RAM.Rows[0].Report.RawHex != "" || out.RAM.Rows[0].Report.PrefixTrusted || out.RAM.Rows[0].RGB8 != nil || len(f.positions) != 1 {
			t.Fatalf("%+v %v", out, err)
		}
		if err := ValidateResult(out, "20005601", req); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaybackModesCursorAndUnknownProfileAreNotCompletion(t *testing.T) {
	for _, mode := range []byte{0, 1, 2, 3, 77} {
		f := &fakeReader{playback: func() (PlaybackRead, error) {
			r := fixtureReply('S')
			r.Bytes = []byte{9, '?', mode, 0, 2, 0, 255, 0xaa, 0xbb}
			return PlaybackRead{Reply: r}, nil
		}}
		req := Request{Kind: Playback}
		out, err := collectFixture(t, f, req, fixtureProfile())
		if err != nil || !out.Complete || f.playbackCalls != 1 || len(f.positions) != 0 || *out.Playback.Fields.Mode != mode || *out.Playback.Fields.Count != 0 || *out.Playback.Fields.NextPosition != 255 || out.Playback.Report.UnknownTailHex != "aabb" {
			t.Fatalf("%+v %v", out, err)
		}
		if mode > 3 && out.Playback.ConfiguredRange != nil {
			t.Fatal(out)
		}
		data, err := MarshalResult(out, "20005601", req)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"playing"`, `"completed"`, `"current_line"`, `"forever"`} {
			if bytes.Contains(data, []byte(forbidden)) {
				t.Fatal(forbidden)
			}
		}
	}
	profile := fixtureProfile()
	profile.ReadReference.RowsDecodable = false
	profile.ReadReference.Applicability = "unresolved"
	profile.ReadReference.PlaybackEndEncoding = "unknown"
	profile.PlaybackProfile.Capabilities.Applicability = "unresolved"
	profile.PlaybackProfile.EndEncoding = "unknown"
	out, err := collectFixture(t, &fakeReader{}, Request{Kind: Playback}, profile)
	if err != nil || !out.Complete || out.Playback.ConfiguredRange != nil || out.Playback.DecodingStatus != "profile-unresolved" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestCancellationBetweenRowsKeepsCollectedPrefixAndNoMoreIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeReader{line: func(p int) (LineRead, error) { cancel(); return LineRead{Position: p, Reply: fixtureReply('R')}, nil }}
	req := ramRequest(0, 3)
	out, err := Collect(ctx, f, req, Identity{Serial: "20005601", Firmware: 204, FirmwareSource: "cached-on-open"}, fixtureProfile())
	out.DaemonInstanceID, out.DeviceSessionID = "daemon_fixture", "session_1"
	if !errors.Is(err, context.Canceled) || len(f.positions) != 1 || len(out.RAM.UnreadPositions) != 2 || out.Status != "partial" || out.RAM.Sequence != nil {
		t.Fatalf("%+v %v", out, err)
	}
	if err := ValidateResult(out, "20005601", req); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Request{{Kind: RAM}, {Kind: RAM, Range: &devicesequence.Range{StartLine: 32, LineCount: 1}}, {Kind: RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: int(^uint(0) >> 1)}}, {Kind: Playback, Range: &devicesequence.Range{StartLine: 0, LineCount: 1}}, {Kind: "flash"}} {
		f := &fakeReader{}
		if _, err := Collect(context.Background(), f, r, Identity{}, fixtureProfile()); !errors.Is(err, ErrInvalid) || len(f.positions) != 0 || f.playbackCalls != 0 {
			t.Fatal(r, err)
		}
	}
}

func TestResultCloneKeepsSDKBuffersProfilesAndNullableFieldsIndependent(t *testing.T) {
	profile := fixtureProfile()
	reply := fixtureReply('R')
	f := &fakeReader{line: func(p int) (LineRead, error) { return LineRead{Position: p, Reply: reply}, nil }}
	req := ramRequest(0, 1)
	out, err := collectFixture(t, f, req, profile)
	if err != nil {
		t.Fatal(err)
	}
	clone := out.Clone()
	reply.Bytes[2] = 99
	profile.ReadReference.Sources[0] = "changed"
	*profile.RAMLines = 1
	if *out.RAM.Rows[0].RGB8 != ([3]byte{16, 32, 48}) || out.Profile.ReadReference.Sources[0] != "fixture-not-hardware" || *out.Profile.RAMLines != 32 {
		t.Fatal(out)
	}
	*clone.RAM.Rows[0].RGB8 = [3]byte{0, 0, 0}
	clone.RAM.Sequence.Lines[0].Color = "#ffffff"
	clone.Profile.PlaybackProfile.Capabilities.Sources[0] = "changed"
	if *out.RAM.Rows[0].RGB8 != ([3]byte{16, 32, 48}) || out.RAM.Sequence.Lines[0].Color != "#102030" || out.Profile.PlaybackProfile.Capabilities.Sources[0] != "fixture-not-hardware" {
		t.Fatal(out)
	}
}

func TestReadJSONRejectsDuplicateMissingNullAndWrongArrayData(t *testing.T) {
	req := ramRequest(0, 1)
	out, err := collectFixture(t, &fakeReader{}, req, fixtureProfile())
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalResult(out, "20005601", req)
	if err != nil {
		t.Fatal(err)
	}
	variants := [][]byte{
		append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"complete":true}`)...),
		bytes.Replace(data, []byte(`"complete":true,`), nil, 1),
		bytes.Replace(data, []byte(`"complete":true`), []byte(`"complete":null`), 1),
		bytes.Replace(data, []byte(`"rgb8":[16,32,48]`), []byte(`"rgb8":[16,32]`), 1),
		bytes.Replace(data, []byte(`"rgb8":[16,32,48]`), []byte(`"rgb8":[16,32,48,99]`), 1),
		bytes.Replace(data, []byte(`"source":`), []byte(`"Source":`), 1),
		append(append([]byte(nil), data...), []byte(` {}`)...),
		[]byte(strings.Repeat(" ", MaxResponseBytes+1)),
	}
	for index, variant := range variants {
		if bytes.Equal(variant, data) {
			t.Fatalf("bad fixture%d", index)
		}
		if _, err := DecodeResult(variant, "20005601", req); err == nil {
			t.Fatalf("accepted%d", index)
		}
	}
	got, err := DecodeResult(data, "20005601", req)
	if err != nil || got.RAM.Sequence == nil {
		t.Fatal(err)
	}
}

func TestStrictWireValidationRejectsFabricatedSuccessOrSequence(t *testing.T) {
	req := ramRequest(0, 1)
	base, err := collectFixture(t, &fakeReader{}, req, fixtureProfile())
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Result){
		func(r *Result) { r.Device.Serial = "20005602" }, func(r *Result) { r.Complete = false }, func(r *Result) { r.RAM.Rows[0].Report.RawHex = "00" }, func(r *Result) { r.RAM.Rows[0].RGB8[0] = 99 }, func(r *Result) { r.RAM.Sequence.Lines[0].Color = "#ffffff" }, func(r *Result) { r.RAM.Rows[0].Report.PrefixTrusted = false }, func(r *Result) { r.RAM.UnreadPositions = []int{0} }, func(r *Result) { r.RAM.Deployable = true }, func(r *Result) { r.Profile.ReadReference.RowsDecodable = false }, func(r *Result) { r.StartedAt = time.Time{} },
	} {
		out := base.Clone()
		mutate(&out)
		if err := ValidateResult(out, "20005601", req); err == nil {
			d, _ := json.Marshal(out)
			t.Fatal(string(d))
		}
	}
}

func TestCentralDecoderRejectsUnpairedUnicodeWithoutChangingNativeFlags(t *testing.T) {
	req := Request{Kind: Playback}
	f := &fakeReader{playback: func() (PlaybackRead, error) {
		r := fixtureReply('S')
		r.Bytes = []byte{1, 'S', 1, 0, 2, 0, 1, 0, 0}
		return PlaybackRead{Reply: r}, errors.New("fixture native message")
	}}
	out, err := collectFixture(t, f, req, fixtureProfile())
	if err == nil {
		t.Fatal("expected partial fixture")
	}
	data, err := MarshalResult(out, "20005601", req)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`\ud800`, `\udfff`, `\ud800\u0000`} {
		mutated := bytes.ReplaceAll(data, []byte("fixture native message"), []byte(bad))
		if _, err := DecodeResult(mutated, "20005601", req); err == nil {
			t.Fatal("silently repaired", bad)
		}
	}
	for _, good := range []string{`\ud83d\ude00`, `\ufffd`, `\\ud800`} {
		mutated := bytes.ReplaceAll(data, []byte("fixture native message"), []byte(good))
		got, err := DecodeResult(mutated, "20005601", req)
		if err != nil || got.Error.MessageUTF8Replaced {
			t.Fatal(good, err)
		}
	}
}
