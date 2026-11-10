package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
)

// The sink copies complete records under a lock: serveRuntime deliberately does
// not join its logger, so bytes.Buffer would be unsafe after serve returns.
type shutdownOutput struct {
	mu      sync.Mutex
	records [][]byte
	result  chan struct{}
	once    sync.Once
}

func newShutdownOutput() *shutdownOutput { return &shutdownOutput{result: make(chan struct{})} }
func (w *shutdownOutput) Write(data []byte) (int, error) {
	var envelope struct{ Event string }
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.records = append(w.records, append([]byte(nil), data...))
	w.mu.Unlock()
	if envelope.Event == "daemon_shutdown_result" {
		w.once.Do(func() { close(w.result) })
	}
	return len(data), nil
}

func (w *shutdownOutput) lifecycle(t *testing.T) []runtimeShutdownRecord {
	t.Helper()
	select {
	case <-w.result:
	case <-time.After(2 * time.Second):
		t.Fatal("unblocked logger did not deliver shutdown result")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var result []runtimeShutdownRecord
	for _, data := range w.records {
		var record runtimeShutdownRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(record.Event, "daemon_shutdown_") {
			if len(data) > 1024 {
				t.Fatalf("unbounded lifecycle record: %d bytes", len(data))
			}
			result = append(result, record)
		}
	}
	if len(result) != 2 || result[0].Event != "daemon_shutdown_trigger" || result[1].Event != "daemon_shutdown_result" {
		t.Fatalf("lifecycle pair missing/out of order: %+v", result)
	}
	for _, record := range result {
		if record.Contract != runtimeLifecycleContract || record.DaemonInstanceID == "" || record.Time.IsZero() {
			t.Fatalf("missing provenance: %+v", record)
		}
	}
	return result
}

type shutdownBackend struct {
	mu          sync.Mutex
	ready       chan struct{}
	readOnce    sync.Once
	stopEntered chan struct{}
	stopOnce    sync.Once
	stopGate    chan struct{}
	stopError   error
	stops       int
	closed      bool
}

func newShutdownBackend() *shutdownBackend { return &shutdownBackend{ready: make(chan struct{})} }
func (*shutdownBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "test-device", Capabilities: []string{"set"}}
}
func (*shutdownBackend) Set(control.Step) error { return nil }
func (b *shutdownBackend) Read() (control.Observation, error) {
	b.readOnce.Do(func() { close(b.ready) })
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *shutdownBackend) Stop(mode string) error {
	if b.stopEntered != nil {
		b.stopOnce.Do(func() { close(b.stopEntered) })
	}
	if b.stopGate != nil {
		<-b.stopGate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode != "off" {
		panic("unexpected cleanup mode")
	}
	b.stops++
	return b.stopError
}
func (b *shutdownBackend) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true }
func (b *shutdownBackend) assertClosed(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed || b.stops != 1 {
		t.Fatalf("cleanup changed: closed=%v stops=%d", b.closed, b.stops)
	}
}

type shutdownFixture struct {
	engine   *control.Engine
	listener net.Listener
	cancel   context.CancelFunc
	done     chan error
}

