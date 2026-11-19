package devicebackend

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/nativeinspection"
	"github.com/b1ug/nb1/internal/ownership"
)

type testLamp struct {
	colors  [2][3]byte
	playing bool
	tickle  bool
	fail    error
	calls   []string
	onClose func()
}

func (f *testLamp) FadeToRGB(r, g, b byte, _ uint, led b1.LEDIndex) error {
	f.calls = append(f.calls, "fade")
	if f.fail != nil {
		return f.fail
	}
	for i := range f.colors {
		if led == b1.LEDAll || int(led) == i+1 {
			f.colors[i] = [3]byte{r, g, b}
		}
	}
	f.playing = false
	return nil
}
func (f *testLamp) ReadRGB(led b1.LEDIndex) (byte, byte, byte, error) {
	c := f.colors[int(led)-1]
	return c[0], c[1], c[2], f.fail
}
func (f *testLamp) ReadPlaystate() (b1.DevicePatternState, error) {
	return b1.DevicePatternState{IsPlaying: f.playing}, f.fail
}
func (f *testLamp) SetTickleMode(play, keep bool, _, _, _ uint) error {
	f.calls = append(f.calls, "tickle")
	if f.fail != nil {
		return f.fail
	}
	if !keep {
		return errors.New("hold cleanup unexpectedly requested firmware off")
	}
	f.tickle = play
	return nil
}
func (f *testLamp) PlayLoop(play bool, _, _, _ uint) error {
	f.calls = append(f.calls, "play")
	if f.fail != nil {
		return f.fail
	}
	f.playing = play
	return nil
}
func (f *testLamp) Close() {
	if f.onClose != nil {
		f.onClose()
	}
}

func TestHoldStopsAutonomousModesAndPreservesRawLEDs(t *testing.T) {
	f := &testLamp{colors: [2][3]byte{{128, 32, 16}, {3, 5, 7}}, playing: true, tickle: true}
	b := &backend{dev: f}
	before := f.colors
	if err := b.Stop("hold"); err != nil {
		t.Fatal(err)
	}
	if f.playing || f.tickle || f.colors != before {
		t.Fatalf("hold altered state: %+v", f)
	}
	if len(f.calls) != 4 || f.calls[0] != "tickle" || f.calls[1] != "play" {
		t.Fatalf("autonomous modes were not stopped before freeze: %v", f.calls)
	}
}
func TestOffStopsAutonomousModesAndBlacksBothLEDs(t *testing.T) {
	f := &testLamp{colors: [2][3]byte{{255, 0, 0}, {0, 0, 255}}, playing: true, tickle: true}
	b := &backend{dev: f}
	if err := b.Stop("off"); err != nil {
		t.Fatal(err)
	}
	if f.playing || f.tickle || f.colors != ([2][3]byte{}) {
		t.Fatalf("not quiescent: %+v", f)
	}
}
func TestBackendDoesNotHideNativeErrors(t *testing.T) {
	want := errors.New("USB failed")
	f := &testLamp{fail: want}
	b := &backend{dev: f}
	if err := b.Stop("off"); !errors.Is(err, want) {
		t.Fatalf("stop error %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("continued after failed stop: %v", f.calls)
	}
}
func TestExactRGBAndValidationBeforeHardware(t *testing.T) {
	f := &testLamp{}
	b := &backend{dev: f}
	if err := b.Set(control.Step{LED: 256, Color: "#800000"}); err == nil {
		t.Fatal("accepted overflowing LED")
	}
	if len(f.calls) != 0 {
		t.Fatal("hardware touched for invalid input")
	}
	if err := b.Set(control.Step{LED: 2, Color: "#800000", FadeMS: 10}); err != nil {
		t.Fatal(err)
	}
	if f.colors[1] != ([3]byte{128, 0, 0}) || f.colors[0] != ([3]byte{}) {
		t.Fatalf("unexpected gamma/addressing: %v", f.colors)
	}
}
func TestNativeHandleClosesBeforeOwnershipRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := ownership.Acquire(dir, "serial")
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	f := &testLamp{onClose: func() {
		closed++
		other, err := ownership.Acquire(dir, "serial")
		if other != nil {
			other.Close()
		}
		if !errors.Is(err, ownership.ErrBusy) {
			t.Errorf("lease released before native close: %v", err)
		}
	}}
	b := &backend{dev: f, lease: lease}
	b.Close()
	b.Close()
	if closed != 1 {
		t.Fatalf("close count %d", closed)
	}
	other, err := ownership.Acquire(dir, "serial")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

type flashLampSpy struct {
	staleState bool
	*testLamp
	meta                 *gid.DeviceInfo
	rows                 [32]b1.DeviceLightState
	writes, reads, saves int
	onWrite              func(uint) error
	onRead               func(uint, *nativeinspection.LineRead) error
	onSave               func() error
}

func (f *flashLampSpy) GetDeviceInfo() *gid.DeviceInfo { return f.meta }
func (f *flashLampSpy) SetPatternLine(pos uint, row b1.DeviceLightState) error {
	f.writes++
	f.rows[pos] = row // A transport error can follow a successful device-side change.
	if f.onWrite != nil {
		return f.onWrite(pos)
	}
	return nil
}
func observedFlashBytes(raw []byte) nativeinspection.Reply {
	return nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, LengthValid: true, ReturnedLength: len(raw), Bytes: raw}
}

