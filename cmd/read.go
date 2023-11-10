package cmd

import (
	"github.com/spf13/cobra"
)

// readCmd represents the read command
var readCmd = &cobra.Command{
	Use:     "read",
	Aliases: aliasesRead,
	Short:   "Read from a blink(1) device",
	Long: hdoc(`
		Perform a specific color changing action on a blink(1) device.
		// TODO:
	`),
	PersistentPreRunE:  openBlink1Device,
	PersistentPostRunE: saveResultData,
}

var (
	readPreviewResult bool
)

func init() {
	rootCmd.AddCommand(readCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// readCmd.PersistentFlags().String("foo", "", "A help for foo")
	readCmd.PersistentFlags().BoolVarP(&readPreviewResult, "preview", "p", true, "whether to preview the result")
	readCmd.PersistentFlags().StringVarP(&outputJSONPath, "json", "j", "", "output JSON file path")
	readCmd.PersistentFlags().StringVarP(&outputTextPath, "text", "t", "", "output Text file path")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// readCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
