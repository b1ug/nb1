package cmd

import (
	"fmt"
	"image/color"
	"time"

	"github.com/1set/gut/ystring"
	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// blinkCmd represents the turn command
var blinkCmd = &cobra.Command{
	Use:     "blink <color>...",
	Aliases: aliasesBlink,
	Short:   "Blink a blink(1) device to given color",
	Long: hdocf(`
		Blink a blink(1) device to given color (or random color) for a given times.
		The colors should be given as arguments, and can be specified as a hex color code, a color name, or a preset color name.
		For multiple colors, the blink(1) will blink to each color in order.
		  e.g. <color1> <off> <color2> <off> <color3> <off> ... <color1> <off> <color2> <off> 
		
		Special colors:
		  %s

		Supported preset colors:
		  %s
	`,
		util.JoinWrapSlice([]string{"random", "on"}, ", ", 100),
		util.JoinWrapSlice(b1.GetColorNames(), ", ", 100)),
	//Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// clean up args
		var colorRaw []string
		for _, arg := range args {
			if ystring.IsNotBlank(arg) {
				colorRaw = append(colorRaw, arg)
			}
		}
		if len(colorRaw) == 0 {
			// default blink to white
			colorRaw = []string{"white"}
		}
		if blinkTimeDur < 10*time.Millisecond {
			// 10ms is the minimum
			blinkTimeDur = 10 * time.Millisecond
		}
		if blinkTimes == 0 {
			blinkTimes = 5
		}
		log.Debugw("raw colors to blink blink(1)", "count", len(colorRaw), "colors", colorRaw, "interval", blinkTimeDur, "led", blinkLedNum, "times", blinkTimes)

		// set color now
		setColorNow := func(index int, cl color.Color) error {
			// set state now
			led := b1.LEDIndex(blinkLedNum)
			st := b1.NewLightState(cl, 0, led)
			log.Debugw("set blink(1) state now", "index", index, "state", st)
			if err := hdwr.PlayState(st); err != nil {
				//log.Warnw("failed to set blink(1) state", "state", st, zap.Error(err))
				return err
			}

			// print state
			fmt.Printf("#%d: Set %s to Color: %s\n", index+1, led, util.FormatNamedColor(cl))

			// wait for next blink
			time.Sleep(blinkTimeDur)

			return nil
		}

		// let blink(1) blink
		for i := 0; i < int(blinkTimes); i++ {
			// parse color
			raw := colorRaw[i%len(colorRaw)]
			cl, err := exchange.ParseColor(raw)
			if err != nil {
				return fmt.Errorf("%w: %q", err, raw)
			}

			// can't be off
			if cl == b1.ColorBlack {
				return fmt.Errorf("blink(1) can't blink to off")
			}

			// blink on
			if err := setColorNow(i, cl); err != nil {
				return err
			}

			// blink off
			if err := setColorNow(i, b1.ColorBlack); err != nil {
				return err
			}
		}

		// stop playing
		// TODO: handle Ctrl+C
		return hdwr.StopPlaying()
	},
}

var (
	blinkTimeDur time.Duration
	blinkLedNum  uint
	blinkTimes   uint
)

func init() {
	rootCmd.AddCommand(blinkCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// blinkCmd.PersistentFlags().String("foo", "", "A help for foo")
	blinkCmd.PersistentFlags().DurationVarP(&blinkTimeDur, "blink-time", "m", 200*time.Millisecond, "duration of blink on/off")
	blinkCmd.PersistentFlags().UintVarP(&blinkLedNum, "led", "l", 0, "which led number to blink, 0=all/1=top/2=bottom (mk2+)")
	blinkCmd.PersistentFlags().UintVarP(&blinkTimes, "times", "t", 5, "how many times to blink")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// blinkCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
