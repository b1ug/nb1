//go:build darwin || linux
// +build darwin linux

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/b1ug/nb1/internal/ownership"
)

type fakeCall struct {
	args   []string
	result CommandResult
	err    error
	check  func()
}

func TestUserStatusDefaultNotesUseEnglishAndPreserveValues(t *testing.T) {
	manager, _, _ := fixtureManager(t)
	status, err := manager.Status(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteUserStatus(&output, status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "nb1 macOS user service") || strings.ContainsFunc(output.String(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		t.Fatalf("default status or notes are not English: %s", output.String())
	}
	status.Config = &Config{Binary: "/tmp/小灯/nb1", Devices: []string{"20005601"}, Listen: "127.0.0.1:8931"}
	output.Reset()
	if err := WriteUserStatus(&output, status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "binary: /tmp/小灯/nb1") || !strings.Contains(output.String(), "devices: 20005601") {
		t.Fatalf("human formatting changed user values: %s", output.String())
	}
}

type fakeLaunchctl struct {
	t     *testing.T
	steps []fakeCall
	calls [][]string
}

func (f *fakeLaunchctl) Run(_ context.Context, args ...string) (CommandResult, error) {
	f.t.Helper()
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(f.steps) == 0 {
		f.t.Fatalf("unexpected launchctl invocation: %q", args)
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	if !reflect.DeepEqual(args, step.args) {
		f.t.Fatalf("launchctl argv %q; want %q", args, step.args)
	}
	if step.check != nil {
		step.check()
	}
	return step.result, step.err
}

func fixtureManager(t *testing.T) (*UserManager, *fakeLaunchctl, Config) {
	t.Helper()
	runner := &fakeLaunchctl{t: t}
	t.Cleanup(func() {
		if len(runner.steps) != 0 {
			t.Errorf("unused fake launchctl expectations: %d", len(runner.steps))
		}
	})
	m, err := newUserManager(t.TempDir(), os.Geteuid(), runner)
	if err != nil {
		t.Fatal(err)
	}
	// Keep profile selection isolated and deterministic even when the caller's
	// shell or CI user manager exports another account's XDG_CONFIG_HOME.
	root := filepath.Join(m.home, "fixture-config")
	t.Setenv("XDG_CONFIG_HOME", root)
	binary := filepath.Join(m.home, "nb1 & fixture program")
	if err := os.WriteFile(binary, []byte("this fixture must never be executed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, runner, Config{Platform: "darwin", Binary: binary, UserConfigRoot: root, Devices: []string{"20005601"}, Listen: "127.0.0.1:8931"}
}

func absentCalls(m *UserManager) []fakeCall {
	return []fakeCall{{args: []string{"print", m.target()}, result: CommandResult{ExitCode: 113}}, {args: []string{"print", m.domain()}}}
}

func identity(m *UserManager, config Config) string {
	return m.target() + " = {\n\tpath = " + m.plistPath() + "\n\tprogram = " + config.Binary + "\n\tenvironment = {\n\t\tPRIVATE_SECRET = never persist this\n\t}\n}\n"
}

func registeredCall(m *UserManager, config Config) fakeCall {
	return fakeCall{args: []string{"print", m.target()}, result: CommandResult{Output: identity(m, config)}}
}

// Prepare a known fixture without running manager operations or launchctl.
func putManaged(t *testing.T, m *UserManager, config Config) []byte {
	t.Helper()
	if err := m.directory(filepath.Dir(m.plistPath()), true); err != nil {
		t.Fatal(err)
	}
	content, err := managedContent(managedDefinition{Version: 1, Label: Label, UID: m.uid, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if err := createManaged(m.plistPath(), content); err != nil {
		t.Fatal(err)
	}
	return content
}

func assertContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file changed or missing: %s %v", path, err)
	}
}

func TestUserConfigOnlyStatusHasNoWritesOrCommands(t *testing.T) {
	m, runner, _ := fixtureManager(t)
	before, _ := os.ReadDir(m.home)
	status, err := m.Status(context.Background(), true)
	after, _ := os.ReadDir(m.home)
	if err != nil || status.ConfigState != "absent" || status.Registration != "not_checked" || len(runner.calls) != 0 || len(before) != len(after) {
		t.Fatalf("config-only status had side effects: %+v %v", status, err)
	}
}

func TestUserInstallPinsCurrentXDGRootWithoutMovingNativeServicePaths(t *testing.T) {
	m, runner, config := fixtureManager(t)
	wantRoot := config.UserConfigRoot
	config.UserConfigRoot = ""
	runner.steps = absentCalls(m)
	status, err := m.Install(context.Background(), config, false)
	if err != nil || status.Config == nil || status.Config.UserConfigRoot != wantRoot {
		t.Fatalf("installer failed to bind XDG: %+v %v", status, err)
	}
	if status.PlistPath != filepath.Join(m.home, "Library", "LaunchAgents", Label+".plist") {
		t.Fatalf("native service path moved: %q", status.PlistPath)
	}
	file, err := m.readManaged()
	if err != nil || file == nil || file.definition.Config.UserConfigRoot != wantRoot || !bytes.Contains(file.content, []byte("<key>EnvironmentVariables</key>")) {
		t.Fatalf("bound root not preserved in complete metadata/plist: %+v %v", file, err)
	}
	if _, err := os.Stat(wantRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installation created profile/library storage: %v", err)
	}
	config.UserConfigRoot = filepath.Join(m.home, "another-root")
	calls := len(runner.calls)
	if _, err := m.Install(context.Background(), config, false); err == nil || len(runner.calls) != calls {
		t.Fatalf("mismatched root reached launchctl or was accepted: %v", err)
	}
}

func TestUserInstallRejectsRelativeXDGRootBeforeCommandsOrWrites(t *testing.T) {
	m, runner, config := fixtureManager(t)
	t.Setenv("XDG_CONFIG_HOME", "relative-root")
	config.UserConfigRoot = ""
	if _, err := m.Install(context.Background(), config, false); err == nil || len(runner.calls) != 0 {
		t.Fatalf("invalid XDG reached launchctl: %v", err)
	}
	if _, err := os.Stat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid XDG wrote a plist: %v", err)
	}
}

func TestOldManagedPlistWithoutXDGRemainsReadableAndRecoverable(t *testing.T) {
	m, runner, config := fixtureManager(t)
	config.UserConfigRoot = ""
	old := putManaged(t, m, config)
	if bytes.Contains(old, []byte("EnvironmentVariables")) || bytes.Contains(old, []byte("user_config_root")) {
		t.Fatal("empty optional field changed the old artifact")
	}
	// Start/stop use the saved definition, even if the current profile root is
	// broken. They neither rewrite old metadata nor import the caller's value.
	t.Setenv("XDG_CONFIG_HOME", "relative-root")
	runner.steps = append(absentCalls(m), fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}}, registeredCall(m, config))
	status, err := m.Start(context.Background())
	if err != nil || status.Config == nil || status.Config.UserConfigRoot != "" {
		t.Fatalf("old service could not start: %+v %v", status, err)
	}
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}}}
	runner.steps = append(runner.steps, absentCalls(m)...)
	if _, err := m.Stop(context.Background()); err != nil {
		t.Fatalf("old service could not stop: %v", err)
	}
	assertContent(t, m.plistPath(), old)
}

