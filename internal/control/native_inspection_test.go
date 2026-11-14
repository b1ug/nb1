package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
	"github.com/b1ug/nb1/internal/patternidentity"
)

// Every handle here is an in-memory fake. These tests never import the SDK,
// enumerate a device, contact HTTP or start a daemon process.
type nativeEngineBackend struct {
	*engineBackend
	inspect func(context.Context, nativeinspection.Request) (nativeinspection.Result, error)
}

func newNativeEngineBackend(serial string) *nativeEngineBackend {
	return &nativeEngineBackend{engineBackend: newEngineBackend(serial)}
}

func (b *nativeEngineBackend) Info() DeviceInfo {
	return DeviceInfo{Serial: b.serial, Product: "fake native mk2", Firmware: 204,
		Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold", "cancel", "observe"}}
}

func nativeEngineResult(request nativeinspection.Request) nativeinspection.Result {
	return nativeinspection.Result{Contract: nativeinspection.Contract, Kind: request.Kind,
		Source: "live-native", Consistency: "sequential-best-effort-not-atomic", Status: "complete", Complete: true}
}

func (b *nativeEngineBackend) InspectNative(ctx context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
	b.begin(engineCall{kind: "inspect"})
	defer b.end()
	if b.inspect != nil {
		return b.inspect(ctx, request)
	}
	return nativeEngineResult(request), nil
}

func (b *nativeEngineBackend) inspectionCalls() int {
	calls, _, _, _, _, _ := b.snapshot()
	n := 0
	for _, call := range calls {
		if call.kind == "inspect" {
			n++
		}
	}
	return n
}

type nativeEngineDriver struct {
	mu      sync.Mutex
	handles map[string][]Backend
	opens   map[string]int
}

func (d *nativeEngineDriver) Open(serial string) (Backend, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := d.opens[serial]
	d.opens[serial]++
	if i >= len(d.handles[serial]) {
		return nil, ErrOffline
	}
	return d.handles[serial][i], nil
}

func (d *nativeEngineDriver) openCount(serial string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opens[serial]
}

func startNativeEngine(t *testing.T, options Options, handles map[string][]Backend) (*Engine, *nativeEngineDriver) {
	t.Helper()
	d := &nativeEngineDriver{handles: handles, opens: map[string]int{}}
	for serial := range handles {
		options.Serials = append(options.Serials, serial)
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Hour
	}
	if options.ShutdownMode == "" {
		options.ShutdownMode = "hold"
	}
	e, err := New(d, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for serial := range handles {
		serial := serial
		awaitEngineCondition(t, func() bool { state, _ := e.Device(serial); return state.Connection == "online" })
	}
	return e, d
}

func nativeRAMRequest() nativeinspection.Request {
	return nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 1}}
}

func nativeAsync(e *Engine, ctx context.Context, serial string, request nativeinspection.Request) <-chan nativeInspectionOutcome {
	out := make(chan nativeInspectionOutcome, 1)
	go func() {
		result, err := e.InspectNative(ctx, serial, request)
		out <- nativeInspectionOutcome{result, err}
	}()
	return out
}

func nativeOutcome(t *testing.T, out <-chan nativeInspectionOutcome) nativeInspectionOutcome {
	t.Helper()
	select {
	case value := <-out:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("inspection did not return")
		return nativeInspectionOutcome{}
	}
}

func nativeReserved(e *Engine, serial string, started bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	call := e.workers[serial].inspection
	return call != nil && call.started == started
}

