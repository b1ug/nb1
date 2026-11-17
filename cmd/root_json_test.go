package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/charmbracelet/x/ansi"
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

func executeRootDiagnosticProcess(t *testing.T, directory string, args []string) (string, string, error) {
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
			Execute()
			os.Exit(0)
		}
	}
	t.Fatal("missing root diagnostic arguments")
}
