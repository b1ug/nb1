package cmd

import (
	"github.com/b1ug/nb1/hdwr"
	"github.com/spf13/cobra"
)

// offCmd represents the turn command
var offCmd = &cobra.Command{
	Use:     "off",
	Aliases: aliasesOff,
	Short:   "Turn blink(1) off and stop playing",
	Long: hdoc(`
		Turn blink(1) black and stop playing any patterns immediately.
	`),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		return hdwr.StopPlaying()
	},
}

func init() {
	rootCmd.AddCommand(offCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// offCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// offCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
