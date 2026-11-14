package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/b1ug/nb1/internal/service"
	"github.com/b1ug/nb1/internal/service/systemduser"
	"github.com/b1ug/nb1/internal/service/taskquery"
)

type fakeUserService[S any] struct {
	status   S
	err      error
	calls    []userServiceAction
	contexts []context.Context
}

func (f *fakeUserService[S]) record(ctx context.Context, action userServiceAction) (S, error) {
	f.calls = append(f.calls, action)
	f.contexts = append(f.contexts, ctx)
	return f.status, f.err
}
func (f *fakeUserService[S]) Install(ctx context.Context, config service.Config, start bool) (S, error) {
	return f.record(ctx, userServiceAction{kind: serviceInstall, config: config, start: start})
}
func (f *fakeUserService[S]) Status(ctx context.Context, configOnly bool) (S, error) {
	return f.record(ctx, userServiceAction{kind: serviceStatus, configOnly: configOnly})
}
func (f *fakeUserService[S]) Start(ctx context.Context) (S, error) {
	return f.record(ctx, userServiceAction{kind: serviceStart})
}
func (f *fakeUserService[S]) Stop(ctx context.Context) (S, error) {
	return f.record(ctx, userServiceAction{kind: serviceStop})
}
func (f *fakeUserService[S]) Uninstall(ctx context.Context, stop bool) (S, error) {
	return f.record(ctx, userServiceAction{kind: serviceUninstall, stop: stop})
}

type serviceCLIContextKey struct{}

type serviceCLIFixture struct {
	management userServiceRuntime
	mac        *fakeUserService[service.UserStatus]
	linux      *fakeUserService[systemduser.Status]
	windows    *fakeWindowsServiceReader
	created    map[string]int
}

func serviceCLITestFixture(t *testing.T, goos string) *serviceCLIFixture {
	t.Helper()
	// Even Cobra's existing global config initialization sees a private home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	fixture := &serviceCLIFixture{
		mac: &fakeUserService[service.UserStatus]{status: service.UserStatus{
			SchemaVersion: 1, Label: "org.b1ug.nb1", Target: "gui/501/org.b1ug.nb1", PlistPath: "/fixture/org.b1ug.nb1.plist",
			ConfigState: "managed", Registration: "registered", RegistrationOwned: true, AutoLoadAtLogin: serviceCLIBool(true), Actions: []string{}, Notes: []string{"mac fixture"},
		}},
		linux: &fakeUserService[systemduser.Status]{status: systemduser.Status{
			SchemaVersion: 1, CheckedAt: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC), Platform: "linux", Label: "nb1.service", Target: "user/1000/nb1.service",
			UnitPath: "/fixture/systemd/user/nb1.service", EnableLinkPath: "/fixture/systemd/user/default.target.wants/nb1.service",
			ConfigState: "managed", EnableLinkState: "owned", ManagedEnabledForLogin: serviceCLIBool(true), Registration: "loaded", RegistrationOwned: true,
			Properties: &systemduser.Properties{LoadState: "loaded", ActiveState: "inactive", SubState: "dead", UnitFileState: "enabled", NoNewPrivileges: serviceCLIBool(true)},
			Actions:    []string{}, Notes: []string{"linux fixture"},
		}},
		windows: &fakeWindowsServiceReader{status: taskquery.Status{
			SchemaVersion: 1, CheckedAt: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC),
			TaskPath: `\B1UG-nb1-S-1-12-1-11-22-33-44`, Presence: taskquery.Present, Ownership: "not_checked",
			Task: &taskquery.Properties{Enabled: true, SchedulerState: 3, SchedulerStateName: "ready", LastTaskResult: 0},
		}},
		created: map[string]int{},
	}
	fixture.management = userServiceRuntime{goos: goos, factories: userServiceFactories{
		darwin: func() (userServiceBackend[service.UserStatus], error) {
			fixture.created["darwin"]++
			return fixture.mac, nil
		},
		linux: func() (userServiceBackend[systemduser.Status], error) {
			fixture.created["linux"]++
			return fixture.linux, nil
		},
		windows: func() (windowsServiceReader, error) {
			fixture.created["windows"]++
			return fixture.windows, nil
		},
	}}
	return fixture
}

type fakeWindowsServiceReader struct {
	status   taskquery.Status
	err      error
	contexts []context.Context
}

