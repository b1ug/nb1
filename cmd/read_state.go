package cmd

import (
	"github.com/spf13/cobra"
)

// readStateCmd represents the state command
var readStateCmd = &cobra.Command{
	Use:     "state",
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
	readCmd.AddCommand(readStateCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// readStateCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// readStateCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
