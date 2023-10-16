package parser

import (
	"image/color"

	b1 "github.com/b1ug/blink1-go"
)

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

	// 2. match config
	// TODO:

	// 3. for predefined colors, hex, rgb, hsv/hsl hacks
	st, err := b1.ParseStateQuery("led:0, fade:0, color:" + raw)
	if err != nil {
		return nil, err
	}
	return st.Color, nil
}
