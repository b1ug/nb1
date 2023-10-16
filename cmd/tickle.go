package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// tickleCmd represents the tickle command
var tickleCmd = &cobra.Command{
	Use:     "tickle",
	Aliases: aliasesTickle,
	Short:   "Tickle blink(1) into a given color",
	Long: hdoc(`
		Perform a specific color changing action on a blink(1) device.
		// TODO:
	`),
	Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {

		// TODO:
		return fmt.Errorf("not implemented")
	},
}

func init() {
	rootCmd.AddCommand(tickleCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// tickleCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// tickleCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
