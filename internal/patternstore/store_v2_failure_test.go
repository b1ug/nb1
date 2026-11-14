package patternstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/patternsv2"
)

type failureV2Transaction struct {
	fakeTransaction
	reads      int
	readLimit  int
	publishRaw []byte
}

func (tx *failureV2Transaction) read(limit int) ([]byte, error) {
	tx.reads++
	tx.readLimit = limit
	return tx.fakeTransaction.read(limit)
}

func (tx *failureV2Transaction) publish(data []byte) (bool, error) {
	tx.publishRaw = append([]byte(nil), data...)
	return tx.fakeTransaction.publish(data)
}

func failureV2Record(t *testing.T, id, title string) patternsv2.Record {
	t.Helper()
	r, err := patternsv2.NewRecord(id, title, patternsv2.Source{
		Kind: "light_action", Contract: lightaction.Contract,
		Action: json.RawMessage(`{"kind":"set","color":"lime"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func failureV2Snapshot(t *testing.T, records ...patternsv2.Record) []byte {
	t.Helper()
	b, err := patternsv2.EncodeSnapshot(records)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func failureV2Store(t *testing.T, tx *failureV2Transaction, wantCreate bool) *StoreV2 {
	t.Helper()
	s, err := NewV2(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	s.open = func(path string, create bool) (transaction, error) {
		if path != s.Path() || create != wantCreate {
			t.Fatalf("open(%q, %t), want (%q, %t)", path, create, s.Path(), wantCreate)
		}
		return tx, nil
	}
	return s
}

func failureV2Zero(t *testing.T, got ImportResultV2) {
	t.Helper()
	if !reflect.DeepEqual(got, ImportResultV2{}) {
		t.Fatalf("failure returned partial success: %+v", got)
	}
}

func failureV2ReadonlySnapshot(t *testing.T, known patternsv2.Record) ([]byte, string, string) {
	t.Helper()
	unknown, err := os.ReadFile(filepath.Join("..", "patternsv2", "testdata", "design", "cases", "unknown-recipe.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	i, err := patternsv2.InspectRecord(unknown)
	if err != nil || i.Support().Verified {
		t.Fatalf("readonly fixture: %+v %v", i.Support(), err)
	}
	k, err := patternsv2.EncodeRecord(known)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(map[string]interface{}{
		"schema_version": 2, "kind": patternsv2.SnapshotKind,
		"records": []json.RawMessage{k, unknown},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := patternsv2.InspectSnapshot(wire)
	if err != nil {
		t.Fatal(err)
	}
	r := i.Record()
	return snapshot.CanonicalJSON(), r.ID, r.Revision
}

func TestStoreV2FailurePublicationBoundary(t *testing.T) {
	writeErr := errors.New("injected v2 write failure")
	closeErr := errors.New("injected v2 close failure")
	input := failureV2Snapshot(t, failureV2Record(t, "new", "新图案"))
	for _, tc := range []struct {
		name      string
		published bool
		writeErr  error
		closeErr  error
		want      error
	}{
		{"before-publish", false, writeErr, nil, writeErr},
		{"before-publish-and-close", false, writeErr, closeErr, writeErr},
		{"published-but-confirmation-failed", true, writeErr, nil, ErrCommitUncertain},
		{"published-but-close-failed", true, nil, closeErr, ErrCommitUncertain},
		{"published-confirmation-and-close-failed", true, writeErr, closeErr, ErrCommitUncertain},
		{"false-without-error-is-not-success", false, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &failureV2Transaction{fakeTransaction: fakeTransaction{published: tc.published, writeErr: tc.writeErr, closeErr: tc.closeErr}}
			s := failureV2Store(t, tx, true)
			result, err := s.Import(input)
			failureV2Zero(t, result)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("publication failure=%v, want %v", err, tc.want)
			}
			if !tc.published && errors.Is(err, ErrCommitUncertain) {
				t.Fatal("pre-publication failure incorrectly became publication_unknown")
			}
			if tx.reads != 1 || tx.readLimit != patternsv2.MaxBytes || tx.writes != 1 || tx.closed != 1 {
				t.Fatalf("reads=%d limit=%d writes=%d closes=%d", tx.reads, tx.readLimit, tx.writes, tx.closed)
			}
			if _, err := patternsv2.DecodeSnapshot(tx.publishRaw); err != nil {
				t.Fatalf("writer received invalid snapshot: %v", err)
			}
		})
	}
}

func TestStoreV2FailureInvalidInputNeverOpens(t *testing.T) {
	known := failureV2Record(t, "known", "Known")
	unknown, _, _ := failureV2ReadonlySnapshot(t, known)
	valid := failureV2Snapshot(t, known)
	for name, input := range map[string][]byte{
		"invalid-json":       []byte(`{"schema_version":`),
		"legacy-package":     []byte(`{"schema_version":1,"patterns":[]}`),
		"unknown-recipe":     unknown,
		"second-value":       append(append([]byte(nil), valid...), []byte(` {}`)...),
		"oversized-package":  []byte(strings.Repeat(" ", patternsv2.MaxBytes+1)),
		"explicit-null":      []byte(`{"schema_version":2,"kind":"nb1-pattern-snapshot/experimental-v2","records":null}`),
		"fractional-version": []byte(`{"schema_version":2.0,"kind":"nb1-pattern-snapshot/experimental-v2","records":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			s, err := NewV2(filepath.Join(t.TempDir(), "not-opened"))
			if err != nil {
				t.Fatal(err)
			}
			s.open = func(string, bool) (transaction, error) {
				t.Fatal("unverified input reached filesystem transaction")
				return nil, nil
			}
			result, err := s.Import(input)
			failureV2Zero(t, result)
			if err == nil || errors.Is(err, ErrCorrupt) || errors.Is(err, ErrCommitUncertain) {
				t.Fatalf("bad incoming input was not cleanly rejected: %v", err)
			}
		})
	}
}

func TestStoreV2FailureReadAndOpenErrorsNeverPublish(t *testing.T) {
	input := failureV2Snapshot(t, failureV2Record(t, "new", "New"))
	readErr := errors.New("injected v2 read failure")
	closeErr := errors.New("injected v2 close failure")
	tx := &failureV2Transaction{fakeTransaction: fakeTransaction{readErr: readErr, closeErr: closeErr}}
	s := failureV2Store(t, tx, true)
	result, err := s.Import(input)
	failureV2Zero(t, result)
	if !errors.Is(err, readErr) || errors.Is(err, ErrCommitUncertain) || tx.writes != 0 || tx.closed != 1 {
		t.Fatalf("read failure err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
	}
	openErr := errors.New("injected v2 open failure")
	var calls int
	s.open = func(string, bool) (transaction, error) { calls++; return nil, openErr }
	result, err = s.Import(input)
	failureV2Zero(t, result)
	if !errors.Is(err, openErr) || calls != 1 || tx.closed != 1 {
		t.Fatalf("open failure err=%v calls=%d old closes=%d", err, calls, tx.closed)
	}
}

func TestStoreV2FailureReadCloseDiscardsAllResultShapes(t *testing.T) {
	r := failureV2Record(t, "known", "Known")
	data := failureV2Snapshot(t, r)
	closeErr := errors.New("injected read close failure")
	for _, name := range []string{"list", "get", "resolve", "export"} {
		t.Run(name, func(t *testing.T) {
			tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: data, closeErr: closeErr}}
			s := failureV2Store(t, tx, false)
			var err error
			switch name {
			case "list":
				var out []patternsv2.Inspection
				out, err = s.List()
				if out != nil {
					t.Fatal("List returned data after close failure")
				}
			case "get":
				var out patternsv2.Inspection
				out, err = s.Get(r.ID)
				if out.Record().ID != "" || out.CanonicalJSON() != nil {
					t.Fatal("Get returned data after close failure")
				}
			case "resolve":
				var out patternsv2.Record
				out, err = s.Resolve(r.ID, r.Revision)
				if out.ID != "" {
					t.Fatal("Resolve returned data after close failure")
				}
			case "export":
				var out []byte
				out, err = s.Export()
				if out != nil {
					t.Fatal("Export returned data after close failure")
				}
			}
			if !errors.Is(err, closeErr) || errors.Is(err, ErrCommitUncertain) || tx.closed != 1 || tx.writes != 0 {
				t.Fatalf("close failure err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
			}
		})
	}
}

func TestStoreV2FailureIdempotentCloseIsNotPublicationUnknown(t *testing.T) {
	input := failureV2Snapshot(t, failureV2Record(t, "known", "Known"))
	closeErr := errors.New("injected idempotent close failure")
	tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: input, closeErr: closeErr}}
	result, err := failureV2Store(t, tx, true).Import(input)
	failureV2Zero(t, result)
	if !errors.Is(err, closeErr) || errors.Is(err, ErrCommitUncertain) || tx.writes != 0 || tx.closed != 1 {
		t.Fatalf("idempotent close failure err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
	}
}

func TestStoreV2FailureReadonlyLibraryCannotBeRewritten(t *testing.T) {
	known := failureV2Record(t, "known", "Known")
	data, _, _ := failureV2ReadonlySnapshot(t, known)
	for name, input := range map[string][]byte{
		"empty":      failureV2Snapshot(t),
		"idempotent": failureV2Snapshot(t, known),
		"new":        failureV2Snapshot(t, failureV2Record(t, "new", "New")),
	} {
		t.Run(name, func(t *testing.T) {
			tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: data}}
			result, err := failureV2Store(t, tx, true).Import(input)
			failureV2Zero(t, result)
			if !errors.Is(err, ErrReadOnly) || errors.Is(err, ErrCorrupt) || tx.writes != 0 || tx.closed != 1 {
				t.Fatalf("readonly err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
			}
			if !bytes.Equal(data, tx.data) {
				t.Fatal("readonly snapshot was mutated")
			}
		})
	}
}

