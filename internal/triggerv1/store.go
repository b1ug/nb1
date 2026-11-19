// Package triggerv1 reads exact-name trigger mappings from the local library.
// Reading a mapping neither opens a device nor resolves or executes its Pattern.
package triggerv1

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/jsoninput"
)

const (
	Filename      = "triggers.json"
	MaxFileBytes  = 1 << 20
	MaxTriggers   = 256
	MaxTitleRunes = 256
)

var (
	ErrNotFound    = errors.New("trigger not found")
	ErrInvalid     = errors.New("invalid trigger configuration")
	ErrUnavailable = errors.New("trigger configuration unavailable")

	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	patternID     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	serialPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)
)

// Trigger identifies one saved v1 Pattern and an optional default device.
// Name is the exact mapping key, not a second name derived from Title.
type Trigger struct {
	Name      string `json:"name"`
	PatternID string `json:"pattern_id"`
	Serial    string `json:"serial,omitempty"`
	Title     string `json:"title,omitempty"`
}

// CheckReport is a read-only check of saved Pattern references and bindings.
// A managed serial's cached connection is informational, even when offline.
type CheckReport struct {
	Items []CheckedTrigger `json:"items"`
}

type CheckedTrigger struct {
	Name       string       `json:"name"`
	PatternID  string       `json:"pattern_id"`
	Serial     string       `json:"serial,omitempty"`
	Title      string       `json:"title,omitempty"`
	Connection string       `json:"connection,omitempty"`
	Issues     []CheckIssue `json:"issues"`
}

type CheckIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Store remembers an immutable path, not a cached copy of its mappings.
// Each Get or List reads and validates one complete file before returning data.
type Store struct {
	path string
	err  error
}

// New captures libraryDir as an absolute path without creating or reading it.
// A later working-directory change cannot redirect this Store.
func New(libraryDir string) *Store {
	if strings.TrimSpace(libraryDir) == "" || strings.ContainsRune(libraryDir, 0) {
		return &Store{err: fmt.Errorf("%w: a library directory is required", ErrInvalid)}
	}
	dir, err := filepath.Abs(libraryDir)
	if err != nil {
		return &Store{err: fmt.Errorf("%w: resolve library directory: %w", ErrInvalid, err)}
	}
	return &Store{path: filepath.Join(dir, Filename)}
}

// ValidName accepts the exact, case-sensitive lowercase trigger-name grammar.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Get reads the latest file snapshot and returns the exact named mapping.
// A missing optional file or missing name returns ErrNotFound. Invalid names
// return ErrInvalid without reading the file. No last-good mapping is reused.
func (s *Store) Get(name string) (Trigger, error) {
	if !ValidName(name) {
		return Trigger{}, fmt.Errorf("%w: invalid trigger name", ErrInvalid)
	}
	triggers, err := s.List()
	if err != nil {
		return Trigger{}, err
	}
	index := sort.Search(len(triggers), func(i int) bool { return triggers[i].Name >= name })
	if index == len(triggers) || triggers[index].Name != name {
		return Trigger{}, ErrNotFound
	}
	return triggers[index], nil
}

// List returns all mappings sorted by exact name. A missing optional file is an
// empty, non-nil slice. One invalid entry rejects the complete file, rather than
// silently returning a partial list or previously valid contents.
func (s *Store) List() ([]Trigger, error) {
	if s == nil || s.path == "" && s.err == nil {
		return nil, fmt.Errorf("%w: a trigger store is required", ErrInvalid)
	}
	if s.err != nil {
		return nil, s.err
	}
	data, err := readFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Trigger{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(data)
}

type fileInput struct {
	SchemaVersion int                   `json:"schema_version"`
	Triggers      map[string]entryInput `json:"triggers"`
}

type entryInput struct {
	PatternID string  `json:"pattern_id"`
	Serial    *string `json:"serial,omitempty"`
	Title     string  `json:"title,omitempty"`
}

func decode(data []byte) ([]Trigger, error) {
	if len(data) > MaxFileBytes || !jsoninput.ValidUnicode(data) {
		return nil, fmt.Errorf("%w: input must contain valid Unicode and be at most 1 MiB", ErrInvalid)
	}
	var input fileInput
	if err := jsoninput.DecodeObject(data, &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if input.SchemaVersion != 1 || input.Triggers == nil {
		return nil, fmt.Errorf("%w: schema_version must be 1 and triggers must be an object", ErrInvalid)
	}
	if len(input.Triggers) > MaxTriggers {
		return nil, fmt.Errorf("%w: at most %d triggers are supported", ErrInvalid, MaxTriggers)
	}
	names := make([]string, 0, len(input.Triggers))
	for name := range input.Triggers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Trigger, 0, len(names))
	for _, name := range names {
		entry := input.Triggers[name]
		if !ValidName(name) {
			return nil, fmt.Errorf("%w: invalid trigger name %q", ErrInvalid, name)
		}
		if !patternID.MatchString(entry.PatternID) {
			return nil, fmt.Errorf("%w: trigger %q requires a v1 pattern_id", ErrInvalid, name)
		}
		if entry.Serial != nil && !serialPattern.MatchString(*entry.Serial) {
			return nil, fmt.Errorf("%w: trigger %q serial must contain exactly eight hexadecimal digits", ErrInvalid, name)
		}
		if !utf8.ValidString(entry.Title) || utf8.RuneCountInString(entry.Title) > MaxTitleRunes {
			return nil, fmt.Errorf("%w: trigger %q title must contain at most %d Unicode characters", ErrInvalid, name, MaxTitleRunes)
		}
		trigger := Trigger{Name: name, PatternID: entry.PatternID, Title: entry.Title}
		if entry.Serial != nil {
			trigger.Serial = *entry.Serial
		}
		result = append(result, trigger)
	}
	return result, nil
}

func readFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect trigger file: %w", ErrUnavailable, err)
	}
	if !before.Mode().IsRegular() || before.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%w: trigger file must be a regular file of at most 1 MiB", ErrInvalid)
	}
	file, err := openFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open trigger file: %w", ErrUnavailable, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: inspect opened trigger file: %w", ErrUnavailable, err)
	}
	// An editor may atomically replace one regular file with another between
	// Lstat and open. The opened descriptor supplies one snapshot; requiring
	// the old inode here would unnecessarily reject a valid remapping.
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%w: trigger file must be a regular file of at most 1 MiB", ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read trigger file: %w", ErrUnavailable, err)
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: trigger file exceeds 1 MiB", ErrInvalid)
	}
	return data, nil
}
