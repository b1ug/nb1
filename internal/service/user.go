package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/userconfig"
)

var ErrUserServiceUnsupported = errors.New("automatic user-service management in this adapter supports macOS only; service render remains available on other platforms; launchctl is not invoked")

const (
	managedMarker   = "<!-- nb1-managed-user-service-v1:"
	maxManagedSize  = 128 << 10
	operationBudget = 10 * time.Second
)

// CommandResult is an internal adapter result. Output may contain launchd
// diagnostic data, so it must never be copied wholesale to reports or logs.
type CommandResult struct {
	Output    string
	ExitCode  int
	Truncated bool
}

type Runner interface {
	Run(context.Context, ...string) (CommandResult, error)
}

type UserManager struct {
	home   string
	uid    int
	runner Runner
}

// UserStatus distinguishes a known on-disk configuration from launchd
// registration. Registered does not mean running, HTTP-ready or USB-online.
type UserStatus struct {
	SchemaVersion     int      `json:"schema_version"`
	Label             string   `json:"label"`
	Target            string   `json:"target"`
	PlistPath         string   `json:"plist_path"`
	ConfigState       string   `json:"config_state"`
	Registration      string   `json:"registration"`
	RegistrationOwned bool     `json:"registration_owned"`
	AutoLoadAtLogin   *bool    `json:"auto_load_at_login"`
	Config            *Config  `json:"config"`
	Actions           []string `json:"actions"`
	Notes             []string `json:"notes"`
}

type managedDefinition struct {
	Version int    `json:"version"`
	Label   string `json:"label"`
	UID     int    `json:"uid"`
	Config  Config `json:"config"`
}

type managedFile struct {
	definition managedDefinition
	content    []byte
	info       os.FileInfo
}

// NewUserManager only addresses this ordinary user's fixed GUI LaunchAgent.
// No arbitrary labels, UIDs, launchctl paths or installation directories are
// accepted by the CLI. Construction performs no writes or launchctl calls.
func NewUserManager() (*UserManager, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrUserServiceUnsupported
	}
	uid := os.Geteuid()
	if uid <= 0 || os.Getuid() != uid {
		return nil, fmt.Errorf("run user-service management as the current ordinary user; sudo, root and identity switching are unsupported")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return newUserManager(home, uid, launchctlRunner{})
}

func newUserManager(home string, uid int, runner Runner) (*UserManager, error) {
	if !filepath.IsAbs(home) || runner == nil || uid < 0 {
		return nil, fmt.Errorf("invalid user service context")
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("inspect user home: %w", err)
	}
	m := &UserManager{home: canonical, uid: uid, runner: runner}
	if err := m.directory(canonical, false); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *UserManager) target() string { return "gui/" + strconv.Itoa(m.uid) + "/" + Label }
func (m *UserManager) domain() string { return "gui/" + strconv.Itoa(m.uid) }
func (m *UserManager) plistPath() string {
	return filepath.Join(m.home, "Library", "LaunchAgents", Label+".plist")
}

func (m *UserManager) initialStatus() UserStatus {
	return UserStatus{SchemaVersion: 1, Label: Label, Target: m.target(), PlistPath: m.plistPath(), ConfigState: "not_checked", Registration: "not_checked", Actions: []string{}, Notes: []string{
		"Writing configuration to LaunchAgents may enable loading at the next graphical login, even without starting it now.",
		"Registered means only that launchd has a registration; it does not establish that a process is running, HTTP is available or a USB device is ready. Check API/device snapshots separately with nb1 doctor.",
		"launchctl print is not a stable machine API; start, stop and uninstall are refused when ownership fields cannot be strictly verified.",
	}}
}

// Status with configOnly performs filesystem reads only. It creates no
// directories or lock files and never calls launchctl in that mode.
func (m *UserManager) Status(ctx context.Context, configOnly bool) (UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status, _, err := m.inspect(ctx, configOnly)
	return status, err
}

// inspect retains the exact file read before querying launchd so lifecycle
// actions can reject changes made while the query was in progress.
func (m *UserManager) inspect(ctx context.Context, configOnly bool) (UserStatus, *managedFile, error) {
	status := m.initialStatus()
	file, err := m.readManaged()
	if err != nil {
		status.ConfigState = "conflict"
		return status, nil, err
	}
	if file != nil {
		status.ConfigState, status.AutoLoadAtLogin = "managed", boolValue(true)
		config := file.definition.Config
		status.Config = &config
	} else {
		status.ConfigState, status.AutoLoadAtLogin = "absent", boolValue(false)
	}
	if !configOnly {
		if err := m.registration(ctx, &status); err != nil {
			return status, file, err
		}
	}
	return status, file, nil
}