func (f *fakeWindowsServiceReader) Query(ctx context.Context) (taskquery.Status, error) {
	f.contexts = append(f.contexts, ctx)
	return f.status, f.err
}

func serviceCLIBool(value bool) *bool { return &value }

func executeServiceCLI(management userServiceRuntime, ctx context.Context, output io.Writer, args ...string) error {
	command := newServiceCommandForRuntime(management)
	command.SetContext(ctx)
	command.SetOut(output)
	command.SetErr(io.Discard)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	return command.Execute()
}

func assertServiceCLIJSON(t *testing.T, output []byte, status interface{}) {
	t.Helper()
	var got, want interface{}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("invalid JSON status %q: %v", output, err)
	}
	if err := json.Unmarshal(encoded, &want); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("platform status was wrapped, renamed or dropped: got=%s want=%s", output, encoded)
	}
}

func TestServiceCLISelectsOnePlatformAndPassesEveryAction(t *testing.T) {
	config := service.Config{Binary: "/fixture/nb1 program", Devices: []string{"20005601", "30000001"}, Listen: "127.0.0.1:8932", LockDir: "/fixture/locks"}
	install := []string{"install", "--binary", config.Binary, "--devices", strings.Join(config.Devices, ","), "--listen", config.Listen, "--lock-dir", config.LockDir}
	for _, goos := range []string{"darwin", "linux"} {
		for _, test := range []struct {
			args []string
			want userServiceAction
		}{
			{install, userServiceAction{kind: serviceInstall, config: config}},
			{append(append([]string{}, install...), "--start"), userServiceAction{kind: serviceInstall, config: config, start: true}},
			{[]string{"status"}, userServiceAction{kind: serviceStatus}},
			{[]string{"status", "--config-only"}, userServiceAction{kind: serviceStatus, configOnly: true}},
			{[]string{"start"}, userServiceAction{kind: serviceStart}},
			{[]string{"stop"}, userServiceAction{kind: serviceStop}},
			{[]string{"uninstall"}, userServiceAction{kind: serviceUninstall}},
			{[]string{"uninstall", "--stop"}, userServiceAction{kind: serviceUninstall, stop: true}},
		} {
			t.Run(goos+"/"+strings.Join(test.args, " "), func(t *testing.T) {
				fixture := serviceCLITestFixture(t, goos)
				ctx := context.WithValue(context.Background(), serviceCLIContextKey{}, "caller")
				var output bytes.Buffer
				err := executeServiceCLI(fixture.management, ctx, &output, append(append([]string{}, test.args...), "--json")...)
				if err != nil || !reflect.DeepEqual(fixture.created, map[string]int{goos: 1}) {
					t.Fatalf("platform dispatch: created=%v err=%v", fixture.created, err)
				}
				calls, contexts := fixture.mac.calls, fixture.mac.contexts
				if goos == "darwin" {
					assertServiceCLIJSON(t, output.Bytes(), fixture.mac.status)
					if len(fixture.linux.calls) != 0 {
						t.Fatal("macOS command reached Linux manager")
					}
				} else {
					calls, contexts = fixture.linux.calls, fixture.linux.contexts
					assertServiceCLIJSON(t, output.Bytes(), fixture.linux.status)
					if len(fixture.mac.calls) != 0 {
						t.Fatal("Linux command reached macOS manager")
					}
				}
				if !reflect.DeepEqual(calls, []userServiceAction{test.want}) || len(contexts) != 1 || contexts[0] != ctx {
					t.Fatalf("action or caller context changed: %+v", calls)
				}
				if len(fixture.windows.contexts) != 0 {
					t.Fatal("native manager reached Windows query")
				}
			})
		}
	}
}

func TestServiceCLIInstallDefaultsToRunningExecutable(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// A different nb1 on PATH must not select the persisted program.
			binDir := t.TempDir()
			for _, name := range []string{"nb1", "nb1.exe"} {
				if err := os.WriteFile(filepath.Join(binDir, name), []byte("not the running executable"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", binDir)
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output,
				"install", "--devices", "20000001", "--json"); err != nil {
				t.Fatal(err)
			}
			calls := fixture.mac.calls
			if goos == "linux" {
				calls = fixture.linux.calls
			}
			if len(calls) != 1 || calls[0].kind != serviceInstall || calls[0].config.Binary != binary ||
				!filepath.IsAbs(calls[0].config.Binary) || calls[0].start ||
				!reflect.DeepEqual(calls[0].config.Devices, []string{"20000001"}) ||
				!reflect.DeepEqual(fixture.created, map[string]int{goos: 1}) {
				t.Fatalf("default executable or install behavior changed: %+v", calls)
			}
		})
	}
}

