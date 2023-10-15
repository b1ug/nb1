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
		log.Debugw("open first blink(1) controller for blank hint", "hint", hint)
		ctrl, err = b1.OpenNextController()
		return
	}

	// if hint is a normal number, i.e. between 1 and 128, list and open the blink(1) controller by number
	if num, e := strconv.Atoi(hint); e == nil && num >= 1 && num <= 128 {
		log.Debugw("open blink(1) controller by number", "hint", hint, "number", num)
		di := b1.ListDeviceInfo()
		if dc := len(di); dc < num {
			err = fmt.Errorf("no blink(1) controller found by number %d, only %d found", num, dc)
			return
		}
		ctrl, err = b1.OpenController(di[num-1])
		return
	}

	// if hint is a serial number, list and open the blink(1) controller by serial number
	log.Debugw("open blink(1) controller by serial number", "hint", hint)
	ctrl, err = b1.OpenControllerBySerialNumber(hint)
	return
}
