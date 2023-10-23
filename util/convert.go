package util

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/b1ug/blink1-go"
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

// convLEDEmoji converts LED index to emoji.
func convLEDEmoji(led blink1.LEDIndex) string {
	const (
		l = `💡`
		e = `  `
	)
	switch led {
	case 0:
		return l + l
	case 1:
		return l + e
	case 2:
		return e + l
	default:
		return l + strconv.Itoa(int(led))
	}
}

// convDurationBlock formats duration as a color block.
func convDurationBlock(dur time.Duration) string {
	if dur == 0 {
		return `no time`
	}
	const (
		charBlk = `░`
		mpBlk   = 250 * time.Millisecond
		maxBlk  = 10
	)
	// colorize text
	num := int(dur / mpBlk)
	if num > maxBlk {
		num = maxBlk
	}
	blk := strings.Repeat(charBlk, num)
	return blk + " " + dur.String()
}