func TestServiceCLIInstallExecutableOverrideAndResolverFailure(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos+"/explicit-override", func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			fixture.management.executable = func() (string, error) {
				t.Fatal("explicit --binary unexpectedly resolved the running executable")
				return "", nil
			}
			selected := filepath.Join(t.TempDir(), "chosen nb1")
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output,
				"install", "--binary", selected, "--devices", "20000001", "--start"); err != nil {
				t.Fatal(err)
			}
			calls := fixture.mac.calls
			if goos == "linux" {
				calls = fixture.linux.calls
			}
			if len(calls) != 1 || calls[0].config.Binary != selected || !calls[0].start {
				t.Fatalf("explicit path or immediate-start flag lost: %+v", calls)
			}
		})
		for _, resolution := range []string{"error", "empty", "relative", "explicit-empty"} {
			t.Run(goos+"/"+resolution, func(t *testing.T) {
				fixture := serviceCLITestFixture(t, goos)
				failure := errors.New("running executable unavailable")
				resolved := 0
				fixture.management.executable = func() (string, error) {
					resolved++
					if resolution == "error" {
						return "", failure
					}
					if resolution == "relative" {
						return "nb1", nil
					}
					return "", nil
				}
				args := []string{"install", "--devices", "20000001"}
				if resolution == "explicit-empty" {
					args = append(args, "--binary", "")
				}
				var output bytes.Buffer
				err := executeServiceCLI(fixture.management, context.Background(), &output, args...)
				if err == nil || !strings.Contains(err.Error(), "--binary") || len(fixture.created) != 0 || output.Len() != 0 {
					t.Fatalf("bad path reached a service manager: %v %v", fixture.created, err)
				}
				wantResolutions := 1
				if resolution == "explicit-empty" {
					wantResolutions = 0
				}
				if resolved != wantResolutions || resolution == "error" && !errors.Is(err, failure) {
					t.Fatalf("unexpected resolver use or lost cause: %d %v", resolved, err)
				}
			})
		}
	}
}

func TestServiceCLIExecutableResolutionOnlyForSupportedInstall(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd"} {
		for _, args := range [][]string{
			{"status"}, {"start"}, {"stop"}, {"uninstall"},
			{"install"}, // The required devices flag fails before resolving.
			{"render", "--platform", "linux", "--devices", "20000001"}, // Target binary remains required.
			{"install", "--help"},
		} {
			t.Run(goos+"/"+strings.Join(args, " "), func(t *testing.T) {
				fixture := serviceCLITestFixture(t, goos)
				fixture.management.executable = func() (string, error) {
					t.Fatal("non-install or invalid request resolved an executable")
					return "", nil
				}
				var output bytes.Buffer
				_ = executeServiceCLI(fixture.management, context.Background(), &output, args...)
			})
		}
	}
	for _, goos := range []string{"windows", "freebsd"} {
		t.Run(goos+"/unsupported-install", func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			fixture.management.executable = func() (string, error) {
				t.Fatal("unsupported install probed the current executable")
				return "", nil
			}
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, context.Background(), &output, "install", "--devices", "20000001")
			if !errors.Is(err, errUserServiceUnsupported) || len(fixture.created) != 0 || output.Len() != 0 {
				t.Fatalf("unsupported install reached a manager: %v %v", fixture.created, err)
			}
		})
	}
}

