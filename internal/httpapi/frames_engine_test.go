package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

type frameRouteBackend struct {
	mu         sync.Mutex
	operations []string
}
type frameRouteDriver struct{ backend *frameRouteBackend }

func (d frameRouteDriver) Open(string) (control.Backend, error) { return d.backend, nil }
func (b *frameRouteBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20005601", Capabilities: []string{"set", "raw-rgb", "mk2-basic"}}
}
func (b *frameRouteBackend) add(operation string) {
	b.mu.Lock()
	b.operations = append(b.operations, operation)
	b.mu.Unlock()
}
func (b *frameRouteBackend) Set(s control.Step) error { b.add("set"); return nil }
func (b *frameRouteBackend) Read() (control.Observation, error) {
	b.add("read")
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *frameRouteBackend) Stop(mode string) error { b.add("stop:" + mode); return nil }
func (b *frameRouteBackend) Close()                 { b.add("close") }

func TestFrameHTTPRealEngineSharedReceiptAndNoMidFrameReads(t *testing.T) {
	b := &frameRouteBackend{}
	engine, err := control.New(frameRouteDriver{b}, control.Options{Serials: []string{"20005601"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := engine.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, _ := engine.Device("20005601")
		if state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake engine did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	instance := engine.Instance()
	ticket := instance.Idempotency.RequestIDPrefix + strings.Repeat("a", 32)
	action := map[string]interface{}{"kind": "orbit", "colors": []string{"red", "blue"}, "period_ms": 400, "cycles": 1, "segments": 4}
	request := map[string]interface{}{"contract": lightactionv2.Contract, "action": action, "policy": "reject", "request_id": ticket, "expected_daemon_instance_id": instance.DaemonInstanceID}
	w := frameHTTPDo(engine, "POST", "/v2/devices/20005601/light", frameHTTPJSON(t, request))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var first control.FrameJob
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	// The low-level surface resolves the very same accepted logical operation.
	low := map[string]interface{}{"program": first.Program, "policy": "reject", "request_id": ticket, "expected_daemon_instance_id": instance.DaemonInstanceID}
	w = frameHTTPDo(engine, "POST", "/v1/devices/20005601/frame-jobs", frameHTTPJSON(t, low))
	if w.Code != 202 || w.Header().Get("X-NB1-Replayed") != "true" {
		t.Fatal(w.Code, w.Body.String())
	}
	var replay control.FrameJob
	_ = json.Unmarshal(w.Body.Bytes(), &replay)
	if replay.ID != first.ID {
		t.Fatal("duplicate logical operation admitted")
	}
	old := map[string]interface{}{"program": control.Program{Steps: []control.Step{{Color: "#000000"}}, OnFinish: "off"}, "policy": "reject", "request_id": ticket, "expected_daemon_instance_id": instance.DaemonInstanceID}
	w = frameHTTPDo(engine, "POST", "/v1/devices/20005601/jobs", frameHTTPJSON(t, old))
	if w.Code != 409 {
		t.Fatal("cross-kind ticket reuse did not conflict", w.Code, w.Body.String())
	}
	for {
		w = frameHTTPDo(engine, "GET", "/v1/frame-jobs/"+first.ID, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &replay)
		if replay.FinishedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("frame job did not terminate")
		}
		time.Sleep(3 * time.Millisecond)
	}
	if replay.Status != "succeeded" || replay.CompletedFrames != 4 || replay.CompletedTargetWrites != 10 {
		t.Fatalf("unexpected terminal frame job: %+v", replay)
	}
	b.mu.Lock()
	ops := append([]string(nil), b.operations...)
	b.mu.Unlock()
	firstSet, lastSet, sets := -1, -1, 0
	for i, op := range ops {
		if op == "set" {
			if firstSet < 0 {
				firstSet = i
			}
			lastSet = i
			sets++
		}
	}
	if sets != 10 {
		t.Fatalf("replay repeated output: %v", ops)
	}
	for _, op := range ops[firstSet : lastSet+1] {
		if op == "read" {
			t.Fatalf("periodic read interrupted the frame sequence: %v", ops)
		}
	}
	if lastSet+2 >= len(ops) || ops[lastSet+1] != "stop:off" || ops[lastSet+2] != "read" {
		t.Fatalf("missing off/read before terminal status: %v", ops)
	}
}
