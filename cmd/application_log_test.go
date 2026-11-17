package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/charmbracelet/x/ansi"
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
			renderer.SetColorProfile(charmColorProfile(profile))
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

type applicationFailingCore struct{ err error }

func (c *applicationFailingCore) Enabled(zapcore.Level) bool        { return true }
func (c *applicationFailingCore) With([]zapcore.Field) zapcore.Core { return c }
func (c *applicationFailingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return checked.AddCore(entry, c)
}
func (c *applicationFailingCore) Write(zapcore.Entry, []zapcore.Field) error { return c.err }
func (c *applicationFailingCore) Sync() error                                { return nil }

func TestApplicationLoggerInternalWriteErrorsUseCharm(t *testing.T) {
	for _, policy := range []string{"plain", "force", "NO_COLOR overrides force"} {
		t.Run(policy, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			t.Setenv("TERM", "xterm-256color")
			if policy != "plain" {
				t.Setenv("CLICOLOR_FORCE", "1")
			}
			if policy != "force" {
				t.Setenv("NO_COLOR", "1")
			}
			var console bytes.Buffer
			logger, err := newApplicationLogger(&console, "", false)
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("synthetic sink failure: 用户/path with spaces.log")
			// This adds an actual failing Zap core to the constructed logger;
			// its CheckedEntry must use our installed ErrorOutput, not the test's
			// own renderer or a fake filesystem failure.
			logger.Desugar().WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
				return zapcore.NewTee(core, &applicationFailingCore{err: cause})
			})).Info("business operation completed")
			plain := ansi.Strip(console.String())
			if strings.Count(plain, "business operation completed") != 1 || strings.Count(plain, "application log output failed") != 1 || !strings.Contains(plain, "ERROR") || !strings.Contains(plain, "details=") || !strings.Contains(plain, cause.Error()) {
				t.Fatalf("internal sink error did not use the separate Charm diagnostic: %q", plain)
			}
			if strings.ContainsRune(console.String(), '\x1b') != (policy == "force") {
				t.Fatalf("internal diagnostic ignored destination color policy: %q", console.String())
			}
		})
	}
}

func TestApplicationLoggerSerializesInternalDiagnosticsWithApplicationRecords(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var console bytes.Buffer
	logger, err := newApplicationLogger(&console, "", false)
	if err != nil {
		t.Fatal(err)
	}
	checked := logger.Desugar().WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, &applicationFailingCore{err: errors.New("synthetic sink failure")})
	}))
	var done sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		done.Add(1)
		go func() {
			defer done.Done()
			for record := 0; record < 8; record++ {
				checked.Info("business operation completed")
			}
		}()
	}
	done.Wait()
	if strings.Count(console.String(), "business operation completed") != 32 || strings.Count(console.String(), "application log output failed") != 32 {
		t.Fatalf("application and internal diagnostic records were lost: %q", console.String())
	}
}

type applicationClosedOutput struct{ calls int }

func (w *applicationClosedOutput) Write([]byte) (int, error) {
	w.calls++
	return 0, io.ErrClosedPipe
}

func TestApplicationLoggerBrokenDiagnosticOutputDoesNotRecurseOrAbort(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	output := &applicationClosedOutput{}
	logger, err := newApplicationLogger(output, "", false)
	if err != nil {
		t.Fatal(err)
	}
	logger.Desugar().WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, &applicationFailingCore{err: errors.New("synthetic sink failure")})
	})).Info("business operation completed")
	if output.calls != 2 {
		t.Fatalf("expected one business write and one best-effort diagnostic, got %d", output.calls)
	}
}

// hlog owns a private lumberjack file writer without a Close API. Keep real
// file tests in a child process so process exit closes that writer on every OS.
// The parent owns the directory and verifies it can remove all files after Wait.
func applicationLogProcessDirectory(t *testing.T) (string, bool) {
	t.Helper()
	const key = "NB1_APPLICATION_LOG_TEST_DIRECTORY"
	if directory := os.Getenv(key); directory != "" {
		return directory, true
	}
	directory := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, binary, "-test.run=^"+t.Name()+"$", "-test.v")
	process.Env = append(os.Environ(), key+"="+directory)
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("application log child failed: %v: %s", err, output)
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatalf("application log files remain open after child exit: %v", err)
	}
	return "", false
}

func applicationLogFileSnapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + string(data)
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func applicationLogFIFO(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX FIFO fixture is not available on Windows")
	}
	executable, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, executable, path).CombinedOutput(); err != nil {
		t.Fatalf("create private FIFO fixture: %v: %s", err, output)
	}
}

