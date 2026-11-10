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
		Short:   "Inspect software compiler capabilities without opening a device",
		Long:    "Read the daemon's software capability catalog by default. Explicit --mode direct returns the local compiler catalog without discovering devices, acquiring device ownership or opening USB. The catalog does not establish that a device is online or supports a hardware operation. An unsupported daemon returns an error without falling back to direct control.",
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
		Short:   "Preview a Program or Effect without creating a Job",
		Long:    "Choose exactly one Program or Effect file. The daemon validates and compiles it by default, returning the normalized Program, nominal duration and limits. Optional --serial checks cached device capabilities; it does not confirm admission or execution. Explicit --mode direct uses the same pure software compiler without contacting a daemon, discovering devices, acquiring device ownership or opening USB; direct preview rejects --serial. Preview accepts no policy or request receipt and creates or cancels no Job. An unsupported daemon returns an error without falling back to direct control.",
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
