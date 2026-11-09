package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/service"
	"github.com/b1ug/nb1/internal/service/systemduser"
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
	created    map[string]int
}

func serviceCLITestFixture(t *testing.T, goos string) *serviceCLIFixture {
	t.Helper()
	// Even Cobra's existing global config initialization sees a private home.
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	}}
	return fixture
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
			})
		}
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
			for _, field := range []string{"manager加载：", "归属已核对=", "持久启用链接：", "本工具登录启用：", "active_state", "unit_file_state", "不表示HTTP可用或USB在线", "停止也不证明硬件清理已完成"} {
				if !strings.Contains(text, field) {
					t.Fatalf("Linux text dropped %q: %s", field, text)
				}
			}
			if configOnly {
				if !strings.Contains(text, "未检查或无法确认") || strings.Contains(text, "本工具登录启用：false") || strings.Contains(text, "active_state：inactive") {
					t.Fatalf("unchecked state invented absence/inactivity: %s", text)
				}
			} else if !strings.Contains(text, "manager加载：loaded") || !strings.Contains(text, "active_state：inactive") || !strings.Contains(text, "unit_file_state：enabled") || !strings.Contains(text, "NoNewPrivileges：true") {
				t.Fatalf("loaded/inactive/enabled were conflated: %s", text)
			}
		})
	}
}
