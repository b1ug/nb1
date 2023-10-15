package hdwr

import b1 "github.com/b1ug/blink1-go"

// PlayState plays a blink(1) state on the opened device.
func PlayState(st b1.LightState) error {
	return ctrl.PlayState(st)
}

// PlayStateAndWait plays a blink(1) state on the opened device and wait for completion.
func PlayStateAndWait(st b1.LightState) error {
	return ctrl.PlayStateBlocking(st)
}
