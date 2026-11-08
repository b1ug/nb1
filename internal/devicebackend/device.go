// Package devicebackend adapts the verified blink SDK path to the executor.
// It never writes patterns or flash and does not use the mk2 single-LED 'n'
// command. New generations require their own verified capability profile.
package devicebackend

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/ownership"
)

type driver struct {
	lockDir string
	metrics retryRegistry
}

func New(lockDir string) control.Driver { return &driver{lockDir: lockDir} }

// Discover reads enumeration metadata; it does not open selected devices or
// query firmware, and it does not imply support for every enumerated model.
func Discover() []control.DeviceInfo {
	result := make([]control.DeviceInfo, 0)
	for _, info := range b1.ListDeviceInfo() {
		result = append(result, control.DeviceInfo{Serial: info.SerialNumber, Product: info.Product, Path: info.Path, Capabilities: []string{}})
	}
	return result
}

func (d *driver) Open(serial string) (control.Backend, error) {
	lease, err := ownership.Acquire(d.lockDir, serial)
	if err != nil {
		if errors.Is(err, ownership.ErrBusy) {
			return nil, fmt.Errorf("%w: %s", control.ErrBusy, serial)
		}
		return nil, err
	}
	var device *b1.Device
	var selected *gid.DeviceInfo
	var info control.DeviceInfo
	matches := 0
	for _, di := range b1.ListDeviceInfo() {
		if di.SerialNumber != serial {
			continue
		}
		matches++
		selected = di
		info = control.DeviceInfo{Serial: serial, Product: di.Product, Path: di.Path}
	}
	if matches != 1 {
		lease.Close()
		return nil, fmt.Errorf("%w: serial %s has %d matching interfaces", control.ErrOffline, serial, matches)
	}
	// Open the same snapshot that passed the unique-interface check.
	if selected.VersionNumber != 2 {
		lease.Close()
		return nil, fmt.Errorf("%w: generation for %s is not covered by the mk2 profile", control.ErrInvalid, serial)
	}
	device, err = b1.OpenDevice(selected)
	if err != nil || device == nil {
		lease.Close()
		if err == nil {
			err = control.ErrOffline
		}
		return nil, err
	}
	tracker := d.metrics.device(serial)
	var version int
	err = retryMK2(tracker, nil, "query_firmware", "open: query firmware v", func() error {
		var queryErr error
		version, queryErr = device.GetVersion()
		return queryErr
	})
	if err != nil || version < 200 || version >= 300 {
		device.Close()
		lease.Close()
		if err == nil {
			err = fmt.Errorf("%w: firmware %d is outside the mk2 profile", control.ErrInvalid, version)
		}
		return nil, err
	}
	info.Firmware = version
	info.Capabilities = []string{"set", "program", "cancel", "hold", "observe", "raw-rgb", "mk2-basic"}
	return &backend{dev: device, lease: lease, info: info, metrics: tracker}, nil
}

type lamp interface {
	FadeToRGB(r, g, b byte, fadeMsec uint, led b1.LEDIndex) error
	ReadRGB(led b1.LEDIndex) (byte, byte, byte, error)
	ReadPlaystate() (b1.DevicePatternState, error)
	SetTickleMode(play, keep bool, start, end, timeout uint) error
	PlayLoop(play bool, start, end, times uint) error
	Close()
}

type backend struct {
	dev     lamp
	lease   *ownership.Lease
	info    control.DeviceInfo
	once    sync.Once
	metrics *retryTracker
	pause   func(time.Duration) // Tests replace backoff only; native I/O stays synchronous.
}

func (b *backend) Info() control.DeviceInfo {
	i := b.info
	i.Capabilities = append([]string(nil), i.Capabilities...)
	return i
}