func TestUserInstallDefaultChecksLaunchdAndEnablesFutureLoginOnly(t *testing.T) {
	m, runner, config := fixtureManager(t)
	runner.steps = absentCalls(m)
	status, err := m.Install(context.Background(), config, false)
	if err != nil || status.ConfigState != "managed" || status.Registration != "absent" || status.AutoLoadAtLogin == nil || !*status.AutoLoadAtLogin || !reflect.DeepEqual(status.Actions, []string{"plist_created"}) {
		t.Fatalf("install status: %+v %v", status, err)
	}
	file, err := m.readManaged()
	if err != nil || file == nil || file.info.Mode().Perm() != 0600 {
		t.Fatalf("managed plist: %+v %v", file, err)
	}
	for _, entry := range mustReadDir(t, filepath.Dir(m.plistPath())) {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("staging file left after successful publication")
		}
	}
	runner.steps = absentCalls(m)
	status, err = m.Install(context.Background(), config, false)
	if err != nil || len(status.Actions) != 0 {
		t.Fatalf("idempotent install rewrote configuration: %+v %v", status, err)
	}
	latest, _ := os.Stat(m.plistPath())
	if !os.SameFile(file.info, latest) {
		t.Fatal("idempotent install replaced file inode")
	}
	config.Listen = "127.0.0.1:8932"
	runner.steps = absentCalls(m)
	if _, err := m.Install(context.Background(), config, false); err == nil {
		t.Fatal("different configuration overwrote existing plist")
	}
	assertContent(t, m.plistPath(), file.content)
}

