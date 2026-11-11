//go:build linux || darwin
// +build linux darwin

package systemduser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/service"
)

// This fake models selected machine properties and reload/activation effects.
// It never starts a process or invokes a real manager, daemon or USB API.
type fakeSystemd struct {
	t               *testing.T
	m               *Manager
	p               Properties
	active          string
	calls           []string
	argv            [][]string
	hook            func(string)
	failAction      string
	failAt          int
	counts          map[string]int
	queryOverride   *Result
	externalEnabled bool
	dropIns         string
	blockShow       bool
}

func absent() Properties {
	return Properties{ID: UnitName, LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
}

func propertyText(p Properties) string {
	job := ""
	if p.Job != 0 {
		job = strconv.FormatUint(uint64(p.Job), 10)
	}
	boolean := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	security := ""
	if p.NoNewPrivileges != nil {
		security = "NoNewPrivileges=" + boolean(*p.NoNewPrivileges) + "\n"
	}
	return fmt.Sprintf("Id=%s\nLoadState=%s\nActiveState=%s\nSubState=%s\nFragmentPath=%s\nSourcePath=%s\nDropInPaths=%s\nTransient=%s\nNeedDaemonReload=%s\nUnitFileState=%s\nJob=%s\n", p.ID, p.LoadState, p.ActiveState, p.SubState, p.FragmentPath, p.SourcePath, p.DropInPaths, boolean(p.Transient), boolean(p.NeedDaemonReload), p.UnitFileState, job) + security
}

func boolPointer(value bool) *bool { return &value }

func (f *fakeSystemd) refresh() {
	if _, err := os.Lstat(f.m.unitPath()); errors.Is(err, os.ErrNotExist) {
		f.p = absent()
		return
	}
	state := "disabled"
	if _, err := os.Lstat(f.m.linkPath()); err == nil || f.externalEnabled {
		state = "enabled"
	}
	active := f.active
	if active == "" {
		active = "inactive"
	}
	sub := "dead"
	if active == "active" {
		sub = "running"
	}
	if active == "failed" {
		sub = "failed"
	}
	f.p = Properties{ID: UnitName, LoadState: "loaded", ActiveState: active, SubState: sub, FragmentPath: f.m.unitPath(), UnitFileState: state, DropInPaths: f.dropIns, NoNewPrivileges: boolPointer(true)}
}

func (f *fakeSystemd) Run(ctx context.Context, args ...string) (Result, error) {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	action := ""
	if reflect.DeepEqual(args, showArgs()) {
		action = "show"
	} else {
		for _, candidate := range []string{"daemon-reload", "start", "stop"} {
			if reflect.DeepEqual(args, commandArgs(candidate)) {
				action = candidate
			}
		}
	}
	if action == "" {
		f.t.Fatalf("unexpected/native/foreign command argv: %q", args)
	}
	f.calls = append(f.calls, action)
	f.argv = append(f.argv, append([]string{}, args...))
	f.counts[action]++
	if f.hook != nil {
		f.hook(action)
	}
	if action == "show" && f.blockShow {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}
	if action == f.failAction && (f.failAt == 0 || f.counts[action] == f.failAt) {
		return Result{ExitCode: 1, Output: "PRIVATE_ENV_SECRET"}, nil
	}
	switch action {
	case "show":
		if f.queryOverride != nil {
			return *f.queryOverride, nil
		}
		return Result{Output: propertyText(f.p)}, nil
	case "daemon-reload":
		f.refresh()
	case "start":
		f.active = "active"
		f.refresh()
	case "stop":
		f.active = "inactive"
		f.refresh()
	}
	return Result{}, nil
}

func fixture(t *testing.T) (*Manager, *fakeSystemd, service.Config) {
	t.Helper()
	f := &fakeSystemd{t: t, p: absent(), counts: make(map[string]int)}
	m, err := newManager(t.TempDir(), os.Geteuid(), f)
	if err != nil {
		t.Fatal(err)
	}
	f.m = m
	binary := filepath.Join(m.configRoot, "nb1 fixture & literal $%")
	if err := os.WriteFile(binary, []byte("fixture never executed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, f, service.Config{Platform: "linux", Binary: binary, Devices: []string{"20005601"}, Listen: "127.0.0.1:8931"}
}

func putUnit(t *testing.T, m *Manager, f *fakeSystemd, c service.Config, enabled bool, active string) []byte {
	t.Helper()
	if err := m.directory(m.unitDir(), true); err != nil {
		t.Fatal(err)
	}
	content, err := unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: c})
	if err != nil {
		t.Fatal(err)
	}
	if err := publish(m.unitPath(), content); err != nil {
		t.Fatal(err)
	}
	if enabled {
		if err := m.directory(filepath.Dir(m.linkPath()), true); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(m.unitPath(), m.linkPath()); err != nil {
			t.Fatal(err)
		}
	}
	f.active = active
	f.refresh()
	return content
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file changed/missing %s: %v", path, err)
	}
}
func countAction(f *fakeSystemd, action string) int {
	n := 0
	for _, value := range f.calls {
		if value == action {
			n++
		}
	}
	return n
}

