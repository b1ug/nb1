package actionv1

import (
	"encoding/hex"
	"encoding/json"

	"github.com/b1ug/nb1/internal/sequence"
)

const updateMS = sequence.UpdateAllowanceMS

func compileSteps(kind string, fields map[string]json.RawMessage, led int, color string, resolve func(string) (string, error), result *sequence.Sequence) ([]sequence.Step, error) {
	switch kind {
	case "set":
		return []sequence.Step{serialStep(led, color, 0, 0)}, nil
	case "off":
		return []sequence.Step{serialStep(led, black, 0, 0)}, nil
	case "fade":
		duration, err := fadeTime(fields, "duration_ms", 0)
		if err != nil {
			return nil, err
		}
		return []sequence.Step{serialStep(led, color, duration, 0)}, nil
	case "blink":
		on, err := integer(fields, "on_ms", 250, updateMS, sequence.MaxHoldMS+updateMS)
		if err != nil {
			return nil, err
		}
		off, err := integer(fields, "off_ms", 250, updateMS, sequence.MaxHoldMS+updateMS)
		if err != nil {
			return nil, err
		}
		return []sequence.Step{serialStep(led, color, 0, on-updateMS), serialStep(led, black, 0, off-updateMS)}, nil
	case "breathe":
		return sampledCycle(fields, led, breatheColors(color), 2000)
	case "pulse":
		attack, err := fadeTime(fields, "attack_ms", 60)
		if err != nil {
			return nil, err
		}
		decay, err := fadeTime(fields, "decay_ms", 240)
		if err != nil {
			return nil, err
		}
		rest, err := integer(fields, "rest_ms", 700, 0, sequence.MaxHoldMS)
		if err != nil {
			return nil, err
		}
		return []sequence.Step{serialStep(led, black, 0, 0), serialStep(led, color, attack, 0), serialStep(led, black, decay, rest)}, nil
	case "alternate":
		targets, err := pair(fields, resolve)
		if err != nil {
			return nil, err
		}
		period, err := integer(fields, "period_ms", 2000, 4*updateMS, 2*(sequence.MaxHoldMS+2*updateMS))
		if err != nil {
			return nil, err
		}
		first := period / 2
		a, b := targets[0], targets[1]
		return []sequence.Step{
			serialStep(a.LED, a.Color, 0, 0), serialStep(b.LED, b.Color, 0, first-2*updateMS),
			serialStep(a.LED, b.Color, 0, 0), serialStep(b.LED, a.Color, 0, period-first-2*updateMS),
		}, nil
	case "orbit":
		targets, err := pair(fields, resolve)
		if err != nil {
			return nil, err
		}
		period, err := integer(fields, "period_ms", 3000, orbitSegments*sequence.MinIntervalMS, orbitSegments*sequence.MaxIntervalMS)
		if err != nil {
			return nil, err
		}
		result.Timing = sequence.TimingInterval
		result.Initial = orbitTargets(targets, 0)
		return orbitSteps(targets, period), nil
	case "rainbow":
		return sampledCycle(fields, led, rainbowColors[:], 3000)
	default:
		return nil, invalid("kind", "unknown action %q", kind)
	}
}

func serialStep(led int, color string, fade, hold int) sequence.Step {
	return sequence.Step{
		Targets: []sequence.Target{{LED: led, Color: color}}, FadeMS: fade, HoldMS: &hold,
	}
}

// Each sample advances a real fade of at least one hardware unit. Preserve
// the requested period by putting any sub-unit remainder into the step hold.
func sampledCycle(fields map[string]json.RawMessage, led int, colors []string, fallback int) ([]sequence.Step, error) {
	segments := len(colors) - 1
	minPeriod := updateMS + segments*(updateMS+sequence.FadeQuantumMS)
	maxPeriod := updateMS + segments*(updateMS+sequence.MaxFadeMS+sequence.FadeQuantumMS-1)
	period, err := integer(fields, "period_ms", fallback, minPeriod, maxPeriod)
	if err != nil {
		return nil, err
	}
	steps := make([]sequence.Step, 0, len(colors))
	steps = append(steps, serialStep(led, colors[0], 0, 0))
	for i := 1; i < len(colors); i++ {
		slot := segmentTime(period-updateMS, i, segments)
		fade := (slot - updateMS) / sequence.FadeQuantumMS * sequence.FadeQuantumMS
		steps = append(steps, serialStep(led, colors[i], fade, slot-updateMS-fade))
	}
	return steps, nil
}

// Fixed 16-segment cosine envelope, expressed as integer ten-thousandths.
var breatheWeights = [...]int{0, 381, 1464, 3087, 5000, 6913, 8536, 9619, 10000, 9619, 8536, 6913, 5000, 3087, 1464, 381, 0}

func breatheColors(color string) []string {
	base := rgb(color)
	colors := make([]string, len(breatheWeights))
	for i, weight := range breatheWeights {
		var sample [3]byte
		for channel, value := range base {
			sample[channel] = byte((int(value)*weight + 5000) / 10000)
		}
		colors[i] = "#" + hex.EncodeToString(sample[:])
	}
	return colors
}

var rainbowColors = [...]string{
	"#ff0000", "#ffa500", "#ffff00", "#00ff00", "#0000ff", "#4b0082", "#8f00ff", "#ff0000",
}

const orbitSegments = 4

var (
	orbitMix   = [...]int64{0, 5000, 10000, 5000, 0}
	orbitShift = [...]int64{0, 20000, 0, -20000, 0}
)

func orbitSteps(pair [2]sequence.Target, period int) []sequence.Step {
	steps := make([]sequence.Step, 0, orbitSegments)
	for phase := 1; phase <= orbitSegments; phase++ {
		interval := segmentTime(period, phase, orbitSegments)
		steps = append(steps, sequence.Step{
			Targets: orbitTargets(pair, phase), FadeMS: interval / sequence.FadeQuantumMS * sequence.FadeQuantumMS, IntervalMS: &interval,
		})
	}
	return steps
}

func orbitTargets(pair [2]sequence.Target, phase int) []sequence.Target {
	a, b := rgb(pair[0].Color), rgb(pair[1].Color)
	weight, shift := orbitMix[phase], orbitShift[phase]
	return []sequence.Target{
		{LED: pair[0].LED, Color: mix(a, b, weight, 30000+shift)},
		{LED: pair[1].LED, Color: mix(b, a, weight, 30000-shift)},
	}
}

func mix(first, second [3]byte, weight, brightness int64) string {
	const denominator = 10000 * 50000
	var color [3]byte
	for channel := range color {
		mixed := int64(first[channel])*(10000-weight) + int64(second[channel])*weight
		color[channel] = byte((mixed*brightness + denominator/2) / denominator)
	}
	return "#" + hex.EncodeToString(color[:])
}

func rgb(color string) [3]byte {
	var bytes [3]byte
	// ResolveColor has already validated and normalized the color.
	_, _ = hex.Decode(bytes[:], []byte(color[1:]))
	return bytes
}

func segmentTime(period, index, segments int) int {
	return period*index/segments - period*(index-1)/segments
}
