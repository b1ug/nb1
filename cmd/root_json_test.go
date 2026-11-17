package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

func TestRootDoctorJSONKeepsDiagnosticsOnStderr(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		debug   bool
		logFile bool
		env     map[string]string
		colored bool
	}{
		{name: "normal"},
		{name: "debug", debug: true},
		{name: "debug with persistent file", debug: true, logFile: true},
		{name: "NO_COLOR overrides force", debug: true, logFile: true, env: map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}},
		{name: "dumb with stale truecolor", debug: true, env: map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}},
		{name: "explicit force on stderr pipe", debug: true, env: map[string]string{"CLICOLOR_FORCE": "1"}, colored: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "CI", "COLORTERM"} {
				t.Setenv(key, "")
			}
			t.Setenv("TERM", "xterm-256color")
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			directory := t.TempDir()
			args := []string{}
			if test.debug {
				args = append(args, "--debug")
			}
			logPath := filepath.Join(directory, "runtime.log")
			if test.logFile {
				args = append(args, "--log-file", logPath)
			}
			args = append(args, "doctor", "--endpoint", endpoint, "--json")
			stdout, stderr, err := executeRootDiagnosticProcess(t, directory, args)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("attention must exit 1: err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			var report struct {
				SchemaVersion int               `json:"schema_version"`
				Status        string            `json:"status"`
				Devices       []json.RawMessage `json:"devices"`
			}
			if err := json.Unmarshal([]byte(stdout), &report); err != nil || report.SchemaVersion != 1 || report.Status != "attention" || report.Devices == nil || len(report.Devices) != 0 || strings.ContainsRune(stdout, '\x1b') {
				t.Fatalf("stdout is not one unstyled diagnostic JSON document: %v; %q", err, stdout)
			}
			if !strings.Contains(stderr, "diagnostics need attention") || strings.Contains(stderr, "using config file") != test.debug {
				t.Fatalf("diagnostics or debug-level behavior lost: %q", stderr)
			}
			// The actual Charm destination profile obeys the same explicit
			// override on every platform; the file encoder stays undecorated.
			wantColor := test.colored
			if strings.ContainsRune(stderr, '\x1b') != wantColor {
				t.Fatalf("stderr color policy differs (want color=%t): %q", wantColor, stderr)
			}
			if test.logFile {
				data, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
				if len(lines) == 0 || !bytes.Contains(data, []byte("using config file")) {
					t.Fatalf("persistent debug log missing: %s", data)
				}
				for _, line := range lines {
					if !json.Valid(line) {
						t.Fatalf("persistent log is no longer JSON: %q", line)
					}
				}
			}
		})
	}
}

func TestRootControlJSONWithDebugUsesOnlyOwnedHTTPFixture(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/devices" {
			t.Errorf("unexpected control request: %s %s", request.Method, request.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "[]")
	}))
	defer server.Close()
	stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), []string{"--debug", "control", "--endpoint", server.URL, "devices"})
	var devices []json.RawMessage
	if err != nil || json.Unmarshal([]byte(stdout), &devices) != nil || devices == nil || len(devices) != 0 || strings.ContainsRune(stdout, '\x1b') {
		t.Fatalf("control stdout is not its JSON result: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "using config file") {
		t.Fatalf("debug diagnostics were lost instead of redirected: %q", stderr)
	}
	if strings.ContainsRune(stderr, '\x1b') {
		t.Fatalf("NO_COLOR debug diagnostics contain styling: %q", stderr)
	}
}

func TestActualRootHelpUsesSelectedPipeAndColorPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     map[string]string
		colored bool
	}{
		{"default pipe", nil, false},
		{"forced pipe", map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"NO_COLOR wins", map[string]string{"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}, false},
		{"dumb ignores truecolor", map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "CI", "COLORTERM"} {
				t.Setenv(key, "")
			}
			t.Setenv("TERM", "xterm-256color")
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), []string{"--help"})
			if err != nil || stderr != "" || !strings.Contains(ansi.Strip(stdout), "nb1 controls blink(1)") || !strings.Contains(ansi.Strip(stdout), "nb1 [command]") {
				t.Fatalf("root help failed: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if strings.ContainsRune(stdout, '\x1b') != test.colored {
				t.Fatalf("root help color differs (want color=%t): %q", test.colored, stdout)
			}
		})
	}
}