func mustReadDir(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestUserInstallRejectsForeignRegistrationEvenWithoutStart(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(fmt.Sprint(start), func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			runner.steps = []fakeCall{{args: []string{"print", m.target()}, result: CommandResult{Output: m.target() + " = {\n\tpath = /another/foreign.plist\n\tprogram = /another/binary\n}\n"}}}
			status, err := m.Install(context.Background(), config, start)
			if err == nil || status.Registration != "registered" || status.RegistrationOwned {
				t.Fatalf("foreign registration accepted: %+v %v", status, err)
			}
			if _, err := os.Lstat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("foreign same-label service caused a future login collision: %v", err)
			}
		})
	}
}

func TestUserStartAndStopUseOnlyExactOwnedTarget(t *testing.T) {
	m, runner, config := fixtureManager(t)
	runner.steps = append(absentCalls(m), fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}, check: func() {
		if file, err := m.readManaged(); err != nil || file == nil {
			t.Fatalf("bootstrap preceded complete managed plist: %v", err)
		}
	}}, registeredCall(m, config))
	status, err := m.Install(context.Background(), config, true)
	if err != nil || !status.RegistrationOwned || !reflect.DeepEqual(status.Actions, []string{"plist_created", "bootstrap_requested"}) {
		t.Fatalf("start: %+v %v", status, err)
	}
	original, _ := os.ReadFile(m.plistPath())
	runner.steps = []fakeCall{registeredCall(m, config)}
	status, err = m.Uninstall(context.Background(), false)
	if err == nil || !status.RegistrationOwned {
		t.Fatalf("registered service removed without --stop: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}, check: func() { assertContent(t, m.plistPath(), original) }}}
	runner.steps = append(runner.steps, absentCalls(m)...)
	status, err = m.Uninstall(context.Background(), true)
	if err != nil || status.ConfigState != "absent" || status.Registration != "absent" || *status.AutoLoadAtLogin || !reflect.DeepEqual(status.Actions, []string{"bootout_requested", "plist_removed"}) {
		t.Fatalf("stop/uninstall: %+v %v", status, err)
	}
	if _, err := os.Lstat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist still present: %v", err)
	}
	locks, _ := os.ReadDir(filepath.Join(m.home, "Library", "Application Support", "nb1", "service-management"))
	if len(locks) != 1 || !strings.HasSuffix(locks[0].Name(), ".lock") {
		t.Fatal("persistent service-management lock was removed")
	}
}

func TestUserIdempotentRegisteredInstallDoesNotRestart(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	for _, start := range []bool{false, true} {
		runner.steps = []fakeCall{registeredCall(m, config)}
		status, err := m.Install(context.Background(), config, start)
		if err != nil || !status.RegistrationOwned || len(status.Actions) != 0 {
			t.Fatalf("already loaded configuration restarted: %+v %v", status, err)
		}
		assertContent(t, m.plistPath(), original)
	}
}

func TestUserUnknownOrModifiedPlistsAreNeverAdopted(t *testing.T) {
	for _, variant := range []string{"plain_render", "changed_content", "wrong_uid", "wrong_label", "unsafe_mode", "symlink", "directory_symlink"} {
		t.Run(variant, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			content, err := managedContent(managedDefinition{Version: 1, Label: Label, UID: m.uid, Config: config})
			if err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "plain_render":
				artifact, _ := Render(config)
				content = artifact.Content
			case "changed_content":
				content = bytes.Replace(content, []byte("<integer>20</integer>"), []byte("<integer>21</integer>"), 1)
			case "wrong_uid":
				content, _ = managedContent(managedDefinition{Version: 1, Label: Label, UID: m.uid + 1, Config: config})
			case "wrong_label":
				content, _ = managedContent(managedDefinition{Version: 1, Label: "org.other.tool", UID: m.uid, Config: config})
			}
			if variant == "directory_symlink" {
				if err := os.MkdirAll(filepath.Join(m.home, "Library"), 0700); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Dir(m.plistPath())); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := m.directory(filepath.Dir(m.plistPath()), true); err != nil {
					t.Fatal(err)
				}
				if variant == "symlink" {
					outside := filepath.Join(t.TempDir(), "foreign.plist")
					if err := os.WriteFile(outside, content, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, m.plistPath()); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(m.plistPath(), content, 0600); err != nil {
					t.Fatal(err)
				}
				if variant == "unsafe_mode" {
					if err := os.Chmod(m.plistPath(), 0666); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, operation := range []string{"status", "install", "uninstall", "start", "stop"} {
				var status UserStatus
				var err error
				switch operation {
				case "status":
					status, err = m.Status(context.Background(), true)
				case "install":
					status, err = m.Install(context.Background(), config, true)
				case "uninstall":
					status, err = m.Uninstall(context.Background(), true)
				case "start":
					status, err = m.Start(context.Background())
				case "stop":
					status, err = m.Stop(context.Background())
				}
				if err == nil || status.ConfigState != "conflict" {
					t.Fatalf("%s adopted %s plist: %+v %v", operation, variant, status, err)
				}
			}
			if len(runner.calls) != 0 {
				t.Fatal("unknown plist reached launchctl")
			}
			if variant != "directory_symlink" {
				assertContent(t, m.plistPath(), content)
			}
		})
	}
}

