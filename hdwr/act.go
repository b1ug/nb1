package hdwr

import (
	"errors"
	"image/color"
	"time"

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

// ReadOnChipSequence reads a blink(1) state sequence from the opened device.
func ReadOnChipSequence() (b1.StateSequence, error) {
	if ctrl == nil {
		return nil, errMissingDevice
	}
	return ctrl.ReadPattern()
}

// PlayOnChipPattern starts playing a blink(1) pattern on the opened device.
func PlayOnChipPattern(start, end, times int, wait bool) error {
	if ctrl == nil {
		return errMissingDevice
	}
	pt := b1.Pattern{
		StartPosition: uint(start),
		EndPosition:   uint(end),
		RepeatTimes:   uint(times),
	}
	if wait {
		return ctrl.PlayPatternBlocking(pt)
	}
	return ctrl.PlayPattern(pt)
}

// TickleOnChipPattern starts playing a blink(1) pattern on the opened device.
func TickleOnChipPattern(start, end int, waitTimeout time.Duration) error {
	if ctrl == nil {
		return errMissingDevice
	}
	return ctrl.SimpleTickle(uint(start), uint(end), waitTimeout, true)
}