// Start loads only a complete, existing managed configuration. A matching
// registration is already started for this command's purpose; never restart it.
func (m *UserManager) Start(ctx context.Context) (UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, file, err := m.inspect(ctx, false)
	if err != nil {
		return status, err
	}
	if file == nil {
		return status, fmt.Errorf("no verified managed configuration; run service install first; no configuration is created and no matching registration is taken over")
	}
	if err := m.bootstrap(ctx, &status, file); err != nil {
		return status, err
	}
	return status, nil
}

// Stop removes only the verified registration and deliberately retains the
// plist. This does not disable automatic loading at the next GUI login.
func (m *UserManager) Stop(ctx context.Context) (UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, file, err := m.inspect(ctx, false)
	status.Notes = append(status.Notes, "Stop retains the plist and does not disable loading at the next graphical login; use service uninstall to remove the configuration.",
		"An absent registration does not establish process exit, native I/O completion, device ownership release or completion of the final Off command; check these separately as needed.")
	if err != nil {
		return status, err
	}
	if err := m.bootout(ctx, &status, file); err != nil {
		return status, err
	}
	return status, nil
}

// Install never replaces a file, including a changed configuration previously
// installed by this tool. start is the explicit live bootstrap boundary.
func (m *UserManager) Install(ctx context.Context, config Config, start bool) (UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	config.Platform = "darwin"
	configRoot, err := userconfig.RootDir()
	if err != nil {
		return status, err
	}
	if config.UserConfigRoot != "" && config.UserConfigRoot != configRoot {
		return status, fmt.Errorf("service configuration root must match the current user's configuration root")
	}
	config.UserConfigRoot = configRoot
	if config.Listen == "" {
		config.Listen = "127.0.0.1:8931"
	}
	if err := validate(config); err != nil {
		return status, err
	}
	// Retain an explicit absolute executable and verify it without executing it.
	if config.BinaryRoot == "" {
		config.Binary, err = executablePath(config.Binary)
	} else {
		_, err = ResolveBoundExecutable(config.Binary, config.BinaryRoot, m.uid)
	}
	if err != nil {
		return status, err
	}
	definition := managedDefinition{Version: 1, Label: Label, UID: m.uid, Config: config}
	content, err := managedContent(definition)
	if err != nil {
		return status, err
	}
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	// Even files-only installation must reject a foreign same-label service:
	// writing LaunchAgents would otherwise create a future login collision.
	status, current, err := m.inspect(ctx, false)
	if err != nil {
		return status, err
	}
	if err := m.unchanged(current); err != nil {
		return status, err
	}
	if current != nil && !bytes.Equal(current.content, content) {
		return status, fmt.Errorf("the existing managed configuration differs from the request; it is not overwritten or upgraded; review and normally uninstall the old configuration first")
	}
	if status.Registration != "absent" {
		// The shared helper only accepts a matching registration here and
		// returns without requesting bootstrap, including files-only installs.
		err := m.bootstrap(ctx, &status, current)
		return status, err
	}
	if current == nil {
		if err := m.directory(filepath.Dir(m.plistPath()), true); err != nil {
			return status, err
		}
		if err := createManaged(m.plistPath(), content); err != nil {
			status.ConfigState, status.AutoLoadAtLogin = "conflict", nil
			return status, fmt.Errorf("configuration creation did not complete; startup was not requested; existing or partial files are retained for inspection and are not overwritten or deleted: %w", err)
		}
		status.Actions = append(status.Actions, "plist_created")
		status.ConfigState, status.Config, status.AutoLoadAtLogin = "managed", &config, boolValue(true)
		current, err = m.readManaged()
		if err != nil {
			return status, err
		}
	}
	if !start {
		return status, nil
	}
	if err := m.bootstrap(ctx, &status, current); err != nil {
		return status, err
	}
	return status, nil
}

