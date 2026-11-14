package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	buildconfig "github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/service"
	"github.com/b1ug/nb1/internal/service/systemduser"
	"github.com/b1ug/nb1/internal/service/taskquery"
	"github.com/spf13/cobra"
)

func newServiceCommand() *cobra.Command {
	return newServiceCommandForRuntime(userServiceRuntime{goos: runtime.GOOS, packagedBinary: buildconfig.ServiceBinary, packagedRoot: buildconfig.ServiceBinaryRoot, factories: userServiceFactories{
		darwin:  func() (userServiceBackend[service.UserStatus], error) { return service.NewUserManager() },
		linux:   func() (userServiceBackend[systemduser.Status], error) { return systemduser.New() },
		windows: func() (windowsServiceReader, error) { return taskquery.New() },
	}})
}

// Only the production constructor above chooses the host platform. Keeping
// factories local to the command makes CLI tests independent of native services.
func newServiceCommandForRuntime(management userServiceRuntime) *cobra.Command {
	command := &cobra.Command{
		Use: "service", Short: "Render startup configurations or manage the current user's service",
		Long: "service render generates reviewable launchd, systemd-user or Windows logon-task configuration without installation. Management uses only the host platform: the current ordinary user's fixed macOS LaunchAgent or Linux systemd user unit. Install arranges possible next-login loading; --start or service start explicitly requests immediate startup. Stop retains the configuration and login enablement. Windows additionally has candidate read-only service status for the verified current user's fixed task; ownership is not checked, and install/start/stop/uninstall or status --config-only are unsupported. Native ordinary-user Windows query validation remains pending. Other platforms support render only. Scheduler/loaded/active states do not establish daemon PID, HTTP or USB readiness. No sudo, automatic elevation or system-wide service management.",
	}
	var config service.Config
	var output string
	render := &cobra.Command{
		Use: "render", Short: "Render one target platform's startup configuration",
		Args:    cobra.NoArgs,
		Example: "  nb1 service render --platform darwin --binary /absolute/path/nb1 --devices 20005601\n  nb1 service render --platform linux --binary /opt/nb1/nb1 --devices 20005601 --output nb1.service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != "" {
				_, err := service.WriteFile(config, output)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "Wrote "+output+"; no service was installed or started.")
				return err
			}
			artifact, err := service.Render(config)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(artifact.Content)
			return err
		},
	}
	render.Flags().StringVar(&config.Platform, "platform", "", "target platform: darwin, linux or windows")
	render.Flags().StringVar(&config.Binary, "binary", "", "explicit absolute path to the target platform's nb1 executable")
	render.Flags().StringSliceVar(&config.Devices, "devices", nil, "comma-separated exact device serials")
	render.Flags().StringVar(&config.Listen, "listen", "127.0.0.1:8931", "stable loopback host:port")
	render.Flags().StringVar(&config.LockDir, "lock-dir", "", "optional absolute shared ownership directory; otherwise the service account's default")
	render.Flags().StringVar(&config.ConfigFile, "config-file", "", "optional absolute target-platform user configuration file")
	render.Flags().StringVar(&config.UserConfigRoot, "user-config-root", "", "optional absolute XDG_CONFIG_HOME for macOS/Linux; native install binds the current user's root")
	render.Flags().StringVar(&output, "output", "", "new staging file; default writes configuration to stdout, existing files are not replaced")
	_ = render.MarkFlagRequired("platform")
	_ = render.MarkFlagRequired("binary")
	_ = render.MarkFlagRequired("devices")
	command.AddCommand(render)
	command.AddCommand(newServiceInstallCommand(management), newServiceStartCommand(management), newServiceStopCommand(management), newServiceStatusCommand(management), newServiceUninstallCommand(management))
	return command
}

