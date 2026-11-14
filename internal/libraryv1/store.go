// Package libraryv1 persists the current local color and Sequence library.
package libraryv1

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/sequence"
)

var (
	ErrNotFound = errors.New("library record not found")
	ErrInvalid  = errors.New("invalid library record")
	ErrCapacity = errors.New("library capacity exhausted")
)

const (
	MaxRecordBytes = 1 << 20
	MaxColors      = 256
	MaxPatterns    = 128
	MaxTitleRunes  = 256
)

var (
	colorName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	patternID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type Color struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Pattern struct {
	ID       string            `json:"id"`
	Title    string            `json:"title,omitempty"`
	Sequence sequence.Sequence `json:"sequence"`
}

type Store struct {
	dir string
	err error
}

// New remembers the directory without creating it. Relative paths are resolved
// now so a later working-directory change cannot redirect this Store.
func New(dir string) *Store {
	if dir == "" {
		return &Store{err: fmt.Errorf("%w: a library directory is required", ErrInvalid)}
	}
	path, err := filepath.Abs(dir)
	return &Store{dir: path, err: err}
}

func (s *Store) ListColors() ([]Color, error) {
	colors := []Color{}
	dir, err := s.directory("colors", false)
	if errors.Is(err, os.ErrNotExist) {
		return colors, nil
	}
	if err != nil {
		return nil, err
	}
	names, err := recordNames(dir, colorName, MaxColors)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		color, err := readColor(filepath.Join(dir, recordFilename(name)), name)
		if err != nil {
			return nil, err
		}
		colors = append(colors, color)
	}
	return colors, nil
}

func (s *Store) ResolveColor(name string) (string, error) {
	if !colorName.MatchString(name) {
		return "", fmt.Errorf("%w: invalid color name", ErrInvalid)
	}
	dir, err := s.directory("colors", false)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	color, err := readColor(filepath.Join(dir, recordFilename(name)), name)
	return color.Color, err
}

func (s *Store) SaveColor(name, color string) (Color, error) {
	record := Color{Name: name, Color: color}
	if err := normalizeColor(&record); err != nil {
		return Color{}, err
	}
	if err := s.save("colors", record.Name, colorName, MaxColors, record); err != nil {
		return Color{}, err
	}
	return record, nil
}

