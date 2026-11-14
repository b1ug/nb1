package cmd

import (
	"bytes"
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
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/doctor"
	"github.com/spf13/cobra"
)

func TestVersionMetadataFallbacks(t *testing.T) {
	const revision = "1234567890abcdef1234567890abcdef12345678"
	for _, test := range []struct {
		name                 string
		build                doctor.Build
		wantSummary, wantGit string
	}{
		{"plain source build", doctor.Build{Revision: revision, Modified: "false"}, "candidate-1234567890ab", revision},
		{"modified source build", doctor.Build{Revision: revision, Modified: "true"}, "candidate-1234567890ab-dirty", revision + "-dirty"},
		{"Go-derived pseudo version remains a source candidate", doctor.Build{Revision: revision, Modified: "true", Main: doctor.Module{Version: "v0.0.2-0.20261113183718-1234567890ab+dirty"}}, "candidate-1234567890ab-dirty", revision + "-dirty"},
		{"installed module", doctor.Build{Main: doctor.Module{Version: "v1.2.3", Source: "module"}, Modified: "false"}, "v1.2.3", ""},
		{"modified module", doctor.Build{Main: doctor.Module{Version: "v1.2.3", Source: "module"}, Modified: "true"}, "v1.2.3-dirty", ""},
		{"existing module dirty marker is not duplicated", doctor.Build{Main: doctor.Module{Version: "v1.2.3+dirty", Source: "module"}, Modified: "true"}, "v1.2.3+dirty", ""},
		{"no metadata", doctor.Build{}, "development", ""},
		{"unversioned module", doctor.Build{Main: doctor.Module{Version: "(devel)"}}, "development", ""},
		{"local replacement is not its requested release", doctor.Build{Main: doctor.Module{RequestedVersion: "v9.9.9", Source: "local_unversioned"}}, "development", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.build.GoVersion = "go1.27.4"
			got := resolvedVersionFields(versionFields{name: "nb1"}, test.build)
			if got.gitSummary != test.wantSummary || got.gitCommit != test.wantGit || got.goVersion != "go1.27.4" {
				t.Fatalf("incorrect metadata: %+v", got)
			}
		})
	}
	got := resolvedVersionFields(versionFields{}, doctor.Build{})
	if got.goVersion != runtime.Version() || got.name != "nb1" {
		t.Fatalf("missing executable runtime fallback: %+v", got)
	}
}

func TestVersionMetadataPreservesLinkerFields(t *testing.T) {
	fields := versionFields{
		name: "nb1", buildNum: "42", buildDate: "2026-11-14", buildHost: "private-build-host",
		goVersion: "linker-go", gitBranch: "release-branch", gitCommit: "linker-commit", gitSummary: "v1.2.3-custom",
	}
	build := doctor.Build{GoVersion: "actual-go", Revision: "other-revision", Modified: "true"}
	if got := resolvedVersionFields(fields, build); !reflect.DeepEqual(got, fields) {
		t.Fatalf("explicit linker identity changed: %+v", got)
	}
	var output bytes.Buffer
	if err := writeVersion(&output, fields, build, false); err != nil {
		t.Fatal(err)
	}
	// Retain the established labels and field order for human consumers.
	last := -1
	for _, pair := range [][2]string{
		{"App Name  ", "nb1"}, {"Build Num ", "42"}, {"Build Date", "2026-11-14"},
		{"Build Host", "private-build-host"}, {"Go Version", "linker-go"},
		{"Git Branch", "release-branch"}, {"Git Commit", "linker-commit"}, {"GitSummary", "v1.2.3-custom"},
	} {
		index := strings.Index(output.String(), pair[0]+": "+pair[1])
		if index <= last {
			t.Fatalf("missing or reordered human field %q:\n%s", pair[0], output.String())
		}
		last = index
	}
}

