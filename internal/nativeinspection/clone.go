package nativeinspection

import "github.com/b1ug/nb1/internal/devicesequence"

func copyPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copySlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append([]T{}, value...)
}

func cloneError(value *ErrorInfo) *ErrorInfo {
	if value == nil {
		return nil
	}
	out := *value
	out.NativePlatform, out.NativeCode = copyPointer(value.NativePlatform), copyPointer(value.NativeCode)
	return &out
}

func cloneFields(value devicesequence.PlaybackFields) devicesequence.PlaybackFields {
	return devicesequence.PlaybackFields{Mode: copyPointer(value.Mode), Start: copyPointer(value.Start), End: copyPointer(value.End), Count: copyPointer(value.Count), NextPosition: copyPointer(value.NextPosition)}
}

func (value Profile) Clone() Profile {
	out := value
	out.SDKReferenceFirmware, out.RAMLines = copyPointer(value.SDKReferenceFirmware), copyPointer(value.RAMLines)
	out.ReadReference.Sources = copySlice(value.ReadReference.Sources)
	caps := &out.PlaybackProfile.Capabilities
	caps.RAMLines, caps.FlashLines, caps.UsableFlashLines = copyPointer(caps.RAMLines), copyPointer(caps.FlashLines), copyPointer(caps.UsableFlashLines)
	caps.MaxDurationMS, caps.Sources = copyPointer(caps.MaxDurationMS), copySlice(caps.Sources)
	return out
}

// Clone keeps worker, transport and caller-owned buffers and nullable fields
// independent. It does not marshal through JSON or discard private profile data.
func (value Result) Clone() Result {
	out := value
	out.Profile, out.Error = value.Profile.Clone(), cloneError(value.Error)
	if value.RAM != nil {
		ram := *value.RAM
		ram.Rows = copySlice(value.RAM.Rows)
		for i := range ram.Rows {
			row := &ram.Rows[i]
			row.RGB8, row.DurationTicks, row.LEDCode = copyPointer(row.RGB8), copyPointer(row.DurationTicks), copyPointer(row.LEDCode)
			row.Error = cloneError(row.Error)
		}
		ram.UnreadPositions, ram.Issues = copySlice(ram.UnreadPositions), copySlice(ram.Issues)
		ram.SequenceDigest = copyPointer(ram.SequenceDigest)
		if value.RAM.Sequence != nil {
			sequence := *value.RAM.Sequence
			sequence.Lines = copySlice(sequence.Lines)
			ram.Sequence = &sequence
		}
		out.RAM = &ram
	}
	if value.Playback != nil {
		playback := *value.Playback
		playback.Fields = cloneFields(playback.Fields)
		playback.ConfiguredRange, playback.Issues = copyPointer(playback.ConfiguredRange), copySlice(playback.Issues)
		playback.Error = cloneError(playback.Error)
		out.Playback = &playback
	}
	return out
}