func TestStoreV2FailureReadonlyAllowsKnownResolveAndExport(t *testing.T) {
	known := failureV2Record(t, "known", "Known")
	data, unknownID, unknownRevision := failureV2ReadonlySnapshot(t, known)
	for _, op := range []string{"known-resolve", "unknown-resolve", "wrong-revision", "get-unknown", "export"} {
		t.Run(op, func(t *testing.T) {
			tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: data}}
			s := failureV2Store(t, tx, false)
			switch op {
			case "known-resolve":
				got, err := s.Resolve(known.ID, known.Revision)
				if err != nil || !reflect.DeepEqual(got, known) {
					t.Fatalf("known record was blocked by another readonly record: %+v %v", got, err)
				}
			case "unknown-resolve":
				got, err := s.Resolve(unknownID, unknownRevision)
				if !errors.Is(err, patternsv2.ErrUnsupported) || errors.Is(err, ErrCorrupt) || got.ID != "" {
					t.Fatalf("unknown resolved as executable or corrupt: %+v %v", got, err)
				}
			case "wrong-revision":
				got, err := s.Resolve(unknownID, "sha256:"+strings.Repeat("0", 64))
				if !errors.Is(err, ErrRevisionConflict) || got.ID != "" {
					t.Fatalf("exact revision was not checked before support: %+v %v", got, err)
				}
			case "get-unknown":
				got, err := s.Get(unknownID)
				if err != nil || got.Support().Verified || got.Record().Revision != unknownRevision {
					t.Fatalf("unknown Get was not a readonly inspection: %+v %v", got.Support(), err)
				}
			case "export":
				got, err := s.Export()
				if err != nil || !bytes.Equal(data, got) {
					t.Fatalf("readonly export changed data: %v", err)
				}
			}
			if tx.writes != 0 || tx.closed != 1 {
				t.Fatalf("readonly operation writes=%d closes=%d", tx.writes, tx.closed)
			}
		})
	}
}

