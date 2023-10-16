package hdwr

import (
	"image/color"

	b1 "github.com/b1ug/blink1-go"
)

// PlayState plays a blink(1) state on the opened device.
func PlayState(st b1.LightState) error {
	return ctrl.PlayState(st)
}

// PlayStateAndWait plays a blink(1) state on the opened device and wait for completion.
func PlayStateAndWait(st b1.LightState) error {
	return ctrl.PlayStateBlocking(st)
}

// StopPlaying stops playing any blink(1) state on the opened device.
func StopPlaying() error {
	return ctrl.StopPlaying()
}

// SetColor sets the color of the opened device.
func SetColor(cl color.Color) error {
	return ctrl.PlayColor(cl)
}
