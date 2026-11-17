package util

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/b1ug/blink1-go"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

var (
	block = `█`
)

// OutputColorProfile selects color for the actual destination without querying
// terminal input or its background. Any nonempty NO_COLOR wins; CLICOLOR_FORCE
// uses Go boolean values such as 1/true, consistently with Fang's profile policy.
func OutputColorProfile(output io.Writer) termenv.Profile {
	forced, _ := strconv.ParseBool(os.Getenv("CLICOLOR_FORCE"))
	if os.Getenv("NO_COLOR") != "" || (os.Getenv("CLICOLOR") == "0" && !forced) {
		return termenv.Ascii
	}
	if !forced && (os.Getenv("TERM") == "dumb" || os.Getenv("CI") != "") {
		return termenv.Ascii
	}
	var options []termenv.OutputOption
	if terminal, ok := output.(interface{ Fd() uintptr }); ok && isatty.IsTerminal(terminal.Fd()) {
		// termenv only recognizes *os.File itself. Preserve the capability of
		// wrappers around a real terminal, without claiming a pipe is a TTY.
		options = append(options, termenv.WithTTY(true))
	}
	profile := termenv.NewOutput(output, options...).ColorProfile()
	if forced && profile == termenv.Ascii {
		return termenv.ANSI
	}
	return profile
}

// FormatLogo retains the established RoseWater palette and line spacing while
// selecting color for the writer that will receive it.
func FormatLogo(content string, output io.Writer) string {
	return formatLogo(content, OutputColorProfile(output))
}

func formatLogo(content string, profile termenv.Profile) string {
	if content == "" || profile == termenv.Ascii {
		return content
	}
	// These are the original colorlogo RoseWater colors and BlendLuv positions.
	top, _ := colorful.Hex("#E55D87")
	bottom, _ := colorful.Hex("#5FC3E4")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var result strings.Builder
	for index, line := range lines {
		color := top.BlendLuv(bottom, float64(index)/float64(len(lines))).Hex()
		result.WriteString(profile.String(line).Foreground(profile.Color(color)).String())
		if index < len(lines)-1 || strings.HasSuffix(content, "\n") {
			result.WriteByte('\n')
		}
	}
	return result.String()
}

// ConvColorToHex converts color.Color to hex string.
func ConvColorToHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}

// FormatNamedColor in terminal-friendly style prints color block and its hex, and if the color's name is known, it is also included.
// It uses the muesli/termenv to format the string with the appropriate escape codes.
func FormatNamedColor(c color.Color) string {
	// get optional color name
	hex := blink1.ColorToHex(c)
	name, ok := blink1.GetNameByColor(c)

	// colorize text
	profile := OutputColorProfile(os.Stdout)
	asciiColor := profile.FromColor(c)
	asciiWhite := profile.FromColor(blink1.ColorWhite)
	colorBlock := profile.String(block).Foreground(asciiColor).Background(asciiColor).String()
	colorHex := profile.String(hex).Foreground(asciiWhite).Background(asciiColor).String()

	// join results
	if ok {
		return fmt.Sprintf(`%s%s(%s)`, colorBlock, colorHex, name)
	}
	return fmt.Sprintf(`%s%s`, colorBlock, colorHex)
}

// FormatLightState in terminal-friendly style prints blink(1) light state.
func FormatLightState(st blink1.LightState) string {
	led := `∀`
	if st.LED > 0 {
		led = strconv.Itoa(int(st.LED))
	}
	return fmt.Sprintf("🎨[%s 💡%s %s%v]", FormatNamedColor(st.Color), led, convDoneEmoji(st.FadeTime == 0), st.FadeTime)
}
