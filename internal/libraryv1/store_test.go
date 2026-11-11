package libraryv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/sequence"
)

func testPattern(id string) Pattern {
	return Pattern{ID: id, Title: "A little light · 小灯", Sequence: sequence.Sequence{
		Timing: sequence.TimingSerial, Steps: []sequence.Step{
			{Targets: []sequence.Target{{LED: 0, Color: "#123456"}}},
		}}}
}

func TestReadMissingLibraryDoesNotCreateIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	s := New(dir)
	if colors, err := s.ListColors(); err != nil || colors == nil || len(colors) != 0 {
		t.Fatalf("missing colors: %+v, %v", colors, err)
	}
	if patterns, err := s.ListPatterns(); err != nil || patterns == nil || len(patterns) != 0 {
		t.Fatalf("missing patterns: %+v, %v", patterns, err)
	}
	if _, err := s.ResolveColor("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.GetPattern("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created directory: %v", err)
	}
}

func TestStoredValuesAreNormalizedIndependentAndReplaceable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	s := New(dir)
	if color, err := s.SaveColor("status-ok", "#AAbbCC"); err != nil || color.Color != "#aabbcc" {
		t.Fatalf("save color: %+v, %v", color, err)
	}
	if _, err := s.SaveColor("status-ok", "#112233"); err != nil {
		t.Fatal(err)
	}
	if color, err := New(dir).ResolveColor("status-ok"); err != nil || color != "#112233" {
		t.Fatalf("replace/reopen: %q, %v", color, err)
	}
	input := testPattern("Foo")
	saved, err := s.SavePattern(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Sequence.Steps[0].Targets[0].Color = "#ffffff"
	saved.Sequence.Steps[0].Targets[0].Color = "#eeeeee"
	*saved.Sequence.Steps[0].HoldMS = 500
	stored, err := s.GetPattern("Foo")
	if err != nil || stored.Sequence.Steps[0].Targets[0].Color != "#123456" || *stored.Sequence.Steps[0].HoldMS != 0 {
		t.Fatalf("frozen sequence changed through caller: %+v, %v", stored, err)
	}
	if _, err := s.SavePattern(testPattern("foo")); err != nil {
		t.Fatal(err)
	}
	patterns, err := s.ListPatterns()
	if err != nil || len(patterns) != 2 || patterns[0].ID != "Foo" || patterns[1].ID != "foo" {
		t.Fatalf("case-distinct Pattern IDs: %+v, %v", patterns, err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{dir, filepath.Join(dir, "colors"), filepath.Join(dir, "patterns"), filepath.Join(dir, ".locks")} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("directory permissions: %s, %v", path, err)
			}
		}
		info, err := os.Stat(filepath.Join(dir, "patterns", recordFilename("Foo")))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("file permissions: %v", err)
		}
	}
}

func TestInvalidAndDamagedRecordsAreRejected(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "library"))
	for _, name := range []string{"", "@ok", "Upper", "../escape", strings.Repeat("a", 33)} {
		if _, err := s.SaveColor(name, "#123456"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid color name %q: %v", name, err)
		}
	}
	for _, color := range []string{"red", "#abc", "#12345g"} {
		if _, err := s.SaveColor("valid", color); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid exact color %q: %v", color, err)
		}
	}
	large := testPattern("large")
	large.Title = strings.Repeat("x", MaxRecordBytes)
	if _, err := s.SavePattern(large); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized write: %v", err)
	}
	if _, err := os.Stat(s.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid writes created the library: %v", err)
	}
	if _, err := s.SaveColor("valid", "#123456"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "colors", recordFilename("valid"))
	for _, raw := range []string{
		`{`, `{"name":"valid","color":"#123456","extra":true}`,
		`{"name":"valid","color":"#123456","color":"#000000"}`,
		`{"name":"other","color":"#123456"}`, strings.Repeat("x", MaxRecordBytes+1),
	} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResolveColor("valid"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("damaged record accepted: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveColor("valid"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("directory record accepted: %v", err)
	}
	if _, err := s.SaveColor("valid", "#ffffff"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("directory replaced: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	original := []byte(`{"name":"valid","color":"#123456"}`)
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Log("symlink creation unavailable:", err)
			return
		}
		t.Fatal(err)
	}
	if _, err := s.ResolveColor("valid"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink read accepted: %v", err)
	}
	if _, err := s.SaveColor("valid", "#ffffff"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink write accepted: %v", err)
	}
	if actual, err := os.ReadFile(outside); err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("symlink target changed: %v", err)
	}
}

