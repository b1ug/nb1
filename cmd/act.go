package cmd

import (
	"github.com/spf13/cobra"
)

// actCmd represents the act command
var actCmd = &cobra.Command{
	Use:     "act",
	Aliases: aliasesAct,
	Short:   "Perform action on blink(1) device",
	Long: hdoc(`
		Perform a specific color changing action on a blink(1) device.
		The action can be described in natural language, and should contain color, duration and led number.
		Only one action can be performed at a time.
	`),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func init() {
	rootCmd.AddCommand(actCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// actCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// actCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
