package cmd

import (
	"fmt"
	"image/color"

	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// readStateCmd represents the state command
var readStateCmd = &cobra.Command{
	Use:     "state",
	Aliases: aliasesState,
	Short:   "Read the state of LED",
	Long: hdoc(`
		Read the current state of LED from a blink(1) device.
	`),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// read
		var (
			lc1 color.Color
			lc2 color.Color
			err error
		)
		switch readStateLedNum {
		case 0:
			log.Debugw("read all led state")
			if lc1, err = hdwr.ReadLEDColor(1); err != nil {
				return err
			}
			if lc2, err = hdwr.ReadLEDColor(2); err != nil {
				return err
			}
		case 1:
			log.Debugw("read top led state")
			if lc1, err = hdwr.ReadLEDColor(1); err != nil {
				return err
			}
		case 2:
			log.Debugw("read bottom led state")
			if lc2, err = hdwr.ReadLEDColor(2); err != nil {
				return err
			}
		}

		// handle result
		jm := make(map[string]interface{})
		saveTextLine = make([]string, 0)
		outputLEDColor := func(ledNum uint, lc color.Color) {
			if lc != nil {
				ln := fmt.Sprintf("LED%d", ledNum)
				cn := util.ConvColorToHex(lc)
				jm[ln] = cn
				saveTextLine = append(saveTextLine, ln+": "+cn)
				if readPreviewResult {
					fmt.Println(ln+":", util.FormatNamedColor(lc))
				}
			}
		}
		outputLEDColor(1, lc1)
		outputLEDColor(2, lc2)
		saveJSONData = jm

		return nil
	},
}

var (
	readStateLedNum uint
)

func init() {
	readCmd.AddCommand(readStateCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// readStateCmd.PersistentFlags().String("foo", "", "A help for foo")
	readStateCmd.PersistentFlags().UintVarP(&readStateLedNum, "led", "l", 0, "which led number to read, 0=all/1=top/2=bottom (mk2+)")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// readStateCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
