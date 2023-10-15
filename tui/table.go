package tui

import (
	"fmt"
	"strings"

	"bitbucket.org/ai69/amoy"
	hid "github.com/b1ug/gid"
	tw "github.com/olekukonko/tablewriter"
)

func PrintDeviceList(dis []*hid.DeviceInfo) error {
	var lines [][]string
	for _, d := range dis {
		lines = append(lines, []string{d.Path, u16hex(d.VendorID), u16hex(d.ProductID), u16hex(d.VersionNumber), d.Manufacturer, d.Product, d.SerialNumber, u16str(d.InputReportLength), u16str(d.OutputReportLength), u16str(d.FeatureReportLength)})
	}
	headers := []string{"Path", "VID", "PID", "Ver", "Mfr", "Product", "SN", "In", "Out", "Feat"}
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

func u8hex(u uint8) string {
	if u == 0 {
		return "0"
	}
	return fmt.Sprintf("0x%02x", u)
}

func u16hex(u uint16) string {
	if u == 0 {
		return "0"
	}
	return fmt.Sprintf("0x%04x", u)
}

func u16str(u uint16) string {
	if u == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", u)
}