func TestNativeInspectionValidatesBeforeDispatchAndRequiresExistingOptionalBackend(t *testing.T) {
	b := newNativeEngineBackend("a")
	e, driver := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {b}})
	for _, request := range []nativeinspection.Request{{}, {Kind: nativeinspection.RAM}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 32, LineCount: 1}}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: -1, LineCount: 1}}, {Kind: nativeinspection.Playback, Range: &devicesequence.Range{StartLine: 0, LineCount: 1}}} {
		result, err := e.InspectNative(context.Background(), "a", request)
		if !errors.Is(err, nativeinspection.ErrInvalid) || result.Contract != "" {
			t.Fatalf("invalid input: %+v %v", result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := e.InspectNative(ctx, "a", nativeRAMRequest()); !errors.Is(err, context.Canceled) || result.Contract != "" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := e.InspectNative(context.Background(), "missing", nativeRAMRequest()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if b.inspectionCalls() != 0 || driver.openCount("a") != 1 {
		t.Fatal("rejection performed I/O or opened another handle")
	}
	plain := newEngineBackend("b")
	old, _ := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"b": {plain}})
	if result, err := old.InspectNative(context.Background(), "b", nativeRAMRequest()); !errors.Is(err, nativeinspection.ErrUnsupported) || result.Contract != "" {
		t.Fatalf("old optional backend: %+v %v", result, err)
	}
}

func TestNativeInspectionBindsWorkerIdentitySessionAndDoesNotMutate(t *testing.T) {
	b := newNativeEngineBackend("a")
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		request.Range.StartLine = 31
		result := nativeEngineResult(request)
		result.Device = nativeinspection.Identity{Serial: "spoof", Product: "spoof", Firmware: 999}
		result.DaemonInstanceID, result.DeviceSessionID = "spoof", "spoof"
		return result, nil
	}
	e, d := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {b}})
	request := nativeRAMRequest()
	first, err := e.InspectNative(context.Background(), "a", request)
	if err != nil || first.DaemonInstanceID != e.Instance().DaemonInstanceID || !strings.HasPrefix(first.DeviceSessionID, "session_") || len(first.DeviceSessionID) != 24 ||
		first.Device.Serial != "a" || first.Device.Product != "fake native mk2" || first.Device.Firmware != 204 || first.Device.FirmwareSource != "cached-on-open" ||
		first.StartedAt.IsZero() || first.FinishedAt.Before(first.StartedAt) || request.Range.StartLine != 0 {
		t.Fatalf("binding/request ownership: %+v %+v %v", first, request, err)
	}
	second, err := e.InspectNative(context.Background(), "a", request)
	if err != nil || second.DeviceSessionID != first.DeviceSessionID || d.openCount("a") != 1 {
		t.Fatal("same handle changed session or reopened", second, err)
	}
	calls, _, _, max, closed, unsafe := b.snapshot()
	for _, call := range calls {
		if call.kind != "read" && call.kind != "inspect" {
			t.Fatal("inspection changed state", calls)
		}
	}
	if max != 1 || closed != 0 || unsafe {
		t.Fatal("native backend escaped worker serialization")
	}
}

func TestNativeInspectionBlocksEveryNewAdmissionWithoutPublishing(t *testing.T) {
	b := newNativeEngineBackend("a")
	gate := newEngineGate()
	defer gate.open()
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		_ = gate.block()
		return nativeEngineResult(request), nil
	}
	e, _ := startNativeEngine(t, Options{}, map[string][]Backend{"a": {b}})
	out := nativeAsync(e, context.Background(), "a", nativeRAMRequest())
	awaitEngineSignal(t, gate.entered)
	for n, policy := range []string{"replace", "queue", "reject"} {
		t.Run("program-"+policy, func(t *testing.T) {
			if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), policy); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
		})
		t.Run("receipt-"+policy, func(t *testing.T) {
			if _, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#ffffff", "hold"), policy, receiptTicket(e, n+1)); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
		})
		for k, kind := range []string{"program", "frame"} {
			t.Run("pattern-"+kind+"-"+policy, func(t *testing.T) {
				ref := patternRef(20 + n*2 + k)
				resolved := patternResolved(t, kind, ref, "#ffffff")
				called := false
				_, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, policy, receiptTicket(e, 20+n*2+k), func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					called = true
					return resolved, nil
				})
				if !errors.Is(err, ErrBusy) || called {
					t.Fatal("busy entered resolver or admitted", err, called)
				}
			})
		}
	}
	if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", receiptTicket(e, 100)); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := e.InspectNative(context.Background(), "a", nativeRAMRequest()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	assertPatternEmpty(t, e)
	gate.open()
	if got := nativeOutcome(t, out); got.err != nil {
		t.Fatal(got.err)
	}
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "reject"); err != nil {
		t.Fatal("gate leaked", err)
	}
}

func TestNativeInspectionBusyDoesNotCancelActiveOrQueuedJobs(t *testing.T) {
	b := newNativeEngineBackend("a")
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	e, _ := startNativeEngine(t, Options{}, map[string][]Backend{"a": {b}})
	active := submitEngineJob(t, e, "a", engineProgram("#ffffff", "hold"), "reject")
	awaitEngineSignal(t, gate.entered)
	queued := submitEngineJob(t, e, "a", engineProgram("#000000", "hold"), "queue")
	result, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
	state, _ := e.Device("a")
	a, _ := e.Job(active.ID)
	q, _ := e.Job(queued.ID)
	if !errors.Is(err, ErrBusy) || result.Contract != "" || a.Status != "running" || q.Status != "queued" || state.QueueDepth != 1 || b.inspectionCalls() != 0 {
		t.Fatalf("%+v %+v %+v %v", state, a, q, err)
	}
	gate.open()
	awaitEngineJob(t, e, active.ID, "succeeded")
	awaitEngineJob(t, e, queued.ID, "succeeded")
}

