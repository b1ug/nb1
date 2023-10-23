package hdwr

import b1 "github.com/b1ug/blink1-go"

// ReadStateSequence reads a blink(1) state sequence from the opened device.
func ReadStateSequence() (b1.StateSequence, error) {
	if ctrl == nil {
		return nil, errMissingDevice
	}
	return ctrl.ReadPattern()
}
