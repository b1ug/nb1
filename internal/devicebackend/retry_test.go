package devicebackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/internal/control"
)

type retryTarget struct {
	r, g, b byte
	fade    uint
	led     b1.LEDIndex
}

type retryLamp struct {
	testLamp
	failures map[string][]error
	events   []string
	targets  []retryTarget
	after    func(string, error)
}

func (f *retryLamp) attempt(kind string) error {
	f.events = append(f.events, kind)
	var err error
	if failures := f.failures[kind]; len(failures) > 0 {
		err = failures[0]
		f.failures[kind] = failures[1:]
	}
	if f.after != nil {
		f.after(kind, err)
	}
	return err
}

func (f *retryLamp) FadeToRGB(r, g, b byte, fade uint, led b1.LEDIndex) error {
	f.targets = append(f.targets, retryTarget{r, g, b, fade, led})
	if err := f.attempt("c"); err != nil {
		return err
	}
	return f.testLamp.FadeToRGB(r, g, b, fade, led)
}

func (f *retryLamp) ReadRGB(led b1.LEDIndex) (byte, byte, byte, error) {
	if err := f.attempt(fmt.Sprintf("r%d", led)); err != nil {
		return 255, 255, 255, err // Failed data must never become an observation.
	}
	return f.testLamp.ReadRGB(led)
}

func (f *retryLamp) SetTickleMode(play, keep bool, start, end, timeout uint) error {
	if err := f.attempt("D"); err != nil {
		return err
	}
	return f.testLamp.SetTickleMode(play, keep, start, end, timeout)
}

func (f *retryLamp) PlayLoop(play bool, start, end, times uint) error {
	if err := f.attempt("p"); err != nil {
		return err
	}
	return f.testLamp.PlayLoop(play, start, end, times)
}

func (f *retryLamp) ReadPlaystate() (b1.DevicePatternState, error) {
	if err := f.attempt("S"); err != nil {
		return b1.DevicePatternState{}, err
	}
	return f.testLamp.ReadPlaystate()
}