func (s *Store) ListPatterns() ([]Pattern, error) {
	patterns := []Pattern{}
	dir, err := s.directory("patterns", false)
	if errors.Is(err, os.ErrNotExist) {
		return patterns, nil
	}
	if err != nil {
		return nil, err
	}
	names, err := recordNames(dir, patternID, MaxPatterns)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		pattern, err := readPattern(filepath.Join(dir, recordFilename(name)), name)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func (s *Store) GetPattern(id string) (Pattern, error) {
	if !patternID.MatchString(id) {
		return Pattern{}, fmt.Errorf("%w: invalid Pattern ID", ErrInvalid)
	}
	dir, err := s.directory("patterns", false)
	if errors.Is(err, os.ErrNotExist) {
		return Pattern{}, ErrNotFound
	}
	if err != nil {
		return Pattern{}, err
	}
	return readPattern(filepath.Join(dir, recordFilename(id)), id)
}

func (s *Store) SavePattern(input Pattern) (Pattern, error) {
	if err := validatePatternMetadata(input.ID, input.Title); err != nil {
		return Pattern{}, err
	}
	normalized, err := sequence.Normalize(input.Sequence)
	if err != nil {
		return Pattern{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	input.Sequence = normalized
	if err := s.save("patterns", input.ID, patternID, MaxPatterns, input); err != nil {
		return Pattern{}, err
	}
	return input, nil
}

func validatePatternMetadata(id, title string) error {
	if !patternID.MatchString(id) || !utf8.ValidString(title) || utf8.RuneCountInString(title) > MaxTitleRunes {
		return fmt.Errorf("%w: Pattern requires a valid ID and a title of at most %d Unicode characters", ErrInvalid, MaxTitleRunes)
	}
	return nil
}

func (s *Store) directory(kind string, create bool) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if err := checkDirectory(s.dir, create); err != nil {
		return "", err
	}
	dir := filepath.Join(s.dir, kind)
	return dir, checkDirectory(dir, create)
}

func checkDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || !privateMode(info) {
		return fmt.Errorf("%w: library directories must be private regular directories", ErrInvalid)
	}
	return nil
}

// Hex filenames preserve distinct IDs on case-insensitive filesystems.
func recordFilename(name string) string { return hex.EncodeToString([]byte(name)) + ".json" }

func recordNames(dir string, valid *regexp.Regexp, limit int) ([]string, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var names []string
	for {
		entries, err := file.ReadDir(32)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".record-") {
				continue
			}
			encoded := strings.TrimSuffix(entry.Name(), ".json")
			decoded, decodeErr := hex.DecodeString(encoded)
			name := string(decoded)
			info, statErr := entry.Info()
			if statErr != nil {
				return nil, statErr
			}
			if decodeErr != nil || recordFilename(name) != entry.Name() || !valid.MatchString(name) || !info.Mode().IsRegular() || !privateMode(info) {
				return nil, fmt.Errorf("%w: unexpected library entry %q", ErrInvalid, entry.Name())
			}
			names = append(names, name)
			if len(names) > limit {
				return nil, ErrCapacity
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) save(kind, name string, valid *regexp.Regexp, limit int, record interface{}) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	data = append(data, '\n')
	if len(data) > MaxRecordBytes {
		return fmt.Errorf("%w: record exceeds 1 MiB", ErrInvalid)
	}
	dir, err := s.directory(kind, true)
	if err != nil {
		return err
	}
	lockDir := filepath.Join(s.dir, ".locks")
	if err := checkDirectory(lockDir, true); err != nil {
		return err
	}
	// The existing OS lease serializes capacity checks and replacement across
	// Store instances and processes. The persistent lock inode stays in place.
	var lease *ownership.Lease
	deadline := time.Now().Add(2 * time.Second)
	for {
		lease, err = ownership.Acquire(lockDir, "library-v1-writer")
		if !errors.Is(err, ownership.ErrBusy) || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	if _, err := s.directory(kind, false); err != nil {
		return err
	}
	names, err := recordNames(dir, valid, limit)
	if err != nil {
		return err
	}
	existing := sort.SearchStrings(names, name)
	if len(names) >= limit && (existing == len(names) || names[existing] != name) {
		return ErrCapacity
	}
	temporary, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceRecord(temporary.Name(), filepath.Join(dir, recordFilename(name)))
}

func readRecord(path string) ([]byte, error) {
	file, err := openRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open record: %v", ErrInvalid, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !privateMode(info) || info.Size() > MaxRecordBytes {
		return nil, fmt.Errorf("%w: record must be a private regular file of at most 1 MiB", ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecordBytes || !jsoninput.ValidUnicode(data) {
		return nil, fmt.Errorf("%w: invalid or oversized record", ErrInvalid)
	}
	return data, nil
}

func readColor(path, name string) (Color, error) {
	data, err := readRecord(path)
	if err != nil {
		return Color{}, err
	}
	var record Color
	if err := jsoninput.DecodeObject(data, &record); err != nil {
		return Color{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if record.Name != name {
		return Color{}, fmt.Errorf("%w: color name differs from filename", ErrInvalid)
	}
	if err := normalizeColor(&record); err != nil {
		return Color{}, err
	}
	return record, nil
}

func normalizeColor(record *Color) error {
	if !colorName.MatchString(record.Name) || len(record.Color) != 7 || record.Color[0] != '#' {
		return fmt.Errorf("%w: color requires a valid name and exact Hex6", ErrInvalid)
	}
	if _, err := hex.DecodeString(record.Color[1:]); err != nil {
		return fmt.Errorf("%w: color requires exact Hex6", ErrInvalid)
	}
	record.Color = strings.ToLower(record.Color)
	return nil
}

func readPattern(path, id string) (Pattern, error) {
	data, err := readRecord(path)
	if err != nil {
		return Pattern{}, err
	}
	var wire struct {
		ID       string          `json:"id"`
		Title    string          `json:"title,omitempty"`
		Sequence json.RawMessage `json:"sequence"`
	}
	if err := jsoninput.DecodeObject(data, &wire); err != nil {
		return Pattern{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if wire.ID != id {
		return Pattern{}, fmt.Errorf("%w: Pattern ID differs from filename", ErrInvalid)
	}
	if err := validatePatternMetadata(wire.ID, wire.Title); err != nil {
		return Pattern{}, err
	}
	compiled, err := sequence.Decode(wire.Sequence)
	if err != nil {
		return Pattern{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return Pattern{ID: wire.ID, Title: wire.Title, Sequence: compiled}, nil
}
