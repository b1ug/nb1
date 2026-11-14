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
		Short:   "Normalize a color to exact Hex6 without playing a light action",
		Long:    "Pass a name or hex color as one argument, or use --file for a bare JSON ColorInput such as RGB8 or HSV. Choose exactly one input. The daemon normalizes colors by default. Explicit --mode direct uses the same pure function without contacting a daemon, discovering devices, acquiring device ownership or opening USB. Normalization applies no implicit gamma or brightness correction and does not verify physical appearance. This command accepts no serial, policy or request ticket. An unsupported daemon returns an error without falling back to direct control. Existing Program, Effect and set Hex6 contracts retain their semantics.",
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
	command.Flags().StringVar(&path, "file", "", "bare JSON ColorInput file, without an HTTP color envelope; mutually exclusive with INPUT")
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
