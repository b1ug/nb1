package devicesequence

import "encoding/json"

const (
	ExclusivePlaybackEnd = "exclusive"
	InclusivePlaybackEnd = "inclusive"
)

// PlaybackProfile is trusted provider input, not an author or HTTP request.
// EndEncoding must come from reviewed firmware evidence, never model guessing.
// Matched reference source does not establish physical operation qualification.
type PlaybackProfile struct {
	Capabilities Capabilities `json:"capabilities"`
	EndEncoding  string       `json:"end_encoding"`
}

// PlaybackObservation retains the reported GID prefix, not a USB wire capture.
// An error may accompany a valid prefix; it must remain visible to the caller.
type PlaybackObservation struct {
	Layout         string `json:"layout"`
	ReadAttempted  bool   `json:"read_attempted"`
	ReturnedLength int    `json:"returned_length"`
	Response       []byte `json:"response"`
	ReadError      string `json:"read_error,omitempty"`
}

type PlaybackFields struct {
	Mode         *byte `json:"mode"`
	Start        *byte `json:"start"`
	End          *byte `json:"end"`
	Count        *byte `json:"count"`
	NextPosition *byte `json:"next_position"`
}

type PlaybackInspection struct {
	Observation PlaybackObservation `json:"observation"`
	Complete    bool                `json:"complete"`
	Fields      PlaybackFields      `json:"raw_fields"`
	// ConfiguredRange describes registers, not an active job or lit row.
	ConfiguredRange *Range  `json:"configured_range"`
	ProfileDigest   string  `json:"profile_digest"`
	DecodingStatus  string  `json:"decoding_status"`
	Deployable      bool    `json:"deployable"`
	Issues          []Issue `json:"issues"`
}

// InspectPlayback performs no reads or writes and does not manufacture a
// completed job, current row, repeat mode or visual completion from S bytes.
// Complete concerns the observation only, independently of its interpretation.
func InspectPlayback(input PlaybackObservation, profile PlaybackProfile) (PlaybackInspection, error) {
	if len(input.Response) > MaxObservedReportBytes || len(input.ReadError) > 1024 {
		return PlaybackInspection{}, invalid("observation_limit", "observation", "Raw observations are bounded to 256 bytes and 1024 diagnostic bytes.")
	}
	if err := validateCapabilities(profile.Capabilities); err != nil {
		return PlaybackInspection{}, err
	}
	if profile.EndEncoding != Unknown && profile.EndEncoding != ExclusivePlaybackEnd && profile.EndEncoding != InclusivePlaybackEnd {
		return PlaybackInspection{}, invalid("invalid_profile", "profile.end_encoding", "Supply an explicit reviewed exclusive or inclusive end encoding, or unknown.")
	}
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return PlaybackInspection{}, err
	}
	out := PlaybackInspection{Observation: input, ProfileDigest: digest(profileJSON), DecodingStatus: "incomplete", Issues: []Issue{}}
	out.Observation.Response = append([]byte(nil), input.Response...)
	validPrefix := input.Layout == GIDStandardLayout && input.ReadAttempted && input.ReturnedLength >= 0 && input.ReturnedLength <= 9 && input.ReturnedLength == len(input.Response)
	if validPrefix {
		fields := []**byte{&out.Fields.Mode, &out.Fields.Start, &out.Fields.End, &out.Fields.Count, &out.Fields.NextPosition}
		for i, field := range fields {
			if len(input.Response) > i+2 {
				value := input.Response[i+2]
				*field = &value
			}
		}
	}
	out.Complete = validPrefix && input.ReturnedLength >= 7 && input.ReadError == ""
	if !out.Complete {
		out.Issues = append(out.Issues, Issue{"incomplete_playback_observation", "observation", "A full standard-layout S prefix without a read error is required for interpretation; available raw fields remain inspectable."})
		return out, nil
	}
	caps := profile.Capabilities
	if caps.Applicability != FirmwareMatched || profile.EndEncoding == Unknown || caps.RAMLines == nil {
		out.DecodingStatus = "profile-unresolved"
		out.Issues = append(out.Issues, Issue{"playback_profile_unresolved", "profile", "A firmware-matched reference, explicit end encoding and RAM capacity are required to interpret registers."})
		return out, nil
	}
	// The reviewed reference family uses modes 0..3. Preserve every raw value;
	// unfamiliar modes do not gain the interpretation of a known mode.
	if *out.Fields.Mode > 3 {
		out.DecodingStatus = "unrepresentable"
		out.Issues = append(out.Issues, Issue{"unknown_playback_mode", "raw_fields.mode", "The raw mode has no interpretation in the reviewed reference family."})
		return out, nil
	}
	start, end := int(*out.Fields.Start), int(*out.Fields.End)
	if profile.EndEncoding == InclusivePlaybackEnd {
		end++
	}
	// Do not apply the p command's end=0 sentinel to an S read response.
	r := Range{StartLine: start, LineCount: end - start}
	if !validRange(r) || end > *caps.RAMLines {
		out.DecodingStatus = "unrepresentable"
		out.Issues = append(out.Issues, Issue{"invalid_playback_range", "raw_fields", "The reported register range is outside the supplied RAM capacity; raw fields are preserved."})
		return out, nil
	}
	out.ConfiguredRange, out.DecodingStatus = &r, "reference-fields-interpreted"
	return out, nil
}
