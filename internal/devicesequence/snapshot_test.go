package devicesequence

import (
	"bytes"
	"errors"
	"testing"
)

func observation(position int, led byte, ticks uint16) RowObservation {
	return RowObservation{Position: position, Layout: GIDStandardLayout, ReadAttempted: true, ReturnedLength: 9, Response: []byte{1, 'R', 16, 32, 48, byte(ticks >> 8), byte(ticks), led, 0xee}}
}

func TestRawRowsDecodeKnownFieldsWithoutInventingMetadata(t *testing.T) {
	row := observation(3, 2, 50)
	s := Snapshot{RequestedRange: Range{3, 1}, Complete: true, Rows: []RowObservation{row}}
	got, e := InspectSnapshot(s)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Snapshot.Complete || got.Sequence == nil || got.DecodingStatus != "exact-known-fields" || got.Deployable {
		t.Fatalf("%+v", got)
	}
	if got.Sequence.Lines[0] != (Line{2, "#102030", 500}) || got.DecodedRows[0] != (DecodedRow{3, 2, [3]byte{16, 32, 48}, 50}) {
		t.Fatal(got)
	}
	if got.Snapshot.Rows[0].Response[8] != 0xee || got.Consistency != "sequential-best-effort-not-atomic" {
		t.Fatal(got)
	}
	// Equivalence is only for known row fields, not report headers or tail bytes.
	other := s
	other.Rows = []RowObservation{observation(3, 2, 50)}
	other.Rows[0].Response[1] = '?'
	other.Rows[0].Response[8] = 0xff
	g2, e := InspectSnapshot(other)
	if e != nil || g2.SequenceDigest == nil || *g2.SequenceDigest != *got.SequenceDigest {
		t.Fatalf("%+v %v", g2, e)
	}
	got.Snapshot.Rows[0].Response[2] = 99
	if s.Rows[0].Response[2] != 16 {
		t.Fatal("output aliases caller raw bytes")
	}
	s.Rows[0].Response[3] = 99
	if got.DecodedRows[0].RGB8[1] != 32 {
		t.Fatal("caller mutation changed decoded result")
	}
}

func TestRawZeroAndUnknownRowsRemainSuccessfulSnapshots(t *testing.T) {
	for _, test := range []struct {
		name  string
		led   byte
		ticks uint16
		code  string
	}{{"zero-time", 0, 0, "zero_duration_unqualified"}, {"unknown-led", 77, 50, "unsupported_led_encoding"}, {"special-flag", 0x81, 50, "unsupported_led_encoding"}} {
		t.Run(test.name, func(t *testing.T) {
			row := observation(0, test.led, test.ticks)
			row.Response[2], row.Response[3], row.Response[4] = 0, 0, 0
			got, e := InspectSnapshot(Snapshot{Range{0, 1}, true, []RowObservation{row}})
			if e != nil || !got.Snapshot.Complete || got.Sequence != nil || got.SequenceDigest != nil || got.DecodingStatus != "unrepresentable" || !hasIssue(got.Issues, test.code) {
				t.Fatalf("%+v %v", got, e)
			}
			if !bytes.Equal(got.Snapshot.Rows[0].Response, row.Response) {
				t.Fatal("legacy raw row was rewritten")
			}
		})
	}
}

func TestPartialObservationsNeverBecomeASequence(t *testing.T) {
	mutations := map[string]func(*RowObservation){
		"write-request-failed": func(r *RowObservation) {
			r.ReadAttempted = false
			r.ReturnedLength = 0
			r.Response = nil
			r.ReadError = "typed request failure"
		},
		"short":              func(r *RowObservation) { r.ReturnedLength = 7; r.Response = r.Response[:7] },
		"partial-plus-error": func(r *RowObservation) { r.ReadError = "read failed after a prefix" },
		"negative-length":    func(r *RowObservation) { r.ReturnedLength = -1; r.Response = nil },
		"over-buffer":        func(r *RowObservation) { r.ReturnedLength = 10; r.Response = append(r.Response, 0xfe) },
		"length-disagrees":   func(r *RowObservation) { r.ReturnedLength = 8 },
		"unknown-layout": func(r *RowObservation) {
			r.Layout = "native-other-layout"
			r.Response = append(r.Response, make([]byte, 52)...)
			r.ReturnedLength = 61
		},
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			row := observation(0, 1, 50)
			change(&row)
			got, e := InspectSnapshot(Snapshot{Range{0, 1}, true, []RowObservation{row}})
			if e != nil || got.Snapshot.Complete || got.Sequence != nil || got.DecodingStatus != "incomplete" || len(got.MissingPositions) != 1 || !bytes.Equal(row.Response, got.Snapshot.Rows[0].Response) {
				t.Fatalf("%+v %v", got, e)
			}
		})
	}
	got, e := InspectSnapshot(Snapshot{Range{0, 1}, false, []RowObservation{observation(0, 1, 50)}})
	if e != nil || got.Snapshot.Complete || got.Sequence != nil {
		t.Fatalf("%+v %v", got, e)
	}
}

func TestSnapshotRangeAndPositionIntegrity(t *testing.T) {
	for _, rows := range [][]RowObservation{{observation(4, 1, 50)}, {observation(3, 1, 50), observation(3, 2, 50)}, {observation(3, 1, 50)}} {
		got, e := InspectSnapshot(Snapshot{Range{3, 2}, true, rows})
		if e != nil || got.Snapshot.Complete || got.Sequence != nil {
			t.Fatalf("%+v %v", got, e)
		}
	}
	// Response order can differ; the author sequence follows absolute addresses.
	a, b := observation(4, 2, 10), observation(3, 1, 50)
	got, e := InspectSnapshot(Snapshot{Range{3, 2}, true, []RowObservation{a, b}})
	if e != nil || got.Sequence == nil || got.Sequence.Lines[0].LED != 1 || got.Sequence.Lines[1].LED != 2 {
		t.Fatalf("%+v %v", got, e)
	}
	for _, r := range []Range{{-1, 1}, {0, 0}, {31, 2}, {32, 1}, {0, 1 << 30}} {
		if _, e := InspectSnapshot(Snapshot{RequestedRange: r}); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}

func TestInspectionBoundsAndEightByteResponse(t *testing.T) {
	row := observation(0, 1, 50)
	row.Response = row.Response[:8]
	row.ReturnedLength = 8
	got, e := InspectSnapshot(Snapshot{Range{0, 1}, true, []RowObservation{row}})
	if e != nil || got.Sequence == nil {
		t.Fatalf("%+v %v", got, e)
	}
	row.Response = make([]byte, MaxObservedReportBytes+1)
	if _, e = InspectSnapshot(Snapshot{Range{0, 1}, true, []RowObservation{row}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	row = observation(0, 1, 50)
	row.ReadError = string(make([]byte, 1025))
	if _, e = InspectSnapshot(Snapshot{Range{0, 1}, true, []RowObservation{row}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