func newServiceInstallCommand(management userServiceRuntime) *cobra.Command {
	var config service.Config
	var start, asJSON bool
	command := &cobra.Command{
		Use: "install", Short: "Install the current user's service on macOS or Linux",
		Long: "Install a macOS LaunchAgent or Linux systemd user service for login loading. " +
			"Use the running nb1 executable unless --binary explicitly selects another absolute executable path. Homebrew builds use their validated stable package entry so upgrades do not pin a deleted Cellar version. " +
			"Device serials come from explicit flags or user configuration. Installation does not request immediate startup unless --start is provided. " +
			"Existing unknown or different configurations are not replaced. No manual daemon is stopped and no system service or elevation is used.",
		Args:    cobra.NoArgs,
		Example: "  nb1 service install --devices 20000001\n  nb1 service install --devices 20000001 --start\n  nb1 service install --binary /absolute/path/nb1 --devices 20000001 --start",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if management.goos == "darwin" || management.goos == "linux" {
				settings, selected, err := applicationSettings(cmd)
				if err != nil {
					return err
				}
				config.Devices, err = configuredServerSerials(cmd, settings, config.Devices)
				if err != nil {
					return err
				}
				if !cmd.Flags().Changed("listen") {
					config.Listen, err = serverAddress(cmd, settings.Server.Listen)
				}
				if err != nil {
					return err
				}
				if selected.Required {
					config.ConfigFile = selected.Path
				} else if _, err := os.Stat(selected.Path); err == nil {
					config.ConfigFile = selected.Path
				}
				if !cmd.Flags().Changed("lock-dir") {
					config.LockDir = os.Getenv("NB1_LOCK_DIR")
				}
				if cmd.Flags().Changed("binary") {
					config.BinaryRoot = ""
					if config.Binary == "" {
						return fmt.Errorf("--binary must name an absolute executable path when provided")
					}
				} else {
					var err error
					config.Binary, config.BinaryRoot, err = management.installExecutable()
					if err != nil {
						return err
					}
				}
			}
			return management.run(cmd, asJSON, userServiceAction{kind: serviceInstall, config: config, start: start})
		},
	}
	command.Flags().StringVar(&config.Binary, "binary", "", "pin an absolute executable path (default: the running nb1; Homebrew uses its stable package entry)")
	command.Flags().StringSliceVar(&config.Devices, "devices", nil, "explicit eight-digit hexadecimal device serials")
	command.Flags().StringVar(&config.Listen, "listen", "127.0.0.1:8931", "stable loopback host:port")
	command.Flags().StringVar(&config.LockDir, "lock-dir", "", "optional absolute shared ownership directory")
	command.Flags().BoolVar(&start, "start", false, "request immediate startup as well as login loading")
	command.Flags().BoolVar(&asJSON, "json", false, "print the host platform's service status as JSON")
	return command
}

func newServiceStartCommand(management userServiceRuntime) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "start", Short: "Start the verified managed user service on macOS or Linux",
		Long: "Start only the current platform's complete, previously installed managed configuration. Refuse unknown origins, overrides, failed queries or changed configuration. An already registered matching macOS service or active Linux unit returns without a restart. Linux requires a queryable systemd user manager. Registered, loaded or active states do not establish HTTP or USB readiness.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStart})
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print the host platform's schema_version=1 service status as JSON")
	return command
}

func newServiceStopCommand(management userServiceRuntime) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "stop", Short: "Stop the verified user service, retaining its configuration and login enablement",
		Long: "On macOS, request bootout only for the single service target with verified ownership. On Linux, stop only nb1.service after checking its source, overrides and enablement. Keep the configuration and login enablement; stopping does not disable loading at the next login. Refuse unknown ownership, failed queries or changed configuration. An absent registration or inactive state does not prove USB cleanup is complete. Do not force-kill, delete persistent locks or elevate privileges.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStop})
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print the host platform's schema_version=1 service status as JSON")
	return command
}

func newServiceStatusCommand(management userServiceRuntime) *cobra.Command {
	var configOnly, asJSON bool
	command := &cobra.Command{
		Use: "status", Short: "Inspect user service status (Windows: read-only candidate)",
		Long: "On macOS/Linux, distinguish configuration on disk from service manager state. The Windows candidate allows only the current ordinary user, verified by token, to read limited fields from the fixed task. A matching task name does not establish managed ownership; Scheduler state does not establish daemon PID, HTTP or USB readiness. Unknown, denied or missing HRESULT results remain unknown without a validated native contract. Do not elevate privileges or switch identities. Windows does not support --config-only; native ordinary-user query validation remains pending.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStatus, configOnly: configOnly})
		},
	}
	command.Flags().BoolVar(&configOnly, "config-only", false, "macOS/Linux only: read configuration on disk without calling launchctl or systemctl; unsupported on Windows")
	command.Flags().BoolVar(&asJSON, "json", false, "print the host platform's schema_version=1 service status as JSON")
	return command
}

func newServiceUninstallCommand(management userServiceRuntime) *cobra.Command {
	var stop, asJSON bool
	command := &cobra.Command{
		Use: "uninstall", Short: "Uninstall the verified macOS/Linux user service; stopping requires --stop",
		Long: "Always query the current platform's user service manager first. Use --stop explicitly for registered macOS services or active Linux units. A loaded but inactive managed Linux unit can be uninstalled directly. Remove only complete managed configuration and this tool's persistent enablement links. Preserve files when the source or overrides are unknown, queries fail or the stop outcome is uncertain. Do not force-kill, delete persistent locks or take over unknown configuration. Uninstallation does not establish that USB cleanup is complete.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceUninstall, stop: stop})
		},
	}
	command.Flags().BoolVar(&stop, "stop", false, "explicitly allow stopping the verified service first; required when registered on macOS or active on Linux")
	command.Flags().BoolVar(&asJSON, "json", false, "print the host platform's schema_version=1 service status as JSON")
	return command
}