func TestVersionJSONHasOnlyCheckedBuildAndNoStylingOrPrivateLinkerData(t *testing.T) {
	build := doctor.Build{
		InfoAvailable: true, GoVersion: "go1.27.4", OS: "darwin", Arch: "arm64",
		Main:     doctor.Module{Path: "github.com/b1ug/nb1", Source: "local_unversioned"},
		Modified: "unknown", Dependencies: []doctor.Module{
			{Path: "github.com/b1ug/gid", RequestedVersion: "v1.0.0", Source: "local_unversioned"},
		},
	}
	var output bytes.Buffer
	err := writeVersion(&output, versionFields{
		name: "nb1", buildHost: "/private/build/host", gitBranch: "private-branch", buildDate: "private-date",
	}, build, true)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte{0x1b}) || strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "/private/") {
		t.Fatalf("styled or private linker data in JSON: %q", output.String())
	}
	decoder := json.NewDecoder(&output)
	var got versionMetadata
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Name != "nb1" || got.Version != "development" || !reflect.DeepEqual(got.Build, build) {
		t.Fatalf("incorrect version envelope: %+v", got)
	}
	if err := decoder.Decode(new(interface{})); !errors.Is(err, io.EOF) {
		t.Fatalf("extra output after JSON: %v", err)
	}
	var names map[string]json.RawMessage
	raw := new(bytes.Buffer)
	if err := writeVersion(raw, versionFields{name: "nb1"}, build, true); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw.Bytes(), &names); err != nil {
		t.Fatal(err)
	}
	if len(names) != 4 || names["schema_version"] == nil || names["name"] == nil || names["version"] == nil || names["build"] == nil {
		t.Fatalf("unexpected schema fields: %v", names)
	}
}

type versionErrorWriter struct{ err error }