func TestInstallEnablesLoginAndSeparatesImmediateStart(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(fmt.Sprint(start), func(t *testing.T) {
			m, f, c := fixture(t)
			status, err := m.Install(context.Background(), c, start)
			if err != nil || status.ConfigState != "managed" || status.EnableLinkState != "owned" || !*status.ManagedEnabledForLogin || !status.RegistrationOwned || status.Registration != "loaded" {
				t.Fatalf("install %+v %v", status, err)
			}
			want := "inactive"
			if start {
				want = "active"
			}
			if status.Properties.ActiveState != want || countAction(f, "start") != map[bool]int{false: 0, true: 1}[start] || countAction(f, "daemon-reload") != 2 {
				t.Fatalf("enable/start conflated: %+v calls=%v", status, f.calls)
			}
			if mode, err := os.Stat(m.unitPath()); err != nil || mode.Mode().Perm() != 0600 {
				t.Fatalf("unit mode: %v %v", mode, err)
			}
			before, _ := os.Stat(m.unitPath())
			status, err = m.Install(context.Background(), c, start)
			after, _ := os.Stat(m.unitPath())
			if err != nil || len(status.Actions) != 0 || !os.SameFile(before, after) {
				t.Fatalf("identical install rewrote/restarted: %+v %v", status, err)
			}
		})
	}
}

func TestStatusConfigOnlyHasNoWritesCommandsOrReadinessClaim(t *testing.T) {
	m, f, c := fixture(t)
	before, _ := os.ReadDir(m.configRoot)
	status, err := m.Status(context.Background(), true)
	after, _ := os.ReadDir(m.configRoot)
	if err != nil || status.ConfigState != "absent" || status.Registration != "not_checked" || status.Properties != nil || len(f.calls) != 0 || len(before) != len(after) {
		t.Fatalf("config-only side effects: %+v %v", status, err)
	}
	putUnit(t, m, f, c, true, "inactive")
	status, err = m.Status(context.Background(), false)
	if err != nil || !status.RegistrationOwned || status.Properties.ActiveState != "inactive" || status.Properties.UnitFileState != "enabled" {
		t.Fatalf("loaded/active/enable conflated: %+v %v", status, err)
	}
}

func TestStartStopPreserveLoginConfiguration(t *testing.T) {
	m, f, c := fixture(t)
	content := putUnit(t, m, f, c, true, "inactive")
	started, err := m.Start(context.Background())
	if err != nil || started.Properties.ActiveState != "active" {
		t.Fatalf("start %+v %v", started, err)
	}
	again, err := m.Start(context.Background())
	if err != nil || len(again.Actions) != 0 || countAction(f, "start") != 1 {
		t.Fatalf("idempotent start %+v %v", again, err)
	}
	stopped, err := m.Stop(context.Background())
	if err != nil || stopped.Properties.ActiveState != "inactive" || stopped.EnableLinkState != "owned" || !*stopped.ManagedEnabledForLogin {
		t.Fatalf("stop changed login enablement: %+v %v", stopped, err)
	}
	assertFile(t, m.unitPath(), content)
	if target, err := os.Readlink(m.linkPath()); err != nil || target != m.unitPath() {
		t.Fatal("Stop deleted enable link")
	}
	again, err = m.Stop(context.Background())
	if err != nil || len(again.Actions) != 0 || countAction(f, "stop") != 1 {
		t.Fatalf("idempotent stop %+v %v", again, err)
	}
}

