package exchange

import (
	"errors"
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/colorkit"
	"github.com/b1ug/nb1/schema"
)

func TestPlayTextCanonicalRoundTrip(t *testing.T) {
	for _, repeat := range []string{"1", "255", "forever"} {
		lines := []string{"// file comment", "", "TiTle:  Desk 夜灯 // literal  ", "RePeAt: " + repeat,
			"SET RGB(1, 2, 3) on led 1", "fade to #abc over 500ms on led 2", "#008000L0T20", "off"}
		got, err := ParsePlayText(lines)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != " Desk 夜灯 // literal  " || got.Length != 4 {
			t.Fatalf("metadata/state loss: %+v", got)
		}
		if (repeat == "forever") != (got.RepeatTimes == 0) {
			t.Fatalf("repeat = %d", got.RepeatTimes)
		}
		encoded, err := EncodePlayText(*got)
		if err != nil {
			t.Fatal(err)
		}
		if encoded[0] != "title:  Desk 夜灯 // literal  " || encoded[1] != "repeat: "+repeat {
			t.Fatalf("noncanonical metadata %q", encoded)
		}
		again, err := ParsePlayText(encoded)
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatalf("export/import mismatch: before=%+v after=%+v err=%v", got, again, err)
		}
	}
	got, err := ParsePlayText([]string{"set red"})
	if err != nil || got.RepeatTimes != 1 {
		t.Fatalf("default repeat = %v, %v", got, err)
	}
}

func TestPlayTextRejectsWholeFileWithLineDiagnostic(t *testing.T) {
	for _, bad := range []string{
		"red blink 3 times", "Repeat Twice", "led 1 red 500ms", "unknown: value", "set red trailing",
		"repeat: 0", "repeat: -1", "repeat: 256", "repeat: 999999999999999999999999", "repeat:",
		"repeat: twice", "repeat: 2 // comment", "#FF0000L1T15", "fade blue over 1ms",
		"title: bad\nline", "// bad\xff", strings.Repeat("x", 4097),
	} {
		t.Run(bad, func(t *testing.T) {
			got, err := ParsePlayText([]string{"set red", bad})
			if got != nil || err == nil || !strings.Contains(err.Error(), "line 2:") {
				t.Fatalf("partial file accepted or missing line: %v %v", got, err)
			}
		})
	}
	for _, lines := range [][]string{{"title: One", "TITLE: Two", "set red"}, {"repeat: 1", "REPEAT: 2", "set red"}} {
		if got, err := ParsePlayText(lines); got != nil || err == nil || !strings.Contains(err.Error(), "line 2: duplicate") {
			t.Fatalf("duplicate metadata: %v %v", got, err)
		}
	}
	if got, err := ParsePlayText([]string{"// only comments", ""}); got != nil || err == nil {
		t.Fatalf("empty file: %v %v", got, err)
	}
}

func TestPlayTextKeepsSDKErrorChain(t *testing.T) {
	_, err := ParsePlayText([]string{"set red", "set hsv(361,100,100)"})
	var detail *b1.ParseError
	if !errors.As(err, &detail) || detail.Field == "" || !errors.Is(err, colorkit.ErrRange) {
		t.Fatalf("SDK diagnostic/cause lost: %v", err)
	}
}

func TestPlayTextExportPreflightsAllStates(t *testing.T) {
	var nilColor *color.RGBA
	for _, state := range []b1.LightState{
		{}, b1.NewLightState(nilColor, 0, b1.LEDAll),
		b1.NewLightState(b1.ColorRed, 0, b1.LEDIndex(3)),
		b1.NewLightState(b1.ColorRed, -time.Millisecond, b1.LEDAll),
		b1.NewLightState(b1.ColorRed, time.Millisecond, b1.LEDAll),
		b1.NewLightState(b1.ColorRed, 15*time.Millisecond, b1.LEDAll),
		b1.NewLightState(b1.ColorRed, 655360*time.Millisecond, b1.LEDAll),
	} {
		ps := schema.PatternSet{RepeatTimes: 1, Sequence: b1.StateSequence{b1.NewLightState(b1.ColorBlue, 0, b1.LED1), state}}
		lines, err := EncodePlayText(ps)
		var detail *b1.ParseError
		if lines != nil || !errors.As(err, &detail) || !strings.Contains(err.Error(), "state[1]") {
			t.Errorf("invalid state exported: %q %v", lines, err)
		}
	}
	for _, ps := range []schema.PatternSet{
		{RepeatTimes: 1},
		{Name: "bad\nname", RepeatTimes: 1, Sequence: b1.StateSequence{b1.NewLightState(b1.ColorRed, 0, b1.LEDAll)}},
		{Name: strings.Repeat("x", 4090), RepeatTimes: 1, Sequence: b1.StateSequence{b1.NewLightState(b1.ColorRed, 0, b1.LEDAll)}},
		{RepeatTimes: 256, Sequence: b1.StateSequence{b1.NewLightState(b1.ColorRed, 0, b1.LEDAll)}},
	} {
		if lines, err := EncodePlayText(ps); lines != nil || err == nil {
			t.Errorf("invalid pattern exported: %q %v", lines, err)
		}
	}
}