func (f *flashLampSpy) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	// Observed FW204 Mac S layout: 00 53 00 00 20 00 00 00.
	raw := []byte{0, 'S', 0, 0, 32, 0, 0, 0}
	if f.playing {
		raw[2] = 1
	}
	if f.staleState {
		raw[1] = 'R'
	}
	read := nativeinspection.PlaybackRead{Reply: observedFlashBytes(raw)}
	read.Fields.Mode = &raw[2]
	return read, f.fail
}

func (f *flashLampSpy) ReadLine(pos int) (nativeinspection.LineRead, error) {
	f.reads++
	row := f.rows[pos]
	rgb := [3]byte{row.R, row.G, row.B}
	led, ticks := byte(row.LED), uint16(row.FadeTimeMsec/10)
	raw := []byte{0, 'R', row.R, row.G, row.B, byte(ticks >> 8), byte(ticks), led}
	result := nativeinspection.LineRead{Position: pos, RGB8: &rgb, LEDCode: &led, DurationTicks: &ticks, Reply: observedFlashBytes(raw)}
	if f.onRead != nil {
		err := f.onRead(uint(pos), &result)
		return result, err
	}
	return result, nil
}
func (f *flashLampSpy) SavePattern() error {
	f.saves++
	if f.onSave != nil {
		return f.onSave()
	}
	return nil
}
func newFlashBackend(firmware int) (*backend, *flashLampSpy) {
	spy := &flashLampSpy{testLamp: &testLamp{}, meta: nativeMetadata()}
	b := &backend{dev: spy, info: control.DeviceInfo{Serial: spy.meta.SerialNumber, Firmware: firmware}, metrics: &retryTracker{}}
	return b, spy
}

