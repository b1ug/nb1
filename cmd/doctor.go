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
		Use: "doctor", Short: "Inspect build information, daemon health and configured device snapshots",
		Long:    "Read the local binary's build information and the local daemon's health and v1 device list within a three-second total budget. This read-only check does not scan or open USB, set colors, submit Jobs, install services or fall back to direct control. Without --serial, show all daemon-configured devices. An HTTP response does not establish that a device is online; observed contains an existing snapshot with a timestamp.",
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
				return fmt.Errorf("diagnostics need attention; review the issues and suggested actions")
			}
			return nil
		},
	}
	command.Flags().StringVar(&endpoint, "endpoint", daemonclient.DefaultEndpoint, "local loopback HTTP service address")
	command.Flags().StringVar(&serial, "serial", "", "inspect only this exact serial; default: all daemon-configured devices")
	command.Flags().BoolVar(&asJSON, "json", false, "print schema_version=1 JSON; exit with status 1 when attention is required")
	return command
}

func init() { rootCmd.AddCommand(newDoctorCommand()) }
