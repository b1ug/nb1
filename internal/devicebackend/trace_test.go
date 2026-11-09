package devicebackend

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/transporttrace"
)

// A write can reach the device despite its host-side failure. The trace must
// preserve this ambiguity, and must not turn diagnostics into another retry.
type appliedErrorLamp struct{ retryLamp }

func (f *appliedErrorLamp) FadeToRGB(r, g, b byte, fade uint, led b1.LEDIndex) error {
	_ = f.testLamp.FadeToRGB(r, g, b, fade, led)
	f.targets = append(f.targets, retryTarget{r, g, b, fade, led})
	return f.attempt("c")
}

func TestTransportTracePreservesAppliedThenErrorRetryAndMetrics(t *testing.T) {
	var beforeTargets []retryTarget
	var beforeEvents []string
	var beforeMetrics []control.TransportMetric
	var beforeWaits []time.Duration
	for _, enabled := range []bool{false, true} {
		failure := codedTestError{"darwin", 0xe00002bc, errors.New("general error")}
		lamp := &appliedErrorLamp{retryLamp: retryLamp{failures: map[string][]error{"c": {failure}, "r1": {failure}, "p": {failure}}}}
		d := New("").(*driver)
		var waits []time.Duration
		base := &backend{dev: lamp, metrics: d.metrics.device("20005601"), info: control.DeviceInfo{Serial: "20005601"}, pause: func(v time.Duration) { waits = append(waits, v) }}
		var rec *transporttrace.Recorder
		if enabled {
			rec = transporttrace.New()
			base.trace = &backendTrace{recorder: rec, scope: transporttrace.EmptyScope(transporttrace.ProgramStep)}
		}
		if err := base.Set(control.Step{LED: 1, Color: "#000099", FadeMS: 750}); err != nil {
			t.Fatal(err)
		}
		if lamp.colors[0] != ([3]byte{0, 0, 153}) {
			t.Fatal("applied target missing")
		}
		if _, err := base.Read(); err != nil {
			t.Fatal(err)
		}
		if err := base.Stop("off"); err != nil {
			t.Fatal(err)
		}
		if !enabled {
			beforeTargets = append([]retryTarget(nil), lamp.targets...)
			beforeEvents = append([]string(nil), lamp.events...)
			beforeMetrics = d.Metrics()
			beforeWaits = waits
			continue
		}
		if !reflect.DeepEqual(beforeTargets, lamp.targets) || !reflect.DeepEqual(beforeEvents, lamp.events) || !reflect.DeepEqual(beforeMetrics, d.Metrics()) || !reflect.DeepEqual(beforeWaits, waits) {
			t.Fatal("trace changed SDK sequence/targets/backoff/metrics")
		}
		s := rec.Snapshot()
		if s.Stats.InFlight != 0 || s.Stats.InvalidScopes != 0 {
			t.Fatal(s.Stats)
		}
		var retried, failedWrites int
		for _, e := range s.Events {
			var wire map[string]interface{}
			b, _ := json.Marshal(e)
			_ = json.Unmarshal(b, &wire)
			if wire["edge"] == "end" && wire["will_retry"] == true {
				retried++
				if wire["native_code"] != float64(0xe00002bc) {
					t.Fatal(wire)
				}
			}
			if wire["operation"] == "set_color" && wire["edge"] == "end" && wire["success"] == false {
				failedWrites++
			}
		}
		if retried != 3 || failedWrites != 1 || len(lamp.targets) < 2 || lamp.targets[0] != lamp.targets[1] {
			t.Fatalf("retry evidence %d/%d targets=%v", retried, failedWrites, lamp.targets)
		}
		rec.Close()
	}
}

