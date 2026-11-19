package triggerv1

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const validFile = `{"schema_version":1,"triggers":{"success":{"pattern_id":"saved-v1-id","serial":"20000001","title":"Success"}}}`

func writeTestFile(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func replaceTestFile(dir, data string) error {
	file, err := os.CreateTemp(dir, ".trigger-test-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Windows can deny replacement while a reader holds the previous snapshot.
	// Keep the complete temporary file and retry the editor's rename operation.
	destination := filepath.Join(dir, Filename)
	const sharingViolation = syscall.Errno(32) // Windows ERROR_SHARING_VIOLATION
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(file.Name(), destination)
		busy := errors.Is(err, os.ErrPermission) || errors.Is(err, sharingViolation)
		if runtime.GOOS != "windows" || !busy || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOptionalMissingFileIsEmptyAndCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	store := New(dir)
	list, err := store.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("missing List = %#v, %v", list, err)
	}
	if _, err := store.Get("success"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Get = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading created a library directory: %v", err)
	}
}

func TestExactMappingsSortedOutputAndAtomicRemapping(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, `{"schema_version":1,"triggers":{"success":{"pattern_id":"Saved-V1","serial":"ABCDEF01","title":"成功 🌈"},"build.done":{"pattern_id":"second"},"build-done":{"pattern_id":"third","title":""}}}`)
	store := New(dir)
	list, err := store.List()
	want := []Trigger{{Name: "build-done", PatternID: "third"}, {Name: "build.done", PatternID: "second"}, {Name: "success", PatternID: "Saved-V1", Serial: "ABCDEF01", Title: "成功 🌈"}}
	if err != nil || !reflect.DeepEqual(list, want) {
		t.Fatalf("List = %#v, %v; want %#v", list, err, want)
	}
	got, err := store.Get("success")
	if err != nil || got != want[2] {
		t.Fatalf("Get = %#v, %v", got, err)
	}
	list[2].PatternID = "caller-mutation"
	if next, err := store.Get("success"); err != nil || next != got {
		t.Fatalf("caller mutation affected next read: %#v %v", next, err)
	}
	if _, err := store.Get("Success"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uppercase became a second trigger name: %v", err)
	}
	if _, err := store.Get("succ"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup was not exact: %v", err)
	}
	if err := replaceTestFile(dir, `{"schema_version":1,"triggers":{"success":{"pattern_id":"remapped"}}}`); err != nil {
		t.Fatal(err)
	}
	next, err := store.Get("success")
	if err != nil || next.PatternID != "remapped" || next.Serial != "" || got.PatternID != "Saved-V1" {
		t.Fatalf("remap did not affect only the next snapshot: old=%#v next=%#v %v", got, next, err)
	}
	writeTestFile(t, dir, `{"schema_version":1,"triggers":{"success":{"pattern_id":"broken","policy":"queue"}}}`)
	if _, err := store.Get("success"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid replacement silently retained old contents: %v", err)
	}
	writeTestFile(t, dir, validFile)
	if _, err := store.Get("success"); err != nil {
		t.Fatalf("corrected file did not recover: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, Filename)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("success"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted mapping remained available: %v", err)
	}
}