func newShutdownFixture(t *testing.T, backend *shutdownBackend, output io.Writer, timeout time.Duration) shutdownFixture {
	t.Helper()
	engine, err := control.New(logDriver{backend}, control.Options{Serials: []string{"test-device"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	f := shutdownFixture{engine: engine, listener: listener, cancel: cancel, done: make(chan error, 1)}
	t.Cleanup(func() { cancel(); _ = listener.Close() })
	go func() { f.done <- serveRuntime(parent, listener, engine, output, timeout) }()
	select {
	case <-backend.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("fake backend not connected")
	}
	return f
}

func (f shutdownFixture) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked on log consumer")
		return nil
	}
}

func TestRuntimeShutdownObservedTriggersAndCleanup(t *testing.T) {
	for _, tc := range []struct{ name, trigger, context, outcome string }{
		{"cancel", "parent_context_done", "context_canceled", "succeeded"},
		{"engine", "engine_stopped", "none", "failed"},
		{"listener", "http_server_returned", "none", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, output := newShutdownBackend(), newShutdownOutput()
			f := newShutdownFixture(t, b, output, time.Second)
			switch tc.name {
			case "cancel":
				f.cancel()
			case "engine":
				if err := f.engine.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "listener":
				if err := f.listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err := f.wait(t)
			if (err == nil) != (tc.outcome == "succeeded") {
				t.Fatalf("original return outcome changed: %v", err)
			}
			b.assertClosed(t)
			records := output.lifecycle(t)
			for _, record := range records {
				triggerMatches := record.Trigger == tc.trigger || (tc.name == "cancel" && record.Trigger == "engine_stopped")
				if !triggerMatches || record.ParentContext != tc.context || record.DaemonInstanceID != f.engine.Instance().DaemonInstanceID {
					t.Fatalf("incorrect observed cause: %+v", record)
				}
			}
			last := records[1]
			if last.HTTPShutdown != "none" || last.EngineClose != "none" || last.EngineDone == nil || !*last.EngineDone || last.Outcome != tc.outcome || last.ElapsedMS == nil || *last.ElapsedMS < 0 {
				t.Fatalf("incorrect cleanup result: %+v", last)
			}
		})
	}
}

type diagnosticTrapError struct{}

func (*diagnosticTrapError) Error() string { panic("diagnostic called Error") }
func (*diagnosticTrapError) Is(error) bool { panic("diagnostic called Is") }
func (*diagnosticTrapError) Unwrap() error { panic("diagnostic called Unwrap") }

type startupErrorWriter struct {
	ready <-chan struct{}
	err   error
}

func (w startupErrorWriter) Write([]byte) (int, error) { <-w.ready; return 0, w.err }

func TestRuntimeShutdownStartupSinkFailureDoesNotInspectErrorOrBlockCleanup(t *testing.T) {
	b, trap := newShutdownBackend(), &diagnosticTrapError{}
	f := newShutdownFixture(t, b, startupErrorWriter{ready: b.ready, err: trap}, time.Second)
	if err := f.wait(t); err != trap {
		t.Fatal("original startup error not returned unchanged")
	}
	b.assertClosed(t)
	// The failed sink cannot deliver its own failure event. Classification must
	// still be safe without invoking user-provided error methods.
	startup := make(chan error, 1)
	startup <- trap
	selected := waitRuntimeShutdown(context.Background(), nil, nil, startup)
	if selected.trigger != "startup_log_failed" || selected.cause != trap || runtimeErrorClass(selected.triggerError) != "other" {
		t.Fatal("startup error branch lost")
	}
}

type blockingLifecycleWriter struct {
	stage   string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	output  *shutdownOutput
}

func (w *blockingLifecycleWriter) Write(data []byte) (int, error) {
	var record struct{ Event string }
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, err
	}
	if record.Event == w.stage {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return w.output.Write(data)
}

func TestRuntimeShutdownBlockedLifecycleSinkNeverBlocksCleanup(t *testing.T) {
	for _, stage := range []string{"daemon_listening", "daemon_shutdown_trigger", "daemon_shutdown_result"} {
		t.Run(stage, func(t *testing.T) {
			b, output := newShutdownBackend(), newShutdownOutput()
			writer := &blockingLifecycleWriter{stage: stage, entered: make(chan struct{}), release: make(chan struct{}), output: output}
			var once sync.Once
			release := func() { once.Do(func() { close(writer.release) }) }
			defer release()
			f := newShutdownFixture(t, b, writer, time.Second)
			f.cancel()
			select {
			case <-writer.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("sink did not reach blocked stage")
			}
			if err := f.wait(t); err != nil {
				t.Fatal(err)
			}
			b.assertClosed(t)
			release()
			if records := output.lifecycle(t); records[1].Outcome != "succeeded" {
				t.Fatal("queued lifecycle pair did not drain after sink recovery")
			}
		})
	}
}

func TestRuntimeShutdownNativeWaitDeadlineIsNotCleanupCompletion(t *testing.T) {
	b, output := newShutdownBackend(), newShutdownOutput()
	b.stopEntered, b.stopGate = make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(b.stopGate) }) }
	defer release()
	f := newShutdownFixture(t, b, output, 20*time.Millisecond)
	f.cancel()
	select {
	case <-b.stopEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("fake native cleanup did not start")
	}
	if err := f.wait(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("original cleanup deadline return changed: %v", err)
	}
	record := output.lifecycle(t)[1]
	if record.EngineClose != "deadline_exceeded" || record.EngineDone == nil || *record.EngineDone || record.Outcome != "failed" {
		t.Fatalf("timeout incorrectly claims cleanup completed: %+v", record)
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		t.Fatal("blocked backend handle released before native return")
	}
	release()
	cleanup, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := f.engine.Close(cleanup); err != nil {
		t.Fatal(err)
	}
	b.assertClosed(t)
}