func TestWindowsServiceStatusUsesOnlyReadOnlyFactory(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmtServiceJSON(asJSON), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			ctx := context.WithValue(context.Background(), serviceCLIContextKey{}, "windows caller")
			args := []string{"status"}
			if asJSON {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, ctx, &output, args...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(fixture.created, map[string]int{"windows": 1}) || len(fixture.windows.contexts) != 1 || fixture.windows.contexts[0] != ctx || len(fixture.mac.calls)+len(fixture.linux.calls) != 0 {
				t.Fatal("wrong platform, ctx, or retry", fixture.created)
			}
			if asJSON {
				assertServiceCLIJSON(t, output.Bytes(), fixture.windows.status)
			} else {
				for _, part := range []string{"Windows read-only task query (candidate)", "registration presence: present", "ownership: not_checked", "managed ownership not verified", "Scheduler state: ready (3)", "last task result: 0", "does not establish a daemon PID, HTTP availability or USB readiness", "management and a reliable stop protocol are not implemented", "Native ordinary-user query validation remains pending"} {
					if !strings.Contains(output.String(), part) {
						t.Fatal("missing Windows boundary", part, output.String())
					}
				}
			}
		})
	}
}

func fmtServiceJSON(value bool) string {
	if value {
		return "json"
	}
	return "text"
}

func TestWindowsUnsupportedOperationsNeverConstructQuery(t *testing.T) {
	for _, args := range [][]string{
		{"install", "--binary", `C:\fixture\nb1.exe`, "--devices", "20005601"},
		{"install", "--binary", `C:\fixture\nb1.exe`, "--devices", "20005601", "--start", "--json"},
		{"start"}, {"start", "--json"}, {"stop"}, {"stop", "--json"},
		{"uninstall"}, {"uninstall", "--stop", "--json"},
		{"status", "--config-only"}, {"status", "--config-only", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, context.Background(), &output, args...)
			if !errors.Is(err, errWindowsServiceUnsupported) || len(fixture.created) != 0 || len(fixture.windows.contexts) != 0 ||
				len(fixture.mac.calls)+len(fixture.linux.calls) != 0 || output.Len() != 0 {
				t.Fatalf("unsupported Windows action reached a backend: %v %v", fixture.created, err)
			}
		})
	}
}

func TestWindowsStatusCannotChooseForeignTaskIdentityOrHost(t *testing.T) {
	for _, args := range [][]string{
		{"status", "other-task"}, {"status", "--user", "other-user"},
		{"status", "--sid", "S-1-5-18"}, {"status", "--task", `\foreign-task`},
		{"status", "--server", "remote-host"}, {"status", "--platform", "linux"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, context.Background(), &output, args...)
			if err == nil || len(fixture.created) != 0 || len(fixture.windows.contexts) != 0 ||
				len(fixture.mac.calls)+len(fixture.linux.calls) != 0 {
				t.Fatal("unscoped task query reached a backend", err)
			}
		})
	}
}

func TestWindowsQueryRefusalsNeverFallbackAndPreserveUnknown(t *testing.T) {
	for _, refusal := range []error{taskquery.ErrIdentity, taskquery.ErrTrust} {
		t.Run(refusal.Error(), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			fixture.management.factories.windows = func() (windowsServiceReader, error) { fixture.created["windows"]++; return nil, refusal }
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, context.Background(), &output, "status", "--json")
			if !errors.Is(err, refusal) || output.Len() != 0 || !reflect.DeepEqual(fixture.created, map[string]int{"windows": 1}) || len(fixture.windows.contexts) != 0 || len(fixture.mac.calls)+len(fixture.linux.calls) != 0 {
				t.Fatal("constructor refusal fell back or invented status", err)
			}
		})
	}
	for _, asJSON := range []bool{false, true} {
		t.Run(fmtServiceJSON(asJSON), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			fixture.windows.status.Presence = taskquery.Unknown
			fixture.windows.status.Task = nil
			fixture.windows.status.Failure = &taskquery.Failure{Kind: "not_found_unverified", Stage: "get_task", HRESULT: "0x80070002"}
			fixture.windows.err = taskquery.ErrAbsenceUnverified
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			args := []string{"status"}
			if asJSON {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, ctx, &output, args...)
			if !errors.Is(err, taskquery.ErrAbsenceUnverified) || len(fixture.windows.contexts) != 1 || fixture.windows.contexts[0] != ctx || fixture.windows.contexts[0].Err() != context.Canceled {
				t.Fatal("query error/caller context lost", err)
			}
			if asJSON {
				assertServiceCLIJSON(t, output.Bytes(), fixture.windows.status)
			} else {
				for _, part := range []string{"registration presence: unknown", "does not establish that the task is absent", "not_found_unverified", "HRESULT=0x80070002"} {
					if !strings.Contains(output.String(), part) {
						t.Fatal("unknown became absence", output.String())
					}
				}
			}
			if !reflect.DeepEqual(fixture.created, map[string]int{"windows": 1}) || len(fixture.mac.calls)+len(fixture.linux.calls) != 0 {
				t.Fatal("query error retried or fell back")
			}
		})
	}
}

