package util

import (
	"fmt"
	"strings"

	"bitbucket.org/ai69/amoy"
	se "github.com/b1ug/nb1/schema"
	tw "github.com/olekukonko/tablewriter"
)

// PrintPatternSequence prints a sequence of patterns to stdout.
func PrintPatternSequence(ps *se.PatternSet) error {
	var lines [][]string
	for _, p := range ps.Sequence {
		lines = append(lines, []string{FormatNamedColor(p.Color), convLEDEmoji(p.LED), fmtDurationBlock(p.FadeTime)})
	}
	headers := []string{"Color", "LED", "Fade Time"}
	printTable(headers, lines)
	return nil
}

// PrintDeviceList prints a list of devices to stdout.
func PrintDeviceList(dis []*se.DeviceDetail) error {
	var lines [][]string
	for _, d := range dis {
		lines = append(lines, []string{d.Path, uint16hex(d.VendorID), uint16hex(d.ProductID), uint16hex(d.VersionNumber), d.Manufacturer, d.Product, d.SerialNumber, uint16str(d.InputReportLength), uint16str(d.OutputReportLength), uint16str(d.FeatureReportLength)})
	}
	headers := []string{"Path", "VID", "PID", "Ver", "Mfr", "Product", "SN", "In", "Out", "Feat"}
	printTable(headers, lines)
	return nil
}

// PrintDeviceListWithFirmware prints a list of devices with firmware version to stdout.
func PrintDeviceListWithFirmware(dis []*se.DeviceDetail) error {
	var lines [][]string
	for _, d := range dis {
		var fw string
		if d.IsBlink1 {
			fw = intstr(d.FirmwareVersion)
		}
		lines = append(lines, []string{d.Path, uint16hex(d.VendorID), uint16hex(d.ProductID), uint16hex(d.VersionNumber), d.Manufacturer, d.Product, d.SerialNumber, fw, uint16str(d.InputReportLength), uint16str(d.OutputReportLength), uint16str(d.FeatureReportLength)})
	}
	headers := []string{"Path", "VID", "PID", "Ver", "Mfr", "Product", "SN", "FW", "In", "Out", "Feat"}
	printTable(headers, lines)
	return nil
}

func printTable(header []string, rows [][]string) {
	s := strings.Builder{}
	table := tw.NewWriter(&s)
	table.SetHeader(append([]string{"No."}, header...))
	table.SetHeaderAlignment(tw.ALIGN_LEFT)
	table.SetAlignment(tw.ALIGN_LEFT)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetTablePadding("\t")

	for i, r := range rows {
		table.Append(append([]string{amoy.Itoa(i + 1)}, r...))
	}

	table.Render()
	fmt.Println(s.String())
}
