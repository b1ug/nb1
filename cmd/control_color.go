package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/spf13/cobra"
)

func newControlColorCommand(options *controlFlags) *cobra.Command {
	var path string
	command := &cobra.Command{
		Use:     "color INPUT | --file FILE",
		Short:   "归一化颜色输入为精确 Hex6，不执行灯光",
		Long:    "名字或 hex 使用一个位置参数；RGB8、HSV 或其他裸 JSON ColorInput 使用 --file。两者只能选一个。默认由 daemon 归一化；显式 --mode direct 使用同一个纯函数，不连接 daemon、不发现设备、不获取设备锁、不打开 USB。结果无隐式 Gamma 或亮度校正，不能证明实际灯光效果。此命令不接受 serial、policy 或请求票据。旧 daemon 不支持时明确报错，不回退直控；Program、Effect 和 set 的原有 Hex6 合同不变。",
		Example: "  nb1 control color green\n  nb1 control color '#0f8'\n  nb1 control color --file color.json\n  nb1 control --mode direct color lime\n  nb1 control --mode direct color --file hsv.json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fileFlag := cmd.Flags().Changed("file")
			if (len(args) == 1) == fileFlag || fileFlag && path == "" {
				return fmt.Errorf("color requires exactly one INPUT or nonempty --file FILE containing bare JSON ColorInput")
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			var input []byte
			var err error
			if fileFlag {
				input, err = readControlColor(path)
			} else {
				input, err = json.Marshal(args[0])
			}
			if err != nil {
				return err
			}
			result, err := colorinput.ResolveJSON(input)
			if err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			if options.mode == "direct" {
				return printControlJSON(cmd.OutOrStdout(), result)
			}
			client, ctx, done, err := options.client(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			result, err = client.ResolveColor(ctx, json.RawMessage(input))
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		},
	}
	command.Flags().StringVar(&path, "file", "", "裸 JSON ColorInput 文件，不是含 color 字段的 HTTP 信封；与 INPUT 互斥")
	return command
}

func readControlColor(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, colorinput.MaxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > colorinput.MaxRequestBytes {
		return nil, fmt.Errorf("color file exceeds %d bytes", colorinput.MaxRequestBytes)
	}
	return data, nil
}
