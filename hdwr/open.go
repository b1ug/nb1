package hdwr

import (
	"sort"

	b1 "github.com/b1ug/blink1-go"
	hid "github.com/b1ug/gid"
	"github.com/b1ug/nb1/schema"
	"go.uber.org/zap"
)

// ListAllBlink1Detail returns a list of all blink(1) devices with details.
func ListAllBlink1Detail(fwVer, showAll bool) ([]*schema.DeviceDetail, error) {
	dis := listAllDeviceInfo(showAll)
	dds := make([]*schema.DeviceDetail, len(dis))
	for i, di := range dis {
		isB1 := b1.IsBlink1Device(di)
		dds[i] = &schema.DeviceDetail{DeviceInfo: di, IsBlink1: isB1}
		if fwVer && isB1 {
			dev, err := b1.OpenDevice(di)
			if err != nil {
				log.Errorw("open device failed", "idx", i, "device_info", di, zap.Error(err))
				return nil, err
			}

			ver, err := dev.GetVersion()
			dev.Close()
			if err != nil {
				log.Errorw("get device firmware version failed", "idx", i, "device", dev, zap.Error(err))
				return nil, err
			}
			dds[i].FirmwareVersion = ver
			log.Debugw("got device firmware version", "idx", i, "device", dev, "version", ver)
		}
	}
	return dds, nil
}

func listAllDeviceInfo(showAll bool) []*hid.DeviceInfo {
	// use different filter function for blink(1) devices or all HID devices
	cond := b1.IsBlink1Device
	if showAll {
		cond = func(di *hid.DeviceInfo) bool {
			return true
		}
	}

	// list all devices
	infos := hid.ListAllDevices(cond)
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].SerialNumber < infos[j].SerialNumber
	})
	return infos
}
