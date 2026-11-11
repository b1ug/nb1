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
		Use: "start", Short: "macOS/Linux：启动已有且归属已核对的托管用户配置",
		Long: "只启动当前平台已安装的完整托管配置。未知来源、覆盖、查询失败或配置变化时拒绝动作。macOS相同配置已注册、Linux已active时均幂等返回，不重启。Linux还须有可查询的用户systemd manager。注册、loaded或active均不表示HTTP或USB已就绪。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStart})
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "输出当前平台的原始schema_version=1状态")
	return command
}

func newServiceStopCommand(management userServiceRuntime) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "stop", Short: "macOS/Linux：停止已核对归属的用户服务，保留配置与登录启用",
		Long: "macOS仅对已核对归属的单个service-target请求bootout；Linux仅停止已核对来源、覆盖及启用状态的nb1.service。保留配置和登录启用，stop不等于禁用下次登录加载。未知归属、查询失败或配置变化时拒绝动作；注册消失或inactive不证明USB清理完成。不强杀、不删除持久锁、不自动提权。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStop})
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "输出当前平台的原始schema_version=1状态")
	return command
}

func newServiceStatusCommand(management userServiceRuntime) *cobra.Command {
	var configOnly, asJSON bool
	command := &cobra.Command{
		Use: "status", Short: "macOS/Linux用户服务状态；Windows候选仅只读查询固定当前用户任务",
		Long: "macOS/Linux区分磁盘配置与manager状态。Windows候选只允许经token核验的当前普通用户查询固定任务的有限字段；同名存在不等于托管，Scheduler状态不等于daemon PID、HTTP或USB就绪。未知/拒绝/缺失HRESULT未有原生合同均保持unknown，不提权或切换身份。Windows不支持--config-only，普通用户原生查询仍待独立验收。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStatus, configOnly: configOnly})
		},
	}
	command.Flags().BoolVar(&configOnly, "config-only", false, "仅macOS/Linux：只读磁盘配置，完全不调用launchctl或systemctl；Windows不支持")
	command.Flags().BoolVar(&asJSON, "json", false, "输出当前平台的原始schema_version=1状态")
	return command
}

func newServiceUninstallCommand(management userServiceRuntime) *cobra.Command {
	var stop, asJSON bool
	command := &cobra.Command{
		Use: "uninstall", Short: "macOS/Linux：核对归属后卸载；需停止时须显式--stop",
		Long: "始终先查询当前平台用户manager。macOS已注册项、Linux仍活动的unit须显式--stop；Linux已加载但inactive的托管unit可直接卸载。只删除完整托管配置与本工具持久启用链接；未知来源、覆盖、查询失败或停止结果不明时保留文件。不强杀、不删除持久锁、不接管未知配置。卸载结果不代表USB清理已完成。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceUninstall, stop: stop})
		},
	}
	command.Flags().BoolVar(&stop, "stop", false, "明确允许先停止归属已核对的服务；macOS已注册或Linux仍活动时需要")
	command.Flags().BoolVar(&asJSON, "json", false, "输出当前平台的原始schema_version=1状态")
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

var errUserServiceUnsupported = errors.New("该用户服务操作不受当前平台支持；自动管理仅macOS/Linux普通用户，Windows候选仅service status只读查询与render；不自动提权")
var errWindowsServiceUnsupported = fmt.Errorf("%w；Windows的install/start/stop/uninstall和status --config-only尚未实现，不构造查询器或修改任务", errUserServiceUnsupported)

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
	fmt.Fprintf(&text, "nb1 Windows任务只读查询（候选）\n目标：%s\n注册存在性：%s\n归属：%s（未验证托管归属）\n", status.TaskPath, status.Presence, status.Ownership)
	if !status.CheckedAt.IsZero() {
		fmt.Fprintf(&text, "查询采样：%s\n", status.CheckedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if status.Task != nil {
		fmt.Fprintf(&text, "enabled：%t\nScheduler状态：%s（%d）\n上次任务结果：%d\n", status.Task.Enabled, status.Task.SchedulerStateName, status.Task.SchedulerState, status.Task.LastTaskResult)
	} else {
		fmt.Fprintln(&text, "Scheduler字段：不可确认；不能据此判断任务不存在。")
	}
	if status.Failure != nil {
		fmt.Fprintf(&text, "查询失败：%s · 阶段=%s", status.Failure.Kind, status.Failure.Stage)
		if status.Failure.HRESULT != "" {
			fmt.Fprintf(&text, " · HRESULT=%s", status.Failure.HRESULT)
		}
		fmt.Fprintln(&text)
	}
	fmt.Fprintln(&text, "同名任务不等于本工具托管；Scheduler状态不代表daemon PID、HTTP可用或USB在线。")
	fmt.Fprintln(&text, "Windows目前仅候选只读status与render；管理与可靠stop协议未实现，普通用户原生查询待独立验收，不提权、不注册/运行/停止任务。")
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
	fmt.Fprintf(&text, "nb1 Linux systemd 用户服务\nunit：%s\n目标：%s\n配置：%s · %s\nmanager加载：%s · 归属已核对=%t\n", status.Label, status.Target, status.UnitPath, status.ConfigState, status.Registration, status.RegistrationOwned)
	fmt.Fprintf(&text, "持久启用链接：%s · %s\n", status.EnableLinkPath, status.EnableLinkState)
	if status.ManagedEnabledForLogin == nil {
		fmt.Fprintln(&text, "本工具登录启用：未检查或无法确认。")
	} else {
		fmt.Fprintf(&text, "本工具登录启用：%t；仅描述default.target持久链接，其他来源看unit_file_state。\n", *status.ManagedEnabledForLogin)
	}
	if status.Properties == nil {
		fmt.Fprintln(&text, "load_state / active_state / sub_state / unit_file_state：未检查或无法确认。")
	} else {
		p := status.Properties
		fmt.Fprintf(&text, "load_state：%s\nactive_state：%s\nsub_state：%s\nunit_file_state：%s\n", p.LoadState, p.ActiveState, p.SubState, p.UnitFileState)
		if p.NoNewPrivileges == nil {
			fmt.Fprintln(&text, "NoNewPrivileges：未检查或无法确认。")
		} else {
			fmt.Fprintf(&text, "NoNewPrivileges：%t\n", *p.NoNewPrivileges)
		}
	}
	if status.Config != nil {
		fmt.Fprintf(&text, "二进制：%s\n设备：%s\n监听：%s\n", status.Config.Binary, strings.Join(status.Config.Devices, ","), status.Config.Listen)
	}
	if len(status.Actions) != 0 {
		fmt.Fprintf(&text, "本次操作：%s\n", strings.Join(status.Actions, ", "))
	}
	fmt.Fprintln(&text, "loaded、active、登录启用与归属已核对均不表示HTTP可用或USB在线；停止也不证明硬件清理已完成。")
	for _, note := range status.Notes {
		fmt.Fprintln(&text, note)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

func init() {
	rootCmd.AddCommand(newServiceCommand())
}
