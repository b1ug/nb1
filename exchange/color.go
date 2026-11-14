package exchange

import (
	"image/color"
	"strings"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/config"
)

// ParseColor resolves an application random keyword or one configured alias,
// then delegates a complete color expression to the SDK. Alias keys are exact.
func ParseColor(raw string) (color.Color, error) {
	raw = strings.TrimSpace(raw)
	// Application keywords are normalized locally, without lowercasing aliases.
	switch strings.ToLower(raw) {
	case "on", "off":
		return b1.ParseColor(raw)
	case "rand", "random":
		return b1.RandomColor(), nil
	}

	// 2. match config: use color map as a shortcut
	cm := config.GetColorMap()
	if c, ok := cm[raw]; ok {
		raw = c
	}

	// Alias values are not recursively resolved or reinterpreted as random.
	cl, err := b1.ParseColor(raw)
	if err != nil {
		return nil, err
	}
	return cl, nil
}
