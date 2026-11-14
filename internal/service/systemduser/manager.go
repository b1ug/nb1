package systemduser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/service"
	"github.com/b1ug/nb1/internal/userconfig"
)

type Manager struct {
	configRoot string
	uid        int
	runner     Runner
}

// New is Linux-only and never accepts arbitrary unit names, UIDs, manager
// addresses or executable paths. It performs no systemctl call or writes.
func New() (*Manager, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	uid := os.Geteuid()
	if uid <= 0 || os.Getuid() != uid {
		return nil, fmt.Errorf("use the current ordinary user; root, sudo and identity switching are unsupported")
	}
	configRoot, err := userconfig.RootDir()
	if err != nil {
		return nil, err
	}
	var executable string
	for _, path := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && info.Mode().Perm()&0022 == 0 && ownedBy(info, 0) {
			executable = resolved
			break
		}
	}
	if executable == "" {
		return nil, fmt.Errorf("a trusted system systemctl executable was not found; no service operation was performed")
	}
	return newManager(configRoot, uid, nativeRunner{binary: executable})
}

func newManager(configRoot string, uid int, runner Runner) (*Manager, error) {
	if !filepath.IsAbs(configRoot) || !utf8.ValidString(configRoot) || strings.IndexFunc(configRoot, unicode.IsControl) >= 0 || uid < 0 || runner == nil {
		return nil, fmt.Errorf("invalid current-user systemd context")
	}
	canonical, err := canonicalPath(configRoot)
	if err != nil {
		return nil, err
	}
	if err := safeAncestors(canonical, uid); err != nil {
		return nil, err
	}
	m := &Manager{configRoot: canonical, uid: uid, runner: runner}
	if err := m.directory(canonical, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m, nil
}

func (m *Manager) initialStatus() Status {
	return Status{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Platform: "linux", Label: UnitName, Target: "user/" + strconv.Itoa(m.uid) + "/" + UnitName,
		UnitPath: m.unitPath(), EnableLinkPath: m.linkPath(), ConfigState: "not_checked", EnableLinkState: "not_checked", Registration: "not_checked", Actions: []string{}, Notes: []string{
			"Loaded, active and login-enabled states are reported separately; systemd status does not establish HTTP or USB readiness.",
			"managed_enabled_for_login describes only this tool's persistent default.target link; other enablement is shown by unit_file_state and is not taken over.",
			"No linger, system-wide service or pre-login startup is configured. Stop retains the unit and login enablement; Uninstall removes managed files.",
		}}
}

func applySnapshot(status *Status, s *snapshot) {
	status.ConfigState, status.EnableLinkState = "absent", "absent"
	enabled := s.link != nil
	status.ManagedEnabledForLogin = &enabled
	if s.unit != nil {
		status.ConfigState = "managed"
		config := s.unit.definition.Config
		status.Config = &config
	}
	if s.link != nil {
		status.EnableLinkState = "owned"
	}
}

func (m *Manager) inspectStatus(ctx context.Context, configOnly bool) (Status, *snapshot, error) {
	status := m.initialStatus()
	s, err := m.inspect()
	if err != nil {
		status.ConfigState, status.EnableLinkState = "conflict", "conflict"
		return status, s, err
	}
	applySnapshot(&status, s)
	if configOnly {
		return status, s, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, queryBudget)
	defer cancel()
	result, err := m.runner.Run(queryCtx, showArgs()...)
	if err != nil || result.Truncated {
		status.Registration = "unknown"
		return status, s, fmt.Errorf("could not complete the systemd user manager query; service registration is unknown")
	}
	if result.ExitCode != 0 && result.ExitCode != 4 {
		status.Registration = "unknown"
		return status, s, fmt.Errorf("could not query the systemd user manager (systemctl exit %d); service management requires a running user session; in a container without one, run 'nb1 server --devices SERIAL' directly", result.ExitCode)
	}
	properties, err := parseProperties(result.Output)
	if err != nil {
		status.Registration = "unknown"
		return status, s, err
	}
	status.Properties = &properties
	if result.ExitCode != 0 && !(result.ExitCode == 4 && absentProperties(properties)) {
		status.Registration = "unknown"
		return status, s, fmt.Errorf("systemctl show did not confirm the unit state; service registration is unknown")
	}
	if absentProperties(properties) {
		status.Registration = "absent"
		return status, s, nil
	}
	if properties.LoadState != "loaded" {
		status.Registration = "unusable"
		return status, s, nil
	}
	status.Registration = "loaded"
	status.RegistrationOwned = m.trustedLoaded(s, properties) == nil
	return status, s, nil
}

func (m *Manager) trustedLoaded(s *snapshot, p Properties) error {
	if s.unit == nil || p.LoadState != "loaded" || p.ID != UnitName || p.Transient || p.NeedDaemonReload || p.SourcePath != "" || p.DropInPaths != "" || p.NoNewPrivileges == nil || !*p.NoNewPrivileges {
		return fmt.Errorf("the unit source, overrides or reload state is unclear; the operation is refused")
	}
	if !filepath.IsAbs(p.FragmentPath) || strings.IndexFunc(p.FragmentPath, unicode.IsControl) >= 0 {
		return fmt.Errorf("FragmentPath is unknown; the operation is refused")
	}
	fragment, err := filepath.EvalSymlinks(p.FragmentPath)
	if err != nil || fragment != m.unitPath() {
		return fmt.Errorf("the matching unit comes from another location; the operation is refused")
	}
	wantState := "disabled"
	if s.link != nil {
		wantState = "enabled"
	}
	if p.UnitFileState != wantState {
		return fmt.Errorf("login enablement includes unknown links, runtime overrides or other sources; takeover is refused")
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, configOnly bool) (Status, error) {
	status, _, err := m.inspectStatus(ctx, configOnly)
	return status, err
}

func (m *Manager) run(ctx context.Context, action string, status *Status) error {
	status.Actions = append(status.Actions, action+"_requested")
	result, err := m.runner.Run(ctx, commandArgs(action)...)
	if err != nil || result.ExitCode != 0 || result.Truncated {
		status.Registration = "unknown"
		status.RegistrationOwned = false
		return fmt.Errorf("the result of systemctl %s is unconfirmed; no retry, force-kill or automatic file rollback is performed; query status again", action)
	}
	return nil
}

func carryActions(current Status, previous Status) Status {
	current.Actions = append([]string{}, previous.Actions...)
	return current
}

func (m *Manager) Install(ctx context.Context, config service.Config, start bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	config.Platform = "linux"
	if config.UserConfigRoot != "" && config.UserConfigRoot != m.configRoot {
		return status, fmt.Errorf("service configuration root must match the current user's systemd configuration root")
	}
	config.UserConfigRoot = m.configRoot
	if config.Listen == "" {
		config.Listen = "127.0.0.1:8931"
	}
	content, err := unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: config})
	if err != nil {
		return status, err
	}
	info, err := os.Stat(config.Binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return status, fmt.Errorf("binary must explicitly name an existing executable regular file by its absolute path")
	}
	if config.BinaryRoot == "" {
		config.Binary, err = filepath.EvalSymlinks(config.Binary)
		if err == nil {
			err = validateBinary(config.Binary, m.uid)
		}
	} else {
		_, err = service.ResolveBoundExecutable(config.Binary, config.BinaryRoot, m.uid)
	}
	if err != nil {
		return status, err
	}
	content, err = unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: config})
	if err != nil {
		return status, err
	}
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, s, err := m.inspectStatus(ctx, false)
	if err != nil {
		return status, err
	}
	if s.unit != nil && !bytes.Equal(s.unit.content, content) {
		return status, fmt.Errorf("the existing managed configuration differs; it is not overwritten or upgraded; review and normally uninstall it first")
	}
	if status.Registration != "absent" {
		if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
			return status, fmt.Errorf("a matching external or unknown unit or override already exists; installation is refused")
		}
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if s.unit == nil {
		if err := m.directory(m.unitDir(), true); err != nil {
			return status, err
		}
		if err := publish(m.unitPath(), content); err != nil {
			status.ConfigState = "conflict"
			return status, err
		}
		status.Actions = append(status.Actions, "unit_created")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
	}
	// Refresh a newly created/unloaded definition before creating a persistent
	// enable link, so foreign drop-ins cannot silently become login startup.
	if status.Registration == "absent" {
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
			return status, fmt.Errorf("this tool's unit source could not be verified after reload; files are retained and login enablement was not set")
		}
		s = current
	}
	if s.link == nil {
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if err := m.directory(filepath.Dir(m.linkPath()), true); err != nil {
			return status, err
		}
		if err := os.Symlink(m.unitPath(), m.linkPath()); err != nil {
			return status, fmt.Errorf("the enablement link already exists or could not be created; it is not overwritten: %w", err)
		}
		status.Actions = append(status.Actions, "enable_link_created")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
			return status, fmt.Errorf("the login link was created but enablement is unconfirmed; the configuration is retained and is not started automatically")
		}
	}
	if !start {
		return status, nil
	}
	return m.changeLocked(ctx, true, status.Actions, s)
}