func TestUninstallLoadedInactiveDoesNotRequireStop(t *testing.T) {
	for _, active := range []string{"inactive", "failed"} {
		t.Run(active, func(t *testing.T) {
			m, f, c := fixture(t)
			putUnit(t, m, f, c, true, active)
			status, err := m.Uninstall(context.Background(), false)
			if err != nil || status.ConfigState != "absent" || status.Registration != "absent" || status.EnableLinkState != "absent" || countAction(f, "stop") != 0 || countAction(f, "daemon-reload") != 2 {
				t.Fatalf("inactive loaded uninstall: %+v %v calls=%v", status, err, f.calls)
			}
			status, err = m.Uninstall(context.Background(), true)
			if err != nil || len(status.Actions) != 0 {
				t.Fatalf("idempotent uninstall: %+v %v", status, err)
			}
		})
	}
}

func TestUninstallActiveRequiresExplicitStop(t *testing.T) {
	m, f, c := fixture(t)
	content := putUnit(t, m, f, c, true, "active")
	if _, err := m.Uninstall(context.Background(), false); err == nil || countAction(f, "stop") != 0 {
		t.Fatal("active service was stopped without authorization")
	}
	assertFile(t, m.unitPath(), content)
	status, err := m.Uninstall(context.Background(), true)
	if err != nil || status.Registration != "absent" || countAction(f, "stop") != 1 {
		t.Fatalf("authorized uninstall: %+v %v", status, err)
	}
}

func TestExternalUnitOrUnknownOverrideIsNotManaged(t *testing.T) {
	for _, variant := range []string{"foreign_fragment", "dropins", "transient", "reload_pending", "runtime_enable", "unknown_load", "unknown_active", "queued_job", "privilege_gain_allowed", "unknown_privilege_policy"} {
		t.Run(variant, func(t *testing.T) {
			m, f, c := fixture(t)
			content := putUnit(t, m, f, c, true, "active")
			switch variant {
			case "foreign_fragment":
				f.p.FragmentPath = "/foreign/nb1.service"
			case "dropins":
				f.p.DropInPaths = "/etc/systemd/user/service.d/override.conf"
			case "transient":
				f.p.Transient = true
			case "reload_pending":
				f.p.NeedDaemonReload = true
			case "runtime_enable":
				f.p.UnitFileState = "enabled-runtime"
			case "unknown_load":
				f.p.LoadState = "masked"
			case "unknown_active":
				f.p.ActiveState = "future-state"
			case "queued_job":
				f.p.Job = 42
			case "privilege_gain_allowed":
				f.p.NoNewPrivileges = boolPointer(false)
			case "unknown_privilege_policy":
				f.p.NoNewPrivileges = nil
			}
			if _, err := m.Stop(context.Background()); err == nil {
				t.Fatal("unknown state accepted stop")
			}
			if _, err := m.Uninstall(context.Background(), true); err == nil {
				t.Fatal("unknown state accepted uninstall")
			}
			if countAction(f, "stop") != 0 || countAction(f, "start") != 0 || countAction(f, "daemon-reload") != 0 {
				t.Fatalf("unknown unit was changed: %v", f.calls)
			}
			assertFile(t, m.unitPath(), content)
		})
	}
	m, f, c := fixture(t)
	f.p = Properties{ID: UnitName, LoadState: "loaded", ActiveState: "inactive", SubState: "dead", FragmentPath: "/usr/lib/systemd/user/nb1.service", UnitFileState: "disabled"}
	if _, err := m.Install(context.Background(), c, false); err == nil {
		t.Fatal("files-only install shadowed an external unit")
	}
	if _, err := os.Lstat(m.unitPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("external unit caused installed collision")
	}
}

