package cmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/spf13/cobra"
)

func newDeviceCommand() *cobra.Command {
	group := &cobra.Command{Use: "device", Short: "Manage patterns stored on a physical blink(1)"}
	options := new(sequenceControlFlags)
	var asJSON bool
	reset := &cobra.Command{
		Use:   "reset SERIAL [off|rainbow|starlight|fireflies]",
		Short: "Replace the device's Flash pattern (default: off)",
		Long: "Replace every persistent pattern row on the selected device with a built-in preset. " +
			"Off keeps both LEDs dark during pattern playback. The daemon verifies the staged rows before requesting one Flash save. " +
			"Power-cycle the device to verify retention.",
		Example: "  nb1 device reset 20000001\n  nb1 device reset 20000001 rainbow --json",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			serial := args[0]
			if _, err := hex.DecodeString(serial); len(serial) != 8 || err != nil {
				return fmt.Errorf("provide the device's 8-digit hexadecimal serial (see nb1 control devices)")
			}
			if cmd.Flags().Changed("device") {
				target, _ := cmd.Flags().GetString("device")
				if target != serial {
					return fmt.Errorf("--device must match the reset target %s", serial)
				}
			}
			preset := flashreset.Default
			if len(args) == 2 {
				preset = args[1]
			}
			if !flashreset.Valid(preset) {
				return fmt.Errorf("unknown Flash preset %q; choose off, rainbow, starlight or fireflies", preset)
			}
			if err := options.configureConnection(cmd); err != nil {
				return err
			}
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				result, err := client.ResetFlash(ctx, serial, preset)
				if asJSON && result.Status != "" {
					if printErr := printControlJSON(cmd.OutOrStdout(), result); printErr != nil {
						return printErr
					}
				}
				if err != nil || asJSON {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s · %s: Flash save acknowledged (%d rows verified in RAM). Power-cycle to verify retention.\n", serial, preset, result.Verified)
				return err
			})
		},
	}
	reset.Flags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	reset.Flags().StringVar(&options.tokenFile, "token-file", "", "private Bearer token file for the local API")
	reset.Flags().DurationVar(&options.timeout, "timeout", 30*time.Second, "daemon request timeout")
	reset.Flags().BoolVar(&asJSON, "json", false, "print the device's Flash reset result as JSON")
	group.AddCommand(reset)
	return group
}

func init() { rootCmd.AddCommand(newDeviceCommand()) }
