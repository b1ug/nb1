package cmd

import (
	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// writeCmd represents the write command
var writeCmd = &cobra.Command{
	Use:     "write <file>",
	Aliases: aliasesWrite,
	Short:   "Write pattern to blink(1) device",
	Long: hdoc(`
		Write given pattern to the blink(1) device.

		Use --reset flag to erase the pattern stored on the blink(1) device before writing the pattern.
		Use --flash flag to write the pattern to the Flash memory as well, otherwise the pattern is written to the RAM only.
	`),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// load and parse pattern file if given
		var (
			didAct bool
			ps     *schema.PatternSet
			err    error
		)
		if len(args) > 0 {
			ps, err = exchange.LoadPatternFile(args[0])
			if err != nil {
				return err
			}
		}

		// preview pattern if requested
		if writePreviewPattern && ps != nil {
			_ = util.PrintPatternSet(ps)
			didAct = true
		}

		// reset blink(1) device if requested
		if writeResetAll {
			const maxPat = 32
			seq := make(b1.StateSequence, maxPat)
			for i := 0; i < maxPat; i++ {
				seq[i] = b1.LightState{}
			}
			log.Infow("resetting blink(1) device", "sequence", seq)
			if err := hdwr.WriteOnChipPattern(0, 0, seq); err != nil {
				return err
			}
			didAct = true
		}

		// write pattern to blink(1) device
		if ps != nil {
			log.Infow("writing pattern to blink(1) device", "sequence", ps.Sequence, "start", writeStartPos, "end", writeEndPos)
			if err := hdwr.WriteOnChipPattern(writeStartPos, writeEndPos, ps.Sequence); err != nil {
				return err
			}
			didAct = true
		}

		// save pattern to flash memory if requested
		if writeSaveFlash {
			log.Infow("saving current pattern to flash memory")
			if err := hdwr.SaveOnChipPattern(); err != nil {
				return err
			}
			didAct = true
		}

		// return errors if no action was taken
		if !didAct {
			return errNoAction
		}
		return nil
	},
}

var (
	writePreviewPattern bool
	writeSaveFlash      bool
	writeResetAll       bool
	writeStartPos       int
	writeEndPos         int
)

func init() {
	rootCmd.AddCommand(writeCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// writeCmd.PersistentFlags().String("foo", "", "A help for foo")
	writeCmd.PersistentFlags().BoolVarP(&writeSaveFlash, "flash", "f", false, "Save the pattern to the Flash memory as well")
	writeCmd.PersistentFlags().BoolVarP(&writeResetAll, "reset", "r", false, "Reset the blink(1) device before writing the pattern")
	writeCmd.PersistentFlags().BoolVarP(&writePreviewPattern, "preview", "p", false, "Preview the pattern to be written")
	writeCmd.PersistentFlags().IntVarP(&writeStartPos, "start", "s", 0, "Start position of the pattern to be written")
	writeCmd.PersistentFlags().IntVarP(&writeEndPos, "end", "e", 0, "End position of the pattern to be written")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// writeCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