func (m *Manager) Start(ctx context.Context) (Status, error) { return m.change(ctx, true) }
func (m *Manager) Stop(ctx context.Context) (Status, error)  { return m.change(ctx, false) }

func (m *Manager) change(ctx context.Context, start bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	return m.changeLocked(ctx, start, nil, nil)
}

func (m *Manager) changeLocked(ctx context.Context, start bool, actions []string, expected *snapshot) (Status, error) {
	status, s, err := m.inspectStatus(ctx, false)
	status.Actions = append([]string{}, actions...)
	if err != nil {
		return status, err
	}
	if expected != nil && !sameSnapshot(expected, s) {
		return status, fmt.Errorf("the unit changed after the outer inspection; the new configuration was not started or stopped")
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
		return status, fmt.Errorf("this tool's complete unit and enablement state were not verified; start and stop are refused")
	}
	p := *status.Properties
	if p.Job != 0 {
		return status, fmt.Errorf("the unit has a pending job; other start or stop operations are not replaced")
	}
	if (start && p.ActiveState == "active") || (!start && inactive(p)) {
		return status, nil
	}
	if start && !inactive(p) {
		return status, fmt.Errorf("the unit is not in a stable state for starting; the current job was not replaced")
	}
	if !start && p.ActiveState != "active" && p.ActiveState != "activating" && p.ActiveState != "reloading" {
		return status, fmt.Errorf("the unit stop state is unknown; no command was sent")
	}
	if start {
		config := s.unit.definition.Config
		var err error
		if config.BinaryRoot == "" {
			err = validateBinary(config.Binary, m.uid)
		} else {
			_, err = service.ResolveBoundExecutable(config.Binary, config.BinaryRoot, m.uid)
		}
		if err != nil {
			return status, err
		}
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	action := "stop"
	if start {
		action = "start"
	}
	if err := m.run(ctx, action, &status); err != nil {
		return status, err
	}
	latest, current, err := m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
		return status, fmt.Errorf("unit ownership is unknown after the operation; files are retained")
	}
	if status.Properties.Job != 0 || start && status.Properties.ActiveState != "active" || !start && !inactive(*status.Properties) {
		return status, fmt.Errorf("the target active state was not confirmed after the systemd operation returned; this does not establish HTTP or hardware readiness")
	}
	return status, nil
}

