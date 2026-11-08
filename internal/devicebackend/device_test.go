package devicebackend

import (
	"errors"
	"path/filepath"
	"testing"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/internal/control"
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
