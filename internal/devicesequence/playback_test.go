package devicesequence

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func playbackProfile(encoding string) PlaybackProfile {
	ram := 32
	return PlaybackProfile{Capabilities: Capabilities{Model: "fixture", Firmware: 204, ReferenceFirmware: 204, Applicability: FirmwareMatched, RAMLines: &ram, Sources: []string{"fixture-reference-not-hardware"}}, EndEncoding: encoding}
}

func playbackObservation() PlaybackObservation {
	return PlaybackObservation{Layout: GIDStandardLayout, ReadAttempted: true, ReturnedLength: 9, Response: []byte{1, 'S', 1, 3, 5, 0, 4, 0xee, 0xff}}
}

func TestPlaybackEndProfilesAreExplicitAndNotCommandSentinels(t *testing.T) {
	for _, encoding := range []string{ExclusivePlaybackEnd, InclusivePlaybackEnd} {
		profile := playbackProfile(encoding)
		// All representable register pairs, including the full32-row range.
		for start := 0; start < 32; start++ {
			for exclusiveEnd := start + 1; exclusiveEnd <= 32; exclusiveEnd++ {
				input := playbackObservation()
				input.Response[3], input.Response[4] = byte(start), byte(exclusiveEnd)
				if encoding == InclusivePlaybackEnd {
					input.Response[4]--
				}
				got, err := InspectPlayback(input, profile)
				if err != nil || !got.Complete || got.ConfiguredRange == nil || *got.ConfiguredRange != (Range{start, exclusiveEnd - start}) || got.Deployable {
					t.Fatalf("%s [%d,%d): %+v %v", encoding, start, exclusiveEnd, got, err)
				}
			}
		}
	}
	input := playbackObservation()
	input.Response[3], input.Response[4] = 0, 0
	exclusive, _ := InspectPlayback(input, playbackProfile(ExclusivePlaybackEnd))
	inclusive, _ := InspectPlayback(input, playbackProfile(InclusivePlaybackEnd))
	if exclusive.ConfiguredRange != nil || !hasIssue(exclusive.Issues, "invalid_playback_range") || inclusive.ConfiguredRange == nil || *inclusive.ConfiguredRange != (Range{0, 1}) {
		t.Fatalf("S end0 must not become p's full-capacity sentinel: %+v %+v", exclusive, inclusive)
	}
}