func TestUserQueryFailuresPreserveFilesAndDoNotMutateLaunchd(t *testing.T) {
	for _, failure := range []string{"service_error", "domain_missing", "transport_error", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			original := putManaged(t, m, config)
			for _, operation := range []string{"install", "uninstall", "start", "stop"} {
				step := fakeCall{args: []string{"print", m.target()}, result: CommandResult{ExitCode: 1}}
				switch failure {
				case "domain_missing":
					step.result.ExitCode = 113
				case "transport_error":
					step.err = errors.New("fake failure")
				case "deadline":
					step.err = context.DeadlineExceeded
				}
				runner.steps = []fakeCall{step}
				if failure == "domain_missing" {
					runner.steps = append(runner.steps, fakeCall{args: []string{"print", m.domain()}, result: CommandResult{ExitCode: 1}})
				}
				var status UserStatus
				var err error
				switch operation {
				case "install":
					status, err = m.Install(context.Background(), config, true)
				case "uninstall":
					status, err = m.Uninstall(context.Background(), true)
				case "start":
					status, err = m.Start(context.Background())
				case "stop":
					status, err = m.Stop(context.Background())
				}
				if err == nil || status.Registration != "unknown" {
					t.Fatalf("%s treated query failure as absent: %+v %v", operation, status, err)
				}
				assertContent(t, m.plistPath(), original)
			}
		})
	}
}

func TestUserUnverifiedRuntimeIdentityProhibitsBootout(t *testing.T) {
	for _, variant := range []string{"wrong_path", "wrong_program", "changed_format", "nested_fields", "duplicate_fields", "truncated"} {
		t.Run(variant, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			original := putManaged(t, m, config)
			output := identity(m, config)
			result := CommandResult{}
			switch variant {
			case "wrong_path":
				output = strings.Replace(output, m.plistPath(), "/foreign/org.b1ug.nb1.plist", 1)
			case "wrong_program":
				output = strings.Replace(output, config.Binary, "/foreign/nb1", 1)
			case "changed_format":
				output = "new output format without documented identity fields"
			case "nested_fields":
				output = m.target() + " = {\n\tsomething = {\n\t\tpath = " + m.plistPath() + "\n\t\tprogram = " + config.Binary + "\n\t}\n}\n"
			case "duplicate_fields":
				output = strings.Replace(output, "\tenvironment", "\tpath = "+m.plistPath()+"\n\tenvironment", 1)
			case "truncated":
				result.Truncated = true
			}
			result.Output = output
			for _, operation := range []func(context.Context) (UserStatus, error){m.Start, m.Stop, func(ctx context.Context) (UserStatus, error) { return m.Uninstall(ctx, true) }} {
				runner.steps = []fakeCall{{args: []string{"print", m.target()}, result: result}}
				status, err := operation(context.Background())
				if err == nil || status.Registration != "registered" || status.RegistrationOwned || len(status.Actions) != 0 {
					t.Fatalf("unverified runtime accepted: %+v %v", status, err)
				}
				assertContent(t, m.plistPath(), original)
			}
		})
	}
}

