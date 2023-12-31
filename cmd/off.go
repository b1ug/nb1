package cmd

import (
	"fmt"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
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
	Args:              cobra.NoArgs,
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Turn to", util.FormatNamedColor(b1.ColorBlack))
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
