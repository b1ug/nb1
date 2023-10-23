package hdwr

import (
	"errors"
	"image/color"

	b1 "github.com/b1ug/blink1-go"
)

var (
	errMissingDevice = errors.New("no blink(1) device opened")
)

// PlayState plays a blink(1) state on the opened device.
func PlayState(st b1.LightState) error {
	if ctrl == nil {
		return errMissingDevice
	}
	return ctrl.PlayState(st)
}

// PlayStateAndWait plays a blink(1) state on the opened device and wait for completion.
func PlayStateAndWait(st b1.LightState) error {
	if ctrl == nil {
		return errMissingDevice
	}
	return ctrl.PlayStateBlocking(st)
}

// StopPlaying stops playing any blink(1) state on the opened device.
func StopPlaying() error {
	if ctrl == nil {
		return errMissingDevice
	}
	return ctrl.StopPlaying()
}

// SetColor sets the color of the opened device.
func SetColor(cl color.Color) error {
	if ctrl == nil {
		return errMissingDevice
	}
	return ctrl.PlayColor(cl)
}

// PlayStateSequence plays a blink(1) state sequence on the opened device.
func PlayStateSequence(seq b1.StateSequence) error {
	if ctrl == nil {
		return errMissingDevice
	}
	// play state sequence one by one
	for _, st := range seq {
		if err := ctrl.PlayStateBlocking(st); err != nil {
			return err
		}
	}
	return nil
}
