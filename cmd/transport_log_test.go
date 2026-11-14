package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/transporttrace"
)

type traceCountWriter struct {
	bytes, lines, maxLine int
	fail                  error
	short                 bool
}

func (w *traceCountWriter) Write(b []byte) (int, error) {
	if w.fail != nil {
		return 0, w.fail
	}
	if w.short {
		return len(b) - 1, nil
	}
	w.bytes += len(b)
	w.lines++
	if len(b) > w.maxLine {
		w.maxLine = len(b)
	}
	if !json.Valid(bytes.TrimSpace(b)) {
		return 0, errors.New("invalid record")
	}
	return len(b), nil
}
func traceEvent(t *testing.T, r *transporttrace.Recorder) transporttrace.Event {
	t.Helper()
	scope := transporttrace.EmptyScope(transporttrace.ProgramStep)
	scope.JobID = "job_" + strings.Repeat("a", 32)
	r.Operation("20005601", scope, transporttrace.SetColor, transporttrace.Target{LED: 1, RGB: [3]byte{1, 2, 3}, HasColor: true}).Begin(1).End(errors.New(strings.Repeat("<", 160)), true, true, 5)
	return r.Snapshot().Events[1]
}
func TestTransportLogBudgetLineBoundAndShortWrite(t *testing.T) {
	r := transporttrace.New()
	defer r.Close()
	event := traceEvent(t, r)
	writer := &traceCountWriter{}
	log := &transportLog{writer: &runtimeJSONWriter{output: writer}, recorder: r, limit: transporttrace.LogBudget}
	if !log.write(r.Header(), false) {
		t.Fatal("header")
	}
	for i := 0; i < 10000; i++ {
		if !log.write(event, false) {
			t.Fatal("bounded writer failed")
		}
	}
	if r.Stats().LogSuppressed == 0 || writer.bytes > transporttrace.LogBudget-transporttrace.SummaryReserve || writer.maxLine > transporttrace.MaxLineBytes {
		t.Fatalf("missing byte cap %+v / %+v", r.Stats(), writer)
	}
	if !log.write(r.Stats(), true) || writer.bytes > transporttrace.LogBudget {
		t.Fatal("summary exceeded reserve")
	}
	for _, failure := range []struct {
		short bool
		err   error
	}{{true, nil}, {false, io.ErrClosedPipe}} {
		bad := &transportLog{writer: &runtimeJSONWriter{output: &traceCountWriter{short: failure.short, fail: failure.err}}, recorder: r, limit: transporttrace.LogBudget}
		if bad.write(event, false) {
			t.Fatal("writer failure treated as recorded")
		}
	}
	if r.Stats().WriterErrors != 2 {
		t.Fatal(r.Stats())
	}
}

type traceBlockedWriter struct {
	entered, release chan struct{}
	once             sync.Once
	buffer           bytes.Buffer
}

func (w *traceBlockedWriter) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(`"event":"transport_attempt"`)) {
		w.once.Do(func() { close(w.entered) })
		<-w.release
	}
	return w.buffer.Write(b)
}
func TestTransportBlockedLogCannotBlockCleanupAndLossIsExplicit(t *testing.T) {
	b := &lifecycleBackend{set: make(chan struct{})}
	e, err := control.New(lifecycleDriver{b}, control.Options{Serials: []string{"test-device"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := transporttrace.New()
	writer := &traceBlockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	logging := startRuntimeLogging(e, writer, map[string]string{"event": "daemon_listening"}, r)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(writer.release) }) }
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = e.Close(ctx)
		r.Close()
		select {
		case <-logging.done:
		case <-ctx.Done():
			t.Error("logger did not finish after release")
		}
	})
	// Start schedules discovery asynchronously. Establish an opened backend
	// before asserting that shutdown stops and closes it; the synthetic trace
	// below does not itself synchronize with the device worker.
	ready, readyCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readyCancel()
	for {
		state, stateErr := e.Device("test-device")
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if state.Connection == "online" {
			break
		}
		select {
		case <-ready.Done():
			t.Fatal("fake device did not become online", ready.Err())
		case <-time.After(time.Millisecond):
		}
	}
	r.Operation("20005601", transporttrace.EmptyScope(transporttrace.IdleObserve), transporttrace.QueryRGB, transporttrace.Target{LED: 1}).Begin(1).End(nil, false, false, 0)
	select {
	case <-writer.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("trace writer not entered")
	}
	for i := 0; i < transporttrace.Capacity; i++ {
		r.Operation("20005601", transporttrace.EmptyScope(transporttrace.IdleObserve), transporttrace.QueryRGB, transporttrace.Target{LED: 2}).Begin(1).End(nil, false, false, 0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = e.Close(ctx); err != nil {
		t.Fatal("bad log blocked owner cleanup", err)
	}
	b.mu.Lock()
	closed, mode := b.closed, b.lastStop
	b.mu.Unlock()
	if !closed || mode != "off" {
		t.Fatal("cleanup not complete while writer blocked")
	}
	if r.Stats().QueueDropped == 0 || r.Stats().Evicted == 0 {
		t.Fatal("trace loss was hidden", r.Stats())
	}
	r.Close()
	release()
	select {
	case <-logging.done:
	case <-ctx.Done():
		t.Fatal("logger did not drain")
	}
	decoder := json.NewDecoder(&writer.buffer)
	var names []string
	for {
		var v map[string]interface{}
		err := decoder.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, v["event"].(string))
	}
	if len(names) < 3 || names[0] != "daemon_listening" || names[1] != "transport_trace_started" || names[len(names)-1] != "transport_trace_summary" {
		t.Fatal("startup/order/summary lost", names)
	}
}

func TestTransportTraceServerFlagDefaultsOff(t *testing.T) {
	cmd := newServerCommand()
	flag := cmd.Flags().Lookup("transport-trace")
	if flag == nil || flag.DefValue != "false" {
		t.Fatal("trace must require explicit opt-in")
	}
}

type failedTraceWriter struct{}

func (failedTraceWriter) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(`"event":"transport_trace_started"`)) {
		return 0, io.ErrClosedPipe
	}
	return len(b), nil
}
func TestTransportTraceWriterFailureDoesNotStopWorker(t *testing.T) {
	b := &lifecycleBackend{set: make(chan struct{})}
	e, err := control.New(lifecycleDriver{b}, control.Options{Serials: []string{"test-device"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := transporttrace.New()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = e.Close(ctx)
		r.Close()
	})
	logging := startRuntimeLogging(e, failedTraceWriter{}, map[string]string{"event": "daemon_listening"}, r)
	select {
	case <-logging.done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer failure did not end logger")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var job control.Job
	for {
		job, err = e.Submit(ctx, "test-device", control.Program{Steps: []control.Step{{Color: "#112233"}}, OnFinish: "off"}, "reject")
		if err == nil {
			break
		}
		if !errors.Is(err, control.ErrOffline) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	for {
		latest, lookupErr := e.Job(job.ID)
		if lookupErr != nil {
			t.Fatal(lookupErr)
		}
		if latest.FinishedAt != nil {
			if latest.Status != "succeeded" {
				t.Fatal(latest)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err = e.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if r.Stats().WriterErrors != 1 {
		t.Fatal("writer failure lost", r.Stats())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed || b.lastStop != "off" {
		t.Fatal("writer prevented cleanup")
	}
}