func TestTransportScopeOnlyEnabledWrapper(t *testing.T) {
	base := &backend{}
	if _, ok := interface{}(base).(transporttrace.ScopeBinder); ok {
		t.Fatal("disabled backend acquired scope binder")
	}
	r := transporttrace.New()
	base.trace = &backendTrace{recorder: r}
	b := &tracedBackend{base}
	scope := transporttrace.EmptyScope(transporttrace.Cleanup)
	scope.Mode = "off"
	b.SetTransportScope(scope)
	if base.trace.scope.Phase != transporttrace.Cleanup || base.trace.scope.Mode != "off" {
		t.Fatal("scope was not installed")
	}
	r.Close()
}

func TestTransportTraceExhaustionAndPermanentErrorsKeepOriginalCause(t *testing.T) {
	general := codedTestError{"darwin", 0xe00002bc, errors.New("general error")}
	unknown := codedTestError{"darwin", 0xdeadbeef, errors.New("general error")}
	for _, tc := range []struct {
		name              string
		failures          []error
		attempts, retries int
		cause             error
	}{
		{"exhausted", []error{general, general, general}, 3, 2, general},
		{"unknown_native", []error{unknown, nil}, 1, 0, unknown},
		{"permanent", []error{control.ErrOffline, nil}, 1, 0, control.ErrOffline},
		{"eligible_then_closed", []error{general, control.ErrClosed, nil}, 2, 1, control.ErrClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := transporttrace.New()
			defer r.Close()
			f := &retryLamp{failures: map[string][]error{"c": tc.failures}}
			d := New("").(*driver)
			b := &backend{dev: f, info: control.DeviceInfo{Serial: "20005601"}, metrics: d.metrics.device("20005601"), pause: func(time.Duration) {}, trace: &backendTrace{recorder: r, scope: transporttrace.EmptyScope(transporttrace.ProgramStep)}}
			err := b.Set(control.Step{LED: 1, Color: "#112233"})
			var failed *OperationError
			if !errors.As(err, &failed) || !errors.Is(err, tc.cause) || failed.Attempts != tc.attempts || len(f.targets) != tc.attempts {
				t.Fatalf("retry policy/cause changed: %v", err)
			}
			metrics := d.Metrics()
			if metrics[0].Retries != uint64(tc.retries) || metrics[0].Failures != 1 {
				t.Fatal(metrics)
			}
			s := r.Snapshot()
			if len(s.Events) != tc.attempts*2 || s.Stats.InFlight != 0 {
				t.Fatal(s.Stats)
			}
			var last map[string]interface{}
			encoded, _ := json.Marshal(s.Events[len(s.Events)-1])
			_ = json.Unmarshal(encoded, &last)
			if last["will_retry"] != false || last["backoff_ms"] != float64(0) || last["success"] != false {
				t.Fatal("terminal diagnostic implies retry/success", last)
			}
		})
	}
}

func TestTransportTraceQueryFailureStageAndMissingNativeCode(t *testing.T) {
	for _, stage := range []string{"write request", "read"} {
		t.Run(stage, func(t *testing.T) {
			r := transporttrace.New()
			defer r.Close()
			lamp := &retryLamp{failures: map[string][]error{"r1": {fmt.Errorf("b1: %s fail: %w", stage, errors.New("general error"))}}}
			base := &backend{dev: lamp, info: control.DeviceInfo{Serial: "20005601"}, pause: func(time.Duration) {}, trace: &backendTrace{recorder: r, scope: transporttrace.EmptyScope(transporttrace.IdleObserve)}}
			if _, err := base.Read(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(lamp.events, []string{"r1", "r1", "r2"}) {
				t.Fatal("complete SDK query was not retried once", lamp.events)
			}
			var end map[string]interface{}
			b, _ := json.Marshal(r.Snapshot().Events[1])
			_ = json.Unmarshal(b, &end)
			if !strings.Contains(end["sdk_error_hint"].(string), stage+" fail") || end["will_retry"] != true {
				t.Fatal("query stage lost", end)
			}
			if _, ok := end["native_code"]; ok {
				t.Fatal("plain general error invented native code", end)
			}
		})
	}
}
