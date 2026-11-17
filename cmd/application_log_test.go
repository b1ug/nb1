package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/muesli/termenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestCharmConsoleBridgeUsesTheActualRenderer(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.TrueColor} {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			var actual, expected bytes.Buffer
			when := time.Date(2026, 11, 17, 12, 34, 56, 0, time.UTC)
			core := newCharmConsoleCore(zapcore.DebugLevel, &actual, profile, false)
			entry := zapcore.Entry{Level: zapcore.WarnLevel, Time: when, LoggerName: "device", Message: "device unavailable"}
			if err := core.Write(entry, []zapcore.Field{zap.Int("count", 2), zap.String("serial", "lamp-a")}); err != nil {
				t.Fatal(err)
			}
			// Compare with the pinned upstream renderer, not a locally reproduced
			// idea of Charm's colors, level label, spacing or field styles.
			renderer := charmlog.NewWithOptions(&expected, charmlog.Options{
				Level: charmlog.DebugLevel, ReportTimestamp: true, TimeFormat: "15:04:05",
				TimeFunction: func(time.Time) time.Time { return when }, Prefix: "device",
			})
			renderer.SetColorProfile(profile)
			renderer.Log(charmlog.WarnLevel, entry.Message, "count", int64(2), "serial", "lamp-a")
			if actual.String() != expected.String() {
				t.Fatalf("console differs from Charm renderer:\nactual %q\nexpect %q", actual.String(), expected.String())
			}
			if bytes.Contains(actual.Bytes(), []byte("\x1b]")) || bytes.Contains(actual.Bytes(), []byte("\x1b[6n")) {
				t.Fatalf("console emitted a terminal query: %q", actual.String())
			}
			if bytes.ContainsRune(actual.Bytes(), '\x1b') != (profile != termenv.Ascii) {
				t.Fatalf("profile differs: %q", actual.String())
			}
		})
	}
}

func TestCharmConsoleBridgePreservesTypedAndContextFields(t *testing.T) {
	var output bytes.Buffer
	core := newCharmConsoleCore(zapcore.DebugLevel, &output, termenv.Ascii, true)
	context := []zapcore.Field{zap.String("scope", "original")}
	left := core.With(context)
	context[0] = zap.String("scope", "mutated")
	right := core.With([]zapcore.Field{zap.String("scope", "other")})
	entry := zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "observed", Stack: "frame-a\nframe-b",
		Caller: zapcore.EntryCaller{Defined: true, File: "/source/cmd/device.go", Line: 42}}
	fields := []zapcore.Field{
		zap.Int("count", 7), zap.Bool("online", true), zap.Duration("elapsed", 125*time.Millisecond),
		zap.Int("pid", 123),
		zap.Error(errors.New("read failed")), zap.Strings("leds", []string{"one", "two"}),
		zap.String("time", "user time"), zap.String("level", "user level"),
		zap.String("caller", "user caller"), zap.String("prefix", "user prefix"), zap.String("msg", "user message"),
		zap.Namespace("details"), zap.String("name", "用户内容"), zap.Int("channel", 2),
	}
	if err := left.Write(entry, fields); err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"scope=original", "count=7", "online=true", "elapsed=125ms", "pid=123", "read failed", "leds=", "one two", "time=\"user time\"", "level=\"user level\"", "caller=\"user caller\"", "prefix=\"user prefix\"", "msg=\"user message\"", "details=", "用户内容", "channel:2", "<cmd/device.go:42>", "stacktrace="} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("typed/context field lost %q: %s", wanted, output.String())
		}
	}
	if strings.Contains(output.String(), "mutated") {
		t.Fatalf("With retained the caller's mutable slice: %s", output.String())
	}
	output.Reset()
	if err := right.Write(entry, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "scope=other") || strings.Contains(output.String(), "scope=original") {
		t.Fatalf("context branches leaked: %s", output.String())
	}
	output.Reset()
	core = newCharmConsoleCore(zapcore.DebugLevel, &output, termenv.Ascii, false)
	if err := core.Write(entry, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "device.go") || strings.Contains(output.String(), "stacktrace") {
		t.Fatalf("normal console unexpectedly exposes debug metadata: %s", output.String())
	}
}

type failingCharmWriter struct {
	err   error
	short bool
	bytes.Buffer
}

func (w *failingCharmWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.short {
		return 0, nil
	}
	return w.Buffer.Write(data)
}

