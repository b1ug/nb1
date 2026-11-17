package util

import (
	"bytes"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func setOutputEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "CI", "TERM", "COLORTERM"} {
		t.Setenv(key, "")
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestOutputColorProfileForPlainWriters(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want termenv.Profile
	}{
		{"pipe stays plain", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, termenv.Ascii},
		{"explicit force", map[string]string{"CLICOLOR_FORCE": "1"}, termenv.ANSI},
		{"force zero is not forced", map[string]string{"CLICOLOR_FORCE": "0"}, termenv.Ascii},
		{"empty no color does not cancel force", map[string]string{"NO_COLOR": "", "CLICOLOR_FORCE": "1"}, termenv.ANSI},
		{"no color overrides force", map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, termenv.Ascii},
		{"no color zero is nonempty", map[string]string{"NO_COLOR": "0", "CLICOLOR_FORCE": "1"}, termenv.Ascii},
		{"no color whitespace is nonempty", map[string]string{"NO_COLOR": " ", "CLICOLOR_FORCE": "1"}, termenv.Ascii},
		{"clicolor zero", map[string]string{"CLICOLOR": "0"}, termenv.Ascii},
		{"force overrides clicolor zero", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"}, termenv.ANSI},
		{"dumb ignores stale truecolor", map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}, termenv.Ascii},
		{"explicit force can override dumb", map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"}, termenv.ANSI},
		{"no color still overrides dumb force", map[string]string{"TERM": "dumb", "NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, termenv.Ascii},
		{"CI stays plain", map[string]string{"CI": "1", "TERM": "xterm-256color"}, termenv.Ascii},
		{"explicit force can override CI", map[string]string{"CI": "1", "CLICOLOR_FORCE": "1"}, termenv.ANSI},
	} {
		t.Run(test.name, func(t *testing.T) {
			setOutputEnvironment(t, test.env)
			if got := OutputColorProfile(&bytes.Buffer{}); got != test.want {
				t.Fatalf("profile=%v, want %v", got, test.want)
			}
		})
	}
}

func TestLogoFormattingPreservesTextAndLineSpacing(t *testing.T) {
	strip := regexp.MustCompile("\x1b\\[[0-9;]*m")
	for _, content := range []string{"", "one line", "one line\n", "\nfirst\nsecond\n", "first\n\nlast\n\n"} {
		for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
			got := formatLogo(content, profile)
			if strip.ReplaceAllString(got, "") != content {
				t.Fatalf("profile=%v changed logo spacing: %q", profile, got)
			}
			if (content == "" || profile == termenv.Ascii) && strings.ContainsRune(got, '\x1b') {
				t.Fatalf("plain or empty logo has styling: %q", got)
			}
		}
	}
	if got := formatLogo("first\nsecond\n", termenv.TrueColor); !strings.HasPrefix(got, "\x1b[38;2;229;93;135mfirst") {
		t.Fatalf("original RoseWater starting color changed: %q", got)
	}
}

func TestLogoUsesSelectedWriterAndLegacyColorHonorsPlainMode(t *testing.T) {
	setOutputEnvironment(t, map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"})
	const logo = "nb1\n"
	if got := FormatLogo(logo, &bytes.Buffer{}); got != logo {
		t.Fatalf("buffer inherited unrelated terminal color: %q", got)
	}
	t.Setenv("CLICOLOR_FORCE", "1")
	if got := FormatLogo(logo, &bytes.Buffer{}); !strings.ContainsRune(got, '\x1b') {
		t.Fatalf("explicit color force was ignored: %q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := FormatLogo(logo, &bytes.Buffer{}); got != logo {
		t.Fatalf("NO_COLOR did not override force: %q", got)
	}
	got := FormatNamedColor(color.RGBA{R: 255, A: 255})
	if strings.ContainsRune(got, '\x1b') || !strings.Contains(strings.ToLower(got), "#ff0000") || !strings.Contains(got, "(red)") {
		t.Fatalf("plain legacy color lost its readable value/name: %q", got)
	}
}