func TestMalformedFilesRejectTheWholeSnapshot(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"empty", ""},
		{"top-array", `[]`},
		{"top-null", `null`},
		{"missing-version", `{"triggers":{}}`},
		{"wrong-version", `{"schema_version":2,"triggers":{}}`},
		{"fractional-version", `{"schema_version":1.0,"triggers":{}}`},
		{"missing-triggers", `{"schema_version":1}`},
		{"triggers-array", `{"schema_version":1,"triggers":[]}`},
		{"triggers-null", `{"schema_version":1,"triggers":null}`},
		{"unknown-top-field", `{"schema_version":1,"triggers":{},"policy":"queue"}`},
		{"field-case", `{"Schema_version":1,"triggers":{}}`},
		{"duplicate-top-field", `{"schema_version":1,"schema_version":1,"triggers":{}}`},
		{"duplicate-trigger", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a"},"success":{"pattern_id":"b"}}}`},
		{"escaped-duplicate-trigger", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a"},"su\u0063cess":{"pattern_id":"b"}}}`},
		{"missing-pattern", `{"schema_version":1,"triggers":{"success":{}}}`},
		{"empty-pattern", `{"schema_version":1,"triggers":{"success":{"pattern_id":""}}}`},
		{"non-v1-pattern-path", `{"schema_version":1,"triggers":{"success":{"pattern_id":"evening/wind"}}}`},
		{"inline-action-not-supported", `{"schema_version":1,"triggers":{"success":{"action":{"kind":"off"}}}}`},
		{"policy-not-supported", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","policy":"queue"}}}`},
		{"compose-not-supported", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","compose":[]}}}`},
		{"second-name-not-supported", `{"schema_version":1,"triggers":{"success":{"name":"alias","pattern_id":"a"}}}`},
		{"duplicate-entry-field", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","pattern_id":"b"}}}`},
		{"null-entry", `{"schema_version":1,"triggers":{"success":null}}`},
		{"null-optional-field", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","serial":null}}}`},
		{"empty-present-serial", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","serial":""}}}`},
		{"invalid-serial", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","serial":"not-a-device"}}}`},
		{"numeric-serial", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","serial":20000001}}}`},
		{"uppercase-name", `{"schema_version":1,"triggers":{"Success":{"pattern_id":"a"}}}`},
		{"underscore-name", `{"schema_version":1,"triggers":{"build_done":{"pattern_id":"a"}}}`},
		{"trailing-document", validFile + `{}`},
		{"invalid-UTF8", "{\"schema_version\":1,\"triggers\":{\"success\":{\"pattern_id\":\"a\",\"title\":\"\xff\"}}}"},
		{"unpaired-surrogate", `{"schema_version":1,"triggers":{"success":{"pattern_id":"a","title":"\ud800"}}}`},
		{"invalid-entry-beside-wanted-name", `{"schema_version":1,"triggers":{"success":{"pattern_id":"good"},"other":{"pattern_id":"bad/path"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, tc.data)
			store := New(dir)
			if list, err := store.List(); !errors.Is(err, ErrInvalid) || list != nil {
				t.Fatalf("invalid List returned a partial snapshot: %#v %v", list, err)
			}
			if _, err := store.Get("success"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Get bypassed invalid file: %v", err)
			}
		})
	}
}

func TestNameAndFileLimits(t *testing.T) {
	for _, name := range []string{"a", "success", "build.done", "build-done", "a.", "a..b", "a" + strings.Repeat("b", 63)} {
		if !ValidName(name) {
			t.Errorf("valid exact name rejected: %q", name)
		}
	}
	for _, name := range []string{"", "Success", "0success", "build_done", "success/other", "success ", "éclair", "a" + strings.Repeat("b", 64)} {
		if ValidName(name) {
			t.Errorf("invalid name accepted: %q", name)
		}
	}
	dir := t.TempDir()
	store := New(dir)
	for _, count := range []int{0, MaxTriggers, MaxTriggers + 1} {
		entries := make(map[string]map[string]string, count)
		for i := 0; i < count; i++ {
			entries[fmt.Sprintf("trigger.%03d", i)] = map[string]string{"pattern_id": "saved"}
		}
		data, err := json.Marshal(map[string]any{"schema_version": 1, "triggers": entries})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, dir, string(data))
		list, err := store.List()
		if count > MaxTriggers {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("excess mapping count accepted: %v", err)
			}
		} else if err != nil || list == nil || len(list) != count {
			t.Fatalf("mapping boundary %d: %#v %v", count, list, err)
		}
	}
	for _, count := range []int{MaxTitleRunes, MaxTitleRunes + 1} {
		data, err := json.Marshal(map[string]any{"schema_version": 1, "triggers": map[string]any{"success": map[string]string{"pattern_id": "saved", "title": strings.Repeat("灯", count)}}})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, dir, string(data))
		_, err = store.Get("success")
		if count == MaxTitleRunes && err != nil || count > MaxTitleRunes && !errors.Is(err, ErrInvalid) {
			t.Fatalf("Unicode title boundary %d: %v", count, err)
		}
	}
	exact := validFile + strings.Repeat(" ", MaxFileBytes-len(validFile))
	writeTestFile(t, dir, exact)
	if _, err := store.Get("success"); err != nil {
		t.Fatalf("exact byte limit rejected: %v", err)
	}
	writeTestFile(t, dir, exact+" ")
	if _, err := store.Get("success"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized file accepted: %v", err)
	}
}

func TestStoreErrorsAndCapturedPath(t *testing.T) {
	for _, store := range []*Store{nil, {}, New(""), New("\x00")} {
		if _, err := store.List(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid store = %v", err)
		}
	}
	dir := t.TempDir()
	// Name-length limits differ by filesystem. A NUL in the underlying
	// path deterministically exercises the failed filesystem read on every OS.
	store := &Store{path: filepath.Join(dir, "invalid\x00name")}
	if _, err := store.List(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("I/O failure lost its category: %v", err)
	} else {
		var pathError *os.PathError
		if !errors.As(err, &pathError) {
			t.Fatalf("I/O failure lost underlying PathError: %v", err)
		}
	}
	if _, err := store.Get("Invalid"); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid name caused a file read: %v", err)
	}
	t.Chdir(dir)
	if err := os.Mkdir("library", 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, "library", validFile)
	store = New("library")
	if err := os.Mkdir("elsewhere", 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(dir, "elsewhere"))
	if got, err := store.Get("success"); err != nil || got.PatternID != "saved-v1-id" {
		t.Fatalf("working-directory change redirected store: %#v %v", got, err)
	}
}

func TestConcurrentAtomicReplacementReturnsCompleteMappings(t *testing.T) {
	dir := t.TempDir()
	first := `{"schema_version":1,"triggers":{"success":{"pattern_id":"first","serial":"AAAAbbbb","title":"first"}}}`
	second := `{"schema_version":1,"triggers":{"success":{"pattern_id":"second","serial":"CCCCdddd","title":"second"}}}`
	writeTestFile(t, dir, first)
	store := New(dir)
	failures := make(chan error, 1)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; i < 50; i++ {
			data := first
			if i%2 == 1 {
				data = second
			}
			if err := replaceTestFile(dir, data); err != nil {
				failures <- err
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		got, err := store.Get("success")
		firstValue := got.PatternID == "first" && got.Serial == "AAAAbbbb" && got.Title == "first"
		secondValue := got.PatternID == "second" && got.Serial == "CCCCdddd" && got.Title == "second"
		if err != nil || got.Name != "success" || !firstValue && !secondValue {
			t.Errorf("mixed or unavailable atomic snapshot: %#v %v", got, err)
			break
		}
	}
	writer.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	if got, err := store.Get("success"); err != nil || got.PatternID != "second" {
		t.Fatalf("final atomic replacement not visible: %#v %v", got, err)
	}
}
