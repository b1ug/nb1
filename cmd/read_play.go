package cmd

import (
	"github.com/spf13/cobra"
)

// readPlayCmd represents the play command
var readPlayCmd = &cobra.Command{
	Use:     "play",
	Aliases: aliasesConvert,
	Short:   "A brief description of your command",
	Long: hdoc(`

	`),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO:
		return errNotImplemented
	},
}

func init() {
	readCmd.AddCommand(readPlayCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// readPlayCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// readPlayCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
