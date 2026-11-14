package exchange

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadFromLineLegacyFormats(t *testing.T) {
	long := strings.Repeat("x", 128*1024)
	for _, test := range []struct {
		name, content string
		want          []string
	}{
		{"empty", "", nil},
		{"blank", "\n\n", []string{"", ""}},
		{"LF", "one\ntwo\n", []string{"one", "two"}},
		{"CRLF", "one\r\ntwo\r\n", []string{"one", "two"}},
		{"no final newline", "one\ntwo", []string{"one", "two"}},
		{"bare CR preserved", "one\rtwo\r", []string{"one\rtwo\r"}},
		{"BOM preserved", "\ufeffTitle: 灯\r\n", []string{"\ufeffTitle: 灯"}},
		{"long final line", long, []string{long}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pattern.txt")
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadFromLine(path)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("text format changed: got %q err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestSaveAsLineLegacyBytesAndExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pattern.txt")
	if err := os.WriteFile(path, []byte("old content that must be truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAsLine([]string{"Title: 灯", "", "red\n"}, path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "Title: 灯\n\nred\n\n" {
		t.Fatalf("line serialization changed: %q err=%v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("existing private permissions changed: info=%v err=%v", info, err)
		}
	}
	if err := SaveAsLine(nil, path); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || len(got) != 0 {
		t.Fatalf("empty line list must truncate to an empty file: %q err=%v", got, err)
	}
}

func TestLoadFromJSONLegacyFormatAndErrors(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pattern.json")
	for _, test := range []struct {
		name, content string
		wantError     bool
	}{
		{"normal whitespace", " {\"name\":\"灯\",\"repeat\":2} \r\n", false},
		{"UTF8 BOM remains invalid", "\ufeff{\"name\":\"灯\"}", true},
		{"trailing JSON rejected", "{} {}", true},
		{"malformed JSON", "{", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			var got map[string]interface{}
			err := LoadFromJSON(&got, path)
			if (err != nil) != test.wantError {
				t.Fatalf("JSON behavior changed: got=%v err=%v", got, err)
			}
			if !test.wantError && (got["name"] != "灯" || got["repeat"] != float64(2)) {
				t.Fatalf("JSON content changed: %v", got)
			}
		})
	}
	missing := filepath.Join(directory, "missing.json")
	var got interface{}
	if err := LoadFromJSON(&got, missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file-open error must remain inspectable: %v", err)
	}
	if _, err := LoadFromLine(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("line-open error must remain inspectable: %v", err)
	}
	if err := SaveAsLine([]string{"red"}, directory); err == nil {
		t.Fatal("writing to a directory must fail")
	}
}

type brokenReader struct{ err error }

func (reader brokenReader) Read([]byte) (int, error) { return 0, reader.err }

type brokenWriteCloser struct {
	writeErr, closeErr error
	short              bool
	closed             bool
}

func (writer *brokenWriteCloser) Write(data []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	if writer.short {
		return len(data) / 2, nil
	}
	return len(data), nil
}
func (writer *brokenWriteCloser) Close() error { writer.closed = true; return writer.closeErr }

func TestLineIOReportsReaderFlushShortWriteAndCloseFailures(t *testing.T) {
	broken := errors.New("storage I/O failed")
	lines, err := readLines(io.MultiReader(strings.NewReader("complete\n"), brokenReader{broken}))
	if !errors.Is(err, broken) || !reflect.DeepEqual(lines, []string{"complete"}) {
		t.Fatalf("reader error was swallowed: lines=%q err=%v", lines, err)
	}
	for _, size := range []int{3, 128 * 1024} {
		output := &brokenWriteCloser{writeErr: broken}
		if err := writeAndCloseLines(output, []string{strings.Repeat("x", size)}); !errors.Is(err, broken) || !output.closed {
			t.Errorf("write/flush error swallowed or file leaked for %d bytes: err=%v closed=%v", size, err, output.closed)
		}
	}
	short := &brokenWriteCloser{short: true}
	if err := writeAndCloseLines(short, []string{"short"}); !errors.Is(err, io.ErrShortWrite) || !short.closed {
		t.Fatalf("short flush must fail and close: %v", err)
	}
	closing := &brokenWriteCloser{closeErr: broken}
	if err := writeAndCloseLines(closing, []string{"red"}); !errors.Is(err, broken) {
		t.Fatalf("close failure was swallowed: %v", err)
	}
	var buffer bytes.Buffer
	if err := writeLines(&buffer, []string{"red", "blue"}); err != nil || buffer.String() != "red\nblue\n" {
		t.Fatalf("successful write changed: %q err=%v", buffer.String(), err)
	}
}

func TestLoadPatternFileStillAcceptsEstablishedJSONAndText(t *testing.T) {
	for _, test := range []struct{ name, content string }{
		{"pattern.json", `{"name":"Desk light","repeat":2,"seq":"#FF0000L1T500;#0000FFL2T100","len":2}`},
		{"pattern.txt", "Title: Desk light\r\nRepeat Twice\r\nled 1 red 500ms\r\nled 2 blue 100ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadPatternFile(path)
			if err != nil || got.Name != "Desk light" || got.RepeatTimes != 2 || len(got.Sequence) != 2 {
				t.Fatalf("established pattern file no longer loads: got=%v err=%v", got, err)
			}
		})
	}
}
