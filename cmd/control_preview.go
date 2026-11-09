package cmd

import (
	"fmt"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/spf13/cobra"
)

func newControlCapabilitiesCommand(options *controlFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "capabilities",
		Short:   "查看软件编译能力目录，不打开设备",
		Long:    "默认读取 daemon 的软件能力目录。显式 --mode direct 返回本地编译器目录，不发现设备、不获取设备锁、不打开 USB。目录不证明设备在线或支持某项硬件功能。旧 daemon 不支持新接口时明确报错，不回退直控。",
		Example: "  nb1 control capabilities\n  nb1 control --mode direct capabilities",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			if options.mode == "direct" {
				return printControlJSON(cmd.OutOrStdout(), control.Capabilities())
			}
			client, ctx, done, err := options.client(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			catalog, err := client.Capabilities(ctx)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), catalog)
		},
	}
}

func newControlPreviewCommand(options *controlFlags) *cobra.Command {
	var programPath, effectPath, serial string
	command := &cobra.Command{
		Use:     "preview --program FILE | --effect FILE [--serial SERIAL]",
		Short:   "权威预览 Program 或 Effect，不创建 Job",
		Long:    "必须且只能指定一个 Program 或 Effect 文件。默认由 daemon 校验并编译，返回规范化 Program、名义时长和限制；可选 --serial 只核对缓存设备能力，不证明执行或准入成功。显式 --mode direct 使用同一纯软件编译器，不连接 daemon、不发现设备、不获取设备锁、不打开 USB；direct 不接受 --serial。预览不接受 policy 或请求回执，不创建/取消 Job。旧 daemon 不支持时不回退。",
		Example: "  nb1 control preview --program program.json\n  nb1 control preview --effect pulse.effect.json\n  nb1 control preview --program program.json --serial 20005601\n  nb1 control preview --effect pulse.effect.json --serial 20005601\n  nb1 control --mode direct preview --effect pulse.effect.json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			programFlag, effectFlag := cmd.Flags().Changed("program"), cmd.Flags().Changed("effect")
			if programFlag == effectFlag || programFlag && programPath == "" || effectFlag && effectPath == "" {
				return fmt.Errorf("preview requires exactly one nonempty --program FILE or --effect FILE")
			}
			serialFlag := cmd.Flags().Changed("serial")
			if options.mode == "direct" && serialFlag {
				return fmt.Errorf("--serial device validation requires the daemon; direct preview is software-only")
			}
			if serialFlag && !control.ValidDeviceSerial(serial) {
				return fmt.Errorf("--serial must be 1–128 ASCII letters, digits, hyphens or underscores")
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			var input httpapi.PreviewRequest
			if programFlag {
				program, err := readControlProgram(programPath)
				if err != nil {
					return err
				}
				input.Program = &program
			} else {
				effect, err := readControlEffect(effectPath)
				if err != nil {
					return err
				}
				input.Effect = &effect
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			if serialFlag {
				input.Serial = &serial
			}
			if options.mode == "direct" {
				result, err := control.Preview(input.Program, input.Effect)
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), result)
			}
			client, ctx, done, err := options.client(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			result, err := client.Preview(ctx, input)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		},
	}
	command.Flags().StringVar(&programPath, "program", "", "strict Program JSON file; mutually exclusive with --effect")
	command.Flags().StringVar(&effectPath, "effect", "", "strict steady/pulse Effect JSON file; mutually exclusive with --program")
	command.Flags().StringVar(&serial, "serial", "", "optional daemon-configured device serial for cached capability validation")
	return command
}