func TestPlaybackRawModesAndCountsDoNotBecomeCompletionOrRepeat(t *testing.T) {
	for mode := 0; mode <= 255; mode++ {
		for _, count := range []byte{0, 1, 255} {
			input := playbackObservation()
			input.Response[2], input.Response[5] = byte(mode), count
			got, err := InspectPlayback(input, playbackProfile(ExclusivePlaybackEnd))
			if err != nil || !got.Complete || *got.Fields.Mode != byte(mode) || *got.Fields.Count != count || *got.Fields.NextPosition != 4 || got.Deployable {
				t.Fatalf("%+v %v", got, err)
			}
			if mode > 3 && (got.ConfiguredRange != nil || !hasIssue(got.Issues, "unknown_playback_mode")) {
				t.Fatal("unknown mode acquired known semantics", got)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{`"playing"`, `"completed"`, `"current_line"`, `"repeat"`, `"forever"`} {
				if bytes.Contains(encoded, []byte(forbidden)) {
					t.Fatalf("invented %s in %s", forbidden, encoded)
				}
			}
		}
	}
}

func TestPlaybackPartialPrefixesAndErrorsPreserveAvailableFields(t *testing.T) {
	for n := 0; n <= 9; n++ {
		for _, readError := range []string{"", "native failure with a valid prefix"} {
			input := playbackObservation()
			input.ReturnedLength, input.Response, input.ReadError = n, input.Response[:n], readError
			got, err := InspectPlayback(input, playbackProfile(ExclusivePlaybackEnd))
			if err != nil || got.Complete != (n >= 7 && readError == "") || !bytes.Equal(got.Observation.Response, input.Response) || got.Observation.ReadError != readError {
				t.Fatalf("n=%d err=%q: %+v %v", n, readError, got, err)
			}
			for index, field := range []*byte{got.Fields.Mode, got.Fields.Start, got.Fields.End, got.Fields.Count, got.Fields.NextPosition} {
				if (field != nil) != (n > index+2) || field != nil && *field != input.Response[index+2] {
					t.Fatalf("invented or lost prefix field n=%d i=%d: %+v", n, index, got)
				}
			}
			if !got.Complete && got.ConfiguredRange != nil {
				t.Fatal("failed observation gained a normalized range", got)
			}
		}
	}
}

func TestPlaybackUntrustedLengthsAndLayoutsNeverDecode(t *testing.T) {
	changes := map[string]func(*PlaybackObservation){
		"negative":    func(o *PlaybackObservation) { o.ReturnedLength = -1 },
		"over-buffer": func(o *PlaybackObservation) { o.ReturnedLength = 10; o.Response = append(o.Response, 0xaa) },
		"mismatched":  func(o *PlaybackObservation) { o.ReturnedLength = 8 },
		"not-read":    func(o *PlaybackObservation) { o.ReadAttempted = false },
		"unknown-layout": func(o *PlaybackObservation) {
			o.Layout = "unknown-layout"
			o.Response = append(o.Response, make([]byte, 60)...)
			o.ReturnedLength = len(o.Response)
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			input := playbackObservation()
			change(&input)
			got, err := InspectPlayback(input, playbackProfile(ExclusivePlaybackEnd))
			if err != nil || got.Complete || got.ConfiguredRange != nil || got.Fields != (PlaybackFields{}) || !bytes.Equal(input.Response, got.Observation.Response) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestPlaybackUnknownReferencePreservesRawWithoutGuessingGeneration(t *testing.T) {
	for _, mutate := range []func(*PlaybackProfile){
		func(p *PlaybackProfile) { p.Capabilities.Applicability = ModelOnly },
		func(p *PlaybackProfile) { p.Capabilities.Applicability = Unknown },
		func(p *PlaybackProfile) { p.Capabilities.RAMLines = nil },
		func(p *PlaybackProfile) { p.EndEncoding = Unknown },
	} {
		profile := playbackProfile(ExclusivePlaybackEnd)
		mutate(&profile)
		got, err := InspectPlayback(playbackObservation(), profile)
		if err != nil || !got.Complete || got.Fields.End == nil || got.ConfiguredRange != nil || got.DecodingStatus != "profile-unresolved" || !hasIssue(got.Issues, "playback_profile_unresolved") {
			t.Fatalf("%+v %v", got, err)
		}
	}
}

func TestPlaybackMalformedRegisterRangesStayRaw(t *testing.T) {
	for _, registers := range [][2]byte{{0, 0}, {4, 4}, {4, 3}, {0, 33}, {32, 33}, {255, 255}} {
		input := playbackObservation()
		input.Response[3], input.Response[4], input.Response[6] = registers[0], registers[1], 255
		got, err := InspectPlayback(input, playbackProfile(ExclusivePlaybackEnd))
		if err != nil || !got.Complete || got.ConfiguredRange != nil || *got.Fields.Start != registers[0] || *got.Fields.End != registers[1] || *got.Fields.NextPosition != 255 || !hasIssue(got.Issues, "invalid_playback_range") {
			t.Fatalf("%+v %v", got, err)
		}
	}
}

func TestPlaybackOwnsRawFieldsAndBindsInterpretationProfile(t *testing.T) {
	input := playbackObservation()
	profile := playbackProfile(ExclusivePlaybackEnd)
	got, err := InspectPlayback(input, profile)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := got.ProfileDigest
	got.Observation.Response[7] = 99
	*got.Fields.Mode = 99
	if input.Response[7] != 0xee || input.Response[2] != 1 {
		t.Fatal("result aliases input")
	}
	input.Response[6] = 99
	if *got.Fields.NextPosition != 4 {
		t.Fatal("decoded fields alias the raw buffer")
	}
	profile.EndEncoding = InclusivePlaybackEnd
	changed, _ := InspectPlayback(input, profile)
	if originalDigest == changed.ProfileDigest {
		t.Fatal("digest does not bind end interpretation")
	}
	profile.Capabilities.Sources[0] = "different-reference"
	changedAgain, _ := InspectPlayback(input, profile)
	if changed.ProfileDigest == changedAgain.ProfileDigest {
		t.Fatal("digest does not bind evidence")
	}
}

func TestPlaybackInspectionRejectsUnboundedInputAndInvalidProfiles(t *testing.T) {
	input := playbackObservation()
	input.Response = make([]byte, MaxObservedReportBytes+1)
	if _, err := InspectPlayback(input, playbackProfile(Unknown)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	input = playbackObservation()
	input.ReadError = string(make([]byte, 1025))
	if _, err := InspectPlayback(input, playbackProfile(Unknown)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PlaybackProfile){
		func(p *PlaybackProfile) { p.EndEncoding = "" },
		func(p *PlaybackProfile) { p.EndEncoding = "infer-from-model" },
		func(p *PlaybackProfile) { p.Capabilities.ReferenceFirmware = 205 },
		func(p *PlaybackProfile) { p.Capabilities.Sources = nil },
	} {
		profile := playbackProfile(ExclusivePlaybackEnd)
		mutate(&profile)
		if _, err := InspectPlayback(playbackObservation(), profile); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}
