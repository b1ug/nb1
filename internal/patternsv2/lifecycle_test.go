package patternsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/b1ug/nb1/internal/control"
)

func TestUnsupportedSourceAndSnapshotRemainReadOnly(t *testing.T) {
	unknown := advRehash(t, advOrbit(t), func(m map[string]interface{}) {
		advMap(t, m["source"])["contract"] = "nb1-light-action/future-v99"
	})
	i, err := InspectRecord(unknown)
	if err != nil || i.Support().Verified || i.Support().Reason != "unsupported_source" {
		t.Fatalf("future source: %+v %v", i.Support(), err)
	}
	if _, err := NewRecord(i.Record().ID, i.Record().Title, i.Record().Source); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("future source constructed: %v", err)
	}
	known, err := DecodeRecord(designFile(t, "records/pulse-lime.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]interface{}{"schema_version": 2, "kind": SnapshotKind, "records": []json.RawMessage{unknown, mustEncode(t, known)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := InspectSnapshot(snapshot)
	if err != nil || len(s.Records()) != 2 {
		t.Fatalf("inspect mixed snapshot: %v", err)
	}
	if _, err := s.VerifiedRecords(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("mixed snapshot accepted for writes: %v", err)
	}
	if _, err := DecodeSnapshot(snapshot); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict mixed snapshot: %v", err)
	}
	b := s.CanonicalJSON()
	b[0] = '!'
	records := s.Records()
	records[0] = Inspection{}
	again, err := InspectSnapshot(s.CanonicalJSON())
	if err != nil || len(again.Records()) != 2 || !bytes.Equal(again.CanonicalJSON(), s.CanonicalJSON()) {
		t.Fatalf("read-only snapshot alias/roundtrip: %v", err)
	}
	// Unknown source is not an excuse to repair or ignore malformed execution.
	bad := advRehash(t, i.Record(), func(m map[string]interface{}) {
		program := advMap(t, advMap(t, advMap(t, m["compiled"])["execution"])["program"])
		delete(program, "repeat")
	})
	if _, err := InspectRecord(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("future source repaired execution: %v", err)
	}
}

func mustEncode(t *testing.T, r Record) []byte {
	t.Helper()
	b, err := EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDirectGoBoundsAndTypedUnion(t *testing.T) {
	base := advOrbit(t)
	cases := []func(*Record){
		func(r *Record) { r.Source.Action = make([]byte, 65537) },
		func(r *Record) { r.Compiled.NormalizedAction = make([]byte, 65537) },
		func(r *Record) { r.Compiled.Execution.Program = &control.Program{} },
		func(r *Record) {
			r.Compiled.Execution.FrameProgram.Frames = make([]control.Frame, control.MaxFrameProgramFrames+1)
		},
		func(r *Record) { r.Compiled.Execution.FrameProgram.Initial[0].Color = strings.Repeat("a", 4096) },
		func(r *Record) { r.Compiled.Execution.FrameProgram.Frames[0].Targets = make([]control.FrameTarget, 3) },
	}
	for n, change := range cases {
		r := cloneRecord(base)
		change(&r)
		if _, err := NormalizeRecord(r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("direct case %d accepted: %v", n, err)
		}
	}
	tooMany := make([]Record, MaxRecords+1)
	if _, err := EncodeSnapshot(tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("record count: %v", err)
	}
	// Both raw and canonical package sizes are bounded, independently of count.
	n, err := parse([]byte(`{"kind":"nb1-pattern-snapshot/experimental-v2","schema_version":2.0,"records":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := raw(n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSnapshot(wire); !errors.Is(err, ErrInvalid) {
		t.Fatalf("fractional schema version normalized: %v", err)
	}
}

func TestConcurrentInspectionCopiesAreIndependent(t *testing.T) {
	i, err := InspectRecord(mustEncode(t, advOrbit(t)))
	if err != nil {
		t.Fatal(err)
	}
	before := i.CanonicalJSON()
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				r := i.Record()
				r.Source.Action[0] = '!'
				r.Compiled.NormalizedAction[0] = '!'
				r.Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
				b := i.CanonicalJSON()
				b[0] = '!'
			}
		}()
	}
	wg.Wait()
	if !bytes.Equal(before, i.CanonicalJSON()) || i.Record().Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color == "#ffffff" {
		t.Fatal("inspection leaked mutable state")
	}
}

func FuzzInspectRecordRoundTrip(f *testing.F) {
	for _, name := range []string{"records/orbit-red-blue.record.json", "records/pulse-lime.record.json", "cases/unknown-recipe.record.json"} {
		// Keep seeds local and portable; no Engine or user library is touched.
		b, err := os.ReadFile("testdata/design/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	for _, b := range [][]byte{[]byte(`null`), []byte(`{"x":1,"x":2}`), []byte(`{"x":"\ud800"}`)} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		i, err := InspectRecord(data)
		if err != nil {
			return
		}
		again, err := InspectRecord(i.CanonicalJSON())
		if err != nil {
			t.Fatalf("successful inspection did not roundtrip: %v", err)
		}
		if !bytes.Equal(i.CanonicalJSON(), again.CanonicalJSON()) || i.Support() != again.Support() || i.ExecutionDigest() != again.ExecutionDigest() {
			t.Fatal("inspection changed after canonical roundtrip")
		}
		if i.Support().Verified {
			if _, err := DecodeRecord(i.CanonicalJSON()); err != nil {
				t.Fatal(err)
			}
		}
	})
}
