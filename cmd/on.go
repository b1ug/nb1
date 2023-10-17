package cmd

import (
	"fmt"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/tui"
	"github.com/spf13/cobra"
)

// onCmd represents the turn command
var onCmd = &cobra.Command{
	Use:     "on",
	Aliases: aliasesOn,
	Short:   "Turn blink(1) full-on white",
	Long: hdoc(`
		Turn blink(1) full-on white immediately.
	`),
	Args:              cobra.NoArgs,
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		cl := b1.ColorWhite
		fmt.Println("Turn to", tui.FormatNamedColor(cl))
		return hdwr.SetColor(cl)
	},
}

func init() {
	rootCmd.AddCommand(onCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// onCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// onCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
