package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// playgroundCmd represents the playground command
var playgroundCmd = &cobra.Command{
	Use:     "playground",
	Aliases: aliasesPlayground,
	Short:   "Playground blink(1) into a given color",
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
	rootCmd.AddCommand(playgroundCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// playgroundCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// playgroundCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
