package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"

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
