package hdwr

import (
	"fmt"
	"os"
	"strconv"

	"github.com/1set/gut/ystring"
	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/ownership"
)

var (
	ctrl        *b1.Controller
	deviceLease *ownership.Lease
)

// OpenBlink1Device opens a corresponding blink(1) device by hint.
func OpenBlink1Device(hint string) (err error) {
	if err = CloseBlink1Device(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			CloseBlink1Device()
		}
	}()
	_, indexErr := strconv.Atoi(hint)
	number, _ := strconv.Atoi(hint)
	isIndex := indexErr == nil && number >= 1 && number <= 128
	// Explicit serials can be checked before enumeration. Some native backends
	// hide exclusively owned devices from another process's enumeration.
	if !ystring.IsBlank(hint) && !isIndex {
		deviceLease, err = ownership.Acquire(os.Getenv("NB1_LOCK_DIR"), hint)
		if err != nil {
			return err
		}
	}
	infos := b1.ListDeviceInfo()
	var selected *gid.DeviceInfo
	// if hint is empty, open the first blink(1) controller found
	if ystring.IsBlank(hint) {
		if len(infos) > 0 {
			selected = infos[0]
		}
	} else if num, e := strconv.Atoi(hint); e == nil && num >= 1 && num <= 128 {
		if len(infos) < num {
			return fmt.Errorf("unable to find corresponding device, provided number: %d, available devices: %d", num, len(infos))
		}
		selected = infos[num-1]
	} else {
		for _, info := range infos {
			if info.SerialNumber != hint {
				continue
			}
			if selected != nil {
				return fmt.Errorf("serial %s matches multiple interfaces", hint)
			}
			selected = info
		}
	}
	if selected == nil {
		return fmt.Errorf("blink(1) device not found for %q", hint)
	}
	if deviceLease == nil {
		deviceLease, err = ownership.Acquire(os.Getenv("NB1_LOCK_DIR"), selected.SerialNumber)
		if err != nil {
			return err
		}
	}
	ctrl, err = b1.OpenController(selected)
	if err != nil {
		deviceLease.Close()
		deviceLease = nil
	}
	return err
}

// CloseBlink1Device stops SDK goroutines, closes HID, then releases ownership.
// It preserves stable output; turning off is an explicit command in legacy CLI.
func CloseBlink1Device() error {
	if ctrl != nil {
		ctrl.Close()
		ctrl = nil
	}
	if deviceLease != nil {
		err := deviceLease.Close()
		deviceLease = nil
		return err
	}
	return nil
}