// Both native managers have the same operations but retain distinct status
// contracts. The typed adapter dispatches actions without changing either JSON.
type userServiceBackend[S any] interface {
	Install(context.Context, service.Config, bool) (S, error)
	Status(context.Context, bool) (S, error)
	Start(context.Context) (S, error)
	Stop(context.Context) (S, error)
	Uninstall(context.Context, bool) (S, error)
}

type userServiceFactories struct {
	darwin  func() (userServiceBackend[service.UserStatus], error)
	linux   func() (userServiceBackend[systemduser.Status], error)
	windows func() (windowsServiceReader, error)
}

// A query reader has no management methods. Do not adapt Windows to a fake
// five-action backend before ownership and stop contracts are implemented.
type windowsServiceReader interface {
	Query(context.Context) (taskquery.Status, error)
}

type userServiceRuntime struct {
	goos           string
	factories      userServiceFactories
	executable     func() (string, error)
	packagedBinary string
	packagedRoot   string
}

func (management userServiceRuntime) currentExecutable() (string, error) {
	resolve := management.executable
	if resolve == nil {
		resolve = os.Executable
	}
	binary, err := resolve()
	if err != nil {
		return "", fmt.Errorf("locate the running nb1 executable; select an absolute path with --binary: %w", err)
	}
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("the running executable path is not absolute; select an absolute path with --binary")
	}
	// Neither argv[0] nor a second PATH lookup selects this executable.
	return binary, nil
}

func (management userServiceRuntime) installExecutable() (string, string, error) {
	current, err := management.currentExecutable()
	if err != nil {
		return "", "", err
	}
	if management.packagedBinary == "" && management.packagedRoot == "" {
		return current, "", nil
	}
	target, err := service.ResolveBoundExecutable(management.packagedBinary, management.packagedRoot, os.Geteuid())
	if err != nil {
		return "", "", err
	}
	running, err := filepath.EvalSymlinks(current)
	if err != nil || target != running {
		return "", "", fmt.Errorf("the active package executable differs from this nb1; run the active package or explicitly select --binary")
	}
	return management.packagedBinary, management.packagedRoot, nil
}

type userServiceActionKind int

const (
	serviceInstall userServiceActionKind = iota
	serviceStatus
	serviceStart
	serviceStop
	serviceUninstall
)

type userServiceAction struct {
	kind       userServiceActionKind
	config     service.Config
	start      bool
	stop       bool
	configOnly bool
}

var errUserServiceUnsupported = errors.New("this user-service operation is unsupported on the current platform; automatic management supports ordinary users on macOS/Linux only; Windows supports candidate read-only service status and render; privileges are not elevated")
var errWindowsServiceUnsupported = fmt.Errorf("%w; Windows install/start/stop/uninstall and status --config-only are not implemented; no query adapter is created and no task is modified", errUserServiceUnsupported)

func (management userServiceRuntime) run(cmd *cobra.Command, asJSON bool, action userServiceAction) error {
	switch management.goos {
	case "darwin":
		return runUserServiceBackend(cmd, asJSON, action, management.factories.darwin, service.WriteUserStatus)
	case "linux":
		return runUserServiceBackend(cmd, asJSON, action, management.factories.linux, writeSystemdUserStatus)
	case "windows":
		if action.kind != serviceStatus || action.configOnly {
			return errWindowsServiceUnsupported
		}
		return runWindowsServiceQuery(cmd, asJSON, management.factories.windows)
	default:
		return errUserServiceUnsupported
	}
}

func runWindowsServiceQuery(cmd *cobra.Command, asJSON bool, factory func() (windowsServiceReader, error)) error {
	reader, err := factory()
	if err != nil {
		return err
	}
	status, queryErr := reader.Query(cmd.Context())
	if asJSON {
		err = printControlJSON(cmd.OutOrStdout(), status)
	} else {
		err = writeWindowsServiceStatus(cmd.OutOrStdout(), status)
	}
	if err != nil {
		return err
	}
	return queryErr
}