func TestUserBootstrapAndBootoutFailuresHaveNoAutomaticRollback(t *testing.T) {
	m, runner, config := fixtureManager(t)
	runner.steps = append(absentCalls(m), fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}, result: CommandResult{ExitCode: 5, Output: "PRIVATE_SECRET"}})
	status, err := m.Install(context.Background(), config, true)
	if err == nil || status.ConfigState != "managed" || status.Registration != "unknown" || !*status.AutoLoadAtLogin {
		t.Fatalf("failed bootstrap lost config/uncertainty: %+v %v", status, err)
	}
	original, _ := os.ReadFile(m.plistPath())
	for _, failure := range []fakeCall{
		{args: []string{"bootout", m.target()}, result: CommandResult{ExitCode: 5, Output: "PRIVATE_SECRET"}},
		{args: []string{"bootout", m.target()}, err: context.DeadlineExceeded},
	} {
		runner.steps = []fakeCall{registeredCall(m, config), failure}
		status, err = m.Uninstall(context.Background(), true)
		if err == nil || status.Registration != "unknown" {
			t.Fatalf("failed bootout lost uncertainty: %+v %v", status, err)
		}
		assertContent(t, m.plistPath(), original)
		encoded, _ := json.Marshal(status)
		if bytes.Contains(encoded, []byte("PRIVATE_SECRET")) || strings.Contains(err.Error(), "PRIVATE_SECRET") {
			t.Fatal("launchctl environment/diagnostic output leaked")
		}
	}
}