func TestRootInvalidLogLevelReturnsOnePresentedErrorBeforeRun(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		spy  bool
	}{
		{"ordinary", []string{"config", "get", "nb1_bootstrap_missing"}, false},
		{"profile recovery", []string{"config", "path"}, false},
		{"profile creation", []string{"config", "init"}, false},
		{"service recovery", []string{"service", "render", "--platform", "linux", "--binary", "/not-executed/nb1"}, false},
		{"hardware hook", []string{"on"}, true},
		{"conversion hook", []string{"convert", "text2json", "not-read.txt"}, true},
		{"control hook", []string{"control", "devices"}, true},
	} {
		for _, color := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/color=%t", test.name, color), func(t *testing.T) {
				t.Setenv("NO_COLOR", "")
				t.Setenv("CLICOLOR_FORCE", "1")
				if !color {
					t.Setenv("NO_COLOR", "yes")
				}
				args := append([]string{"--log-level", "not-a-level"}, test.args...)
				var environment []string
				if test.spy {
					environment = []string{"NB1_JSON_ROOT_TEST_BOOTSTRAP_SPY=1"}
				}
				stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), args, environment...)
				var exit *exec.ExitError
				text := ansi.Strip(stderr)
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout != "" ||
					strings.Count(text, "unrecognized level") != 1 || strings.Contains(text, "bootstrap spy") ||
					strings.ContainsRune(stderr, '\x1b') != color {
					t.Fatalf("bootstrap must return one presented error before hooks/Run: err=%v stdout=%q stderr=%q", err, stdout, stderr)
				}
			})
		}
	}
}

