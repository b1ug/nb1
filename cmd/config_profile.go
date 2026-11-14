package cmd

import (
	"fmt"
	"os"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/spf13/cobra"
)

func addProfileCommands(parent *cobra.Command) {
	parent.AddCommand(&cobra.Command{Use: "path", Short: "Show the selected user configuration path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, err := applicationConfigSelection(cmd)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), selected.Path)
			return err
		}})
	parent.AddCommand(&cobra.Command{Use: "init", Short: "Create a private configuration template without overwriting a file", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, err := profileSelection(cmd)
			if err != nil {
				return err
			}
			if err := appconfig.Init(selected.Path); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), selected.Path)
			return err
		}})
	parent.AddCommand(&cobra.Command{Use: "show", Short: "Show effective file settings and defaults without accessing USB", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, selected, err := applicationSettings(cmd)
			if err != nil {
				return err
			}
			device, err := preferredSerial(cmd, settings)
			if err != nil {
				return err
			}
			settings.Server.Listen, err = serverAddress(cmd, settings.Server.Listen)
			if err != nil {
				return err
			}
			if value := os.Getenv("NB1_LIBRARY_DIR"); value != "" {
				settings.LibraryDir = value
			}
			if err := appconfig.Validate(settings); err != nil {
				return err
			}
			locks, err := ownership.DefaultDir()
			if err != nil {
				return err
			}
			if value := os.Getenv("NB1_LOCK_DIR"); value != "" {
				locks = value
			}
			return printControlJSON(cmd.OutOrStdout(), map[string]interface{}{
				"config_file": selected.Path, "config_dir": selected.BaseDir,
				"device": device, "server": settings.Server, "library_dir": settings.LibraryDir,
				"device_lock_dir": locks,
				"jobs":            map[string]interface{}{"terminal_limit": settings.Jobs.TerminalLimit, "terminal_ttl": settings.Jobs.TerminalTTL.String()},
			})
		}})
}
