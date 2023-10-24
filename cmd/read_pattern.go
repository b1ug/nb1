package cmd

import (
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// readPatternCmd represents the pattern command
var readPatternCmd = &cobra.Command{
	Use:     "pattern",
	Aliases: aliasesPattern,
	Short:   "Read all patterns from the RAM",
	Long: hdoc(`
		Read all patterns from the RAM of a blink(1) device.
	`),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		seq, err := hdwr.ReadOnChipSequence()
		if err != nil {
			return err
		}

		// preview
		if readPreviewResult {
			_ = util.PrintStateSequence(seq)
		}

		// save json result
		ps := schema.PatternSet{
			Name:        "from_device",
			RepeatTimes: 1,
			Sequence:    seq,
		}
		ps.AutoFill()
		saveJSONData = ps

		// save text result
		saveTextLine = make([]string, len(seq))
		for i, s := range seq {
			b, _ := s.MarshalText()
			saveTextLine[i] = string(b)
		}

		return errNotImplemented
	},
}

func init() {
	readCmd.AddCommand(readPatternCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// readPatternCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// readPatternCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