func TestCharmConsoleBridgeWriterAndLevelBoundaries(t *testing.T) {
	failed := errors.New("diagnostic sink unavailable")
	output := &failingCharmWriter{err: failed}
	level := zap.NewAtomicLevelAt(zapcore.WarnLevel)
	core := newCharmConsoleCore(level, output, termenv.Ascii, false)
	if core.Check(zapcore.Entry{Level: zapcore.InfoLevel}, nil) != nil {
		t.Fatal("level filtering changed")
	}
	level.SetLevel(zapcore.DebugLevel)
	if core.Check(zapcore.Entry{Level: zapcore.DebugLevel}, nil) == nil {
		t.Fatal("dynamic level did not enable debug")
	}
	entry := zapcore.Entry{Level: zapcore.WarnLevel, Time: time.Now(), Message: "first failed record"}
	if err := core.Write(entry, nil); !errors.Is(err, failed) {
		t.Fatalf("writer error lost: %v", err)
	}
	output.err = nil
	output.short = true
	if err := core.Write(entry, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write lost: %v", err)
	}
	output.short = false
	entry.Message = "recovered record"
	if err := core.Write(entry, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "first failed record") || strings.Count(output.String(), "recovered record") != 1 {
		t.Fatalf("upstream buffered a failed record for later replay: %q", output.String())
	}
	// The bridge uses Charm.Log, so rendering a Fatal-level entry itself does
	// not call Charm.Fatal/Exit and cannot skip the command's cleanup.
	entry.Level = zapcore.FatalLevel
	if err := core.Write(entry, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(&charmOutput{}).(interface{ Fd() uintptr }); ok {
		t.Fatal("Charm constructor can see the terminal descriptor")
	}
	if _, ok := any(&charmOutput{}).(io.Reader); ok {
		t.Fatal("Charm constructor can consume input")
	}
}

func TestApplicationLoggerPreservesFileJSONAndLevelBehavior(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprint(debug), func(t *testing.T) {
			var console bytes.Buffer
			filename := filepath.Join(t.TempDir(), "application.jsonl")
			logger := newApplicationLogger(&console, filename, debug)
			if err := logger.SetLevelString("debug"); err != nil {
				t.Fatal(err)
			}
			logger.Debugw("device inspected", "serial", "lamp-a", "channel", 2)
			logger.Desugar().Named("device").WithOptions(zap.AddStacktrace(zapcore.ErrorLevel)).Error("read failed", zap.Error(errors.New("offline")))
			if err := logger.SetLevelString("warn"); err != nil {
				t.Fatal(err)
			}
			logger.Infow("filtered info")
			logger.Warnw("device unavailable")
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.ContainsRune(data, '\x1b') || bytes.Contains(data, []byte("filtered info")) || bytes.ContainsRune(console.Bytes(), '\x1b') {
				t.Fatalf("file format/filter or plain console changed: file=%s console=%s", data, console.Bytes())
			}
			lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
			if len(lines) != 3 {
				t.Fatalf("record count=%d: %s", len(lines), data)
			}
			for _, line := range lines {
				var record map[string]any
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"time", "level", "caller", "msg", "pid"} {
					if _, ok := record[key]; !ok {
						t.Fatalf("file field %q missing: %s", key, line)
					}
				}
				if record["pid"] != float64(os.Getpid()) || !strings.Contains(record["caller"].(string), "application_log_test.go:") {
					t.Fatalf("file PID/caller differs: %s", line)
				}
				if record["msg"] == "read failed" && (record["stacktrace"] == nil || record["error"] != "offline" || record["logger"] != "device") {
					t.Fatalf("file error/name/stacktrace lost: %s", line)
				}
			}
			if strings.Contains(console.String(), "pid=") != debug || strings.Contains(console.String(), "application_log_test.go:") != debug || strings.Contains(console.String(), "stacktrace=") != debug {
				t.Fatalf("debug console metadata differs: %s", console.Bytes())
			}
			if !strings.Contains(console.String(), "DEBU") || !strings.Contains(console.String(), "serial=lamp-a") || !strings.Contains(console.String(), "channel=2") {
				t.Fatalf("debug filtering or business fields lost: %s", console.Bytes())
			}
		})
	}
}

func TestApplicationLoggerConcurrentRecordsAndFileRotation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var console bytes.Buffer
	filename := filepath.Join(t.TempDir(), "application.jsonl")
	logger := newApplicationLogger(&console, filename, false)
	var done sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		done.Add(1)
		go func(worker int) {
			defer done.Done()
			for record := 0; record < 10; record++ {
				logger.Infow("record observed", "worker", worker, "record", record)
			}
		}(worker)
	}
	done.Wait()
	if strings.Count(console.String(), "record observed") != 80 {
		t.Fatal("concurrent console records interleaved or disappeared")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if !json.Valid(line) {
			t.Fatalf("file records interleaved: %q", line)
		}
	}
	// Exercise the retained 10 MiB limit and compression rather than merely
	// inspecting option literals. More than one backup survives (retention=0).
	filename = filepath.Join(t.TempDir(), "rotating.jsonl")
	logger = newApplicationLogger(io.Discard, filename, false)
	large := strings.Repeat("x", 1024*1024)
	for record := 0; record < 25; record++ {
		logger.Infow(large, "record", record)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		backups, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "rotating-*.jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		if len(backups) >= 2 {
			for _, name := range backups {
				file, err := os.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				compressed, err := gzip.NewReader(file)
				if err != nil {
					file.Close()
					t.Fatal(err)
				}
				var first map[string]any
				err = json.NewDecoder(compressed).Decode(&first)
				compressed.Close()
				file.Close()
				if err != nil || first["pid"] != float64(os.Getpid()) || first["level"] != "info" {
					t.Fatalf("rotated file schema differs: %v", err)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("two compressed backup files were not retained")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stat, err := os.Stat(filename)
	if err != nil || stat.Size() > 10*1024*1024 {
		t.Fatalf("rotation threshold changed: stat=%v err=%v", stat, err)
	}
}
