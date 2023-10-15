package hdwr

import (
	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/schema"
	"go.uber.org/zap"
)

// ListAllBlink1Detail returns a list of all blink(1) devices with details.
func ListAllBlink1Detail(fwVer bool) ([]*schema.DeviceDetail, error) {
	dis := b1.ListDeviceInfo()
	dds := make([]*schema.DeviceDetail, len(dis))
	for i, di := range dis {
		dds[i] = &schema.DeviceDetail{DeviceInfo: di}
		if fwVer {
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
