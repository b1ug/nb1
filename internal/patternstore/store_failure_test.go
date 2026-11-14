package patternstore

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeTransaction struct {
	data      []byte
	readErr   error
	published bool
	writeErr  error
	closeErr  error
	writes    int
	closed    int
}

func (tx *fakeTransaction) read(int) ([]byte, error) { return tx.data, tx.readErr }
func (tx *fakeTransaction) publish([]byte) (bool, error) {
	tx.writes++
	return tx.published, tx.writeErr
}
func (tx *fakeTransaction) close() error { tx.closed++; return tx.closeErr }

func TestPublicationFailureBoundary(t *testing.T) {
	writeFailure := errors.New("injected publication failure")
	closeFailure := errors.New("injected close failure")
	for _, test := range []struct {
		name      string
		published bool
		writeErr  error
		closeErr  error
		want      error
	}{
		{"before-publication", false, writeFailure, nil, writeFailure},
		{"before-publication-close-also-fails", false, writeFailure, closeFailure, writeFailure},
		{"after-publication", true, writeFailure, nil, ErrCommitUncertain},
		{"after-publication-close-fails", true, nil, closeFailure, ErrCommitUncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := New(filepath.Join(t.TempDir(), "library"))
			if err != nil {
				t.Fatal(err)
			}
			tx := &fakeTransaction{published: test.published, writeErr: test.writeErr, closeErr: test.closeErr}
			store.open = func(string, bool) (transaction, error) { return tx, nil }
			result, err := store.Import([]byte(`{"schema_version":1,"patterns":[{"id":"a","title":"A","effect":{"kind":"steady","color":"#ff0000"}}]}`))
			if !errors.Is(err, test.want) || !reflect.DeepEqual(result, ImportResult{}) {
				t.Fatalf("result=%+v err=%v, want zero result and %v", result, err, test.want)
			}
			if tx.writes != 1 || tx.closed != 1 {
				t.Fatalf("writes=%d closed=%d", tx.writes, tx.closed)
			}
		})
	}
}

func TestInvalidImportNeverOpensFilesystem(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	store.open = func(string, bool) (transaction, error) {
		t.Fatal("invalid package touched filesystem")
		return nil, nil
	}
	if _, err := store.Import([]byte(`{"schema_version":1,"patterns":[{"id":"a","title":"A","effect":{"kind":"steady","color":"red"}}]}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestUnconfirmedPublicationReturnsNoSuccess(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	tx := &fakeTransaction{} // Neither an error nor an acknowledged publication.
	store.open = func(string, bool) (transaction, error) { return tx, nil }
	result, err := store.Import([]byte(`{"schema_version":1,"patterns":[{"id":"a","title":"A","effect":{"kind":"steady","color":"#ff0000"}}]}`))
	if err == nil || !reflect.DeepEqual(result, ImportResult{}) || tx.closed != 1 {
		t.Fatalf("result=%+v err=%v closed=%d", result, err, tx.closed)
	}
}

func TestCorruptSnapshotDoesNotPublish(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	tx := &fakeTransaction{data: []byte(`{"schema_version":1,"patterns":[]}`)}
	store.open = func(string, bool) (transaction, error) { return tx, nil }
	if _, err := store.Import([]byte(`{"schema_version":1,"patterns":[]}`)); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	if tx.writes != 0 || tx.closed != 1 {
		t.Fatalf("writes=%d closed=%d", tx.writes, tx.closed)
	}
}

func TestReadCloseFailureReturnsNoData(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	closeFailure := errors.New("injected close failure")
	tx := &fakeTransaction{closeErr: closeFailure}
	store.open = func(string, bool) (transaction, error) { return tx, nil }
	if records, err := store.List(); records != nil || !errors.Is(err, closeFailure) {
		t.Fatalf("records=%v err=%v", records, err)
	}
	if tx.closed != 1 {
		t.Fatalf("closed=%d", tx.closed)
	}
}