func (b *backend) Set(step control.Step) error {
	// Defense in depth for direct Backend callers; normal jobs are normalized
	// before they enter the queue and therefore before hardware is opened.
	program, err := control.Normalize(control.Program{Steps: []control.Step{step}})
	if err != nil {
		return err
	}
	step = program.Steps[0]
	rgb, _ := hex.DecodeString(step.Color[1:])
	return b.operation("set_color", fmt.Sprintf("set: c led=%d color=%s fade_ms=%d", step.LED, step.Color, step.FadeMS), func() error {
		return b.dev.FadeToRGB(rgb[0], rgb[1], rgb[2], uint(step.FadeMS), b1.LEDIndex(step.LED))
	})
}

func (b *backend) Read() (control.Observation, error) {
	return b.read("observe")
}

func (b *backend) read(stage string) (control.Observation, error) {
	var colors [2]string
	for index := range colors {
		var r, g, blue byte
		err := b.operation("query_rgb", fmt.Sprintf("%s: query r led=%d", stage, index+1), func() error {
			var queryErr error
			r, g, blue, queryErr = b.dev.ReadRGB(b1.LEDIndex(index + 1))
			return queryErr
		})
		if err != nil {
			return control.Observation{}, err
		}
		colors[index] = fmt.Sprintf("#%02x%02x%02x", r, g, blue)
	}
	return control.Observation{LED1: colors[0], LED2: colors[1], SampledAt: time.Now().UTC()}, nil
}

func (b *backend) Stop(mode string) error {
	if mode != "off" && mode != "hold" {
		return fmt.Errorf("%w: cleanup mode", control.ErrInvalid)
	}
	// D(play=false,keep=true) disables the server-down timer without calling
	// firmware off(); p(false) stops autonomous playback without changing RGB.
	if err := b.operation("disable_tickle", "stop("+mode+"): disable tickle D", func() error {
		return b.dev.SetTickleMode(false, true, 0, 0, 0)
	}); err != nil {
		return err
	}
	if err := b.operation("stop_playback", "stop("+mode+"): stop playback p", func() error {
		return b.dev.PlayLoop(false, 0, 0, 0)
	}); err != nil {
		return err
	}
	var expected control.Observation
	if mode == "hold" {
		var err error
		expected, err = b.read("stop(hold) snapshot")
		if err != nil {
			return err
		}
		for index, color := range []string{expected.LED1, expected.LED2} {
			rgb, _ := hex.DecodeString(color[1:])
			if err = b.operation("freeze_rgb", fmt.Sprintf("stop(hold): freeze c led=%d color=%s", index+1, color), func() error {
				return b.dev.FadeToRGB(rgb[0], rgb[1], rgb[2], 0, b1.LEDIndex(index+1))
			}); err != nil {
				return err
			}
		}
	} else {
		expected = control.Observation{LED1: "#000000", LED2: "#000000"}
		if err := b.operation("off_rgb", "stop(off): c all black", func() error {
			return b.dev.FadeToRGB(0, 0, 0, 0, b1.LEDAll)
		}); err != nil {
			return err
		}
	}
	var state b1.DevicePatternState
	if err := b.operation("query_playback", "stop("+mode+"): query playback S", func() error {
		var queryErr error
		state, queryErr = b.dev.ReadPlaystate()
		return queryErr
	}); err != nil {
		return err
	}
	if state.IsPlaying {
		return fmt.Errorf("stop(%s): device still reports autonomous playback", mode)
	}
	// Zero-time fades are applied on the firmware's next LED update. Check
	// actual output rather than acknowledging cleanup before that update.
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		actual, err := b.read("stop(" + mode + ") verify")
		if err != nil {
			return err
		}
		if actual.LED1 == expected.LED1 && actual.LED2 == expected.LED2 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cleanup readback mismatch: expected %s/%s got %s/%s", expected.LED1, expected.LED2, actual.LED1, actual.LED2)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *backend) Close() {
	b.once.Do(func() {
		b.dev.Close()
		if b.lease != nil {
			b.lease.Close()
		}
	})
}