func TestNativeInspectionPatternResolverFinalGateAndReceiptReplay(t *testing.T) {
	b := newNativeEngineBackend("a")
	e, _ := startNativeEngine(t, Options{}, map[string][]Backend{"a": {b}})
	ticket := receiptTicket(e, 1)
	prior, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#ffffff", "hold"), "reject", ticket)
	if err != nil {
		t.Fatal(err)
	}
	awaitEngineJob(t, e, prior.Job.ID, "succeeded")
	resolveGate, inspectGate := newEngineGate(), newEngineGate()
	defer resolveGate.open()
	defer inspectGate.open()
	ref := patternRef(2)
	resolved := patternResolved(t, "program", ref, "#ffffff")
	patternResult := make(chan error, 1)
	newTicket := receiptTicket(e, 2)
	go func() {
		_, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "replace", newTicket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
			_ = resolveGate.block()
			return resolved, nil
		})
		patternResult <- err
	}()
	awaitEngineSignal(t, resolveGate.entered)
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		_ = inspectGate.block()
		return nativeEngineResult(request), nil
	}
	out := nativeAsync(e, context.Background(), "a", nativeRAMRequest())
	awaitEngineSignal(t, inspectGate.entered)
	replay, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#ffffff", "hold"), "reject", ticket)
	if err != nil || !replay.Replayed || replay.Job.ID != prior.Job.ID {
		t.Fatal("receipt readback blocked", replay, err)
	}
	resolveGate.open()
	if err := <-patternResult; !errors.Is(err, ErrBusy) {
		t.Fatal("resolver bypassed final gate", err)
	}
	assertReceiptUsage(t, e, 1, 1)
	inspectGate.open()
	if got := nativeOutcome(t, out); got.err != nil {
		t.Fatal(got.err)
	}
}

func TestNativeInspectionPendingCancellationAndCloseDoNotDispatch(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%t", closing), func(t *testing.T) {
			b := newNativeEngineBackend("a")
			e, d := startNativeEngine(t, Options{ReadOnly: true, PollInterval: 10 * time.Millisecond}, map[string][]Backend{"a": {b}})
			readGate := newEngineGate()
			defer readGate.open()
			b.hooks(nil, nil, readGate.block)
			awaitEngineSignal(t, readGate.entered)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := nativeAsync(e, ctx, "a", nativeRAMRequest())
			awaitEngineCondition(t, func() bool { return nativeReserved(e, "a", false) })
			want := context.Canceled
			if closing {
				e.beginClose()
				want = ErrClosed
			} else {
				cancel()
			}
			got := nativeOutcome(t, out)
			if !errors.Is(got.err, want) || got.result.Contract != "" || b.inspectionCalls() != 0 || d.openCount("a") != 1 {
				t.Fatalf("%+v %v", got.result, got.err)
			}
			readGate.open()
			if !closing {
				if _, err := e.InspectNative(context.Background(), "a", nativeRAMRequest()); err != nil {
					t.Fatal("pending cancellation leaked admission", err)
				}
			}
		})
	}
}