func TestRootDebugOverridesInvalidLogLevelAndVersionBypassesIt(t *testing.T) {
	t.Setenv("NO_COLOR", "yes")
	for _, args := range [][]string{
		{"--debug", "--log-level", "not-a-level", "config", "get", "nb1_bootstrap_missing"},
		{"--debug", "--log-level", "not-a-level", "config", "path"},
		{"--log-level", "not-a-level", "version", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), args)
			if err != nil || strings.Contains(stderr, "unrecognized level") || strings.ContainsRune(stdout+stderr, '\x1b') {
				t.Fatalf("debug/version initialization behavior changed: err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			if args[len(args)-1] == "--json" && (!json.Valid([]byte(stdout)) || stderr != "") {
				t.Fatalf("version JSON initialized runtime or changed stdout: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

func TestRootProfileRecoveryStillIgnoresOnlyConfigurationFailure(t *testing.T) {
	t.Setenv("NO_COLOR", "yes")
	directory := t.TempDir()
	broken := filepath.Join(directory, "broken.yaml")
	content := []byte("jobs:\n  terminal_limit: 0\n")
	if err := os.WriteFile(broken, content, 0600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeRootDiagnosticProcess(t, directory,
		[]string{"--config", broken, "--log-level", "info", "config", "path"})
	if err != nil || strings.TrimSpace(stdout) != broken || stderr != "" {
		t.Fatalf("valid logger blocked profile recovery: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	stdout, stderr, err = executeRootDiagnosticProcess(t, directory,
		[]string{"--config", broken, "--log-level", "not-a-level", "config", "path"})
	if err == nil || stdout != "" || strings.Count(stderr, "unrecognized level") != 1 {
		t.Fatalf("profile recovery hid logger failure: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if data, err := os.ReadFile(broken); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("recovery modified the broken profile: %v %q", err, data)
	}
}

func TestLoggerInitializationGuardRestoresExistingHooks(t *testing.T) {
	previous := loggerInitError
	t.Cleanup(func() { loggerInitError = previous })
	want := errors.New("logger initialization failed")
	calls := 0
	root := &cobra.Command{Use: "fixture", PersistentPreRunE: func(*cobra.Command, []string) error { calls++; return nil }}
	child := &cobra.Command{Use: "child", PersistentPreRun: func(*cobra.Command, []string) { calls++ }}
	root.AddCommand(child)
	restore := guardLoggerInitialization(root)
	loggerInitError = want
	for _, command := range []*cobra.Command{root, child} {
		if err := command.PersistentPreRunE(command, nil); !errors.Is(err, want) {
			t.Fatalf("override bypassed initialization failure: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("failed initialization entered an existing hook")
	}
	loggerInitError = nil
	if err := child.PersistentPreRunE(child, nil); err != nil || calls != 1 {
		t.Fatalf("successful void hook changed: %v calls=%d", err, calls)
	}
	restore()
	if child.PersistentPreRunE != nil || child.PersistentPreRun == nil {
		t.Fatal("original void hook not restored")
	}
	if err := root.PersistentPreRunE(root, nil); err != nil || calls != 2 {
		t.Fatalf("original error hook not restored: %v calls=%d", err, calls)
	}
}

func TestRootLogFileFailureCannotRunCommandsOrChangeDirectory(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, args := range [][]string{
		{"config", "show"},
		{"server", "--devices", "00000000"},
		{"fade", "red"},
		{"convert", "text2json", "unused.txt"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "log-target")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep these contents"), 0600); err != nil {
				t.Fatal(err)
			}
			before := applicationLogFileSnapshot(t, target)
			arguments := append([]string{"--debug", "--log-file", target}, args...)
			// Replacing Run/the nearest pre-run with spies keeps even a
			// regression in a hardware/server command safe on a developer Mac.
			stdout, stderr, err := executeRootDiagnosticProcess(t, directory, arguments, "NB1_JSON_ROOT_TEST_BOOTSTRAP_SPY=1")
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout != "" || strings.Count(stderr, "log file") != 1 || strings.Contains(stderr, "bootstrap spy") {
				t.Fatalf("sink failure did not stop initialization exactly once: err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			if after := applicationLogFileSnapshot(t, target); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected directory changed: before=%v after=%v", before, after)
			}
			backups, err := filepath.Glob(target + "-*")
			if err != nil || len(backups) != 0 {
				t.Fatalf("directory was rotated: %v %v", backups, err)
			}
		})
	}
}

func TestRootLogFileRejectsLeafSymlinkAndFIFOWithoutWriting(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, kind := range []string{"symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "application.log")
			if kind == "symlink" {
				actual := filepath.Join(directory, "actual.log")
				if err := os.WriteFile(actual, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(actual, target); err != nil {
					t.Skipf("symlink fixture unavailable: %v", err)
				}
			} else {
				applicationLogFIFO(t, target)
			}
			// The helper creates this profile too; seed it before the snapshot.
			if err := os.WriteFile(filepath.Join(directory, "config.yaml"), appconfig.Template(), 0600); err != nil {
				t.Fatal(err)
			}
			before := applicationLogFileSnapshot(t, directory)
			stdout, stderr, err := executeRootDiagnosticProcess(t, directory,
				[]string{"--debug", "--log-file", target, "config", "show"}, "NB1_JSON_ROOT_TEST_BOOTSTRAP_SPY=1")
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout != "" || strings.Count(stderr, "log file") != 1 || strings.Contains(stderr, "bootstrap spy") {
				t.Fatalf("special sink failure escaped initialization: err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			if after := applicationLogFileSnapshot(t, directory); !reflect.DeepEqual(after, before) {
				t.Fatalf("special sink changed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRootHelpAndVersionDoNotInitializeLogFiles(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, kind := range []string{"directory", "missing"} {
		for _, args := range [][]string{{"--help"}, {"control", "run", "--help"}, {"version", "--json"}} {
			t.Run(kind+"/"+strings.Join(args, " "), func(t *testing.T) {
				directory := t.TempDir()
				target := filepath.Join(directory, "log-target")
				if kind == "directory" {
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep these contents"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(directory, "config.yaml"), appconfig.Template(), 0600); err != nil {
					t.Fatal(err)
				}
				before := applicationLogFileSnapshot(t, directory)
				arguments := append([]string{"--debug", "--log-file", target}, args...)
				stdout, stderr, err := executeRootDiagnosticProcess(t, directory, arguments)
				if err != nil || stdout == "" || stderr != "" {
					t.Fatalf("inspection initialized runtime logging: err=%v stdout=%q stderr=%q", err, stdout, stderr)
				}
				if args[0] == "version" && !json.Valid([]byte(stdout)) {
					t.Fatalf("version stdout is not JSON: %q", stdout)
				}
				if after := applicationLogFileSnapshot(t, directory); !reflect.DeepEqual(after, before) {
					t.Fatalf("help/version changed log target: before=%v after=%v", before, after)
				}
			})
		}
	}
}

func executeRootDiagnosticProcess(t *testing.T, directory string, args []string, helperEnvironment ...string) (string, string, error) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(profile, appconfig.Template(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	arguments := append([]string{"-test.run=^TestRootDiagnosticProcessHelper$", "--", "--config", profile}, args...)
	process := exec.CommandContext(ctx, binary, arguments...)
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if strings.HasPrefix(key, "NB1_") || key == "XDG_CONFIG_HOME" || key == "XDG_STATE_HOME" {
			continue
		}
		process.Env = append(process.Env, value)
	}
	process.Env = append(process.Env, "NB1_JSON_ROOT_TEST_HELPER=1", "XDG_CONFIG_HOME="+directory, "XDG_STATE_HOME="+directory)
	process.Env = append(process.Env, helperEnvironment...)
	process.Dir = directory
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	err = process.Run()
	if ctx.Err() != nil {
		t.Fatalf("root diagnostic process timed out: %v", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

func TestRootDiagnosticProcessHelper(t *testing.T) {
	if os.Getenv("NB1_JSON_ROOT_TEST_HELPER") != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument == "--" {
			os.Args = append([]string{"nb1"}, os.Args[index+1:]...)
			if os.Getenv("NB1_JSON_ROOT_TEST_BOOTSTRAP_SPY") == "1" {
				command, _, err := rootCmd.Find(os.Args[1:])
				if err != nil {
					t.Fatal(err)
				}
				command.Run = nil
				command.RunE = func(*cobra.Command, []string) error {
					fmt.Fprintln(os.Stdout, "bootstrap spy Run executed")
					return nil
				}
				for parent := command; parent != nil && parent != rootCmd; parent = parent.Parent() {
					if parent.PersistentPreRunE != nil || parent.PersistentPreRun != nil {
						parent.PersistentPreRun = nil
						parent.PersistentPreRunE = func(*cobra.Command, []string) error {
							fmt.Fprintln(os.Stderr, "bootstrap spy pre-run executed")
							return nil
						}
						break
					}
				}
			}
			Execute()
			os.Exit(0)
		}
	}
	t.Fatal("missing root diagnostic arguments")
}