func writeWindowsServiceStatus(output io.Writer, status taskquery.Status) error {
	var text strings.Builder
	fmt.Fprintf(&text, "nb1 Windows read-only task query (candidate)\ntarget: %s\nregistration presence: %s\nownership: %s (managed ownership not verified)\n", status.TaskPath, status.Presence, status.Ownership)
	if !status.CheckedAt.IsZero() {
		fmt.Fprintf(&text, "query sampled at: %s\n", status.CheckedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if status.Task != nil {
		fmt.Fprintf(&text, "enabled: %t\nScheduler state: %s (%d)\nlast task result: %d\n", status.Task.Enabled, status.Task.SchedulerStateName, status.Task.SchedulerState, status.Task.LastTaskResult)
	} else {
		fmt.Fprintln(&text, "Scheduler fields: unconfirmed; this does not establish that the task is absent.")
	}
	if status.Failure != nil {
		fmt.Fprintf(&text, "query failure: %s · stage=%s", status.Failure.Kind, status.Failure.Stage)
		if status.Failure.HRESULT != "" {
			fmt.Fprintf(&text, " · HRESULT=%s", status.Failure.HRESULT)
		}
		fmt.Fprintln(&text)
	}
	fmt.Fprintln(&text, "A matching task name does not establish managed ownership; Scheduler state does not establish a daemon PID, HTTP availability or USB readiness.")
	fmt.Fprintln(&text, "Windows currently supports only candidate read-only status and render; management and a reliable stop protocol are not implemented. Native ordinary-user query validation remains pending. No privilege elevation or task registration, execution or stopping is performed.")
	_, err := io.WriteString(output, text.String())
	return err
}

func runUserServiceBackend[S any](cmd *cobra.Command, asJSON bool, action userServiceAction, factory func() (userServiceBackend[S], error), writeStatus func(io.Writer, S) error) error {
	manager, err := factory()
	if err != nil {
		return err
	}
	var status S
	var actionErr error
	switch action.kind {
	case serviceInstall:
		status, actionErr = manager.Install(cmd.Context(), action.config, action.start)
	case serviceStatus:
		status, actionErr = manager.Status(cmd.Context(), action.configOnly)
	case serviceStart:
		status, actionErr = manager.Start(cmd.Context())
	case serviceStop:
		status, actionErr = manager.Stop(cmd.Context())
	case serviceUninstall:
		status, actionErr = manager.Uninstall(cmd.Context(), action.stop)
	default:
		return fmt.Errorf("unknown user-service action")
	}
	if asJSON {
		err = printControlJSON(cmd.OutOrStdout(), status)
	} else {
		err = writeStatus(cmd.OutOrStdout(), status)
	}
	if err != nil {
		return err
	}
	return actionErr
}

func writeSystemdUserStatus(output io.Writer, status systemduser.Status) error {
	var text strings.Builder
	fmt.Fprintf(&text, "nb1 Linux systemd user service\nunit: %s\ntarget: %s\nconfiguration: %s · %s\nmanager registration: %s · ownership verified=%t\n", status.Label, status.Target, status.UnitPath, status.ConfigState, status.Registration, status.RegistrationOwned)
	fmt.Fprintf(&text, "persistent enablement link: %s · %s\n", status.EnableLinkPath, status.EnableLinkState)
	if status.ManagedEnabledForLogin == nil {
		fmt.Fprintln(&text, "Managed login enablement: not checked or unconfirmed.")
	} else {
		fmt.Fprintf(&text, "Managed login enablement: %t; this describes only the persistent default.target link; see unit_file_state for other sources.\n", *status.ManagedEnabledForLogin)
	}
	if status.Properties == nil {
		fmt.Fprintln(&text, "load_state / active_state / sub_state / unit_file_state: not checked or unconfirmed.")
	} else {
		p := status.Properties
		fmt.Fprintf(&text, "load_state: %s\nactive_state: %s\nsub_state: %s\nunit_file_state: %s\n", p.LoadState, p.ActiveState, p.SubState, p.UnitFileState)
		if p.NoNewPrivileges == nil {
			fmt.Fprintln(&text, "NoNewPrivileges: not checked or unconfirmed.")
		} else {
			fmt.Fprintf(&text, "NoNewPrivileges: %t\n", *p.NoNewPrivileges)
		}
	}
	if status.Config != nil {
		fmt.Fprintf(&text, "binary: %s\ndevices: %s\nlisten: %s\n", status.Config.Binary, strings.Join(status.Config.Devices, ","), status.Config.Listen)
	}
	if len(status.Actions) != 0 {
		fmt.Fprintf(&text, "actions: %s\n", strings.Join(status.Actions, ", "))
	}
	fmt.Fprintln(&text, "Loaded, active, login-enabled or ownership-verified states do not establish HTTP availability or USB readiness; stopping does not establish that hardware cleanup is complete.")
	for _, note := range status.Notes {
		fmt.Fprintln(&text, note)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

func init() {
	rootCmd.AddCommand(newServiceCommand())
}
