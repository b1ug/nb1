package nativeinspection_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type wireDriver struct {
	backend *wireBackend
	opens   int32
}

func (d *wireDriver) Open(string) (control.Backend, error) {
	atomic.AddInt32(&d.opens, 1)
	return d.backend, nil
}

type wireBackend struct {
	set, stop, close int32
	reader           wireReader
}

func (b *wireBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20000001", Product: "Synthetic MK2", Firmware: 204, Capabilities: []string{"observe"}}
}
func (b *wireBackend) Set(control.Step) error { atomic.AddInt32(&b.set, 1); return nil }
func (b *wireBackend) Stop(string) error      { atomic.AddInt32(&b.stop, 1); return nil }
func (b *wireBackend) Close()                 { atomic.AddInt32(&b.close, 1) }
func (b *wireBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *wireBackend) InspectNative(ctx context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
	return nativeinspection.Collect(ctx, &b.reader, request, nativeinspection.Identity{Serial: "20000001", Product: "Synthetic MK2", Firmware: 204, FirmwareSource: "cached-on-open"}, wireProfile())
}
func wireProfile() nativeinspection.Profile {
	ram, ref := 32, 205
	return nativeinspection.Profile{SDKModel: "mk2", SDKReferenceFirmware: &ref, SDKApplicability: "model-only", RAMLines: &ram, OperationQualification: "unverified", ReadReference: nativeinspection.ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture://not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"}, PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &ram, Sources: []string{"fixture://not-hardware"}}, EndEncoding: "exclusive"}}
}

type wireError struct{}

func (wireError) Error() string          { return "synthetic partial response" }
func (wireError) NativePlatform() string { return "fixture" }
func (wireError) NativeCode() uint64     { return ^uint64(0) }

type wireReader struct {
	entered, release     chan struct{}
	once                 sync.Once
	lineCalls, playCalls int32
}

func (r *wireReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	atomic.AddInt32(&r.lineCalls, 1)
	if r.entered != nil {
		r.once.Do(func() { close(r.entered); <-r.release })
	}
	data := []byte{1, 'R', 255, 0, 0, 0, 50, 0, 0xee}
	if position%2 == 1 {
		data = []byte{1, 'R', 0, 0, 255, 0, 50, 2, 0xdd}
	}
	return nativeinspection.LineRead{Position: position, Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: data}}, nil
}
func (r *wireReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	atomic.AddInt32(&r.playCalls, 1)
	return nativeinspection.PlaybackRead{Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: []byte{1, 'S', 1, 0, 2, 0, 1, 0xaa, 0xbb}}}, wireError{}
}

func wireEngine(t *testing.T, b *wireBackend) (*control.Engine, *wireDriver) {
	t.Helper()
	d := &wireDriver{backend: b}
	e, err := control.New(d, control.Options{Serials: []string{"20000001"}, PollInterval: time.Hour, ShutdownMode: "hold"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := e.Device("20000001")
		if err == nil && state.Connection == "online" {
			return e, d
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("synthetic worker did not connect")
	return nil, nil
}

func TestNativeReadTraversesClientHTTPWorkerAndCollectorWithoutControlIO(t *testing.T) {
	b := &wireBackend{}
	e, d := wireEngine(t, b)
	server := httptest.NewServer(httpapi.New(e))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 2}}
	out, err := client.InspectNative(context.Background(), "20000001", req)
	if err != nil || !out.Complete || out.RAM.Sequence == nil || out.RAM.Sequence.Lines[0].LED != 0 || out.RAM.Rows[1].Report.UnknownTailHex != "dd" || out.DaemonInstanceID != e.Instance().DaemonInstanceID || out.DeviceSessionID == "" {
		t.Fatalf("%+v %v", out, err)
	}
	firstSession := out.DeviceSessionID
	partial, err := client.InspectNative(context.Background(), "20000001", nativeinspection.Request{Kind: nativeinspection.Playback})
	var incomplete *daemonclient.InspectionError
	if !errors.As(err, &incomplete) || partial.Status != "partial" || partial.Complete || partial.Playback.Fields.Count == nil || *partial.Playback.Fields.Count != 0 || partial.Playback.ConfiguredRange != nil || partial.Error.NativeCode == nil || *partial.Error.NativeCode != "18446744073709551615" || partial.DeviceSessionID != firstSession {
		t.Fatalf("%+v %v", partial, err)
	}
	*incomplete.Result.Playback.Fields.Mode = 99
	if *partial.Playback.Fields.Mode != 1 {
		t.Fatal("error result aliases caller result")
	}
	state, err := e.Device("20000001")
	if err != nil || state.ActiveJob != "" || state.QueueDepth != 0 || atomic.LoadInt32(&d.opens) != 1 || atomic.LoadInt32(&b.set) != 0 || atomic.LoadInt32(&b.stop) != 0 || atomic.LoadInt32(&b.close) != 0 || atomic.LoadInt32(&b.reader.lineCalls) != 2 || atomic.LoadInt32(&b.reader.playCalls) != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestHTTPReadCancellationKeepsWorkerGateUntilNativeReturns(t *testing.T) {
	b := &wireBackend{reader: wireReader{entered: make(chan struct{}), release: make(chan struct{})}}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(b.reader.release) }) }
	defer release()
	e, d := wireEngine(t, b)
	server := httptest.NewServer(httpapi.New(e))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.InspectNative(ctx, "20000001", nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 1}})
		done <- err
	}()
	select {
	case <-b.reader.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled HTTP wait succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP caller stayed blocked")
	}
	program := control.Program{Steps: []control.Step{{LED: 1, Color: "#ff0000", FadeMS: 10, HoldMS: 10}}, Repeat: 1, OnFinish: "off"}
	if _, err := e.Submit(context.Background(), "20000001", program, "replace"); !errors.Is(err, control.ErrBusy) {
		t.Fatalf("lost native gate: %v", err)
	}
	if atomic.LoadInt32(&b.close) != 0 || atomic.LoadInt32(&b.stop) != 0 || atomic.LoadInt32(&b.set) != 0 {
		t.Fatal("client cancellation controlled the native handle")
	}
	release()
	deadline := time.Now().Add(3 * time.Second)
	var out nativeinspection.Result
	for time.Now().Before(deadline) {
		out, err = e.InspectNative(context.Background(), "20000001", nativeinspection.Request{Kind: nativeinspection.Playback})
		if !errors.Is(err, control.ErrBusy) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if out.Contract != nativeinspection.Contract || out.Status != "partial" || atomic.LoadInt32(&b.reader.lineCalls) != 1 || atomic.LoadInt32(&b.reader.playCalls) != 1 || atomic.LoadInt32(&d.opens) != 1 || atomic.LoadInt32(&b.close) != 0 {
		t.Fatalf("gate/handle did not survive: %+v %v", out, err)
	}
}
