package patternstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternsv2"
)

var (
	// ErrReadOnly distinguishes an intact but unsupported library from corrupt
	// data. Even an empty or idempotent import must not rewrite such a library.
	ErrReadOnly       = errors.New("pattern library is read-only because a source or recipe is unsupported")
	ErrFormatMismatch = errors.New("pattern library uses a different snapshot format")
)

// StoreV2 is an independent, experimental library format. It uses the same
// private transaction/openFS implementation as Store, including all platform
// permissions, locking, identity checks, publication and close behavior. No
// operation reads a runtime library, submits a job or accesses a device.
type StoreV2 struct {
	mu   sync.Mutex
	dir  string
	open func(string, bool) (transaction, error)
}

type ImportResultV2 struct {
	Records  []patternsv2.Record `json:"records"`
	Created  []string            `json:"created"`
	Existing []string            `json:"existing"`
	Total    int                 `json:"total"`
}

// DefaultV2Dir only computes a path. It never reads, creates, migrates or
// combines a v1/v2 directory; selecting a path remains the caller's job.
func DefaultV2Dir() (string, error) {
	legacy, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(legacy), "patterns-v2"), nil
}

func NewV2(dir string) (*StoreV2, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: library directory must be absolute", ErrInvalid)
	}
	return &StoreV2{dir: filepath.Clean(dir), open: func(path string, create bool) (transaction, error) {
		return openFS(path, create)
	}}, nil
}

func (s *StoreV2) Path() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// List includes explicit read-only inspections for supported outer records
// whose source/recipe verifier is unavailable. Each inspection owns its data.
func (s *StoreV2) List() (records []patternsv2.Inspection, err error) {
	err = s.withRead(func(values []patternsv2.Inspection, _ []byte) error { records = values; return nil })
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *StoreV2) Get(id string) (record patternsv2.Inspection, err error) {
	if !validID(id) {
		return record, fmt.Errorf("%w: invalid id", ErrInvalid)
	}
	err = s.withRead(func(values []patternsv2.Inspection, _ []byte) error {
		for _, value := range values {
			if value.Record().ID == id {
				record = value
				return nil
			}
		}
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	})
	if err != nil {
		return patternsv2.Inspection{}, err
	}
	return record, nil
}

// Resolve requires exact identity and a known verifier for this record only.
// Another unsupported record in the library does not block this read. It never
// returns a current-compiler replacement or claims execution availability.
func (s *StoreV2) Resolve(id, revision string) (patternsv2.Record, error) {
	if !validRevision(revision) {
		return patternsv2.Record{}, fmt.Errorf("%w: invalid exact revision", ErrInvalid)
	}
	i, err := s.Get(id)
	if err != nil {
		return patternsv2.Record{}, err
	}
	if i.Record().Revision != revision {
		return patternsv2.Record{}, fmt.Errorf("%w: %q", ErrRevisionConflict, id)
	}
	return i.VerifiedRecord()
}

// Export preserves the exact canonical snapshot, including intact unsupported
// records. It does not encode those records via the strict write path.
func (s *StoreV2) Export() (data []byte, err error) {
	err = s.withRead(func(_ []patternsv2.Inspection, canonical []byte) error {
		data = append([]byte(nil), canonical...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Import accepts a complete, verified v2 frozen snapshot, never an authoring
// envelope or raw FrameProgram. Input validation finishes before opening or
// creating the library. The transaction is create-only and all-or-nothing.
func (s *StoreV2) Import(data []byte) (result ImportResultV2, err error) {
	incomingSnapshot, err := patternsv2.InspectSnapshot(data)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := incomingSnapshot.VerifiedRecords(); err != nil {
		return result, fmt.Errorf("%w: import requires verified records: %v", ErrInvalid, err)
	}
	incoming := incomingSnapshot.Records()
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
			result = ImportResultV2{}
			if published {
				err = fmt.Errorf("%w: %v", ErrCommitUncertain, err)
			}
		}
	}()
	current, _, err := loadV2(tx)
	if err != nil {
		return result, err
	}
	byID := make(map[string]patternsv2.Inspection, len(current)+len(incoming))
	for _, value := range current {
		if !value.Support().Verified {
			return result, fmt.Errorf("%w: %s (%s)", ErrReadOnly, value.Record().ID, value.Support().Reason)
		}
		byID[value.Record().ID] = value
	}
	result.Records = make([]patternsv2.Record, 0, len(incoming))
	result.Created = make([]string, 0, len(incoming))
	result.Existing = make([]string, 0, len(incoming))
	for _, value := range incoming {
		record := value.Record()
		if previous, exists := byID[record.ID]; exists {
			if previous.Record().Revision != record.Revision || !bytes.Equal(previous.CanonicalJSON(), value.CanonicalJSON()) {
				return result, fmt.Errorf("%w: %q", ErrConflict, record.ID)
			}
			result.Existing = append(result.Existing, record.ID)
			result.Records = append(result.Records, previous.Record())
			continue
		}
		byID[record.ID] = value
		result.Created = append(result.Created, record.ID)
		result.Records = append(result.Records, record)
	}
	if len(byID) > patternsv2.MaxRecords {
		return result, fmt.Errorf("%w: at most %d records", ErrLimit, patternsv2.MaxRecords)
	}
	merged := make([]patternsv2.Record, 0, len(byID))
	for _, value := range byID {
		merged = append(merged, value.Record())
	}
	// The pure codec sorts and checks the complete combined canonical budget.
	encoded, err := patternsv2.EncodeSnapshot(merged)
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

func (s *StoreV2) valid() error {
	if s == nil || s.dir == "" || s.open == nil {
		return fmt.Errorf("%w: use NewV2 to select an absolute library directory", ErrInvalid)
	}
	return nil
}

func (s *StoreV2) withRead(fn func([]patternsv2.Inspection, []byte) error) (err error) {
	if err = s.valid(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.open(s.dir, false)
	if errors.Is(err, os.ErrNotExist) {
		empty, err := patternsv2.EncodeSnapshot(nil)
		if err != nil {
			return err
		}
		return fn(make([]patternsv2.Inspection, 0), empty)
	}
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := tx.close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	records, canonical, err := loadV2(tx)
	if err != nil {
		return err
	}
	return fn(records, canonical)
}

func loadV2(tx transaction) ([]patternsv2.Inspection, []byte, error) {
	data, err := tx.read(patternsv2.MaxBytes)
	if err != nil {
		return nil, nil, err
	}
	if data == nil {
		empty, err := patternsv2.EncodeSnapshot(nil)
		return make([]patternsv2.Inspection, 0), empty, err
	}
	snapshot, err := patternsv2.InspectSnapshot(data)
	if err != nil {
		// A valid v1 package is a configuration mismatch, not an invitation to
		// migrate or replace it. The old Store has its unchanged own decoder.
		if _, legacyErr := patterns.DecodePackage(data); legacyErr == nil {
			return nil, nil, ErrFormatMismatch
		}
		return nil, nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	canonical := snapshot.CanonicalJSON()
	if !bytes.Equal(data, canonical) {
		return nil, nil, fmt.Errorf("%w: expected an exact sorted v2 frozen snapshot", ErrCorrupt)
	}
	return snapshot.Records(), canonical, nil
}