func TestNativeInspectionInflightCancellationKeepsGateAndHandleUntilReturn(t *testing.T) {
	b := newNativeEngineBackend("a")
	gate := newEngineGate()
	defer gate.open()
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		_ = gate.block()
		return nativeEngineResult(request), nil
	}
	e, d := startNativeEngine(t, Options{}, map[string][]Backend{"a": {b}})
	ctx, cancel := context.WithCancel(context.Background())
	out := nativeAsync(e, ctx, "a", nativeRAMRequest())
	awaitEngineSignal(t, gate.entered)
	cancel()
	got := nativeOutcome(t, out)
	if !errors.Is(got.err, context.Canceled) || got.result.Contract != "" || !nativeReserved(e, "a", true) {
		t.Fatal("cancellation ended native ownership", got.err)
	}
	if _, err := e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "replace"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	_, _, _, _, closed, unsafe := b.snapshot()
	if closed != 0 || unsafe || d.openCount("a") != 1 {
		t.Fatal("cancellation closed or reopened native handle")
	}
	gate.open()
	awaitEngineCondition(t, func() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.workers["a"].inspection == nil })
	if state, _ := e.Device("a"); state.Connection != "online" {
		t.Fatal(state)
	}
	if _, err := e.InspectNative(context.Background(), "a", nativeRAMRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInspectionInflightCloseWaitsBeforeNormalShutdown(t *testing.T) {
	b := newNativeEngineBackend("a")
	gate := newEngineGate()
	defer gate.open()
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		_ = gate.block()
		return nativeEngineResult(request), nil
	}
	e, _ := startNativeEngine(t, Options{ShutdownMode: "off"}, map[string][]Backend{"a": {b}})
	out := nativeAsync(e, context.Background(), "a", nativeRAMRequest())
	awaitEngineSignal(t, gate.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	calls, _, _, _, closed, unsafe := b.snapshot()
	for _, call := range calls {
		if call.kind == "close" || call.kind == "stop" {
			t.Fatal("shutdown ran while native call blocked", calls)
		}
	}
	if closed != 0 || unsafe {
		t.Fatal("premature native close")
	}
	gate.open()
	if got := nativeOutcome(t, out); got.err != nil || got.result.Contract == "" {
		t.Fatal(got.err)
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, _, _, max, closed, unsafe := b.snapshot()
	stops := 0
	for _, call := range calls {
		if call.kind == "stop" {
			stops++
			if call.mode != "off" {
				t.Fatal(call)
			}
		}
	}
	if stops != 1 || closed != 1 || max != 1 || unsafe {
		t.Fatal("normal shutdown ownership", calls, max, closed, unsafe)
	}
}

func TestNativeInspectionPendingDoesNotCrossPollFailureAndNewSession(t *testing.T) {
	first, second := newNativeEngineBackend("a"), newNativeEngineBackend("a")
	e, d := startNativeEngine(t, Options{ReadOnly: true, PollInterval: 10 * time.Millisecond}, map[string][]Backend{"a": {first, second}})
	e.mu.Lock()
	oldSession := e.workers["a"].deviceSessionID
	e.mu.Unlock()
	gate := newEngineGate()
	defer gate.open()
	first.hooks(nil, nil, func() error { _ = gate.block(); return errors.New("fake polling failure") })
	awaitEngineSignal(t, gate.entered)
	out := nativeAsync(e, context.Background(), "a", nativeRAMRequest())
	awaitEngineCondition(t, func() bool { return nativeReserved(e, "a", false) })
	gate.open()
	if got := nativeOutcome(t, out); !errors.Is(got.err, ErrOffline) || got.result.Contract != "" {
		t.Fatal("pending request survived old handle", got.err)
	}
	awaitEngineCondition(t, func() bool { state, _ := e.Device("a"); return state.Connection == "online" && d.openCount("a") == 2 })
	if first.inspectionCalls() != 0 || second.inspectionCalls() != 0 {
		t.Fatal("pending inspection was replayed on another session")
	}
	got, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
	if err != nil || got.DeviceSessionID == oldSession || got.DeviceSessionID == "" {
		t.Fatal("new connection did not change session", got, err)
	}
}

type nativeEngineCause struct{ cause error }

func (e *nativeEngineCause) Error() string        { return "fake typed transport error" }
func (e *nativeEngineCause) Unwrap() error        { return e.cause }
func (*nativeEngineCause) NativePlatform() string { return "darwin" }
func (*nativeEngineCause) NativeCode() uint64     { return 0xe00002bc }

func TestNativeInspectionPartialAndTypedErrorStayOwnedWithoutReconnect(t *testing.T) {
	b := newNativeEngineBackend("a")
	rgb, ticks, led, capacity, message, code := [3]byte{1, 2, 3}, uint16(0), byte(0xff), 32, "native", "3758097084"
	stored := nativeEngineResult(nativeRAMRequest())
	stored.Complete, stored.Status = false, "partial"
	stored.Profile.RAMLines = &capacity
	stored.Profile.ReadReference.Sources = []string{"source"}
	stored.Error = &nativeinspection.ErrorInfo{Code: "native_read", NativePlatform: &message, NativeCode: &code}
	stored.RAM = &nativeinspection.RAMResult{Rows: []nativeinspection.Row{{Position: 0, RGB8: &rgb, DurationTicks: &ticks, LEDCode: &led}}, UnreadPositions: []int{1}, Issues: []devicesequence.Issue{{Code: "partial"}}}
	sentinel := errors.New("transport cause")
	native := &nativeEngineCause{sentinel}
	b.inspect = func(context.Context, nativeinspection.Request) (nativeinspection.Result, error) {
		return stored, fmt.Errorf("wrapped: %w", native)
	}
	e, d := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {b}})
	got, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
	var typed *nativeEngineCause
	if !errors.Is(err, sentinel) || !errors.As(err, &typed) || typed != native || got.Complete || got.Status != "partial" || got.Contract == "" || b.inspectionCalls() != 1 {
		t.Fatalf("lost partial or typed cause: %+v %v", got, err)
	}
	got.RAM.Rows[0].RGB8[0] = 9
	*got.RAM.Rows[0].DurationTicks = 9
	*got.RAM.Rows[0].LEDCode = 9
	got.RAM.UnreadPositions[0] = 9
	got.RAM.Issues[0].Code = "changed"
	*got.Profile.RAMLines = 1
	got.Profile.ReadReference.Sources[0] = "changed"
	*got.Error.NativeCode = "0"
	if rgb[0] != 1 || ticks != 0 || led != 0xff || capacity != 32 || stored.RAM.UnreadPositions[0] != 1 || stored.RAM.Issues[0].Code != "partial" || stored.Profile.ReadReference.Sources[0] != "source" || code != "3758097084" {
		t.Fatal("result shares adapter data")
	}
	state, _ := e.Device("a")
	if state.Connection != "online" || d.openCount("a") != 1 {
		t.Fatal("inspection failure manufactured offline/reopen", state)
	}
}