func TestResetFlashStagesWholePresetThenSavesOnce(t *testing.T) {
	for _, firmware := range []int{204, 205} {
		for _, preset := range []string{"off", "rainbow", "starlight", "fireflies"} {
			t.Run(fmt.Sprintf("%d/%s", firmware, preset), func(t *testing.T) {
				b, spy := newFlashBackend(firmware)
				for i := range spy.rows {
					spy.rows[i] = b1.DeviceLightState{B: 255, LED: b1.LED2}
				}
				spy.onSave = func() error {
					if spy.writes != 16 || spy.reads != 16 {
						t.Fatalf("saved before verifying entire image: %d writes, %d reads", spy.writes, spy.reads)
					}
					return nil
				}
				result, err := b.ResetFlash(context.Background(), preset)
				if err != nil || result.Validate(b.info.Serial, preset) != nil || result.Firmware != firmware || result.Lines != 16 || result.Status != flashreset.Acknowledged || spy.saves != 1 {
					t.Fatalf("%+v, saves=%d, err=%v", result, spy.saves, err)
				}
				distinct := map[[3]byte]bool{}
				for i, row := range spy.rows {
					if i >= 16 {
						if row != (b1.DeviceLightState{B: 255, LED: b1.LED2}) {
							t.Fatalf("modified RAM-only row %d", i)
						}
						continue
					}
					if row.LED != b1.LEDAll || row.FadeTimeMsec < 10 || row.FadeTimeMsec%10 != 0 {
						t.Fatalf("unrepresentable row %d: %+v", i, row)
					}
					rgb := [3]byte{row.R, row.G, row.B}
					distinct[rgb] = true
					if preset == "off" && rgb != [3]byte{} {
						t.Fatalf("Off row %d is lit", i)
					}
				}
				if preset != "off" && len(distinct) < 3 {
					t.Fatalf("static preset %s", preset)
				}
			})
		}
	}
}

func TestResetFlashRejectsUnsafeTargetsBeforeStaging(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*backend, *flashLampSpy)
		why    error
	}{
		{"unknown-firmware", func(b *backend, _ *flashLampSpy) { b.info.Firmware = 206 }, flashreset.ErrUnsupported},
		{"different-device", func(_ *backend, s *flashLampSpy) { s.meta.SerialNumber = "20000002" }, flashreset.ErrUnsupported},
		{"absent-metadata", func(_ *backend, s *flashLampSpy) { s.meta = nil }, flashreset.ErrUnsupported},
		{"traced-session", func(b *backend, _ *flashLampSpy) { b.trace = &backendTrace{} }, flashreset.ErrUnsupported},
		{"mk3-startup-collateral", func(b *backend, s *flashLampSpy) {
			s.meta.VersionNumber, s.meta.Product, s.meta.SerialNumber = 0x0101, "blink(1) mk3", "30000001"
			b.info.Serial, b.info.Firmware = s.meta.SerialNumber, 304
		}, flashreset.ErrUnsupported},
		{"autonomous-playback", func(_ *backend, s *flashLampSpy) { s.playing = true }, control.ErrBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, spy := newFlashBackend(204)
			tc.change(b, spy)
			if got := slices.Contains(b.Info().Capabilities, "flash-reset"); got != errors.Is(tc.why, control.ErrBusy) {
				t.Fatalf("unexpected Flash capability: %v", got)
			}
			receipt, err := b.ResetFlash(context.Background(), "off")
			if !errors.Is(err, tc.why) || receipt.Status != "" || spy.writes+spy.reads+spy.saves != 0 || len(spy.calls) != 0 {
				t.Fatalf("%+v, %v, calls %+v", receipt, err, spy)
			}
		})
	}
	b, spy := newFlashBackend(204)
	if _, err := b.ResetFlash(context.Background(), "not-a-preset"); !errors.Is(err, control.ErrInvalid) || spy.writes+spy.reads+spy.saves != 0 {
		t.Fatal(err, spy)
	}
}

