package util

import (
	"fmt"
	"image/color"
	"sync"

	"github.com/b1ug/blink1-go"
)

var (
	hexToName = map[string]string{}
	onceColor sync.Once
)

func initColor() {
	names := blink1.GetColorNames()
	for _, n := range names {
		c, ok := blink1.GetColorByName(n)
		if !ok {
			continue
		}
		h := ConvColorToHex(c)
		hexToName[h] = n
	}
}

// ConvColorToHex converts color.Color to hex string.
func ConvColorToHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}

// ConvHexToColor converts hex string to color.Color.
func ConvHexToColor(s string) (color.Color, error) {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02X%02X%02X", &r, &g, &b); err != nil {
		return nil, fmt.Errorf("invalid hex color: %s - %w", s, err)
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xff}, nil
}

// ConvColorToNameOrHex converts color.Color to its name if known, otherwise to hex string.
func ConvColorToNameOrHex(c color.Color) (string, bool) {
	onceColor.Do(initColor)

	// get optional color name
	hex := ConvColorToHex(c)
	name, ok := hexToName[hex]

	// join results
	if ok {
		return name, true
	}
	return hex, false
}
