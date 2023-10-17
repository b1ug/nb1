package tui

import (
	"fmt"
	"image/color"
	"sync"

	"github.com/b1ug/blink1-go"
	"github.com/muesli/termenv"
)

var (
	block = `█`

	onceColor sync.Once
	hexToName = map[string]string{}
)

func initColor() {
	names := blink1.GetColorNames()
	for _, n := range names {
		c, ok := blink1.GetColorByName(n)
		if !ok {
			continue
		}
		h := convColorToHex(c)
		hexToName[h] = n
	}
}

// FormatNamedColor in terminal-friendly style prints color block and its hex, and if the color's name is known, it is also included.
// It uses the muesli/termenv to format the string with the appropriate escape codes.
func FormatNamedColor(c color.Color) string {
	onceColor.Do(initColor)

	// get optional color name
	hex := convColorToHex(c)
	name, ok := hexToName[hex]

	// colorize text
	asciiColor := termenv.ColorProfile().FromColor(c)
	asciiWhite := termenv.ColorProfile().FromColor(blink1.ColorWhite)
	colorBlock := termenv.String(block).Foreground(asciiColor).Background(asciiColor).String()
	colorHex := termenv.String(hex).Foreground(asciiWhite).Background(asciiColor).String()

	// join results
	if ok {
		return fmt.Sprintf(`%s%s(%s)`, colorBlock, colorHex, name)
	}
	return fmt.Sprintf(`%s%s`, colorBlock, colorHex)
}

// FormatLightState in terminal-friendly style prints blink(1) light state.
func FormatLightState(st blink1.LightState) string {
	return fmt.Sprintf("🎨[%s 💡%d %s%v]", FormatNamedColor(st.Color), st.LED, convDoneEmoji(st.FadeTime == 0), st.FadeTime)
}
