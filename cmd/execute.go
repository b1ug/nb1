package cmd

import (
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// executeCmd represents the execute command
var executeCmd = &cobra.Command{
	Use:     "execute",
	Aliases: aliasesExecute,
	Short:   "Execute pattern files",
	Long: hdoc(`
		Load pattern files and execute them by playing the patterns.

		Supported formats:
		  - Play Text (e.g. "red blink 3 times")
		  - Pattern JSON (e.g. '{"repeat":1,"seq":"#FF0000L0T1500;#FF0000L0T3500"...}')
	`),
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// load and parse pattern file
		ps, err := exchange.LoadPatternFile(args[0])
		if err != nil {
			return err
		}

		// preview
		if execPreviewPattern {
			_ = util.PrintPatternSet(ps)
		}

		// execute
		if times := int(ps.RepeatTimes); times == 0 {
			log.Debugw("executing pattern set forever", "pattern", ps, "times", times)
			idx := 0
			for {
				log.Debugw("playing state sequence", "index", idx, "length", len(ps.Sequence))
				if err := hdwr.PlayStateSequence(ps.Sequence); err != nil {
					return err
				}
				idx++
			}
		} else {
			log.Debugw("executing pattern set for limited times", "pattern", ps, "times", times)
			for idx := 0; idx < times; idx++ {
				log.Debugw("playing state sequence", "index", idx, "length", len(ps.Sequence))
				if err := hdwr.PlayStateSequence(ps.Sequence); err != nil {
					return err
				}
			}
		}

		// TODO: handle Ctrl+C
		return hdwr.StopPlaying()
	},
}

var (
	execPreviewPattern bool
)

func init() {
	rootCmd.AddCommand(executeCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// executeCmd.PersistentFlags().String("foo", "", "A help for foo")
	executeCmd.PersistentFlags().BoolVarP(&execPreviewPattern, "preview", "p", false, "Preview the pattern to be executed")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// executeCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