func TestNativeInspectionAcrossDevicesDoesNotHoldEngineMutex(t *testing.T) {
	a, b := newNativeEngineBackend("a"), newNativeEngineBackend("b")
	gate := newEngineGate()
	defer gate.open()
	a.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		_ = gate.block()
		return nativeEngineResult(request), nil
	}
	e, _ := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {a}, "b": {b}})
	aOut := nativeAsync(e, context.Background(), "a", nativeRAMRequest())
	awaitEngineSignal(t, gate.entered)
	bResult := nativeOutcome(t, nativeAsync(e, context.Background(), "b", nativeinspection.Request{Kind: nativeinspection.Playback}))
	if bResult.err != nil || bResult.result.Device.Serial != "b" {
		t.Fatal(bResult.err)
	}
	gate.open()
	aResult := nativeOutcome(t, aOut)
	if aResult.err != nil || aResult.result.DeviceSessionID == bResult.result.DeviceSessionID || aResult.result.DaemonInstanceID != bResult.result.DaemonInstanceID {
		t.Fatal("cross-device sessions", aResult, bResult)
	}
}

func TestNativeInspectionConcurrentAdmissionHasExactlyOneWinner(t *testing.T) {
	for _, kind := range []string{"program", "receipt", "frame", "pattern-program", "pattern-frame"} {
		t.Run(kind, func(t *testing.T) {
			b := newNativeEngineBackend("a")
			inspectGate, jobGate := newEngineGate(), newEngineGate()
			defer inspectGate.open()
			defer jobGate.open()
			b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
				_ = inspectGate.block()
				return nativeEngineResult(request), nil
			}
			b.hooks(nil, func(string) error { return jobGate.block() }, nil)
			e, _ := startNativeEngine(t, Options{}, map[string][]Backend{"a": {b}})
			ticket := receiptTicket(e, 1)
			ref := patternRef(1)
			resolved := patternResolved(t, "program", ref, "#ffffff")
			if kind == "pattern-frame" {
				resolved = patternResolved(t, "frame", ref, "#ffffff")
			}
			start, admitted := make(chan struct{}), make(chan error, 1)
			inspected := make(chan nativeInspectionOutcome, 1)
			go func() {
				<-start
				result, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
				inspected <- nativeInspectionOutcome{result, err}
			}()
			go func() {
				<-start
				var err error
				switch kind {
				case "program":
					_, err = e.Submit(context.Background(), "a", engineProgram("#ffffff", "hold"), "queue")
				case "receipt":
					_, err = e.SubmitWithReceipt(context.Background(), "a", engineProgram("#ffffff", "hold"), "replace", ticket)
				case "frame":
					_, err = e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", ticket)
				default:
					_, err = e.SubmitPatternWithReceipt(context.Background(), "a", ref, "queue", ticket, patternResolver(resolved))
				}
				admitted <- err
			}()
			close(start)
			select {
			case <-inspectGate.entered:
			case <-jobGate.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("neither worker operation entered")
			}
			var admissionErr error
			select {
			case admissionErr = <-admitted:
			case <-time.After(2 * time.Second):
				t.Fatal("admission blocked on native operation")
			}
			inspectGate.open()
			jobGate.open()
			inspection := nativeOutcome(t, inspected)
			if (admissionErr == nil) == (inspection.err == nil) {
				t.Fatalf("exactly one operation must be accepted: admission=%v inspection=%v", admissionErr, inspection.err)
			}
			if admissionErr != nil && !errors.Is(admissionErr, ErrBusy) || inspection.err != nil && !errors.Is(inspection.err, ErrBusy) {
				t.Fatalf("wrong race rejection: %v / %v", admissionErr, inspection.err)
			}
		})
	}
}

