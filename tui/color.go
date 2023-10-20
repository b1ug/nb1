package tui

import (
	"fmt"
	"image/color"

	"github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/util"
	"github.com/muesli/termenv"
)

var (
	block = `█`
)

// FormatNamedColor in terminal-friendly style prints color block and its hex, and if the color's name is known, it is also included.
// It uses the muesli/termenv to format the string with the appropriate escape codes.
func FormatNamedColor(c color.Color) string {
	// get optional color name
	hex := util.ConvColorToHex(c)
	name, ok := util.ConvColorToNameOrHex(c)

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