func TestResetFlashRetainsPartialStateAndNeverRetriesSave(t *testing.T) {
	cause := errors.New("injected HID failure")
	for _, tc := range []struct {
		name                    string
		hook                    func(*flashLampSpy, context.CancelFunc)
		staged, verified, saves int
		status                  string
		wantErr                 error
	}{
		{"playback-query", func(s *flashLampSpy, _ context.CancelFunc) { s.fail = cause }, 0, 0, 0, flashreset.StagingFailed, cause},
		{"partial-RAM", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onWrite = func(i uint) error {
				if i == 5 {
					return cause
				}
				return nil
			}
		}, 5, 0, 0, flashreset.StagingFailed, cause},
		{"read-error", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onRead = func(i uint, _ *nativeinspection.LineRead) error {
				if i == 7 {
					return cause
				}
				return nil
			}
		}, 16, 7, 0, flashreset.StagingFailed, cause},
		{"stale-S", func(s *flashLampSpy, _ context.CancelFunc) { s.staleState = true }, 0, 0, 0, flashreset.StagingFailed, nil},
		{"stale-R", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onRead = func(i uint, row *nativeinspection.LineRead) error {
				if i == 1 {
					row.Reply.Bytes[1] = 'P'
				}
				return nil
			}
		}, 16, 1, 0, flashreset.StagingFailed, nil},
		{"truncated-R", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onRead = func(i uint, row *nativeinspection.LineRead) error {
				if i == 1 {
					row.Reply = observedFlashBytes(row.Reply.Bytes[:7])
				}
				return nil
			}
		}, 16, 1, 0, flashreset.StagingFailed, nil},

		{"missing-LED", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onRead = func(i uint, row *nativeinspection.LineRead) error {
				if i == 3 {
					row.LEDCode = nil
				}
				return nil
			}
		}, 16, 3, 0, flashreset.StagingFailed, nil},
		{"wrong-LED", func(s *flashLampSpy, _ context.CancelFunc) {
			s.onRead = func(i uint, row *nativeinspection.LineRead) error {
				if i == 11 {
					*row.LEDCode = 1
				}
				return nil
			}
		}, 16, 11, 0, flashreset.StagingFailed, nil},
		{"cancel-while-staging", func(s *flashLampSpy, cancel context.CancelFunc) {
			s.onWrite = func(i uint) error {
				if i == 0 {
					cancel()
				}
				return nil
			}
		}, 1, 0, 0, flashreset.StagingFailed, context.Canceled},
		{"cancel-after-final-read", func(s *flashLampSpy, cancel context.CancelFunc) {
			s.onRead = func(i uint, _ *nativeinspection.LineRead) error {
				if i == 15 {
					cancel()
				}
				return nil
			}
		}, 16, 16, 0, flashreset.StagingFailed, context.Canceled},
		{"save-error", func(s *flashLampSpy, _ context.CancelFunc) { s.onSave = func() error { return cause } }, 16, 16, 1, flashreset.Unconfirmed, cause},
		{"cancel-during-accepted-save", func(s *flashLampSpy, cancel context.CancelFunc) { s.onSave = func() error { cancel(); return nil } }, 16, 16, 1, flashreset.Acknowledged, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, spy := newFlashBackend(204)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tc.hook(spy, cancel)
			r, err := b.ResetFlash(ctx, "rainbow")
			if r.Validate(b.info.Serial, "rainbow") != nil || r.Status != tc.status || r.Staged != tc.staged || r.Verified != tc.verified || spy.saves != tc.saves || (err == nil) != (tc.status == flashreset.Acknowledged) || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("receipt %+v, saves %d, err %v", r, spy.saves, err)
			}
			if tc.name == "partial-RAM" && (spy.writes != 6 || spy.rows[5].FadeTimeMsec == 0) {
				t.Fatalf("missing attempted partial row: %+v", spy)
			}
		})
	}
}

func TestFlashCommandEchoAcceptsNativePrefixes(t *testing.T) {
	// The first two examples are real eight-byte FW204 observations. The
	// standard ID-slot representation may also include an extra trailing byte.
	for _, tc := range []struct {
		raw     string
		command byte
		prefix  int
		ok      bool
	}{
		{"0053000020000000", 'S', 7, true},
		{"0052ff0000003201", 'R', 8, true},
		{"01520000ff003202fd", 'R', 8, true},
		{"00530000200000", 'S', 7, true},
		{"005300002000", 'S', 7, false},
		{"0050000000000000", 'R', 8, false},
	} {
		raw, err := hex.DecodeString(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := echoesFlashCommand(observedFlashBytes(raw), tc.command, tc.prefix); got != tc.ok {
			t.Fatalf("reply %s command %c: %v", tc.raw, tc.command, got)
		}
	}
}
