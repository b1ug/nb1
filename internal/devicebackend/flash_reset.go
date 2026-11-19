package devicebackend

import (
	"context"
	"fmt"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

// sdkLamp exposes raw RAM observations on the same handle as writes. The
// existing native reader counts each R/S once and preserves the returned prefix.
type sdkLamp struct {
	*b1.Device
	nativeSDKReader
}

type flashLamp interface {
	nativeinspection.Reader
	GetDeviceInfo() *gid.DeviceInfo
	SetPatternLine(uint, b1.DeviceLightState) error
	SavePattern() error
}

// flashStorage is used for both capability advertisement and operation admission.
func (b *backend) flashStorage() (flashLamp, int) {
	device, ok := b.dev.(flashLamp)
	// The existing trace contract cannot describe P/l/R/W. Do not emit
	// unrecorded hardware writes while presenting a complete trace.
	if !ok || b.trace != nil {
		return nil, 0
	}
	info := device.GetDeviceInfo()
	if info == nil || b.info.Serial == "" || info.SerialNumber != b.info.Serial {
		return nil, 0
	}
	profile := b1.IdentifyPatternCapabilities(info, b.info.Firmware)
	usable := profile.Storage.UsableFlashLines
	if profile.Identity.Model != b1.ProfileModelMK2 || profile.Applicability != b1.PatternApplicabilityFirmwareMatched ||
		(b.info.Firmware != 204 && b.info.Firmware != 205) || usable.Knowledge != b1.PatternKnowledgeKnown || usable.Value == nil {
		return nil, 0
	}
	return device, *usable.Value
}

// ResetFlash runs on the idle worker that owns this handle. FW204/205 W copies
// the first 16 RAM rows. FW304 W includes unrelated RAM startup parameters;
// FW206+ on MK2 has unresolved reserved rows. Neither is a pattern-only reset.
func (b *backend) ResetFlash(ctx context.Context, preset string) (flashreset.Result, error) {
	var empty flashreset.Result
	if !flashreset.Valid(preset) {
		return empty, fmt.Errorf("%w: unknown Flash preset %q", control.ErrInvalid, preset)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	device, lines := b.flashStorage()
	if device == nil {
		return empty, flashreset.ErrUnsupported
	}
	rows, err := flashreset.Image(preset, lines)
	if err != nil {
		return empty, err
	}
	result := flashreset.Result{Serial: b.info.Serial, Preset: preset, Firmware: b.info.Firmware, Lines: len(rows), Status: flashreset.StagingFailed}

	// The running native scheduler can change the LED selection between the l
	// and P commands used by the SDK. Leave autonomous playback undisturbed.
	state, err := device.ReadPlayback()
	if err != nil {
		return result, fmt.Errorf("read playback state before staging: %w", err)
	}
	if !echoesFlashCommand(state.Reply, 'S', 7) || state.Fields.Mode == nil {
		return result, fmt.Errorf("invalid playback response before staging")
	}
	if *state.Fields.Mode != 0 {
		return empty, fmt.Errorf("%w: stop native playback before resetting Flash", control.ErrBusy)
	}

	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		err := singleNativeOperation(b.metrics, "stage_flash_pattern_line", func() error {
			return device.SetPatternLine(uint(i), b1.DeviceLightState{R: row.R, G: row.G, B: row.B, LED: b1.LEDAll, FadeTimeMsec: uint(row.Ticks) * 10})
		})
		if err != nil {
			return result, fmt.Errorf("stage RAM row %d: %w", i, err)
		}
		result.Staged++
	}

	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		observed, err := device.ReadLine(i)
		if err != nil {
			return result, fmt.Errorf("read staged RAM row %d: %w", i, err)
		}
		// SDK raw R uses a separate response buffer. In both matched firmware
		// sources byte 7 is the stored LED selector, not a position echo.
		if !echoesFlashCommand(observed.Reply, 'R', 8) || observed.Position != i || observed.RGB8 == nil || *observed.RGB8 != [3]byte{row.R, row.G, row.B} ||
			observed.DurationTicks == nil || *observed.DurationTicks != row.Ticks ||
			observed.LEDCode == nil || *observed.LEDCode != byte(b1.LEDAll) {
			return result, fmt.Errorf("staged RAM row %d did not match the requested preset", i)
		}
		result.Verified++
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Status = flashreset.Unconfirmed
	// A native failure on FW204 can occur after persistence. Repeating W is
	// unsafe, and R only observes RAM until an independent cold restart.
	if err := singleNativeOperation(b.metrics, "save_flash_pattern", device.SavePattern); err != nil {
		return result, fmt.Errorf("Flash save outcome unknown: %w", err)
	}
	result.Status = flashreset.Acknowledged
	return result, nil
}

// Both matched firmware versions first copy the request into the response and
// retain the R/S command in byte 1. GID's first (report-ID) slot can be 0 on a
// real MK2. The final byte after the known fields is optional. The requested
// row number is not echoed; even a matching R cannot authenticate the address.
func echoesFlashCommand(reply nativeinspection.Reply, command byte, prefix int) bool {
	return reply.ReadAttempted && reply.LengthValid && reply.ReturnedLength == len(reply.Bytes) &&
		len(reply.Bytes) >= prefix && reply.Bytes[1] == command
}
