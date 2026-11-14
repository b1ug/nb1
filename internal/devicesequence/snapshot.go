package devicesequence

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const GIDStandardLayout = "gid-standard-report-id-slot/v1"
const MaxObservedReportBytes = 256

// RowObservation retains only bytes the transport says it returned. This is a
// GID buffer observation, not a claim to capture the physical USB transaction.
// Position is the requested absolute address, not inferred from response[7].
type RowObservation struct {
	Position       int    `json:"position"`
	Layout         string `json:"layout"`
	ReadAttempted  bool   `json:"read_attempted"`
	ReturnedLength int    `json:"returned_length"`
	Response       []byte `json:"response"`
	ReadError      string `json:"read_error,omitempty"`
}

type Snapshot struct {
	RequestedRange Range `json:"requested_range"`
	// A false collector flag cannot be promoted to complete by the decoder.
	Complete bool             `json:"complete"`
	Rows     []RowObservation `json:"rows"`
}

type DecodedRow struct {
	Position      int     `json:"position"`
	LEDCode       byte    `json:"led_code"`
	RGB8          [3]byte `json:"rgb8"`
	DurationTicks uint16  `json:"duration_ticks"`
}

type Inspection struct {
	Snapshot         Snapshot     `json:"snapshot"`
	Consistency      string       `json:"consistency"`
	DecodedRows      []DecodedRow `json:"decoded_rows"`
	MissingPositions []int        `json:"missing_positions"`
	Sequence         *Sequence    `json:"sequence"`
	SequenceDigest   *string      `json:"sequence_digest"`
	DecodingStatus   string       `json:"decoding_status"`
	Deployable       bool         `json:"deployable"`
	Issues           []Issue      `json:"issues"`
}

// InspectSnapshot preserves raw observations before author validation. A
// complete raw snapshot can contain unrepresentable zero-time or special rows.
// Partial/error/unknown-layout observations never produce a complete sequence.
// No read, import, replay or device operation takes place here.
func InspectSnapshot(input Snapshot) (Inspection, error) {
	if !validRange(input.RequestedRange) {
		return Inspection{}, invalid("range_out_of_bounds", "requested_range", "Snapshot addresses must stay within0..31.")
	}
	if len(input.Rows) > MaxLines {
		return Inspection{}, invalid("too_many_observations", "rows", "A snapshot can contain at most32 row observations.")
	}
	out := Inspection{Snapshot: Snapshot{RequestedRange: input.RequestedRange, Complete: input.Complete, Rows: make([]RowObservation, 0, len(input.Rows))}, Consistency: "sequential-best-effort-not-atomic", DecodedRows: []DecodedRow{}, MissingPositions: []int{}, DecodingStatus: "unrepresentable", Issues: []Issue{}}
	seen := make(map[int]bool)
	validRows := make(map[int]DecodedRow)
	for index, row := range input.Rows {
		if len(row.Response) > MaxObservedReportBytes || len(row.ReadError) > 1024 {
			return Inspection{}, invalid("observation_limit", fmt.Sprintf("rows[%d]", index), "Raw observations are bounded to256 bytes and1024 diagnostic bytes; unknown layouts remain undecoded.")
		}
		row.Response = append([]byte(nil), row.Response...)
		out.Snapshot.Rows = append(out.Snapshot.Rows, row)
		path := fmt.Sprintf("rows[%d]", index)
		if row.Position < input.RequestedRange.StartLine || row.Position >= input.RequestedRange.StartLine+input.RequestedRange.LineCount || seen[row.Position] {
			out.Snapshot.Complete = false
			out.Issues = append(out.Issues, Issue{"unexpected_or_duplicate_position", path + ".position", "The observation does not identify a unique requested absolute position."})
			continue
		}
		seen[row.Position] = true
		if row.Layout != GIDStandardLayout || !row.ReadAttempted || row.ReturnedLength < 8 || row.ReturnedLength > 9 || row.ReturnedLength != len(row.Response) || row.ReadError != "" {
			out.Snapshot.Complete = false
			out.Issues = append(out.Issues, Issue{"incomplete_row_observation", path, "A full standard-layout R response without a read error is required for decoding."})
			continue
		}
		data := row.Response
		decoded := DecodedRow{row.Position, data[7], [3]byte{data[2], data[3], data[4]}, uint16(data[5])<<8 | uint16(data[6])}
		validRows[row.Position] = decoded
		out.DecodedRows = append(out.DecodedRows, decoded)
		if decoded.LEDCode > 2 {
			out.Issues = append(out.Issues, Issue{"unsupported_led_encoding", path, "Raw LED codes and special flags remain inspectable but are not authorable by this draft."})
		}
		if decoded.DurationTicks == 0 {
			out.Issues = append(out.Issues, Issue{"zero_duration_unqualified", path, "Zero-time rows remain in the snapshot; they are not silently removed or changed."})
		}
	}
	for i := input.RequestedRange.StartLine; i < input.RequestedRange.StartLine+input.RequestedRange.LineCount; i++ {
		if _, ok := validRows[i]; !ok {
			out.MissingPositions = append(out.MissingPositions, i)
			out.Snapshot.Complete = false
		}
	}
	if !out.Snapshot.Complete {
		out.DecodingStatus = "incomplete"
		out.Issues = append(out.Issues, Issue{"snapshot_incomplete", "complete", "An incomplete observation cannot become a complete DeviceSequence."})
		return out, nil
	}
	if len(out.Issues) != 0 {
		return out, nil
	}
	sequence := Sequence{Contract: Contract, ColorSpace: ColorSpace, Lines: make([]Line, 0, input.RequestedRange.LineCount)}
	for i := input.RequestedRange.StartLine; i < input.RequestedRange.StartLine+input.RequestedRange.LineCount; i++ {
		row := validRows[i]
		sequence.Lines = append(sequence.Lines, Line{LED(row.LEDCode), "#" + hex.EncodeToString(row.RGB8[:]), int(row.DurationTicks) * TickMS})
	}
	canonical, err := json.Marshal(sequence)
	if err != nil {
		return Inspection{}, err
	}
	value := digest(canonical)
	out.Sequence, out.SequenceDigest, out.DecodingStatus = &sequence, &value, "exact-known-fields"
	return out, nil
}