// executablePath inspects an explicit binary without executing it.
func executablePath(binary string) (string, error) {
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("--binary must point to an existing executable regular file; it is not downloaded or executed automatically")
	}
	return filepath.EvalSymlinks(binary)
}

// currentUser rechecks credentials at the live action boundary. Production
// construction also rejects root and switched identities before any operation.
func (m *UserManager) currentUser() error {
	if os.Getuid() != m.uid || os.Geteuid() != m.uid {
		return fmt.Errorf("the current user identity has changed; starting or stopping the user service is refused")
	}
	return nil
}

// bootstrap is shared by install --start and start. The caller holds the
// management lease and supplies the file snapshot from before the query.
func (m *UserManager) bootstrap(ctx context.Context, status *UserStatus, file *managedFile) error {
	if err := m.unchanged(file); err != nil {
		return err
	}
	if err := m.currentUser(); err != nil {
		return err
	}
	if status.Registration == "registered" && status.RegistrationOwned && file != nil {
		status.Notes = append(status.Notes, "The same managed configuration is already registered; no restart, kickstart or process replacement was performed.")
		return nil
	}
	if status.Registration != "absent" || file == nil {
		return fmt.Errorf("launchd has a matching registration with unverified ownership, or registration state is unknown; it is not bootstrapped, replaced or stopped")
	}
	configuration := file.definition.Config
	var binary string
	var err error
	if configuration.BinaryRoot == "" {
		binary, err = executablePath(configuration.Binary)
	} else {
		binary, err = ResolveBoundExecutable(configuration.Binary, configuration.BinaryRoot, m.uid)
	}
	if err != nil {
		return err
	}
	if configuration.BinaryRoot == "" && binary != configuration.Binary {
		return fmt.Errorf("the managed binary path has become a symbolic link; loading is refused; verify the configuration again")
	}
	if err := m.unchanged(file); err != nil {
		return err
	}
	status.Actions = append(status.Actions, "bootstrap_requested")
	result, err := m.runner.Run(ctx, "bootstrap", m.domain(), m.plistPath())
	if err != nil || result.ExitCode != 0 {
		status.Registration = "unknown"
		return fmt.Errorf("bootstrap success is unconfirmed; the configuration is retained and may still load at the next login; check service status; no automatic rollback, retry or bootout is performed")
	}
	if err := m.registration(ctx, status); err != nil {
		return err
	}
	if err := m.unchanged(file); err != nil {
		return err
	}
	if !status.RegistrationOwned {
		return fmt.Errorf("registration ownership could not be verified after bootstrap returned; the configuration is retained; check service status; no automatic bootout is performed")
	}
	return nil
}