func (writer versionErrorWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestVersionPropagatesOutputErrors(t *testing.T) {
	failure := errors.New("version output unavailable")
	for _, asJSON := range []bool{false, true} {
		err := writeVersion(versionErrorWriter{failure}, versionFields{name: "nb1"}, doctor.Build{}, asJSON)
		if !errors.Is(err, failure) {
			t.Fatalf("json=%t: writer error lost: %v", asJSON, err)
		}
	}
}

func TestVersionCommandUsesSelectedOutputAndReadsOnlyBuildOnce(t *testing.T) {
	var output bytes.Buffer
	calls := 0
	command := newVersionCommand(func() doctor.Build {
		calls++
		return doctor.Build{GoVersion: "fixture-go", Main: doctor.Module{Version: "v1.2.3"}}
	})
	command.SetOut(&output)
	command.SetArgs([]string{"--json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var got versionMetadata
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.Build.GoVersion != "fixture-go" || got.Version != "v1.2.3" {
		t.Fatalf("wrong build read or output destination: calls=%d, result=%+v", calls, got)
	}
}

func TestVersionAliasesBypassUnrelatedRuntimeConfigurationErrors(t *testing.T) {
	for _, alias := range []string{"version", "ver", "v"} {
		t.Run(alias, func(t *testing.T) {
			parent := &cobra.Command{Use: "nb1", PersistentPreRunE: func(*cobra.Command, []string) error {
				t.Fatal("version should not require a runtime profile")
				return errors.New("invalid profile")
			}}
			parent.AddCommand(newVersionCommand(func() doctor.Build { return doctor.Build{GoVersion: "fixture-go"} }))
			var output bytes.Buffer
			parent.SetOut(&output)
			parent.SetArgs([]string{alias, "--json"})
			if err := parent.Execute(); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(output.Bytes()) {
				t.Fatalf("alias %q did not produce JSON: %q", alias, output.String())
			}
		})
	}
}

// Run the actual root command in a fresh process: a synthetic Cobra parent does
// not exercise the package's registered OnInitialize callback or global logger.
func TestVersionActualRootDoesNotInitializeRuntimeConfiguration(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		args      []string
		noProfile bool
	}{
		{"debug before command", []string{"--debug", "version", "--json"}, false},
		{"debug after command", []string{"version", "--json", "--debug"}, false},
		{"short debug flag", []string{"-D", "version", "--json"}, false},
		{"ver alias", []string{"--debug", "ver", "--json"}, false},
		{"v alias", []string{"v", "--debug", "--json"}, false},
		{"invalid log level before command", []string{"--log-level", "not-a-level", "version", "--json"}, false},
		{"invalid log level after command", []string{"version", "--json", "--log-level=not-a-level"}, false},
		{"explicit empty config", []string{"--config", "", "version", "--json"}, false},
		{"missing default profile with debug", []string{"--debug", "version", "--json"}, true},
		{"missing default profile with trailing debug", []string{"version", "--json", "--debug"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			profile := filepath.Join(directory, "invalid profile.yaml")
			if test.noProfile {
				profile = ""
			} else if err := os.WriteFile(profile, []byte("jobs:\n  terminal_limit: 0\n"), 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(directory, "must-not-exist.log")
			args := append([]string{"--log-file", logPath}, test.args...)
			stdout, stderr, err := executeVersionRootProcess(t, binary, directory, profile, args)
			if err != nil || stderr != "" {
				t.Fatalf("version failed: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			var metadata versionMetadata
			if err := json.Unmarshal([]byte(stdout), &metadata); err != nil {
				t.Fatalf("stdout is not pure JSON: %v; %q", err, stdout)
			}
			if metadata.SchemaVersion != 1 || metadata.Name != "nb1" || metadata.Build.GoVersion == "" || strings.ContainsRune(stdout, '\x1b') {
				t.Fatalf("incorrect or styled metadata: %q", stdout)
			}
			if _, err := os.Lstat(logPath); !os.IsNotExist(err) {
				t.Fatalf("version created a runtime log file: %v", err)
			}
			entries, err := os.ReadDir(directory)
			wantEntries := 1
			if test.noProfile {
				wantEntries = 0
			}
			if err != nil || len(entries) != wantEntries || (!test.noProfile && entries[0].Name() != filepath.Base(profile)) {
				t.Fatalf("version created runtime files: %v; %v", entries, err)
			}
		})
	}
}

func TestVersionInitializationBypassDoesNotApplyToOtherCommands(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"configuration validation", []string{"config", "list"}, "terminal_limit"},
		// The word version used as a flag value is not the version command.
		{"command name as flag value", []string{"--device", "version", "config", "list"}, "terminal_limit"},
		{"log level validation", []string{"--log-level", "not-a-level", "config", "list"}, "not-a-level"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			profile := filepath.Join(directory, "invalid profile.yaml")
			if err := os.WriteFile(profile, []byte("jobs:\n  terminal_limit: 0\n"), 0600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, err := executeVersionRootProcess(t, binary, directory, profile, test.args)
			if err == nil || !strings.Contains(stderr, test.want) {
				t.Fatalf("runtime initialization was bypassed: err=%v stdout=%q stderr=%q; want %q", err, stdout, stderr, test.want)
			}
		})
	}
}

func executeVersionRootProcess(t *testing.T, binary, directory, profile string, args []string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestVersionActualRootHelper$", "--"}, args...)...)
	// Retain platform execution settings, but never inherit a user's nb1 profile
	// or local daemon/device selection into these USB-free diagnostic tests.
	for _, value := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(value, "=", 2)[0])
		if strings.HasPrefix(key, "NB1_") || key == "HOME" || key == "USERPROFILE" || key == "APPDATA" || key == "LOCALAPPDATA" || key == "XDG_CONFIG_HOME" || key == "XDG_STATE_HOME" {
			continue
		}
		process.Env = append(process.Env, value)
	}
	process.Env = append(process.Env, "NB1_VERSION_ROOT_TEST_HELPER=1", "NB1_CONFIG="+profile,
		"HOME="+directory, "USERPROFILE="+directory, "APPDATA="+directory, "LOCALAPPDATA="+directory,
		"XDG_CONFIG_HOME="+directory, "XDG_STATE_HOME="+directory)
	process.Dir = directory
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	err := process.Run()
	if ctx.Err() != nil {
		t.Fatalf("root diagnostic timed out: %v", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

func TestVersionActualRootHelper(t *testing.T) {
	if os.Getenv("NB1_VERSION_ROOT_TEST_HELPER") != "1" {
		return
	}
	for i, argument := range os.Args {
		if argument != "--" {
			continue
		}
		// Replace the testing runner arguments with the actual CLI invocation;
		// both Cobra Execute and the initializer now see the real process args.
		os.Args = append([]string{"nb1"}, os.Args[i+1:]...)
		if err := rootCmd.Execute(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		command, _, err := rootCmd.Find(os.Args[1:])
		if err == nil && command == versionCmd && log != nil {
			fmt.Fprintln(os.Stderr, "version initialized the runtime logger")
			os.Exit(3)
		}
		os.Exit(0)
	}
	t.Fatal("missing root command arguments")
}
