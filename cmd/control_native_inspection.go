package cmd

import (
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
	"github.com/spf13/cobra"
)

func isNativeInspectionCommand(command *cobra.Command) bool {
	return command.Parent() != nil && command.Parent().Name() == "native-sequence"
}

func nativeInspectionFlags(command *cobra.Command, options *controlFlags) error {
	if options.mode != "daemon" {
		return fmt.Errorf("native-sequence requires --mode daemon; it never opens USB directly")
	}
	for _, name := range []string{"policy", "request-id", "expected-daemon-instance-id", "wait", "direct", "lock-dir"} {
		if command.Flags().Changed(name) {
			return fmt.Errorf("native-sequence does not accept --%s", name)
		}
	}
	return nil
}

func newControlNativeInspectionCommand(options *controlFlags) *cobra.Command {
	parent := &cobra.Command{
		Use: "native-sequence", Short: "Inspect experimental device RAM rows and playback registers through the daemon",
		Long: "Read the daemon-owned device through one experimental native-inspection GET. RAM reads require an explicit range and are sequential, not an atomic snapshot. These commands do not create a Job, request ticket or host Pattern, and never fall back to direct USB. A client timeout does not forcibly cancel an in-flight native call. An incomplete observation is printed as JSON before the command returns an error.",
	}
	var startText, countText string
	ram := &cobra.Command{
		Use: "ram SERIAL", Args: cobra.ExactArgs(1), Short: "Read an explicit range of native RAM rows without changing them",
		Example: "  nb1 control native-sequence ram 20000001 --start-line 0 --line-count 32",
		RunE: func(command *cobra.Command, args []string) error {
			if !command.Flags().Changed("start-line") || !command.Flags().Changed("line-count") {
				return fmt.Errorf("RAM inspection requires explicit --start-line and --line-count")
			}
			start, startOK := nativeCLIInteger(startText)
			count, countOK := nativeCLIInteger(countText)
			if !startOK || !countOK {
				return fmt.Errorf("--start-line and --line-count require unsigned decimal integers")
			}
			return runNativeInspection(command, options, args[0], nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: start, LineCount: count}})
		},
	}
	ram.Flags().StringVar(&startText, "start-line", "", "required absolute RAM start line, decimal 0..31")
	ram.Flags().StringVar(&countText, "line-count", "", "required row count, decimal 1..32 within the RAM range")
	playback := &cobra.Command{
		Use: "playback SERIAL", Args: cobra.ExactArgs(1), Short: "Read native playback registers without starting or stopping playback",
		Example: "  nb1 control native-sequence playback 20000001",
		RunE: func(command *cobra.Command, args []string) error {
			return runNativeInspection(command, options, args[0], nativeinspection.Request{Kind: nativeinspection.Playback})
		},
	}
	parent.AddCommand(ram, playback)
	return parent
}

func nativeCLIInteger(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 31)
	return int(n), err == nil
}

func runNativeInspection(command *cobra.Command, options *controlFlags, serial string, request nativeinspection.Request) error {
	if len(serial) != 8 {
		return fmt.Errorf("native inspection requires an 8-digit hexadecimal device serial")
	}
	if _, err := hex.DecodeString(serial); err != nil {
		return fmt.Errorf("native inspection requires an 8-digit hexadecimal device serial")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	client, ctx, done, err := options.client(command.Context())
	if err != nil {
		return err
	}
	defer done()
	result, inspectionErr := client.InspectNative(ctx, serial, request)
	if result.Contract == nativeinspection.Contract {
		if err := printControlJSON(command.OutOrStdout(), result); err != nil {
			return err
		}
	}
	return inspectionErr
}
