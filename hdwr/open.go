package hdwr

import (
	"fmt"
	"strconv"

	"github.com/1set/gut/ystring"
	b1 "github.com/b1ug/blink1-go"
)

var (
	ctrl *b1.Controller
)

// OpenBlink1Device opens a corresponding blink(1) device by hint.
func OpenBlink1Device(hint string) (err error) {
	// if hint is empty, open the first blink(1) controller found
	if ystring.IsBlank(hint) {
		log.Debugw("attempting to open first available blink(1) controller", "hint", hint)
		ctrl, err = b1.OpenNextController()
		return
	}

	// if hint is a normal number, i.e. between 1 and 128, list and open the blink(1) controller by number
	if num, e := strconv.Atoi(hint); e == nil && num >= 1 && num <= 128 {
		log.Debugw("attempting to open blink(1) controller by number", "hint", hint, "provided_number", num)
		di := b1.ListDeviceInfo()
		if dc := len(di); dc < num {
			err = fmt.Errorf("unable to find corresponding device, provided number: %d, available devices: %d", num, dc)
			return
		}
		ctrl, err = b1.OpenController(di[num-1])
		return
	}

	// if hint is a serial number, list and open the blink(1) controller by serial number
	log.Debugw("attempting to open blink(1) controller by serial number", "hint", hint, "serial_number", hint)
	ctrl, err = b1.OpenControllerBySerialNumber(hint)
	return
}
