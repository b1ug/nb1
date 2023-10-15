package cmd

import (
	"fmt"

	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/tui"
	"github.com/spf13/cobra"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: aliasesList,
	Short:   "List blink(1) devices",
	Long: hdoc(`
		List all attached blink(1) devices with detailed information.

		Their path, vendor id, product id, version/release number, manufacturer, product name, 
		serial number, input report size, output report size and feature report size will be
		printed in table for each device.
	`),
	RunE: func(cmd *cobra.Command, args []string) error {
		dis := hdwr.ListAllBlink1()
		if len(dis) == 0 {
			log.Debugw("no blink(1) devices found")
			fmt.Println("No blink(1) devices found.")
			return nil
		}

		// print device list
		log.Debugw("blink(1) devices found", "count", len(dis))
		tui.PrintDeviceList(dis)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// listCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// listCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
