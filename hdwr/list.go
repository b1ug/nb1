package hdwr

import (
	"os"
	"sort"

	b1 "github.com/b1ug/blink1-go"
	hid "github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/schema"
	"go.uber.org/zap"
)

// ListDeviceDetails returns a list of all blink(1) devices with details.
func ListDeviceDetails(fwVer, showAll bool) ([]*schema.DeviceDetail, error) {
	dis := listAllDeviceInfo(showAll)
	dds := make([]*schema.DeviceDetail, len(dis))
	for i, di := range dis {
		isB1 := b1.IsBlink1Device(di)
		dds[i] = &schema.DeviceDetail{DeviceInfo: di, IsBlink1: isB1}
		if fwVer && isB1 {
			ver, err := readDeviceFirmware(di)
			if err != nil {
				log.Errorw("get device firmware version failed", "idx", i, "device_info", di, zap.Error(err))
				return nil, err
			}
			dds[i].FirmwareVersion = ver
			log.Debugw("got device firmware version", "idx", i, "device_info", di, "version", ver)
		}
	}
	return dds, nil
}

func readDeviceFirmware(info *hid.DeviceInfo) (int, error) {
	lease, err := ownership.Acquire(os.Getenv("NB1_LOCK_DIR"), info.SerialNumber)
	if err != nil {
		return 0, err
	}
	defer lease.Close()
	device, err := b1.OpenDevice(info)
	if err != nil {
		return 0, err
	}
	defer device.Close()
	return device.GetVersion()
}

func listAllDeviceInfo(showAll bool) []*hid.DeviceInfo {
	return listDeviceInfoFrom(showAll, b1.ListDeviceInfo, func() []*hid.DeviceInfo {
		return hid.ListAllDevices(nil)
	})
}

func listDeviceInfoFrom(showAll bool, blinkDevices, allDevices func() []*hid.DeviceInfo) []*hid.DeviceInfo {
	var infos []*hid.DeviceInfo
	if showAll {
		infos = allDevices()
	} else {
		// The SDK requests the exact blink(1) VID/PID at the discovery entry.
		// Do not enumerate unrelated HID collections before filtering them.
		infos = blinkDevices()
	}
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].SerialNumber < infos[j].SerialNumber
	})
	return infos
}
