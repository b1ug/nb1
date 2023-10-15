// Package tui provides helper functions to render text-based user interface.
package tui

import (
	"fmt"
	"strconv"
)

func uint8hex(u uint8) string {
	if u == 0 {
		return "0"
	}
	return fmt.Sprintf("0x%02x", u)
}

func uint16hex(u uint16) string {
	if u == 0 {
		return "0"
	}
	return fmt.Sprintf("0x%04x", u)
}

func uint16str(u uint16) string {
	if u == 0 {
		return "0"
	}
	return strconv.Itoa(int(u))
}

func intstr(i int) string {
	if i == 0 {
		return "0"
	}
	return strconv.Itoa(i)
}