func TestUnknownUnitFilesAndSymlinksAreNeverOverwrittenOrDeleted(t *testing.T) {
	for _, variant := range []string{"plain_unit", "edited_unit", "unit_symlink", "wrong_link", "regular_link", "dropin_directory", "unsafe_directory"} {
		t.Run(variant, func(t *testing.T) {
			m, f, c := fixture(t)
			content := putUnit(t, m, f, c, true, "inactive")
			switch variant {
			case "plain_unit":
				content = []byte("[Service]\nExecStart=/foreign/program\n")
				os.WriteFile(m.unitPath(), content, 0600)
			case "edited_unit":
				content = append(content, []byte("# foreign edit\n")...)
				os.WriteFile(m.unitPath(), content, 0600)
			case "unit_symlink":
				os.Remove(m.unitPath())
				os.Symlink(c.Binary, m.unitPath())
			case "wrong_link":
				os.Remove(m.linkPath())
				os.Symlink("/foreign/unit", m.linkPath())
			case "regular_link":
				os.Remove(m.linkPath())
				os.WriteFile(m.linkPath(), []byte("unknown file"), 0600)
			case "dropin_directory":
				os.Mkdir(m.unitPath()+".d", 0700)
			case "unsafe_directory":
				os.Chmod(m.unitDir(), 0777)
			}
			for _, operation := range []string{"install", "stop", "uninstall"} {
				var err error
				switch operation {
				case "install":
					_, err = m.Install(context.Background(), c, true)
				case "stop":
					_, err = m.Stop(context.Background())
				case "uninstall":
					_, err = m.Uninstall(context.Background(), true)
				}
				if err == nil {
					t.Fatalf("%s accepted unknown %s", operation, variant)
				}
			}
			if len(f.calls) != 0 {
				t.Fatalf("unknown disk state reached systemctl: %v", f.calls)
			}
			if variant == "plain_unit" || variant == "edited_unit" {
				assertFile(t, m.unitPath(), content)
			}
		})
	}
}

func TestExternalEnableLinkIsPreservedAndBlocksUnitRemoval(t *testing.T) {
	m, f, c := fixture(t)
	content := putUnit(t, m, f, c, true, "inactive")
	other := filepath.Join(m.unitDir(), "other.target.wants")
	os.Mkdir(other, 0700)
	otherLink := filepath.Join(other, UnitName)
	os.Symlink(m.unitPath(), otherLink)
	f.externalEnabled = true
	f.refresh()
	status, err := m.Uninstall(context.Background(), false)
	if err == nil || status.ConfigState != "managed" || countAction(f, "stop") != 0 {
		t.Fatalf("unknown enable links were treated as ours: %+v %v", status, err)
	}
	assertFile(t, m.unitPath(), content)
	if target, err := os.Readlink(otherLink); err != nil || target != m.unitPath() {
		t.Fatal("unknown enable link was removed")
	}
	if _, err := os.Lstat(m.linkPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("own link was not removed before enable-state check")
	}
}

func TestPartialReloadAndControlFailuresKeepFiles(t *testing.T) {
	for _, reloadNumber := range []int{1, 2} {
		t.Run(fmt.Sprint(reloadNumber), func(t *testing.T) {
			m, f, c := fixture(t)
			f.failAction = "daemon-reload"
			f.failAt = reloadNumber
			status, err := m.Install(context.Background(), c, true)
			if err == nil || status.ConfigState != "managed" || status.Registration != "unknown" || countAction(f, "start") != 0 {
				t.Fatalf("reload failure hidden/started anyway: %+v %v", status, err)
			}
			if _, err := os.Stat(m.unitPath()); err != nil {
				t.Fatal("uncertain install rolled back unit")
			}
			if reloadNumber == 1 {
				if _, err := os.Lstat(m.linkPath()); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("persistent enablement preceded provenance check")
				}
			}
		})
	}
	for _, action := range []string{"start", "stop"} {
		t.Run(action, func(t *testing.T) {
			m, f, c := fixture(t)
			active := "inactive"
			if action == "stop" {
				active = "active"
			}
			content := putUnit(t, m, f, c, true, active)
			f.failAction = action
			var status Status
			var err error
			if action == "start" {
				status, err = m.Start(context.Background())
			} else {
				status, err = m.Stop(context.Background())
			}
			if err == nil || status.Registration != "unknown" {
				t.Fatalf("uncertain command outcome hidden: %+v %v", status, err)
			}
			assertFile(t, m.unitPath(), content)
			encoded, _ := json.Marshal(status)
			if bytes.Contains(encoded, []byte("PRIVATE_ENV_SECRET")) || strings.Contains(err.Error(), "PRIVATE_ENV_SECRET") {
				t.Fatal("raw command output leaked")
			}
		})
	}
}

