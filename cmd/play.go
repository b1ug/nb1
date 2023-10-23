package cmd

import (
	"strconv"
	"strings"

	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// playCmd represents the play command
var playCmd = &cobra.Command{
	Use:     "play <start-end>",
	Aliases: aliasesPlay,
	Short:   "Play pattern stored in blink(1)",
	Long: hdoc(`
		Send a command to blink(1) to play a pattern stored on a blink(1) device.
		Default start-end is 0-0, which means play the whole pattern stored on the device.
		You can specify a start-end range to play a sub-pattern, and you can preview the pattern by using the --preview flag.

		The following pattern ranges are supported:
		  - 0 or 0-0: play the whole pattern stored on the device
		  - 5: play the sub-pattern at index 5 until the end
		  - 5-10: play the sub-pattern from index 5 to 10
	`),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse sub-pattern range
		if err := getPatternPosArgs(cmd, args); err != nil {
			return err
		}

		// preview full pattern
		if playPreviewPattern {
			seq, err := hdwr.ReadStateSequence()
			if err != nil {
				return err
			}
			_ = util.PrintStateSequence(seq)
		}

		// TODO: handle Ctrl+C to stop playing

		// play and wait
		if waitComplete {
			log.Infow("start playing pattern and wait", "start", patternStartPos, "end", patternEndPos, "repeat", playRepeatTimes)
			if err := hdwr.StartPlayPattern(patternStartPos, patternEndPos, playRepeatTimes, true); err != nil {
				return err
			}
			return hdwr.StopPlaying()
		}

		log.Infow("start playing pattern", "start", patternStartPos, "end", patternEndPos, "repeat", playRepeatTimes)
		return hdwr.StartPlayPattern(patternStartPos, patternEndPos, playRepeatTimes, false)
	},
}

var (
	playPreviewPattern bool
	playRepeatTimes    int
)

func init() {
	rootCmd.AddCommand(playCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// playCmd.PersistentFlags().String("foo", "", "A help for foo")
	playCmd.PersistentFlags().BoolVarP(&playPreviewPattern, "preview", "p", false, "Load and preview the pattern stored on the blink(1) device")
	playCmd.PersistentFlags().IntVarP(&playRepeatTimes, "times", "t", 1, "Pattern repeat times (0 means forever)")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// playCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

var (
	patternStartPos int
	patternEndPos   int
)

func getPatternPosArgs(cmd *cobra.Command, args []string) error {
	// default start-end is 0-0
	if len(args) == 0 {
		patternStartPos = 0
		patternEndPos = 0
		return nil
	}

	// split by "-"
	var err error
	as := strings.SplitN(args[0], "-", 2)
	if len(as) == 1 {
		// only start
		if patternStartPos, err = strconv.Atoi(as[0]); err != nil {
			return err
		}
		patternEndPos = 0
		return nil
	} else if len(as) == 2 {
		if patternStartPos, err = strconv.Atoi(as[0]); err != nil {
			return err
		}
		if patternEndPos, err = strconv.Atoi(as[1]); err != nil {
			return err
		}
	}
	return nil
}