// bootout is shared by stop and uninstall --stop. It observes registration,
// never process exit or hardware cleanup, and never removes the plist.
func (m *UserManager) bootout(ctx context.Context, status *UserStatus, original *managedFile) error {
	if err := m.unchanged(original); err != nil {
		return err
	}
	if err := m.currentUser(); err != nil {
		return err
	}
	if status.Registration == "absent" {
		return nil
	}
	if status.Registration != "registered" || !status.RegistrationOwned || original == nil {
		return fmt.Errorf("ownership of the matching registration is unclear; the service is not stopped and configuration is not deleted")
	}
	status.Actions = append(status.Actions, "bootout_requested")
	result, err := m.runner.Run(ctx, "bootout", m.target())
	if err != nil || result.ExitCode != 0 {
		status.Registration, status.RegistrationOwned = "unknown", false
		return fmt.Errorf("bootout success is unconfirmed; the configuration is retained; run service status again; no force-kill or file deletion is performed")
	}
	// Do not use --wait, which current launchctl help says may wait forever.
	for {
		if err := m.registration(ctx, status); err != nil {
			return err
		}
		if err := m.unchanged(original); err != nil {
			return err
		}
		if status.Registration == "absent" {
			return nil
		}
		if !status.RegistrationOwned {
			return fmt.Errorf("registration ownership changed after stopping; the configuration is retained")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for deregistration; the configuration is retained; process or hardware cleanup completion is not guaranteed")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Uninstall always queries launchd first. stop authorizes bootout only after
// both the on-disk marker and the exact loaded path/program have been checked.
// It never treats a query error or unknown output format as 'not loaded'.
func (m *UserManager) Uninstall(ctx context.Context, stop bool) (UserStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, original, err := m.inspect(ctx, false)
	if err != nil {
		return status, err
	}
	if status.Registration == "registered" {
		if !status.RegistrationOwned {
			return status, fmt.Errorf("ownership of the matching registration is unclear; the service is not stopped and configuration is not deleted")
		}
		if !stop {
			return status, fmt.Errorf("the service is still registered; explicit service uninstall --stop is required to request stopping and uninstalling")
		}
		if err := m.bootout(ctx, &status, original); err != nil {
			return status, err
		}
	}
	if status.Registration != "absent" {
		return status, fmt.Errorf("registration state is unclear; the configuration is retained")
	}
	file, err := m.readManaged()
	if err != nil {
		return status, err
	}
	if err := sameManaged(original, file); err != nil {
		return status, err
	}
	if file != nil {
		latest, err := os.Lstat(m.plistPath())
		if err != nil || !os.SameFile(file.info, latest) {
			return status, fmt.Errorf("the configuration file changed after inspection; deletion is refused")
		}
		// The persistent management lease coordinates this tool's writers.
		// Lstat/Remove is not atomic against a deliberately racing process
		// with the same UID; observed changes are refused, not silently ignored.
		if err := os.Remove(m.plistPath()); err != nil {
			return status, err
		}
		status.Actions = append(status.Actions, "plist_removed")
	}
	status.ConfigState, status.Config, status.AutoLoadAtLogin = "absent", nil, boolValue(false)
	status.Notes = append(status.Notes, "Removing the registration does not establish native I/O completion, device ownership release or completion of the final Off command; check these separately as needed. The persistent management lock file is retained.")
	return status, nil
}

func (m *UserManager) unchanged(original *managedFile) error {
	current, err := m.readManaged()
	if err != nil {
		return err
	}
	return sameManaged(original, current)
}

func sameManaged(original, current *managedFile) error {
	if original == nil && current == nil {
		return nil
	}
	if original == nil || current == nil || !os.SameFile(original.info, current.info) || !bytes.Equal(original.content, current.content) {
		return fmt.Errorf("the managed configuration was replaced or changed during inspection; start, stop and deletion are refused; verify it again")
	}
	return nil
}

func (m *UserManager) registration(ctx context.Context, status *UserStatus) error {
	status.Registration, status.RegistrationOwned = "unknown", false
	result, err := m.runner.Run(ctx, "print", m.target())
	if err != nil {
		return fmt.Errorf("launchd could not be queried; ownership is unknown; no start, stop or configuration deletion is performed")
	}
	if result.ExitCode == 0 {
		status.Registration = "registered"
		if !result.Truncated && status.Config != nil {
			status.RegistrationOwned = printedIdentity(result.Output, m.target(), m.plistPath(), status.Config.Binary)
		}
		return nil
	}
	// On this Darwin launchctl, 113 is the missing-service status. Confirm
	// the GUI domain itself is reachable; do not interpret arbitrary failures
	// (including absent GUI sessions or permission errors) as an unloaded job.
	if result.ExitCode == 113 {
		domain, err := m.runner.Run(ctx, "print", m.domain())
		if err == nil && domain.ExitCode == 0 {
			status.Registration = "absent"
			return nil
		}
	}
	return fmt.Errorf("the launchd query failed or the GUI login domain is unavailable; registration state is unknown; no start, stop or configuration deletion is performed")
}

// print is explicitly a human diagnostic format. Recognize only a narrow
// top-level form, never nested fields, partial matches or a PID inferred from
// text. Format changes degrade to unverified ownership and prohibit bootout.
func printedIdentity(output, target, path, binary string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 3 || lines[0] != target+" = {" || lines[len(lines)-1] != "}" {
		return false
	}
	var paths, programs []string
	for _, line := range lines[1 : len(lines)-1] {
		for _, indent := range []string{"\t", "    "} {
			if strings.HasPrefix(line, indent+"path = ") {
				paths = append(paths, strings.TrimPrefix(line, indent+"path = "))
			}
			if strings.HasPrefix(line, indent+"program = ") {
				programs = append(programs, strings.TrimPrefix(line, indent+"program = "))
			}
		}
	}
	return len(paths) == 1 && paths[0] == path && len(programs) == 1 && programs[0] == binary
}

func managedContent(definition managedDefinition) ([]byte, error) {
	artifact, err := Render(definition.Config)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	marker := managedMarker + base64.StdEncoding.EncodeToString(metadata) + " -->\n"
	return bytes.Replace(artifact.Content, []byte(xml.Header), []byte(xml.Header+marker), 1), nil
}

func (m *UserManager) readManaged() (*managedFile, error) {
	if err := m.directory(filepath.Dir(m.plistPath()), false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	f, err := openManaged(m.plistPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the configuration could not be read safely; it is not overwritten or deleted: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedBy(info, m.uid) || info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("the configuration must be a regular file owned by the current user and not writable by group or others; it is not overwritten or deleted")
	}
	content, err := io.ReadAll(io.LimitReader(f, maxManagedSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxManagedSize {
		return nil, fmt.Errorf("the configuration is too large; it is treated as an unknown file and is not overwritten or deleted")
	}
	lines := bytes.SplitN(content, []byte("\n"), 3)
	if len(lines) != 3 || string(lines[0])+"\n" != xml.Header || !bytes.HasPrefix(lines[1], []byte(managedMarker)) || !bytes.HasSuffix(lines[1], []byte(" -->")) {
		return nil, fmt.Errorf("the existing plist lacks this tool's complete managed marker; it is not taken over, overwritten or deleted")
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(string(lines[1]), managedMarker), " -->")
	metadata, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("the managed marker is damaged; the configuration is not overwritten or deleted")
	}
	var definition managedDefinition
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil || definition.Version != 1 || definition.Label != Label || definition.UID != m.uid || definition.Config.Platform != "darwin" {
		return nil, fmt.Errorf("the managed marker identity or version does not match; the configuration is not overwritten or deleted")
	}
	canonical, err := managedContent(definition)
	if err != nil || !bytes.Equal(canonical, content) {
		return nil, fmt.Errorf("the managed plist was modified or its marker is inconsistent; it is not overwritten or deleted")
	}
	return &managedFile{definition: definition, content: content, info: info}, nil
}

func (m *UserManager) managementLease() (*ownership.Lease, error) {
	path := filepath.Join(m.home, "Library", "Application Support", "nb1", "service-management")
	if err := m.directory(path, true); err != nil {
		return nil, err
	}
	return ownership.Acquire(path, "managed-user-service/"+Label)
}

// All traversed directories below the canonical home must be real, owned by
// this user and not writable by other users. Existing permissions are not
// changed. Exclusive publication never replaces a pre-existing final plist.
func (m *UserManager) directory(path string, create bool) error {
	relative, err := filepath.Rel(m.home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("service path escaped user home")
	}
	paths := []string{m.home}
	current := m.home
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, item := range paths {
		info, err := os.Lstat(item)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(item, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(item)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || !ownedBy(info, m.uid) || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("the user-service directory is not a safe current-user directory; symbolic links, directories owned by others, and directories writable by group or others are refused")
		}
	}
	return nil
}

func boolValue(value bool) *bool { return &value }

func WriteUserStatus(output io.Writer, status UserStatus) error {
	var text strings.Builder
	fmt.Fprintf(&text, "nb1 macOS user service\nlabel: %s\ntarget: %s\nconfiguration: %s · %s\nlaunchd registration: %s · ownership verified=%t\n", status.Label, status.Target, status.PlistPath, status.ConfigState, status.Registration, status.RegistrationOwned)
	if status.AutoLoadAtLogin == nil {
		fmt.Fprintln(&text, "Automatic loading at the next graphical login: not checked or unconfirmed; this does not establish configuration presence, absence or whether it is safe to manage.")
	} else if *status.AutoLoadAtLogin {
		fmt.Fprintln(&text, "May load automatically at the next graphical login: yes; this does not establish that it is running now.")
	} else {
		fmt.Fprintln(&text, "The managed plist is absent; this configuration has not arranged loading at the next login.")
	}
	if status.Config != nil {
		fmt.Fprintf(&text, "binary: %s\ndevices: %s\nlisten: %s\n", status.Config.Binary, strings.Join(status.Config.Devices, ","), status.Config.Listen)
	}
	if len(status.Actions) != 0 {
		fmt.Fprintf(&text, "actions: %s\n", strings.Join(status.Actions, ", "))
	}
	for _, note := range status.Notes {
		fmt.Fprintln(&text, note)
	}
	_, err := io.WriteString(output, text.String())
	return err
}
