// Package schema provides the data structures for internal use of nb1 app.
package schema

import (
	hid "github.com/b1ug/gid"
)

// DeviceDetail is a struct that contains the device info and extra information.
type DeviceDetail struct {
	// DeviceInfo is the general HID device information of the device.
	*hid.DeviceInfo
	// IsBlink1 is true if the device is a blink(1) device.
	IsBlink1 bool
	// FirmwareVersion is the blink(1) firmware version of the device.
	FirmwareVersion int
}