func TestUserPostBootoutQueryFailureKeepsConfiguration(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}}, {args: []string{"print", m.target()}, result: CommandResult{ExitCode: 1}}}
	status, err := m.Uninstall(context.Background(), true)
	if err == nil || status.Registration != "unknown" {
		t.Fatalf("post-stop query failure removed config: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
}

func TestUserObservedFileChangeBeforeBootoutOrRemovalIsRefused(t *testing.T) {
	for _, registered := range []bool{false, true} {
		for _, mutation := range []string{"same_inode_edit", "same_inode_new_managed_config", "inode_replacement"} {
			t.Run(fmt.Sprintf("registered=%t/%s", registered, mutation), func(t *testing.T) {
				m, runner, config := fixtureManager(t)
				original := putManaged(t, m, config)
				var changed []byte
				mutate := func() {
					changed = []byte("foreign edit observed during launchctl query")
					if mutation != "same_inode_edit" {
						updated := config
						updated.Listen = "127.0.0.1:8932"
						changed, _ = managedContent(managedDefinition{Version: 1, Label: Label, UID: m.uid, Config: updated})
					}
					if mutation == "inode_replacement" {
						temporary := m.plistPath() + ".replacement"
						if err := os.WriteFile(temporary, changed, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(temporary, m.plistPath()); err != nil {
							t.Fatal(err)
						}
					} else if err := os.WriteFile(m.plistPath(), changed, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if registered {
					step := registeredCall(m, config)
					step.check = mutate
					runner.steps = []fakeCall{step}
				} else {
					runner.steps = absentCalls(m)
					runner.steps[0].check = mutate
				}
				status, err := m.Uninstall(context.Background(), true)
				if err == nil || len(status.Actions) != 0 {
					t.Fatalf("observed change was acted on: %+v %v", status, err)
				}
				if bytes.Equal(original, changed) {
					t.Fatal("fixture did not change content")
				}
				assertContent(t, m.plistPath(), changed)
			})
		}
	}
}

func TestUserObservedFileChangeAfterBootoutPreventsDeletion(t *testing.T) {
	m, runner, config := fixtureManager(t)
	putManaged(t, m, config)
	changed := []byte("unknown edit during bootout")
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}, check: func() {
		if err := os.WriteFile(m.plistPath(), changed, 0600); err != nil {
			t.Fatal(err)
		}
	}}}
	runner.steps = append(runner.steps, absentCalls(m)...)
	status, err := m.Uninstall(context.Background(), true)
	if err == nil || !reflect.DeepEqual(status.Actions, []string{"bootout_requested"}) {
		t.Fatalf("changed file was removed: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), changed)
}

func TestUserUninstallAbsentRegistrationAndOrphanProtection(t *testing.T) {
	m, runner, config := fixtureManager(t)
	putManaged(t, m, config)
	runner.steps = absentCalls(m)
	status, err := m.Uninstall(context.Background(), false)
	if err != nil || !reflect.DeepEqual(status.Actions, []string{"plist_removed"}) {
		t.Fatalf("files-only removal: %+v %v", status, err)
	}
	runner.steps = absentCalls(m)
	status, err = m.Uninstall(context.Background(), true)
	if err != nil || len(status.Actions) != 0 {
		t.Fatalf("idempotent uninstall: %+v %v", status, err)
	}
	runner.steps = []fakeCall{registeredCall(m, config)}
	status, err = m.Uninstall(context.Background(), true)
	if err == nil || status.RegistrationOwned {
		t.Fatalf("orphan/foreign label was stopped without managed file: %+v %v", status, err)
	}
}

func TestUserManagementLockDoesNotUseDeviceLease(t *testing.T) {
	m, runner, config := fixtureManager(t)
	lease, err := m.managementLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := m.Install(context.Background(), config, false); !errors.Is(err, ownership.ErrBusy) {
		t.Fatalf("parallel management operation was not excluded: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("parallel installer reached launchctl")
	}
	if _, err := os.Stat(filepath.Join(m.home, "Library", "Application Support", "nb1", "device-locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("service management created hardware ownership state")
	}
}

func TestUserEarlyFailureDoesNotReportAnUncheckedPlistAbsent(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	invalid := config
	invalid.Binary += "-missing"
	status, err := m.Install(context.Background(), invalid, false)
	if err == nil || status.ConfigState != "not_checked" || status.AutoLoadAtLogin != nil {
		t.Fatalf("invalid binary invented absence: %+v %v", status, err)
	}
	lease, err := m.managementLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	status, err = m.Uninstall(context.Background(), true)
	if err == nil || status.ConfigState != "not_checked" || status.AutoLoadAtLogin != nil {
		t.Fatalf("busy management lock invented absence: %+v %v", status, err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("early failure reached launchctl")
	}
	assertContent(t, m.plistPath(), original)
	var text bytes.Buffer
	if err := WriteUserStatus(&text, status); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "The managed plist is absent") || !strings.Contains(text.String(), "not checked or unconfirmed") {
		t.Fatalf("text asserted unchecked absence: %s", text.String())
	}
}

func TestUserBinaryValidationAndNoOverwritePublication(t *testing.T) {
	for _, mode := range []string{"missing", "relative", "directory", "not_executable", "implicit_device"} {
		t.Run(mode, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			switch mode {
			case "missing":
				config.Binary += "-missing"
			case "relative":
				config.Binary = "nb1"
			case "directory":
				config.Binary = m.home
			case "not_executable":
				if err := os.Chmod(config.Binary, 0600); err != nil {
					t.Fatal(err)
				}
			case "implicit_device":
				config.Devices = nil
			}
			if _, err := m.Install(context.Background(), config, true); err == nil {
				t.Fatal("invalid native binary/target accepted")
			}
			if len(runner.calls) != 0 {
				t.Fatal("invalid configuration reached launchctl")
			}
			if _, err := os.Lstat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid input created plist")
			}
		})
	}
	m, _, _ := fixtureManager(t)
	if err := m.directory(filepath.Dir(m.plistPath()), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.plistPath(), []byte("foreign existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := createManaged(m.plistPath(), []byte("new content")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("no-replace publication: %v", err)
	}
	assertContent(t, m.plistPath(), []byte("foreign existing file"))
}

func TestUserStatusDoesNotPersistLaunchctlEnvironment(t *testing.T) {
	m, runner, config := fixtureManager(t)
	putManaged(t, m, config)
	runner.steps = []fakeCall{registeredCall(m, config)}
	status, err := m.Status(context.Background(), false)
	if err != nil || !status.RegistrationOwned {
		t.Fatalf("status: %+v %v", status, err)
	}
	encoded, _ := json.Marshal(status)
	var text bytes.Buffer
	if err := WriteUserStatus(&text, status); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("PRIVATE_SECRET")) || strings.Contains(text.String(), "PRIVATE_SECRET") || !strings.Contains(text.String(), "does not establish that a process is running") {
		t.Fatal("status leaked print output or confused registration with readiness")
	}
	status, err = m.Status(context.Background(), true)
	if err != nil || status.Registration != "not_checked" || status.RegistrationOwned {
		t.Fatalf("config-only claimed runtime confirmation: %+v %v", status, err)
	}
}

func TestUserStandaloneStartStopPreserveConfigurationAndRegistrationMeaning(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	before, _ := os.Stat(m.plistPath())
	runner.steps = append(absentCalls(m), fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}}, registeredCall(m, config))
	status, err := m.Start(context.Background())
	if err != nil || !status.RegistrationOwned || !reflect.DeepEqual(status.Actions, []string{"bootstrap_requested"}) {
		t.Fatalf("standalone start: %+v %v", status, err)
	}
	runner.steps = []fakeCall{registeredCall(m, config)}
	status, err = m.Start(context.Background())
	if err != nil || len(status.Actions) != 0 || !status.RegistrationOwned {
		t.Fatalf("matching registration restarted: %+v %v", status, err)
	}
	// A successful bootout may leave the job registered briefly. Poll the same
	// target, then confirm the reachable GUI domain when it disappears.
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}}, registeredCall(m, config)}
	runner.steps = append(runner.steps, absentCalls(m)...)
	status, err = m.Stop(context.Background())
	if err != nil || status.ConfigState != "managed" || status.Registration != "absent" || status.RegistrationOwned || status.AutoLoadAtLogin == nil || !*status.AutoLoadAtLogin || !reflect.DeepEqual(status.Actions, []string{"bootout_requested"}) {
		t.Fatalf("standalone stop: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
	after, _ := os.Stat(m.plistPath())
	if !os.SameFile(before, after) {
		t.Fatal("start/stop replaced plist inode")
	}
	var output bytes.Buffer
	if err := WriteUserStatus(&output, status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Stop retains the plist and does not disable loading at the next graphical login") || !strings.Contains(output.String(), "An absent registration does not establish process exit") {
		t.Fatalf("stop overstated lifecycle completion: %s", output.String())
	}
	runner.steps = absentCalls(m)
	status, err = m.Stop(context.Background())
	if err != nil || len(status.Actions) != 0 || status.ConfigState != "managed" || !*status.AutoLoadAtLogin {
		t.Fatalf("already absent registration: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
}

func TestUserStandaloneControlsNeverAdoptMissingConfiguration(t *testing.T) {
	for _, operation := range []string{"start", "stop"} {
		for _, registered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/registered=%t", operation, registered), func(t *testing.T) {
				m, runner, config := fixtureManager(t)
				if registered {
					runner.steps = []fakeCall{registeredCall(m, config)}
				} else {
					runner.steps = absentCalls(m)
				}
				var status UserStatus
				var err error
				if operation == "start" {
					status, err = m.Start(context.Background())
				} else {
					status, err = m.Stop(context.Background())
				}
				if (operation == "start" || registered) && err == nil {
					t.Fatalf("%s adopted missing configuration: %+v", operation, status)
				}
				if len(status.Actions) != 0 || status.RegistrationOwned || status.ConfigState != "absent" {
					t.Fatalf("%s changed unknown service: %+v %v", operation, status, err)
				}
				if _, err := os.Lstat(m.plistPath()); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s created plist: %v", operation, err)
				}
			})
		}
	}
}