func TestWindowsQueryOutputFailureAndHelpDoNotAddActions(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		fixture := serviceCLITestFixture(t, "windows")
		failure := errors.New("fake Windows output failure")
		args := []string{"status"}
		if asJSON {
			args = append(args, "--json")
		}
		err := executeServiceCLI(fixture.management, context.Background(), failingServiceCLIWriter{failure}, args...)
		if !errors.Is(err, failure) || len(fixture.windows.contexts) != 1 || !reflect.DeepEqual(fixture.created, map[string]int{"windows": 1}) {
			t.Fatal("output failure retried/swallowed", err)
		}
	}
	fixture := serviceCLITestFixture(t, "windows")
	var output bytes.Buffer
	if err := executeServiceCLI(fixture.management, context.Background(), &output, "status", "--help"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.created) != 0 || !strings.Contains(output.String(), "Windows does not support --config-only") || !strings.Contains(output.String(), "native ordinary-user query validation remains pending") {
		t.Fatal("help lost Windows limits or constructed reader", output.String())
	}
}

func TestServiceCLIUnsupportedAndInvalidArgumentsNeverConstructManagers(t *testing.T) {
	for _, goos := range []string{"windows", "freebsd", "plan9"} {
		for _, args := range [][]string{{"install", "--binary", "/fixture/nb1", "--devices", "20005601"}, {"status", "--config-only"}, {"start"}, {"stop"}, {"uninstall", "--stop"}} {
			t.Run(goos+"/"+args[0], func(t *testing.T) {
				fixture := serviceCLITestFixture(t, goos)
				var output bytes.Buffer
				err := executeServiceCLI(fixture.management, context.Background(), &output, args...)
				if !errors.Is(err, errUserServiceUnsupported) || len(fixture.created) != 0 || output.Len() != 0 {
					t.Fatalf("unsupported platform reached a manager: created=%v output=%q err=%v", fixture.created, output.String(), err)
				}
			})
		}
	}
	for _, args := range [][]string{{"install"}, {"start", "extra"}, {"stop", "extra"}, {"status", "extra"}, {"uninstall", "extra"}, {"start", "--platform", "linux"}, {"install", "--binary", "/fixture/nb1", "--devices", "20005601", "--platform", "linux"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "darwin")
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output, args...); err == nil || len(fixture.created) != 0 {
				t.Fatalf("invalid arguments or platform override reached manager: %v %v", fixture.created, err)
			}
		})
	}
}

func TestServiceCLIRenderRemainsCrossPlatformWithoutManager(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			fixture := serviceCLITestFixture(t, "windows")
			binary := "/absolute/nb1"
			if platform == "windows" {
				binary = `C:\Program Files\nb1\nb1.exe`
			}
			var output bytes.Buffer
			err := executeServiceCLI(fixture.management, context.Background(), &output, "render", "--platform", platform, "--binary", binary, "--devices", "20005601")
			artifact, renderErr := service.Render(service.Config{Platform: platform, Binary: binary, Devices: []string{"20005601"}, Listen: "127.0.0.1:8931"})
			if err != nil || renderErr != nil || !bytes.Equal(output.Bytes(), artifact.Content) || len(fixture.created) != 0 {
				t.Fatalf("render constructed native manager or changed artifact: created=%v err=%v %v", fixture.created, err, renderErr)
			}
		})
	}
}

