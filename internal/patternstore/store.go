// Package patternstore manages one bounded, create-only local Pattern library.
// It does not play patterns, create Jobs, or access hardware.
package patternstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/b1ug/nb1/internal/patterns"
)

var (
	ErrInvalid          = errors.New("invalid pattern store request")
	ErrLimit            = errors.New("pattern library limit exceeded")
	ErrNotFound         = errors.New("pattern not found")
	ErrConflict         = errors.New("pattern id already has different content")
	ErrRevisionConflict = errors.New("pattern revision does not match")
	ErrCorrupt          = errors.New("pattern library is corrupt or noncanonical")
	ErrBusy             = errors.New("pattern library is busy")
	ErrUnsafe           = errors.New("pattern library path or files are unsafe")
	ErrUnsupported      = errors.New("pattern persistence is unsupported on this platform")
	ErrCommitUncertain  = errors.New("pattern publication occurred but final confirmation failed")
)

// transaction is private so callers cannot bypass validation or publish
// arbitrary bytes. published distinguishes failures after atomic publication
// from failures that leave the previous snapshot in place.
type transaction interface {
	read(maxBytes int) ([]byte, error)
	publish(data []byte) (published bool, err error)
	close() error
}

// Store has no open handles or cache. Each operation obtains a short,
// cross-process library lock and checks the current on-disk snapshot.
type Store struct {
	mu   sync.Mutex
	dir  string
	open func(string, bool) (transaction, error)
}

type ImportResult struct {
	Records  []patterns.Record `json:"records"`
	Created  []string          `json:"created"`
	Existing []string          `json:"existing"`
	Total    int               `json:"total"`
}

// DefaultDir reports the current user's application directory; it creates
// nothing and does not fall back to a source, system, or working directory.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("%w: application directory must be absolute", ErrInvalid)
	}
	return filepath.Join(base, "nb1", "patterns"), nil
}

// New validates the explicitly selected directory without reading or writing
// it. Filesystem ownership, privacy, type and identity are checked per operation.
func New(dir string) (*Store, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: library directory must be absolute", ErrInvalid)
	}
	return &Store{dir: filepath.Clean(dir), open: func(path string, create bool) (transaction, error) {
		return openFS(path, create)
	}}, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// List returns independent frozen records in exact ASCII ID order. A library
// directory that does not exist is empty and is not created by a read.
func (s *Store) List() (records []patterns.Record, err error) {
	err = s.withRead(func(values []patterns.Record) error {
		records = values
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) Get(id string) (record patterns.Record, err error) {
	if !validID(id) {
		return record, fmt.Errorf("%w: invalid id", ErrInvalid)
	}
	err = s.withRead(func(records []patterns.Record) error {
		for _, value := range records {
			if value.ID == id {
				record = value
				return nil
			}
		}
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	})
	if err != nil {
		return patterns.Record{}, err
	}
	return record, nil
}

// Resolve requires an exact content revision and returns the saved snapshot,
// never a newly compiled replacement. Callers may submit its CompiledProgram
// through the existing Job path; resolving itself has no execution behavior.
func (s *Store) Resolve(id, revision string) (patterns.Record, error) {
	if !validRevision(revision) {
		return patterns.Record{}, fmt.Errorf("%w: revision must be sha256 followed by 64 lowercase hex digits", ErrInvalid)
	}
	record, err := s.Get(id)
	if err != nil {
		return patterns.Record{}, err
	}
	if record.Revision != revision {
		return patterns.Record{}, fmt.Errorf("%w: %q", ErrRevisionConflict, id)
	}
	return record, nil
}

// Export returns a canonical frozen package, including an empty records array
// for a missing library. It does not select or write an external output file.
func (s *Store) Export() (data []byte, err error) {
	err = s.withRead(func(records []patterns.Record) error {
		data, err = patterns.EncodeSnapshot(records)
		return err
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Import validates the entire authoring or frozen package before touching the
// library. Equal IDs and canonical content are idempotent; any conflict rejects
// the entire package. There is no replacement, deletion, replay or migration.
func (s *Store) Import(data []byte) (result ImportResult, err error) {
	incoming, err := patterns.DecodePackage(data)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err = s.valid(); err != nil {
		return result, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.open(s.dir, true)
	if err != nil {
		return result, err
	}
	published := false
	defer func() {
		closeErr := tx.close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			result = ImportResult{}
			if published {
				err = fmt.Errorf("%w: %v", ErrCommitUncertain, err)
			}
		}
	}()
	current, err := load(tx)
	if err != nil {
		return result, err
	}
	byID := make(map[string]patterns.Record, len(current)+len(incoming))
	for _, record := range current {
		byID[record.ID] = record
	}
	result.Records = make([]patterns.Record, 0, len(incoming))
	result.Created = make([]string, 0, len(incoming))
	result.Existing = make([]string, 0, len(incoming))
	for _, record := range incoming {
		if previous, exists := byID[record.ID]; exists {
			if !sameContent(previous, record) {
				return result, fmt.Errorf("%w: %q", ErrConflict, record.ID)
			}
			result.Existing = append(result.Existing, record.ID)
			result.Records = append(result.Records, previous)
			continue
		}
		byID[record.ID] = record
		result.Created = append(result.Created, record.ID)
		result.Records = append(result.Records, record)
	}
	if len(byID) > patterns.MaxRecords {
		return result, fmt.Errorf("%w: at most %d records", ErrLimit, patterns.MaxRecords)
	}
	merged := make([]patterns.Record, 0, len(byID))
	for _, record := range byID {
		merged = append(merged, record)
	}
	sortRecords(merged)
	// Both inputs have already passed full record validation. The remaining
	// EncodeSnapshot failure is the combined canonical package byte budget.
	encoded, err := patterns.EncodeSnapshot(merged)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrLimit, err)
	}
	result.Total = len(merged)
	if len(result.Created) == 0 {
		return result, nil
	}
	published, err = tx.publish(encoded)
	if err == nil && !published {
		err = errors.New("pattern publication was not confirmed")
	}
	return result, err
}

func (s *Store) valid() error {
	if s == nil || s.dir == "" || s.open == nil {
		return fmt.Errorf("%w: use New to select an absolute library directory", ErrInvalid)
	}
	return nil
}

func (s *Store) withRead(fn func([]patterns.Record) error) (err error) {
	if err = s.valid(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.open(s.dir, false)
	if errors.Is(err, os.ErrNotExist) {
		return fn(make([]patterns.Record, 0))
	}
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := tx.close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	records, err := load(tx)
	if err != nil {
		return err
	}
	return fn(records)
}

func load(tx transaction) ([]patterns.Record, error) {
	data, err := tx.read(patterns.MaxPackageBytes)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return make([]patterns.Record, 0), nil
	}
	records, err := patterns.DecodePackage(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	sortRecords(records)
	canonical, err := patterns.EncodeSnapshot(records)
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("%w: expected an exact sorted frozen snapshot", ErrCorrupt)
	}
	return records, nil
}

func sortRecords(records []patterns.Record) {
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
}

func sameContent(a, b patterns.Record) bool {
	if a.Revision != b.Revision {
		return false
	}
	x, errA := patterns.CanonicalRecordJSON(a)
	y, errB := patterns.CanonicalRecordJSON(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func validID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := range value {
		c := value[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return false
		}
	}
	return true
}

func validRevision(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, c := range value[len("sha256:"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
