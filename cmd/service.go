package cmd

import (
	"context"
	"fmt"

	"github.com/b1ug/nb1/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "service", Short: "Render startup configurations or manage the current macOS user's agent",
		Long: "service render generates reviewable launchd, systemd-user or Windows logon-task configuration without installation. macOS install/status/uninstall manage only the current ordinary user's fixed org.b1ug.nb1 LaunchAgent. Installing its plist may enable automatic loading at the next GUI login; --start is required to bootstrap immediately. No sudo or system-wide service management.",
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
	command.AddCommand(newServiceInstallCommand(), newServiceStatusCommand(), newServiceUninstallCommand())
	return command
}

func newServiceInstallCommand() *cobra.Command {
	var config service.Config
	var start, asJSON bool
	command := &cobra.Command{
		Use: "install", Short: "macOS：安装托管用户plist，下次图形登录可能自动加载",
		Long:    "默认只读查询launchd归属后写当前用户LaunchAgents配置，不请求立即加载；下次图形登录可能自动加载。--start才请求立即bootstrap。查询异常、已有外部同名注册项、未知或修改后的plist均拒绝安装；现有配置不同也拒绝升级替换。二进制必须是当前机器上已存在的绝对可执行路径，目标设备必须明确指定。不安装系统LaunchDaemon，不停止已有手工daemon。",
		Args:    cobra.NoArgs,
		Example: "  nb1 service install --binary /absolute/path/nb1 --devices 20005601\n  nb1 service install --binary /absolute/path/nb1 --devices 20005601 --start",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserService(cmd, asJSON, func(ctx context.Context, manager *service.UserManager) (service.UserStatus, error) {
				return manager.Install(ctx, config, start)
			})
		},
	}
	command.Flags().StringVar(&config.Binary, "binary", "", "已存在的绝对可执行文件路径，不使用PATH搜索")
	command.Flags().StringSliceVar(&config.Devices, "devices", nil, "明确的8位十六进制设备序列号列表")
	command.Flags().StringVar(&config.Listen, "listen", "127.0.0.1:8931", "固定loopback host:port")
	command.Flags().StringVar(&config.LockDir, "lock-dir", "", "可选明确的绝对设备锁目录；不收集环境变量")
	command.Flags().BoolVar(&start, "start", false, "显式请求立即bootstrap；否则仅安装磁盘配置")
	command.Flags().BoolVar(&asJSON, "json", false, "输出schema_version=1的配置/注册状态")
	_ = command.MarkFlagRequired("binary")
	_ = command.MarkFlagRequired("devices")
	return command
}

func newServiceStatusCommand() *cobra.Command {
	var configOnly, asJSON bool
	command := &cobra.Command{
		Use: "status", Short: "macOS：分别检查磁盘配置与launchd注册，不表示硬件在线",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserService(cmd, asJSON, func(ctx context.Context, manager *service.UserManager) (service.UserStatus, error) {
				return manager.Status(ctx, configOnly)
			})
		},
	}
	command.Flags().BoolVar(&configOnly, "config-only", false, "只读磁盘配置，完全不调用launchctl")
	command.Flags().BoolVar(&asJSON, "json", false, "输出schema_version=1的配置/注册状态")
	return command
}

func newServiceUninstallCommand() *cobra.Command {
	var stop, asJSON bool
	command := &cobra.Command{
		Use: "uninstall", Short: "macOS：确认注册归属后卸载；已注册项须显式--stop",
		Long: "始终先查询launchd。只有托管配置完整、当前注册path/program均精确匹配时，--stop才请求bootout该单一label。未知归属、查询失败、停止结果不明时保留文件，不强杀、不删除持久锁、不接管未知plist。注册移除不代表设备清理已完成。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserService(cmd, asJSON, func(ctx context.Context, manager *service.UserManager) (service.UserStatus, error) {
				return manager.Uninstall(ctx, stop)
			})
		},
	}
	command.Flags().BoolVar(&stop, "stop", false, "明确允许bootout归属已核对的注册项；否则已注册时拒绝卸载")
	command.Flags().BoolVar(&asJSON, "json", false, "输出schema_version=1的配置/注册状态")
	return command
}

func runUserService(cmd *cobra.Command, asJSON bool, action func(context.Context, *service.UserManager) (service.UserStatus, error)) error {
	manager, err := service.NewUserManager()
	if err != nil {
		return err
	}
	status, actionErr := action(cmd.Context(), manager)
	if asJSON {
		err = printControlJSON(cmd.OutOrStdout(), status)
	} else {
		err = service.WriteUserStatus(cmd.OutOrStdout(), status)
	}
	if err != nil {
		return err
	}
	return actionErr
}

func init() {
	rootCmd.AddCommand(newServiceCommand())
}