func TestUserStandaloneStartRechecksExecutableWithoutExecuting(t *testing.T) {
	for _, variant := range []string{"missing", "directory", "not_executable", "new_symlink"} {
		t.Run(variant, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			original := putManaged(t, m, config)
			switch variant {
			case "missing", "directory", "new_symlink":
				if err := os.Remove(config.Binary); err != nil {
					t.Fatal(err)
				}
				if variant == "directory" {
					if err := os.Mkdir(config.Binary, 0700); err != nil {
						t.Fatal(err)
					}
				} else if variant == "new_symlink" {
					target := filepath.Join(m.home, "different program")
					if err := os.WriteFile(target, []byte("must never run"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, config.Binary); err != nil {
						t.Fatal(err)
					}
				}
			case "not_executable":
				if err := os.Chmod(config.Binary, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner.steps = absentCalls(m)
			status, err := m.Start(context.Background())
			if err == nil || len(status.Actions) != 0 || status.Registration != "absent" {
				t.Fatalf("changed binary loaded: %+v %v", status, err)
			}
			assertContent(t, m.plistPath(), original)
		})
	}
}

func TestUserLifecycleControlsRecheckFileObservedDuringQuery(t *testing.T) {
	for _, operation := range []string{"start", "stop", "install"} {
		for _, registered := range []bool{false, true} {
			for _, mutation := range []string{"same_inode_edit", "same_inode_managed_change", "identical_inode_replacement", "removed"} {
				t.Run(fmt.Sprintf("%s/registered=%t/%s", operation, registered, mutation), func(t *testing.T) {
					m, runner, config := fixtureManager(t)
					original := putManaged(t, m, config)
					mutate := func() {
						changed := []byte("foreign edit")
						if mutation == "same_inode_managed_change" {
							updated := config
							updated.Listen = "127.0.0.1:8932"
							changed, _ = managedContent(managedDefinition{Version: 1, Label: Label, UID: m.uid, Config: updated})
						}
						var err error
						switch mutation {
						case "identical_inode_replacement":
							temporary := m.plistPath() + ".replacement"
							if err = os.WriteFile(temporary, original, 0600); err == nil {
								err = os.Rename(temporary, m.plistPath())
							}
						case "removed":
							err = os.Remove(m.plistPath())
						default:
							err = os.WriteFile(m.plistPath(), changed, 0600)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					if registered {
						runner.steps = []fakeCall{registeredCall(m, config)}
					} else {
						runner.steps = absentCalls(m)
					}
					runner.steps[0].check = mutate
					var status UserStatus
					var err error
					if operation == "start" {
						status, err = m.Start(context.Background())
					} else if operation == "stop" {
						status, err = m.Stop(context.Background())
					} else {
						status, err = m.Install(context.Background(), config, false)
					}
					if err == nil || len(status.Actions) != 0 {
						t.Fatalf("%s acted after file change: %+v %v", operation, status, err)
					}
				})
			}
		}
	}
}

func TestUserStandaloneStopCancellationPreservesUncertainRegistration(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := registeredCall(m, config)
	pending.check = cancel
	runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}}, pending}
	status, err := m.Stop(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for deregistration") || status.Registration != "registered" || !reflect.DeepEqual(status.Actions, []string{"bootout_requested"}) {
		t.Fatalf("unconfirmed unregister reported complete: %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
}

func TestUserStandaloneControlsShareManagementLock(t *testing.T) {
	m, runner, config := fixtureManager(t)
	original := putManaged(t, m, config)
	lease, err := m.managementLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, operation := range []func(context.Context) (UserStatus, error){m.Start, m.Stop} {
		status, err := operation(context.Background())
		if !errors.Is(err, ownership.ErrBusy) || status.ConfigState != "not_checked" || status.AutoLoadAtLogin != nil || len(runner.calls) != 0 {
			t.Fatalf("parallel control passed management lock: %+v %v", status, err)
		}
	}
	assertContent(t, m.plistPath(), original)
}

func TestUserStandaloneControlFailuresRetainConfigurationWithoutRollback(t *testing.T) {
	for _, variant := range []string{"bootstrap_error", "bootstrap_query_error", "bootstrap_file_changed", "bootout_error", "bootout_query_error", "bootout_file_changed", "bootout_identity_changed"} {
		t.Run(variant, func(t *testing.T) {
			m, runner, config := fixtureManager(t)
			wantContent := putManaged(t, m, config)
			start := strings.HasPrefix(variant, "bootstrap")
			var action string
			var invoke func(context.Context) (UserStatus, error)
			if start {
				action, invoke = "bootstrap_requested", m.Start
				runner.steps = absentCalls(m)
				runner.steps = append(runner.steps, fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}})
			} else {
				action, invoke = "bootout_requested", m.Stop
				runner.steps = []fakeCall{registeredCall(m, config), {args: []string{"bootout", m.target()}}}
			}
			last := &runner.steps[len(runner.steps)-1]
			switch {
			case strings.HasSuffix(variant, "_query_error"):
				runner.steps = append(runner.steps, fakeCall{args: []string{"print", m.target()}, err: context.DeadlineExceeded})
			case strings.HasSuffix(variant, "_file_changed"):
				wantContent = []byte("changed during native request")
				last.check = func() {
					if err := os.WriteFile(m.plistPath(), wantContent, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if start {
					runner.steps = append(runner.steps, registeredCall(m, config))
				} else {
					runner.steps = append(runner.steps, absentCalls(m)...)
				}
			case strings.HasSuffix(variant, "_identity_changed"):
				foreign := config
				foreign.Binary = "/foreign/program"
				runner.steps = append(runner.steps, registeredCall(m, foreign))
			default:
				last.result = CommandResult{ExitCode: 5, Output: "PRIVATE_SECRET"}
			}
			status, err := invoke(context.Background())
			if err == nil || !reflect.DeepEqual(status.Actions, []string{action}) || status.AutoLoadAtLogin == nil || !*status.AutoLoadAtLogin {
				t.Fatalf("uncertain control result: %+v %v", status, err)
			}
			encoded, _ := json.Marshal(status)
			if bytes.Contains(encoded, []byte("PRIVATE_SECRET")) || strings.Contains(err.Error(), "PRIVATE_SECRET") {
				t.Fatal("launchctl diagnostic output leaked")
			}
			assertContent(t, m.plistPath(), wantContent)
		})
	}
}
