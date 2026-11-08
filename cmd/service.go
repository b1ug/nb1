package cmd

import (
	"fmt"

	"github.com/b1ug/nb1/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "service", Short: "Render per-user startup configurations for review",
		Long: "Generate native launchd, systemd-user or Windows logon-task configuration. No configuration is installed, registered or started automatically.",
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
	return command
}

func init() {
	rootCmd.AddCommand(newServiceCommand())
}
