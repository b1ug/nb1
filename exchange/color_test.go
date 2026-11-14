package exchange

import (
	"image/color"
	"testing"

	"github.com/b1ug/nb1/config"
	"github.com/spf13/viper"
)

func TestApplicationColorResolution(t *testing.T) {
	old := config.GetColorMap()
	t.Cleanup(func() { viper.Set("color", old) })
	viper.Set("color", map[string]string{
		"desk": "HSV(210, 75, 90)", "夜灯": "#abc", "chain": "desk",
		"chance": "random", "on": "red", "off": "blue",
	})
	for _, tc := range []struct {
		input string
		want  color.RGBA
	}{
		{"ReD", color.RGBA{R: 255, A: 255}},
		{" RGB(1, 2, 3) ", color.RGBA{R: 1, G: 2, B: 3, A: 255}},
		{"HsV(210,75,90)", color.RGBA{R: 57, G: 143, B: 230, A: 255}},
		{"desk", color.RGBA{R: 57, G: 143, B: 230, A: 255}},
		{"夜灯", color.RGBA{R: 170, G: 187, B: 204, A: 255}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseColor(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("ParseColor(%q) = %v, %v, want %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, raw := range []string{"on", "OFF", "chain", "chance", "Desk", "@desk", "red garbage"} {
		got, err := ParseColor(raw)
		if err == nil || got != nil {
			t.Errorf("%q unexpectedly became executable color %v, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"RANDOM", "RaNd"} {
		if got, err := ParseColor(raw); err != nil || got == nil {
			t.Errorf("application keyword %q failed: %v, %v", raw, got, err)
		}
	}
}