func TestRetryOnlyKnownNativeLeafCauses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("b1: write fail: %w", errors.New("general error")), true},
		{fmt.Errorf("query: %w", fmt.Errorf("b1: read fail: %w", errors.New("I/O Timeout"))), true},
		{errors.New("device is busy"), true},
		{control.ErrBusy, false},
		{control.ErrInvalid, false},
		{fmt.Errorf("request: %w", context.Canceled), false},
		{context.DeadlineExceeded, false},
		{errors.New("hid: device disconnected"), false},
		{errors.New("b1: device closed"), false},
		{errors.New("device offline"), false},
		{errors.New("unsupported function"), false},
		{errors.New("general error in invalid argument"), false},
		{io.ErrUnexpectedEOF, false},
		{fmt.Errorf("%w: general error", control.ErrInvalid), false},
	} {
		if got := transientMK2Transport(tc.err); got != tc.want {
			t.Errorf("retry eligibility for %v = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestSetRetriesExactTargetWithBoundedBackoff(t *testing.T) {
	f := &retryLamp{failures: map[string][]error{"c": {
		fmt.Errorf("b1: write fail: %w", errors.New("general error")),
		errors.New("device is busy"),
	}}}
	d := New("").(*driver)
	var waits []time.Duration
	b := &backend{dev: f, metrics: d.metrics.device("a"), pause: func(delay time.Duration) { waits = append(waits, delay) }}
	if err := b.Set(control.Step{LED: 2, Color: "#8000FF", FadeMS: 120}); err != nil {
		t.Fatal(err)
	}
	want := retryTarget{128, 0, 255, 120, b1.LED2}
	if len(f.targets) != 3 || f.targets[0] != want || f.targets[1] != want || f.targets[2] != want {
		t.Fatalf("retries changed exact target: %+v", f.targets)
	}
	if !reflect.DeepEqual(waits, []time.Duration{5 * time.Millisecond, 15 * time.Millisecond}) {
		t.Fatalf("retry backoff = %v", waits)
	}
	metrics := d.Metrics()
	if len(metrics) != 1 || metrics[0].Calls != 1 || metrics[0].Attempts != 3 || metrics[0].Retries != 2 || metrics[0].Failures != 0 || metrics[0].LastError == "" {
		t.Fatalf("recovery not observable: %+v", metrics)
	}
	if got := SnapshotMetrics(d); len(got) != 1 || got[0].RecoveredOperations != 1 || got[0].FailedOperations != 0 {
		t.Fatalf("recovery summary = %+v", got)
	}
}

func TestPermanentTransientFailureReturnsFinalCauseAndAttempts(t *testing.T) {
	first, second, third := errors.New("general error"), errors.New("I/O Timeout"), errors.New("general error")
	f := &retryLamp{failures: map[string][]error{"c": {first, second, third, nil}}}
	d := New("").(*driver)
	b := &backend{dev: f, metrics: d.metrics.device("a"), pause: func(time.Duration) {}}
	err := b.Set(control.Step{LED: 1, Color: "#ff0000"})
	var failed *OperationError
	if !errors.As(err, &failed) || failed.Attempts != 3 || !errors.Is(err, third) || !strings.Contains(failed.Operation, "led=1") {
		t.Fatalf("missing final operation context: %v", err)
	}
	if len(f.targets) != 3 {
		t.Fatalf("native calls exceeded bound: %d", len(f.targets))
	}
	metrics := d.Metrics()
	if len(metrics) != 1 || metrics[0].Failures != 1 || metrics[0].Retries != 2 {
		t.Fatalf("permanent failure metrics = %+v", metrics)
	}
	if got := SnapshotMetrics(d); got[0].ExhaustedOperations != 1 {
		t.Fatalf("exhausted retry not recorded: %+v", got)
	}
}

func TestNonRetryableFailureImmediatelyReturnsItsCause(t *testing.T) {
	for _, failure := range []error{errors.New("hid: device disconnected"), control.ErrInvalid, io.ErrUnexpectedEOF} {
		f := &retryLamp{failures: map[string][]error{"c": {failure, nil}}}
		b := &backend{dev: f, pause: func(time.Duration) { t.Fatal("nonretryable failure slept") }}
		err := b.Set(control.Step{Color: "#ff0000"})
		var operation *OperationError
		if !errors.As(err, &operation) || operation.Attempts != 1 || !errors.Is(err, failure) || len(f.targets) != 1 {
			t.Fatalf("nonretryable failure lost or retried: %v, calls=%d", err, len(f.targets))
		}
	}
}

func TestQueriesRepeatCompleteSDKCallsAndDiscardFailedData(t *testing.T) {
	f := &retryLamp{
		testLamp: testLamp{colors: [2][3]byte{{1, 2, 3}, {4, 5, 6}}},
		failures: map[string][]error{"r1": {fmt.Errorf("b1: read fail: %w", errors.New("I/O Timeout"))}},
	}
	b := &backend{dev: f, pause: func(time.Duration) {}}
	observed, err := b.Read()
	if err != nil || observed.LED1 != "#010203" || observed.LED2 != "#040506" {
		t.Fatalf("retried query leaked failed response: %+v, %v", observed, err)
	}
	if got := strings.Join(f.events, ","); got != "r1,r1,r2" {
		t.Fatalf("query did not reissue whole SDK ReadRGB: %s", got)
	}
}

func TestHoldRetryUsesCapturedTargetsWithoutResnapshot(t *testing.T) {
	f := &retryLamp{
		testLamp: testLamp{colors: [2][3]byte{{128, 0, 0}, {0, 0, 128}}, playing: true, tickle: true},
		failures: map[string][]error{"c": {errors.New("general error")}},
	}
	f.after = func(kind string, err error) {
		if kind == "c" && err != nil {
			// An uncertain failed write may coincide with a still-running fade.
			f.colors[0] = [3]byte{255, 255, 255}
		}
	}
	b := &backend{dev: f, pause: func(time.Duration) {}}
	if err := b.Stop("hold"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.events, ","); got != "D,p,r1,r2,c,c,c,S,r1,r2" {
		t.Fatalf("hold retry repeated stop/snapshot or skipped verification: %s", got)
	}
	if len(f.targets) != 3 || f.targets[0] != f.targets[1] || f.colors != ([2][3]byte{{128, 0, 0}, {0, 0, 128}}) {
		t.Fatalf("hold did not preserve captured colors: targets=%+v rgb=%v", f.targets, f.colors)
	}
}

func TestStopRetriesOnlyFailingIdempotentStage(t *testing.T) {
	f := &retryLamp{failures: map[string][]error{
		"D": {errors.New("device is busy")},
		"p": {errors.New("I/O Timeout")},
		"S": {errors.New("general error")},
	}}
	b := &backend{dev: f, pause: func(time.Duration) {}}
	if err := b.Stop("off"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.events, ","); got != "D,D,p,p,c,S,S,r1,r2" {
		t.Fatalf("stop retried outside the failing stage: %s", got)
	}
}

func TestRetryMetricsAreConcurrentDetachedAndPersistAcrossReopens(t *testing.T) {
	d := New("").(*driver)
	tracker := d.metrics.device("a")
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for i := 0; i < 100; i++ {
			_ = d.Metrics()
			_ = SnapshotMetrics(d)
		}
	}()
	for i := 0; i < 100; i++ {
		attempts := 0
		if err := retryMK2(d.metrics.device("a"), func(time.Duration) {}, "query_rgb", "query r led=1", func() error {
			attempts++
			if attempts == 1 {
				return errors.New("general error")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	done.Wait()
	if d.metrics.device("a") != tracker {
		t.Fatal("reopen reset retry counters")
	}
	metrics := d.Metrics()
	if len(metrics) != 1 || metrics[0].Calls != 100 || metrics[0].Attempts != 200 || metrics[0].Retries != 100 || metrics[0].Failures != 0 {
		t.Fatalf("cumulative metrics = %+v", metrics)
	}
	metrics[0].Calls = 0
	if d.Metrics()[0].Calls != 100 {
		t.Fatal("metrics snapshot aliases mutable counters")
	}
}

func TestRetryStopsWhenLaterAttemptReportsClosedDevice(t *testing.T) {
	closed := errors.New("b1: device closed")
	f := &retryLamp{failures: map[string][]error{"c": {errors.New("general error"), closed, nil}}}
	b := &backend{dev: f, pause: func(time.Duration) {}}
	err := b.Set(control.Step{Color: "#ff0000"})
	var failure *OperationError
	if !errors.As(err, &failure) || failure.Attempts != 2 || !errors.Is(err, closed) || len(f.targets) != 2 {
		t.Fatalf("closed handle was retried or hidden: %v, calls=%d", err, len(f.targets))
	}
}

func TestAutonomousPlaybackMismatchIsNotRetried(t *testing.T) {
	f := &retryLamp{}
	f.after = func(kind string, err error) {
		if kind == "S" {
			f.playing = true // Contradicts the successfully issued p(false).
		}
	}
	b := &backend{dev: f, pause: func(time.Duration) { t.Fatal("logical mismatch was retried") }}
	err := b.Stop("off")
	if err == nil || !strings.Contains(err.Error(), "still reports autonomous playback") {
		t.Fatalf("logical stop failure lost: %v", err)
	}
	if got := strings.Join(f.events, ","); got != "D,p,c,S" {
		t.Fatalf("logical mismatch repeated hardware calls: %s", got)
	}
}
