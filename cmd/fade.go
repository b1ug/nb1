package cmd

import (
	"fmt"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// fadeCmd represents the turn command
var fadeCmd = &cobra.Command{
	Use:     "fade",
	Aliases: aliasesFade,
	Short:   "Fade blink(1) into a given color",
	Long: hdocf(`
		Perform a specific color changing action on a blink(1) device.
		
		Special colors:
		  %s

		Supported preset colors:
		  %s
	`,
		util.JoinWrapSlice([]string{"random", "off", "on"}, ", ", 100),
		util.JoinWrapSlice(b1.GetColorNames(), ", ", 100)),
	Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse query as color
		query := util.NormalizeQuery(args...)
		cl, err := exchange.ParseColor(query)
		if err != nil {
			return err
		}

		// build state
		st := b1.NewLightState(cl, fadeTimeDur, b1.LEDIndex(fadeLedNum))
		log.Debugw("parsed blink(1) state", "state", st)

		// perform action
		fmt.Println("Fade to State:", util.FormatLightState(st))
		if waitComplete {
			return hdwr.PlayStateAndWait(st)
		}
		return hdwr.PlayState(st)
	},
}

var (
	fadeTimeDur time.Duration
	fadeLedNum  uint
)

func init() {
	rootCmd.AddCommand(fadeCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// fadeCmd.PersistentFlags().String("foo", "", "A help for foo")
	fadeCmd.PersistentFlags().DurationVarP(&fadeTimeDur, "fade-time", "m", 300*time.Millisecond, "duration of fade")
	fadeCmd.PersistentFlags().UintVarP(&fadeLedNum, "led", "l", 0, "which led number to fade, 0=all/1=top/2=bottom (mk2+)")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// fadeCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
