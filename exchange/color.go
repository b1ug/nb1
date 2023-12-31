package exchange

import (
	"image/color"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/config"
)

// ParseColor parses a natural language query string into a color.Color object.
func ParseColor(raw string) (color.Color, error) {
	// 1. for special colors
	switch raw {
	case "on":
		return b1.ColorWhite, nil
	case "off":
		return b1.ColorBlack, nil
	case "rand", "random":
		return b1.RandomColor(), nil
	}

	// 2. match config: use color map as a shortcut
	cm := config.GetColorMap()
	if c, ok := cm[raw]; ok {
		raw = c
	}

	// 3. for predefined colors, hex, rgb, hsv/hsl hacks
	cl, err := b1.ParseColor(raw)
	if err != nil {
		return nil, err
	}
	return cl, nil
}
