package cmd

import (
	"time"

	"github.com/b1ug/nb1/hdwr"
	"github.com/spf13/cobra"
)

// tickleCmd represents the tickle command
var tickleCmd = &cobra.Command{
	Use:     "tickle",
	Aliases: aliasesTickle,
	Short:   "Tickle blink(1) device not to play before the timeout",
	Long: hdoc(`
		Send a command to blink(1) to tickle the device not to play the pattern before the timeout.
		If the next command is sent before the timeout, the current tickle command is cancelled.
		If the next command is not sent before the timeout, the device will play the given pattern.
	`),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse sub-pattern range
		if err := getPatternPosArgs(cmd, args); err != nil {
			return err
		}

		// let's tickle
		log.Infow("tickle server mode on blink(1) device", "start", patternStartPos, "end", patternEndPos, "timeout", tickleTimeout)
		return hdwr.TickleOnChipPattern(patternStartPos, patternEndPos, tickleTimeout)
	},
}

var (
	tickleTimeout time.Duration
)

func init() {
	rootCmd.AddCommand(tickleCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// tickleCmd.PersistentFlags().String("foo", "", "A help for foo")
	tickleCmd.PersistentFlags().DurationVarP(&tickleTimeout, "timeout", "t", 5*time.Second, "Timeout before the device plays the pattern")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// tickleCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