func TestServiceCLIFactoryAndActionErrorsPreserveStatus(t *testing.T) {
	failure := errors.New("fake user manager unavailable")
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos+"/constructor", func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			if goos == "darwin" {
				fixture.management.factories.darwin = func() (userServiceBackend[service.UserStatus], error) { return nil, failure }
			} else {
				fixture.management.factories.linux = func() (userServiceBackend[systemduser.Status], error) { return nil, failure }
			}
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output, "status", "--json"); !errors.Is(err, failure) || output.Len() != 0 || len(fixture.mac.calls)+len(fixture.linux.calls) != 0 {
				t.Fatalf("constructor failure invoked action or invented status: %q %v", output.String(), err)
			}
		})
		t.Run(goos+"/action", func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			fixture.mac.err, fixture.linux.err = failure, failure
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, ctx, &output, "stop", "--json"); !errors.Is(err, failure) {
				t.Fatalf("action failure lost: %v", err)
			}
			if goos == "darwin" {
				assertServiceCLIJSON(t, output.Bytes(), fixture.mac.status)
				if len(fixture.mac.calls) != 1 || fixture.mac.contexts[0].Err() != context.Canceled {
					t.Fatal("failed action retried or caller cancellation lost")
				}
			} else {
				assertServiceCLIJSON(t, output.Bytes(), fixture.linux.status)
				if len(fixture.linux.calls) != 1 || fixture.linux.contexts[0].Err() != context.Canceled {
					t.Fatal("failed action retried or caller cancellation lost")
				}
			}
		})
	}
}

type failingServiceCLIWriter struct{ err error }

func (f failingServiceCLIWriter) Write([]byte) (int, error) { return 0, f.err }

func TestServiceCLIOutputErrorsAreReturnedWithoutRetry(t *testing.T) {
	failure := errors.New("fake output failure")
	for _, goos := range []string{"darwin", "linux"} {
		for _, format := range [][]string{{"status"}, {"status", "--json"}} {
			t.Run(goos+"/"+strings.Join(format, " "), func(t *testing.T) {
				fixture := serviceCLITestFixture(t, goos)
				err := executeServiceCLI(fixture.management, context.Background(), failingServiceCLIWriter{failure}, format...)
				if !errors.Is(err, failure) || len(fixture.mac.calls)+len(fixture.linux.calls) != 1 {
					t.Fatalf("output failure swallowed or action retried: %v", err)
				}
			})
		}
	}
}

func TestServiceCLITextPreservesMacAndSeparatesLinuxStates(t *testing.T) {
	fixture := serviceCLITestFixture(t, "darwin")
	var got, want bytes.Buffer
	if err := executeServiceCLI(fixture.management, context.Background(), &got, "status"); err != nil {
		t.Fatal(err)
	}
	if err := service.WriteUserStatus(&want, fixture.mac.status); err != nil || got.String() != want.String() {
		t.Fatalf("macOS text changed: %s %v", got.String(), err)
	}
	for _, configOnly := range []bool{false, true} {
		name := "manager"
		if configOnly {
			name = "config-only"
		}
		t.Run(name, func(t *testing.T) {
			linux := serviceCLITestFixture(t, "linux")
			args := []string{"status"}
			if configOnly {
				linux.linux.status.Properties = nil
				linux.linux.status.Registration = "not_checked"
				linux.linux.status.RegistrationOwned = false
				linux.linux.status.ManagedEnabledForLogin = nil
				args = append(args, "--config-only")
			}
			var output bytes.Buffer
			if err := executeServiceCLI(linux.management, context.Background(), &output, args...); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			for _, field := range []string{"manager registration: ", "ownership verified=", "persistent enablement link: ", "Managed login enablement: ", "active_state", "unit_file_state", "do not establish HTTP availability or USB readiness", "stopping does not establish that hardware cleanup is complete"} {
				if !strings.Contains(text, field) {
					t.Fatalf("Linux text dropped %q: %s", field, text)
				}
			}
			if configOnly {
				if !strings.Contains(text, "not checked or unconfirmed") || strings.Contains(text, "Managed login enablement: false") || strings.Contains(text, "active_state: inactive") {
					t.Fatalf("unchecked state invented absence/inactivity: %s", text)
				}
			} else if !strings.Contains(text, "manager registration: loaded") || !strings.Contains(text, "active_state: inactive") || !strings.Contains(text, "unit_file_state: enabled") || !strings.Contains(text, "NoNewPrivileges: true") {
				t.Fatalf("loaded/inactive/enabled were conflated: %s", text)
			}
		})
	}
}

func TestServiceCLIDefaultMessagesUseEnglish(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			fixture := serviceCLITestFixture(t, platform)
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output, "status"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "nb1 ") || strings.ContainsFunc(output.String(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Fatalf("default %s status is not English: %s", platform, output.String())
			}
		})
	}
	for _, err := range []error{errUserServiceUnsupported, errWindowsServiceUnsupported} {
		if strings.ContainsFunc(err.Error(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Fatalf("default unsupported message is not English: %v", err)
		}
	}
}