func validateBinary(path string, uid int) error {
	if err := safeAncestors(filepath.Dir(path), uid); err != nil {
		return fmt.Errorf("a binary path ancestor is writable by untrusted users; startup or installation was not performed: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
		return fmt.Errorf("binary is missing, is not an executable regular file, has become a symbolic link or is writable by other users; installation or startup was not performed")
	}
	return nil
}

func (m *Manager) Uninstall(ctx context.Context, stop bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, s, err := m.inspectStatus(ctx, false)
	if err != nil {
		return status, err
	}
	original := s
	if status.Registration != "absent" {
		if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
			return status, fmt.Errorf("ownership or overrides of the matching unit are unknown; stop and deletion are refused")
		}
		if !inactive(*status.Properties) {
			if !stop {
				return status, fmt.Errorf("the unit is still active; explicit Uninstall(stop=true) is required to stop it first")
			}
			if err := m.unchanged(original); err != nil {
				return status, err
			}
			status, err = m.changeLocked(ctx, false, status.Actions, original)
			if err != nil {
				return status, err
			}
		}
	}
	if err := m.unchanged(original); err != nil {
		return status, err
	}
	if original.link != nil {
		if err := os.Remove(m.linkPath()); err != nil {
			return status, err
		}
		status.Actions = append(status.Actions, "enable_link_removed")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil || !inactive(*status.Properties) {
			return status, fmt.Errorf("unknown enablement, overrides or active state remain after removing this tool's link; the unit is retained and other links are not deleted")
		}
		s = current
	}
	if s.unit == nil {
		return status, nil
	}
	// Recheck the manager after any stop/link phase and immediately before
	// deleting its definition. An observed new activation is not absence.
	latest, current, err := m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Registration != "absent" && (status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil || !inactive(*status.Properties)) {
		return status, fmt.Errorf("activity, overrides or an unknown source were found before deleting the unit; files are retained")
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	// The lease serializes this tool only. Content/inode rechecks and Remove
	// cannot form an atomic compare-delete against malicious same-UID writers.
	if err := os.Remove(m.unitPath()); err != nil {
		return status, err
	}
	status.Actions = append(status.Actions, "unit_removed")
	status.ConfigState = "absent"
	status.Config = nil
	if err := m.run(ctx, "daemon-reload", &status); err != nil {
		return status, err
	}
	latest, _, err = m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if status.Registration != "absent" {
		return status, fmt.Errorf("this tool's files were removed, but the manager still has a matching source; it is not taken over or stopped")
	}
	return status, nil
}
