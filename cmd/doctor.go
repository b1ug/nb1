package cmd

import (
	"fmt"

	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var endpoint, serial string
	var asJSON bool
	command := &cobra.Command{
		Use: "doctor", Short: "只读检查构建版本、daemon与已配置设备快照",
		Long:    "在3秒总预算内读取本机二进制构建信息及本地daemon的health/v1设备列表。不会扫描或打开USB，不设色、不提交任务、不安装服务、不自动回退直控。未指定--serial时显示daemon配置的全部目标。HTTP响应不代表设备在线；observed是带时间戳的已有快照。",
		Args:    cobra.NoArgs,
		Example: "  nb1 doctor\n  nb1 doctor --serial 20005601 --json\n  nb1 doctor --endpoint http://127.0.0.1:8932",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// gid.Supported is a compile-time backend availability check, not
			// enumeration or Open. HTTP diagnostics work even in CGO=0 builds.
			report, err := doctor.Collect(cmd.Context(), doctor.Options{Endpoint: endpoint, Serial: serial}, doctor.ReadBuild(gid.Supported()))
			if err != nil {
				return err
			}
			if asJSON {
				err = printControlJSON(cmd.OutOrStdout(), report)
			} else {
				err = doctor.WriteText(cmd.OutOrStdout(), report)
			}
			if err != nil {
				return err
			}
			if report.Status != "ok" {
				return fmt.Errorf("诊断发现待检查项目；请查看issues及建议")
			}
			return nil
		},
	}
	command.Flags().StringVar(&endpoint, "endpoint", daemonclient.DefaultEndpoint, "本地loopback HTTP服务地址")
	command.Flags().StringVar(&serial, "serial", "", "只检查这个明确序列号；省略时使用daemon配置目标")
	command.Flags().BoolVar(&asJSON, "json", false, "输出schema_version=1的JSON；attention时退出码为1")
	return command
}

func init() { rootCmd.AddCommand(newDoctorCommand()) }
