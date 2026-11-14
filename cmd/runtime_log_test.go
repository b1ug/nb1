package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
)

func TestRuntimeJSONWriterSerializesConcurrentRecords(t *testing.T) {
	var buffer bytes.Buffer
	writer := &runtimeJSONWriter{output: &buffer}
	var done sync.WaitGroup
	for i := 0; i < 16; i++ {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			for j := 0; j < 10; j++ {
				if err := writer.encode(map[string]interface{}{"worker": index, "record": j}); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	done.Wait()
	decoder := json.NewDecoder(&buffer)
	count := 0
	for {
		var record map[string]interface{}
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("JSON records interleaved: %v", err)
		}
		count++
	}
	if count != 160 {
		t.Fatalf("record count=%d", count)
	}
}

type logDriver struct{ backend control.Backend }

func (d logDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type samplingLogBackend struct {
	*lifecycleBackend
	reads int32
	ready chan struct{}
	once  sync.Once
}

func (b *samplingLogBackend) Read() (control.Observation, error) {
	if atomic.AddInt32(&b.reads, 1) >= 3 {
		b.once.Do(func() { close(b.ready) })
	}
	return b.lifecycleBackend.Read()
}

func TestConnectionLoggerWritesStartupFirstAndSkipsSamples(t *testing.T) {
	backend := &samplingLogBackend{lifecycleBackend: &lifecycleBackend{}, ready: make(chan struct{})}
	engine, err := control.New(logDriver{backend}, control.Options{Serials: []string{"test-device"}, ReadOnly: true, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })
	var buffer bytes.Buffer
	logging := startRuntimeLogging(engine, &buffer, map[string]string{"event": "daemon_listening"})
	select {
	case <-backend.ready:
	case <-time.After(time.Second):
		t.Fatal("poll samples were not exercised")
	}
	if err := engine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-logging.done:
	case <-time.After(time.Second):
		t.Fatal("unblocked logger did not drain events")
	}
	decoder := json.NewDecoder(&buffer)
	var records []map[string]interface{}
	for {
		var record map[string]interface{}
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != 5 || records[0]["event"] != "daemon_listening" {
		t.Fatalf("startup/sampling log records: %+v", records)
	}
	for i, state := range []string{"connecting", "online", "closing", "closed"} {
		if records[i+1]["event"] != "device_connection" || records[i+1]["connection"] != state || records[i+1]["sequence"] != float64(i+1) {
			t.Fatalf("out-of-order connection record: %+v", records[i+1])
		}
	}
}

type blockedRuntimeWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedRuntimeWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(data), nil
}

func TestBlockedStartupLogCannotBlockHardwareShutdown(t *testing.T) {
	backend := &lifecycleBackend{}
	engine, err := control.New(logDriver{backend}, control.Options{Serials: []string{"test-device"}})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &blockedRuntimeWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(output.release)
	finished := make(chan error, 1)
	go func() { finished <- serveRuntime(parent, listener, engine, output, time.Second) }()
	select {
	case <-output.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not block startup announcement")
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := engine.Device("test-device")
		if state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device could not connect while startup log was blocked")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked log writer delayed daemon/hardware shutdown")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.closed || backend.lastStop != "off" {
		t.Fatalf("hardware cleanup incomplete: closed=%v stop=%s", backend.closed, backend.lastStop)
	}
}
