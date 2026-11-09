package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/b1ug/nb1/internal/service"
	"github.com/b1ug/nb1/internal/service/systemduser"
	"github.com/spf13/cobra"
)

func newServiceCommand() *cobra.Command {
	return newServiceCommandForRuntime(userServiceRuntime{goos: runtime.GOOS, factories: userServiceFactories{
		darwin: func() (userServiceBackend[service.UserStatus], error) { return service.NewUserManager() },
		linux:  func() (userServiceBackend[systemduser.Status], error) { return systemduser.New() },
	}})
}

// Only the production constructor above chooses the host platform. Keeping
// factories local to the command makes CLI tests independent of native services.
func newServiceCommandForRuntime(management userServiceRuntime) *cobra.Command {
	command := &cobra.Command{
		Use: "service", Short: "Render startup configurations or manage the current user's service",
		Long: "service render generates reviewable launchd, systemd-user or Windows logon-task configuration without installation. Management uses only the host platform: the current ordinary user's fixed macOS LaunchAgent or Linux systemd user unit. Install arranges possible next-login loading; --start or service start explicitly requests immediate startup. Stop retains the configuration and login enablement. Loaded/active states do not establish HTTP or USB readiness. Other platforms support render only. No sudo, automatic elevation or system-wide service management.",
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
		Use: "install", Short: "macOS/Linux：安装并安排用户登录加载；默认不立即启动",
		Long:    "macOS写当前用户LaunchAgents配置；Linux写当前用户nb1.service及本工具的default.target持久启用链接，并刷新用户manager。默认不立即启动；下次登录可能加载，--start才请求立即启动。未知同名项、覆盖配置、查询异常或已有配置不同均拒绝接管或升级。二进制必须是当前机器上已存在的绝对可执行路径，设备须明确指定。不停止手工daemon，不管理系统服务、不自动提权。",
		Args:    cobra.NoArgs,
		Example: "  nb1 service install --binary /absolute/path/nb1 --devices 20005601\n  nb1 service install --binary /absolute/path/nb1 --devices 20005601 --start",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceInstall, config: config, start: start})
		},
	}
	command.Flags().StringVar(&config.Binary, "binary", "", "已存在的绝对可执行文件路径，不使用PATH搜索")
	command.Flags().StringSliceVar(&config.Devices, "devices", nil, "明确的8位十六进制设备序列号列表")
	command.Flags().StringVar(&config.Listen, "listen", "127.0.0.1:8931", "固定loopback host:port")
	command.Flags().StringVar(&config.LockDir, "lock-dir", "", "可选明确的绝对设备锁目录；不收集环境变量")
	command.Flags().BoolVar(&start, "start", false, "显式请求立即启动；否则仅安装配置和登录启用")
	command.Flags().BoolVar(&asJSON, "json", false, "输出当前平台的原始schema_version=1状态")
	_ = command.MarkFlagRequired("binary")
	_ = command.MarkFlagRequired("devices")
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
		Use: "status", Short: "macOS/Linux：分别检查磁盘配置、加载/活动与登录启用，不表示USB在线",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return management.run(cmd, asJSON, userServiceAction{kind: serviceStatus, configOnly: configOnly})
		},
	}
	command.Flags().BoolVar(&configOnly, "config-only", false, "只读磁盘配置，完全不调用launchctl或systemctl")
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
	darwin func() (userServiceBackend[service.UserStatus], error)
	linux  func() (userServiceBackend[systemduser.Status], error)
}

type userServiceRuntime struct {
	goos      string
	factories userServiceFactories
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

var errUserServiceUnsupported = errors.New("用户服务自动管理只支持macOS和Linux普通用户；当前平台可使用service render，不会运行服务管理器或自动提权")

func (management userServiceRuntime) run(cmd *cobra.Command, asJSON bool, action userServiceAction) error {
	switch management.goos {
	case "darwin":
		return runUserServiceBackend(cmd, asJSON, action, management.factories.darwin, service.WriteUserStatus)
	case "linux":
		return runUserServiceBackend(cmd, asJSON, action, management.factories.linux, writeSystemdUserStatus)
	default:
		return errUserServiceUnsupported
	}
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