func TestApplicationLoggerRejectsInvalidFileTargetsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink", "fifo", "file-parent", "read-only-file", "read-only-parent"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "application.log")
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep directory contents"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				actual := filepath.Join(directory, "actual.log")
				if kind == "symlink" {
					if err := os.WriteFile(actual, []byte("keep existing file"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(actual, target); err != nil {
					t.Skipf("symlink fixture unavailable: %v", err)
				}
			case "fifo":
				applicationLogFIFO(t, target)
			case "file-parent":
				parent := filepath.Join(directory, "not-a-directory")
				if err := os.WriteFile(parent, []byte("keep parent contents"), 0600); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(parent, "application.log")
			case "read-only-file", "read-only-parent":
				if runtime.GOOS == "windows" || os.Geteuid() == 0 {
					t.Skip("permission fixture requires a non-root POSIX user")
				}
				if kind == "read-only-file" {
					if err := os.WriteFile(target, []byte("keep read-only file"), 0400); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(target, 0600) })
				} else {
					parent := filepath.Join(directory, "read-only")
					if err := os.Mkdir(parent, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
					target = filepath.Join(parent, "application.log")
				}
			}
			before := applicationLogFileSnapshot(t, directory)
			var console bytes.Buffer
			logger, err := newApplicationLogger(&console, target, true)
			if err == nil || logger != nil || !strings.Contains(err.Error(), "log file") || console.Len() != 0 {
				t.Fatalf("invalid sink reached logger construction: logger=%v error=%v console=%q", logger != nil, err, console.String())
			}
			if after := applicationLogFileSnapshot(t, directory); !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid sink changed files, contents or permissions: before=%v after=%v", before, after)
			}
		})
	}
}

func TestApplicationLoggerPreflightPreservesAppendAndCreatesPrivateFiles(t *testing.T) {
	directory, child := applicationLogProcessDirectory(t)
	if !child {
		return
	}
	for _, kind := range []string{"append", "new-parent"} {
		t.Run(kind, func(t *testing.T) {
			filename := filepath.Join(directory, kind, "application.jsonl")
			var before os.FileInfo
			prefix := []byte("{\"earlier\":true}\n")
			if kind == "append" {
				if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, prefix, 0640); err != nil {
					t.Fatal(err)
				}
				var err error
				before, err = os.Stat(filename)
				if err != nil {
					t.Fatal(err)
				}
			}
			var console bytes.Buffer
			logger, err := newApplicationLogger(&console, filename, false)
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filename); err != nil || kind == "append" && !bytes.Equal(data, prefix) || kind != "append" && len(data) != 0 {
				t.Fatalf("preflight wrote or truncated content: %q %v", data, err)
			}
			logger.Infow("after preflight", "count", 1)
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "append" {
				if !bytes.HasPrefix(data, prefix) {
					t.Fatal("existing log content was replaced")
				}
				data = data[len(prefix):]
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil || record["msg"] != "after preflight" || record["count"] != float64(1) {
				t.Fatalf("new JSON record differs: %s %v", data, err)
			}
			after, err := os.Stat(filename)
			if err != nil {
				t.Fatal(err)
			}
			if before != nil && (!os.SameFile(before, after) || before.Mode() != after.Mode()) {
				t.Fatal("append target was renamed, replaced or chmodded")
			}
			if before == nil && runtime.GOOS != "windows" && after.Mode().Perm() != 0600 {
				t.Fatalf("new file is not private: %v", after.Mode())
			}
		})
	}
}

func TestApplicationLoggerPreservesFileJSONAndLevelBehavior(t *testing.T) {
	directory, child := applicationLogProcessDirectory(t)
	if !child {
		return
	}
	t.Setenv("NO_COLOR", "1")
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprint(debug), func(t *testing.T) {
			var console bytes.Buffer
			filename := filepath.Join(directory, fmt.Sprintf("application-%t.jsonl", debug))
			logger, err := newApplicationLogger(&console, filename, debug)
			if err != nil {
				t.Fatal(err)
			}
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
	directory, child := applicationLogProcessDirectory(t)
	if !child {
		return
	}
	t.Setenv("NO_COLOR", "1")
	var console bytes.Buffer
	filename := filepath.Join(directory, "application.jsonl")
	logger, err := newApplicationLogger(&console, filename, false)
	if err != nil {
		t.Fatal(err)
	}
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
	filename = filepath.Join(directory, "rotating.jsonl")
	logger, err = newApplicationLogger(io.Discard, filename, false)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 1024*1024)
	for record := 0; record < 25; record++ {
		logger.Infow(large, "record", record)
	}
	deadline := time.Now().Add(5 * time.Second)
	var incomplete error
	for {
		backups, err := filepath.Glob(filepath.Join(filepath.Dir(filename), "rotating-*.jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		complete := len(backups) >= 2
		for _, name := range backups {
			// A .gz path appears before asynchronous compression finishes.
			// Read through EOF (including the gzip checksum) before accepting it.
			data, err := readCompressedApplicationLog(name)
			if err != nil {
				incomplete = err
				complete = false
				break
			}
			var first map[string]any
			err = json.NewDecoder(bytes.NewReader(data)).Decode(&first)
			if err != nil || first["pid"] != float64(os.Getpid()) || first["level"] != "info" {
				t.Fatalf("rotated file schema differs: %v", err)
			}
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("two complete compressed backup files were not retained: %v", incomplete)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stat, err := os.Stat(filename)
	if err != nil || stat.Size() > 10*1024*1024 {
		t.Fatalf("rotation threshold changed: stat=%v err=%v", stat, err)
	}
}

func readCompressedApplicationLog(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	return io.ReadAll(compressed)
}