func TestNativeInspectionNoCollectionErrorsRemainEmptyAndDoNotReconnect(t *testing.T) {
	for _, cause := range []error{nativeinspection.ErrUnsupported, context.Canceled, errors.New("provider unavailable"), nil} {
		t.Run(fmt.Sprint(cause), func(t *testing.T) {
			b := newNativeEngineBackend("a")
			b.inspect = func(context.Context, nativeinspection.Request) (nativeinspection.Result, error) {
				return nativeinspection.Result{}, cause
			}
			e, driver := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {b}})
			result, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
			if result.Contract != "" || result.DaemonInstanceID != "" || result.DeviceSessionID != "" || err == nil || cause != nil && !errors.Is(err, cause) {
				t.Fatalf("no-collection rejection became data or lost cause: %+v %v", result, err)
			}
			state, _ := e.Device("a")
			_, _, _, _, closed, _ := b.snapshot()
			if state.Connection != "online" || closed != 0 || driver.openCount("a") != 1 || b.inspectionCalls() != 1 {
				t.Fatal("non-native cancellation/rejection changed device lifecycle", state)
			}
		})
	}
}

func TestNativeInspectionPendingRequestOwnsRangeBeforeCallerChangesIt(t *testing.T) {
	b := newNativeEngineBackend("a")
	seen := make(chan devicesequence.Range, 1)
	b.inspect = func(_ context.Context, request nativeinspection.Request) (nativeinspection.Result, error) {
		seen <- *request.Range
		return nativeEngineResult(request), nil
	}
	e, _ := startNativeEngine(t, Options{ReadOnly: true, PollInterval: 10 * time.Millisecond}, map[string][]Backend{"a": {b}})
	gate := newEngineGate()
	defer gate.open()
	b.hooks(nil, nil, gate.block)
	awaitEngineSignal(t, gate.entered)
	request := nativeRAMRequest()
	out := nativeAsync(e, context.Background(), "a", request)
	awaitEngineCondition(t, func() bool { return nativeReserved(e, "a", false) })
	request.Range.StartLine = 31
	gate.open()
	if got := nativeOutcome(t, out); got.err != nil {
		t.Fatal(got.err)
	}
	if actual := <-seen; actual.StartLine != 0 || actual.LineCount != 1 {
		t.Fatal("pending request aliases caller range", actual)
	}
}

func TestNativeInspectionEmptyContractDiscardsResidualData(t *testing.T) {
	for _, cause := range []error{nativeinspection.ErrUnsupported, nil} {
		t.Run(fmt.Sprint(cause), func(t *testing.T) {
			b := newNativeEngineBackend("a")
			rgb := [3]byte{1, 2, 3}
			residual := nativeinspection.Result{Kind: nativeinspection.RAM, Complete: true,
				RAM: &nativeinspection.RAMResult{Rows: []nativeinspection.Row{{RGB8: &rgb}}}}
			b.inspect = func(context.Context, nativeinspection.Request) (nativeinspection.Result, error) {
				return residual, cause
			}
			e, _ := startNativeEngine(t, Options{ReadOnly: true}, map[string][]Backend{"a": {b}})
			result, err := e.InspectNative(context.Background(), "a", nativeRAMRequest())
			if err == nil || cause != nil && !errors.Is(err, cause) || result.Contract != "" || result.Kind != "" || result.Complete || result.RAM != nil || result.Playback != nil || result.Error != nil || result.DaemonInstanceID != "" || result.DeviceSessionID != "" {
				t.Fatalf("uncollected residual escaped: %+v %v", result, err)
			}
		})
	}
}
