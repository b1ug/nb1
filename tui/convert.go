// Package tui provides helper functions to render text-based user interface.
package tui

import (
	"fmt"
	"image/color"
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

// invertColor returns its inverse color.
func invertColor(inputColor color.Color) color.Color {
	// extract RGB components
	r, g, b, a := inputColor.RGBA()

	// bit shifting with '>> 8' is used here to convert these 16-bit values to 8 bits
	r8 := r >> 8
	g8 := g >> 8
	b8 := b >> 8

	// invert the color components
	rInv := 255 - r8
	gInv := 255 - g8
	bInv := 255 - b8
	return color.RGBA{uint8(rInv), uint8(gInv), uint8(bInv), uint8(a >> 8)}
}

// convDoneEmoji converts playing state to emoji.
func convDoneEmoji(done bool) string {
	if done {
		return `⌛`
	}
	return `⏳`
}