func TestObservedQueryTimeChangesPrecedeEveryControlOrDeletion(t *testing.T) {
	for _, operation := range []string{"start", "start_noop", "stop", "stop_noop", "uninstall_outer", "uninstall_inner"} {
		t.Run(operation, func(t *testing.T) {
			m, f, c := fixture(t)
			active := "inactive"
			if operation == "stop" || operation == "start_noop" || strings.HasPrefix(operation, "uninstall") {
				active = "active"
			}
			putUnit(t, m, f, c, true, active)
			changedConfig := c
			changedConfig.Listen = "127.0.0.1:8932"
			changed, _ := unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: changedConfig})
			showAt := 1
			if operation == "uninstall_inner" {
				showAt = 2
			}
			f.hook = func(action string) {
				if action == "show" && f.counts["show"] == showAt {
					if err := os.WriteFile(m.unitPath(), changed, 0600); err != nil {
						t.Fatal(err)
					}
					f.refresh()
				}
			}
			var err error
			switch {
			case strings.HasPrefix(operation, "start"):
				_, err = m.Start(context.Background())
			case strings.HasPrefix(operation, "stop"):
				_, err = m.Stop(context.Background())
			default:
				_, err = m.Uninstall(context.Background(), true)
			}
			if err == nil || countAction(f, "start") != 0 || countAction(f, "stop") != 0 || countAction(f, "daemon-reload") != 0 {
				t.Fatalf("query-time replacement acted on: %s err=%v calls=%v", operation, err, f.calls)
			}
			assertFile(t, m.unitPath(), changed)
		})
	}
}

func TestObservedLinkReplacementAndPostStopEditArePreserved(t *testing.T) {
	for _, variant := range []string{"link_replacement", "inode_replacement", "edit_during_stop"} {
		t.Run(variant, func(t *testing.T) {
			m, f, c := fixture(t)
			content := putUnit(t, m, f, c, true, "active")
			f.hook = func(action string) {
				if variant == "link_replacement" && action == "show" && f.counts[action] == 1 {
					os.Remove(m.linkPath())
					os.Symlink("/foreign/untouched", m.linkPath())
				}
				if variant == "inode_replacement" && action == "show" && f.counts[action] == 1 {
					replacement := m.unitPath() + ".replacement"
					os.WriteFile(replacement, content, 0600)
					os.Rename(replacement, m.unitPath())
				}
				if variant == "edit_during_stop" && action == "stop" {
					os.WriteFile(m.unitPath(), []byte("unknown changed unit"), 0600)
				}
			}
			if _, err := m.Uninstall(context.Background(), true); err == nil {
				t.Fatal("changed artifact was removed")
			}
			if _, err := os.Lstat(m.unitPath()); err != nil {
				t.Fatal("changed unit was deleted")
			}
			if variant != "edit_during_stop" && countAction(f, "stop") != 0 {
				t.Fatal("changed artifact was stopped")
			}
			if variant == "link_replacement" {
				if target, err := os.Readlink(m.linkPath()); err != nil || target != "/foreign/untouched" {
					t.Fatal("foreign symlink removed")
				}
			}
		})
	}
}

func TestUnavailableUserManagerInstallLeavesNoUnit(t *testing.T) {
	m, f, c := fixture(t)
	// A container without a user bus exits nonzero and prints no properties.
	f.queryOverride = &Result{ExitCode: 1}
	status, err := m.Install(context.Background(), c, true)
	if err == nil || !strings.Contains(err.Error(), "systemd user manager") || !strings.Contains(err.Error(), "nb1 server --devices SERIAL") {
		t.Fatalf("missing actionable user-manager diagnostic: %+v %v", status, err)
	}
	if status.Registration != "unknown" || status.RegistrationOwned || len(status.Actions) != 0 || !reflect.DeepEqual(f.calls, []string{"show"}) {
		t.Fatalf("unavailable manager triggered a change or false status: %+v calls=%v", status, f.calls)
	}
	for _, path := range []string{m.unitPath(), m.linkPath()} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unavailable manager left service configuration at %s: %v", path, err)
		}
	}
}

