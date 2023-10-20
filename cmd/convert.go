package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// convertCmd represents the turn command
var convertCmd = &cobra.Command{
	Use:     "convert",
	Aliases: aliasesConvert,
	Short:   "Convert pattern formats",
	Long: hdoc(`
		Convert a pattern from one format to another.
		
		Supported formats:
		  - Play Text (e.g. "red blink 3 times")
		  - Pattern JSON (e.g. '{"repeat":1,"seq":"#FF0000L0T1500;#FF0000L0T3500"...}')
		  - Starlark Script (e.g. 'play(red, blue, green)')
	`),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		//
		return errors.New("not implemented")
	},
}

var (
	convertPreviewPattern bool
)

func init() {
	rootCmd.AddCommand(convertCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// convertCmd.PersistentFlags().String("foo", "", "A help for foo")
	convertCmd.PersistentFlags().BoolVarP(&convertPreviewPattern, "preview", "p", false, "Preview the converted pattern")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// convertCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