func TestRuntimeShutdownHTTPDrainDeadlineIsReportedSeparately(t *testing.T) {
	b, output := newShutdownBackend(), newShutdownOutput()
	f := newShutdownFixture(t, b, output, 20*time.Millisecond)
	connection, err := net.DialTimeout("tcp", f.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	request := "POST /v1/light/preview HTTP/1.1\r\nHost: " + f.listener.Addr().String() + "\r\nContent-Type: application/json\r\nContent-Length: 100\r\nExpect: 100-continue\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	// The 100 response proves the real handler began reading the uncompleted
	// body. Shutdown must now spend its shared budget draining this connection.
	line, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || line != "HTTP/1.1 100 Continue\r\n" {
		t.Fatalf("handler did not enter body read: %q %v", line, err)
	}
	f.cancel()
	if err := f.wait(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP shutdown error return changed: %v", err)
	}
	record := output.lifecycle(t)[1]
	if record.HTTPShutdown != "deadline_exceeded" || record.Outcome != "failed" || record.EngineDone == nil {
		t.Fatalf("HTTP drain failure conflated with engine cleanup: %+v", record)
	}
	cleanup, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := f.engine.Close(cleanup); err != nil {
		t.Fatal(err)
	}
	b.assertClosed(t)
}

type failedResultWriter struct {
	output    *shutdownOutput
	attempted chan struct{}
	once      sync.Once
}

func (w *failedResultWriter) Write(data []byte) (int, error) {
	var record struct{ Event string }
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, err
	}
	if record.Event == "daemon_shutdown_result" {
		w.once.Do(func() { close(w.attempted) })
		return 0, io.ErrClosedPipe
	}
	return w.output.Write(data)
}

func TestRuntimeShutdownLateSinkFailureDoesNotChangeCleanupOutcome(t *testing.T) {
	b := newShutdownBackend()
	w := &failedResultWriter{output: newShutdownOutput(), attempted: make(chan struct{})}
	f := newShutdownFixture(t, b, w, time.Second)
	f.cancel()
	if err := f.wait(t); err != nil {
		t.Fatal(err)
	}
	b.assertClosed(t)
	select {
	case <-w.attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("late sink failure was not exercised")
	}
}

func TestRuntimeShutdownCleanupErrorIsBoundedAndSeparate(t *testing.T) {
	b, output := newShutdownBackend(), newShutdownOutput()
	b.stopError = errors.New("PRIVATE-NATIVE-ERROR:" + strings.Repeat("x", 65536))
	f := newShutdownFixture(t, b, output, time.Second)
	f.cancel()
	if err := f.wait(t); err == nil {
		t.Fatal("original cleanup error swallowed")
	}
	records := output.lifecycle(t)
	last := records[1]
	if last.TriggerErrorClass != "none" || last.EngineClose != "other" || last.HTTPShutdown != "none" || last.Outcome != "failed" || last.EngineDone == nil || !*last.EngineDone {
		t.Fatalf("cleanup failure conflated with trigger: %+v", last)
	}
	for _, record := range records {
		data, _ := json.Marshal(record)
		if strings.Contains(string(data), "PRIVATE-NATIVE-ERROR") {
			t.Fatal("lifecycle leaked arbitrary error text")
		}
	}
	b.assertClosed(t)
}

func TestRuntimeShutdownClassifierNeverCallsCustomErrorMethods(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "none"}, {context.Canceled, "context_canceled"},
		{context.DeadlineExceeded, "deadline_exceeded"}, {http.ErrServerClosed, "http_server_closed"},
		{&diagnosticTrapError{}, "other"},
		{nonComparableDiagnosticError{1}, "other"},
	} {
		if got := runtimeErrorClass(tc.err); got != tc.want {
			t.Fatalf("classification=%s want=%s", got, tc.want)
		}
	}
}

type nonComparableDiagnosticError []int

func (nonComparableDiagnosticError) Error() string { panic("diagnostic called non-comparable Error") }
