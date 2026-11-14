package lightaction

import (
	"encoding/hex"

	"github.com/b1ug/nb1/internal/control"
)

const flowPhases = 32
const flowMinimumPeriodMS = 1300
const flowWeightScale = 10000
const flowBrightnessScale = 50000

// Round-half-up samples of (1-cos(2*pi*p/32))/2, in ten-thousandths.
// Fixed integer tables are part of the experimental transition contract.
var flowCosine32 = [...]int{
	0, 96, 381, 843, 1464, 2222, 3087, 4025,
	5000, 5975, 6913, 7778, 8536, 9157, 9619, 9904,
	10000, 9904, 9619, 9157, 8536, 7778, 6913, 5975,
	5000, 4025, 3087, 2222, 1464, 843, 381, 96, 0,
}

// Round-half-away-from-zero samples of sin(2*pi*p/32), in ten-thousandths.
// The endpoints and half-cycle are exact zero, avoiding platform float noise.
var flowSine32 = [...]int{
	0, 1951, 3827, 5556, 7071, 8315, 9239, 9808,
	10000, 9808, 9239, 8315, 7071, 5556, 3827, 1951,
	0, -1951, -3827, -5556, -7071, -8315, -9239, -9808,
	-10000, -9808, -9239, -8315, -7071, -5556, -3827, -1951, 0,
}

// Two initial writes and 32 pairs of sequential targets form a closed cycle.
// Every target uses the same existing executor; two LED fades are not parallel.
func buildAlternateFlow(a NormalizedAction) []control.Step {
	first, _ := hex.DecodeString(a.Colors[0][1:])
	second, _ := hex.DecodeString(a.Colors[1][1:])
	colors := flowColors(first, second, a.Transition, 0)
	steps := make([]control.Step, 0, 2+2*flowPhases)
	steps = append(steps, control.Step{LED: a.LEDs[0], Color: colors[0]}, control.Step{LED: a.LEDs[1], Color: colors[1]})
	duration := *a.PeriodMS - 2*control.StepOverheadMS
	for phase := 1; phase <= flowPhases; phase++ {
		colors = flowColors(first, second, a.Transition, phase)
		for output := 0; output < 2; output++ {
			index := (phase-1)*2 + output + 1
			steps = append(steps, transition(a.LEDs[output], colors[output], duration, index, 2*flowPhases))
		}
	}
	return steps
}

func flowColors(first, second []byte, mode string, phase int) [2]string {
	weight := flowCosine32[phase]
	if mode == "linear" {
		if phase <= flowPhases/2 {
			weight = phase * flowWeightScale / (flowPhases / 2)
		} else {
			weight = (flowPhases - phase) * flowWeightScale / (flowPhases / 2)
		}
	}
	brightnessFirst, brightnessSecond := flowBrightnessScale, flowBrightnessScale
	if mode == "orbit" {
		// 0.6 +/- 0.4*sin(theta), expressed exactly relative to the fixed sine
		// table. Do not round a mixed RGB or brightness before multiplying.
		brightnessFirst = 30000 + 2*flowSine32[phase]
		brightnessSecond = 30000 - 2*flowSine32[phase]
	}
	return [2]string{flowMix(first, second, weight, brightnessFirst), flowMix(second, first, weight, brightnessSecond)}
}

func flowMix(first, second []byte, weight, brightness int) string {
	var rgb [3]byte
	const denominator = int64(flowWeightScale) * flowBrightnessScale
	for i := range rgb {
		mixed := int64(first[i])*int64(flowWeightScale-weight) + int64(second[i])*int64(weight)
		// int64 is required on 32-bit platforms: the numerator can exceed 2^31.
		rgb[i] = byte((mixed*int64(brightness) + denominator/2) / denominator)
	}
	return "#" + hex.EncodeToString(rgb[:])
}