func TestStoreV2FailureWrongOrNoncanonicalDiskDataNeverPublishes(t *testing.T) {
	legacy, err := os.ReadFile(filepath.Join("..", "patternsv2", "testdata", "design", "legacy-v1.snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := failureV2Snapshot(t, failureV2Record(t, "known", "Known"))
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"legacy-v1", legacy, ErrFormatMismatch},
		{"trailing-newline", append(append([]byte(nil), valid...), '\n'), ErrCorrupt},
		{"corrupt-json", []byte(`{"broken":`), ErrCorrupt},
		{"empty-file", []byte{}, ErrCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: tc.data}}
			result, err := failureV2Store(t, tx, true).Import(valid)
			failureV2Zero(t, result)
			if !errors.Is(err, tc.want) || tx.writes != 0 || tx.closed != 1 {
				t.Fatalf("wrong-disk-data err=%v want=%v writes=%d closes=%d", err, tc.want, tx.writes, tx.closed)
			}
			if !bytes.Equal(tc.data, tx.data) {
				t.Fatal("invalid disk data was modified")
			}
		})
	}
}

func TestStoreV2FailureConflictAndCombinedCountAreAtomic(t *testing.T) {
	known := failureV2Record(t, "known", "Known")
	current := failureV2Snapshot(t, known)
	conflict := failureV2Record(t, "known", "Different title")
	incoming := failureV2Snapshot(t, failureV2Record(t, "aaa-new", "New first"), conflict)
	tx := &failureV2Transaction{fakeTransaction: fakeTransaction{data: current}}
	result, err := failureV2Store(t, tx, true).Import(incoming)
	failureV2Zero(t, result)
	if !errors.Is(err, ErrConflict) || tx.writes != 0 || tx.closed != 1 {
		t.Fatalf("partial merge escaped conflict: err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
	}
	records := make([]patternsv2.Record, patternsv2.MaxRecords)
	for i := range records {
		records[i] = failureV2Record(t, fmt.Sprintf("r%03d", i), "Record")
	}
	tx = &failureV2Transaction{fakeTransaction: fakeTransaction{data: failureV2Snapshot(t, records...)}}
	incoming = failureV2Snapshot(t, failureV2Record(t, "extra", "Extra"))
	result, err = failureV2Store(t, tx, true).Import(incoming)
	failureV2Zero(t, result)
	if !errors.Is(err, ErrLimit) || tx.writes != 0 || tx.closed != 1 {
		t.Fatalf("combined record count overflow: err=%v writes=%d closes=%d", err, tx.writes, tx.closed)
	}
}