func TestQueryFailureNeverMeansAbsentAndBudgetIsRespected(t *testing.T) {
	for _, variant := range []string{"bus_error", "incomplete", "duplicate", "truncated", "unknown_key"} {
		t.Run(variant, func(t *testing.T) {
			m, f, c := fixture(t)
			content := putUnit(t, m, f, c, true, "inactive")
			result := Result{Output: propertyText(absent())}
			switch variant {
			case "bus_error":
				result.ExitCode = 1
			case "incomplete":
				result.Output = "LoadState=not-found\n"
			case "duplicate":
				result.Output += "Id=nb1.service\n"
			case "truncated":
				result.Truncated = true
			case "unknown_key":
				result.Output += "Environment=PRIVATE_ENV_SECRET\n"
			}
			f.queryOverride = &result
			status, err := m.Uninstall(context.Background(), true)
			if err == nil || status.Registration != "unknown" || countAction(f, "stop") != 0 {
				t.Fatalf("bad query treated as absent: %+v %v", status, err)
			}
			assertFile(t, m.unitPath(), content)
		})
	}
	m, f, _ := fixture(t)
	f.blockShow = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	status, err := m.Status(ctx, false)
	if err == nil || status.Registration != "unknown" || time.Since(start) > time.Second {
		t.Fatalf("unbounded/false absent query %+v %v", status, err)
	}
	m, f, _ = fixture(t)
	f.queryOverride = &Result{ExitCode: 4, Output: propertyText(absent())}
	status, err = m.Status(context.Background(), false)
	if err != nil || status.Registration != "absent" {
		t.Fatalf("valid machine not-found not recognized: %+v %v", status, err)
	}
}

func TestEarlyErrorsLeaveDiskNotCheckedAndManagementLockIsSeparate(t *testing.T) {
	m, f, c := fixture(t)
	content := putUnit(t, m, f, c, true, "inactive")
	bad := c
	bad.Binary += "-missing"
	status, err := m.Install(context.Background(), bad, false)
	if err == nil || status.ConfigState != "not_checked" || status.ManagedEnabledForLogin != nil {
		t.Fatalf("early binary error invented disk state: %+v %v", status, err)
	}
	lease, err := m.managementLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	status, err = m.Uninstall(context.Background(), true)
	if !errors.Is(err, ownership.ErrBusy) || status.ConfigState != "not_checked" || len(f.calls) != 0 {
		t.Fatalf("management exclusion failed: %+v %v", status, err)
	}
	assertFile(t, m.unitPath(), content)
	if _, err := os.Stat(filepath.Join(m.configRoot, "nb1", "device-locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("service management touched device ownership")
	}
}

func TestStartRechecksExecutableAndStopCanCleanUpMissingBinary(t *testing.T) {
	for _, variant := range []string{"missing", "symlink", "not_executable", "writable_by_others"} {
		t.Run(variant, func(t *testing.T) {
			m, f, c := fixture(t)
			content := putUnit(t, m, f, c, true, "inactive")
			switch variant {
			case "missing":
				if err := os.Remove(c.Binary); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := c.Binary + "-target"
				if err := os.Rename(c.Binary, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, c.Binary); err != nil {
					t.Fatal(err)
				}
			case "not_executable":
				if err := os.Chmod(c.Binary, 0600); err != nil {
					t.Fatal(err)
				}
			case "writable_by_others":
				if err := os.Chmod(c.Binary, 0777); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Start(context.Background()); err == nil || countAction(f, "start") != 0 {
				t.Fatal("changed executable was started")
			}
			assertFile(t, m.unitPath(), content)
			f.active = "active"
			f.refresh()
			status, err := m.Stop(context.Background())
			if err != nil || status.Properties.ActiveState != "inactive" || countAction(f, "stop") != 1 {
				t.Fatalf("missing/changed executable prevented cleanup: %+v %v", status, err)
			}
		})
	}
}
