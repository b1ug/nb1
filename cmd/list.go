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
	Short:   "List blink(1) devices with details",
	Long: hdoc(`
		List all attached blink(1) devices with their path, vendor id, product id, version/release number,
		manufacturer, product name, serial number, firmware version (optional), input report size, output 
		report size and feature report size. The information will be printed in a table for each device.
		
		If no blink(1) devices are found, a message will be printed indicating that no devices were found.
	`),
	RunE: func(cmd *cobra.Command, args []string) error {
		dis, err := hdwr.ListDeviceDetails(showFirmwareVersion, showAllHIDDevices)
		if err != nil {
			return err
		}

		// for no devices found
		if len(dis) == 0 {
			log.Debugw("no blink(1) devices found")
			fmt.Println("No blink(1) devices found.")
			return nil
		}

		// print device list
		log.Debugw("blink(1) devices found", "count", len(dis))
		if showFirmwareVersion {
			_ = tui.PrintDeviceListWithFirmware(dis)
		} else {
			_ = tui.PrintDeviceList(dis)
		}
		return nil
	},
}

var (
	showFirmwareVersion bool
	showAllHIDDevices   bool
)

func init() {
	rootCmd.AddCommand(listCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// listCmd.PersistentFlags().String("foo", "", "A help for foo")
	listCmd.Flags().BoolVarP(&showFirmwareVersion, "firmware", "f", false, "show firmware version")
	listCmd.Flags().BoolVarP(&showAllHIDDevices, "all", "a", false, "show all HID devices as well")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// listCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
