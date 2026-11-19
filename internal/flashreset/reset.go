// Package flashreset defines the portable presets and receipts used to replace
// the persistent Pattern rows of a blink(1). Hardware access lives in devicebackend.
package flashreset

import (
	"errors"
	"fmt"
)

const (
	Off       = "off"
	Rainbow   = "rainbow"
	Starlight = "starlight"
	Fireflies = "fireflies"
	Default   = Off

	Acknowledged  = "acknowledged"
	Unconfirmed   = "unconfirmed"
	StagingFailed = "staging_failed"
)

var ErrUnsupported = errors.New("flash preset reset is unsupported for this device or session")

// Row always targets both LEDs. Ticks are the native 10 ms units shared by the
// fade and the time until the next row; a black zero-tick row would be skipped.
type Row struct {
	R, G, B byte
	Ticks   uint16
}

var motifs = map[string][]Row{
	Off: {{0, 0, 0, 10}},
	Rainbow: {
		{255, 0, 0, 60}, {255, 105, 0, 60}, {255, 220, 0, 60}, {0, 200, 55, 60},
		{0, 185, 225, 60}, {0, 40, 255, 60}, {105, 15, 210, 60}, {220, 10, 120, 60},
	},
	Starlight: {
		{4, 6, 19, 65}, {180, 214, 255, 12}, {4, 6, 19, 41}, {4, 6, 19, 95},
		{125, 90, 195, 17}, {4, 6, 19, 45}, {235, 224, 202, 14}, {4, 6, 19, 70},
	},
	Fireflies: {
		{0, 0, 0, 90}, {122, 220, 18, 29}, {18, 32, 2, 40}, {0, 0, 0, 65},
		{215, 155, 8, 36}, {16, 18, 1, 45}, {76, 140, 10, 22}, {0, 0, 0, 60},
	},
}

func Valid(preset string) bool { _, ok := motifs[preset]; return ok }

// Image repeats the preset over the entire usable Flash region. The caller
// obtains its capacity from the matched SDK profile before programming it.
func Image(preset string, lines int) ([]Row, error) {
	motif, ok := motifs[preset]
	if !ok {
		return nil, fmt.Errorf("unknown Flash preset %q", preset)
	}
	if lines != 16 && lines != 32 {
		return nil, fmt.Errorf("unsupported Flash image size %d", lines)
	}
	rows := make([]Row, lines)
	for i := range rows {
		rows[i] = motif[i%len(motif)]
	}
	return rows, nil
}

// Result counts successful RAM staging calls and matching RAM observations.
// A failed staging call can have changed the next RAM row as well. Acknowledged
// only describes acceptance of a single save request, never cold verification.
type Result struct {
	Serial   string `json:"serial"`
	Preset   string `json:"preset"`
	Firmware int    `json:"firmware"`
	Lines    int    `json:"lines"`
	Staged   int    `json:"staged"`
	Verified int    `json:"verified"`
	Status   string `json:"status"`
}

func (r Result) Validate(serial, preset string) error {
	if serial == "" || r.Serial != serial || !Valid(preset) || r.Preset != preset {
		return errors.New("Flash reset receipt target or preset mismatch")
	}
	if r.Firmware <= 0 || (r.Lines != 16 && r.Lines != 32) || r.Staged < 0 || r.Staged > r.Lines ||
		r.Verified < 0 || r.Verified > r.Staged || (r.Verified > 0 && r.Staged != r.Lines) {
		return errors.New("invalid Flash reset receipt counts")
	}
	switch r.Status {
	case StagingFailed:
	case Acknowledged, Unconfirmed:
		if r.Verified != r.Lines {
			return errors.New("Flash reset save requires a fully verified RAM image")
		}
	default:
		return errors.New("unknown Flash reset receipt status")
	}
	return nil
}