func TestPatternTitleRuneBoundaryOnSaveAndRead(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "library"))
	input := testPattern("title")
	input.Title = strings.Repeat("灯", MaxTitleRunes)
	if _, err := s.SavePattern(input); err != nil {
		t.Fatalf("title at boundary: %v", err)
	}
	input.Title += "灯"
	if _, err := s.SavePattern(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("title beyond boundary: %v", err)
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "patterns", recordFilename(input.ID)), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPattern(input.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized persisted title: %v", err)
	}
}

func TestConcurrentWritersLeaveWholeRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	colors := []string{"#112233", "#445566", "#778899", "#aabbcc"}
	var writers sync.WaitGroup
	for _, color := range colors {
		color := color
		writers.Add(1)
		go func() {
			defer writers.Done()
			s := New(dir)
			for i := 0; i < 8; i++ {
				if _, err := s.SaveColor("shared", color); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.ResolveColor("shared"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	stored, err := New(dir).ListColors()
	if err != nil || len(stored) != 1 {
		t.Fatalf("concurrent update: %+v, %v", stored, err)
	}
	if _, err := New(dir).SaveColor("shared", "#ffffff"); err != nil {
		t.Fatal(err)
	}
	if color, err := New(dir).ResolveColor("shared"); err != nil || color != "#ffffff" {
		t.Fatalf("last completed writer: %q, %v", color, err)
	}
}

func TestWriterContentionHasBoundedWait(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "library"))
	if _, err := s.SaveColor("existing", "#123456"); err != nil {
		t.Fatal(err)
	}
	lease, err := ownership.Acquire(filepath.Join(s.dir, ".locks"), "library-v1-writer")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	started := time.Now()
	if _, err := s.SaveColor("blocked", "#abcdef"); !errors.Is(err, ownership.ErrBusy) {
		t.Fatalf("writer contention: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("writer lock waited %s", elapsed)
	}
	if _, err := s.ResolveColor("blocked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("contended write published a record: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveColor("blocked", "#abcdef"); err != nil {
		t.Fatalf("released writer lock still blocks: %v", err)
	}
}

func TestCrossProcessCapacityIsSerialized(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	s := New(dir)
	colorsDir, err := s.directory("colors", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxColors-1; i++ {
		name := fmt.Sprintf("seed-%d", i)
		raw := fmt.Sprintf(`{"name":%q,"color":"#123456"}`, name)
		if err := os.WriteFile(filepath.Join(colorsDir, recordFilename(name)), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var processes []*exec.Cmd
	for i := 0; i < 6; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLibraryProcessWriter$")
		cmd.Env = append(os.Environ(), "NB1_LIBRARYV1_HELPER_DIR="+dir, fmt.Sprintf("NB1_LIBRARYV1_HELPER_NAME=winner-%d", i))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, cmd)
	}
	accepted := 0
	for _, process := range processes {
		err := process.Wait()
		if err == nil {
			accepted++
		} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
			t.Fatalf("writer subprocess: %v", err)
		}
	}
	colors, err := s.ListColors()
	if err != nil || len(colors) != MaxColors || accepted != 1 {
		t.Fatalf("cross-process count=%d, accepted=%d, err=%v", len(colors), accepted, err)
	}
	if _, err := s.SaveColor("seed-0", "#abcdef"); err != nil {
		t.Fatalf("update at capacity: %v", err)
	}
}

func TestLibraryProcessWriter(t *testing.T) {
	dir := os.Getenv("NB1_LIBRARYV1_HELPER_DIR")
	if dir == "" {
		return
	}
	_, err := New(dir).SaveColor(os.Getenv("NB1_LIBRARYV1_HELPER_NAME"), "#123456")
	if errors.Is(err, ErrCapacity) {
		os.Exit(3)
	}
	if err != nil {
		t.Fatal(err)
	}
}
