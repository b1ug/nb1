package cmd

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/spf13/cobra"
)

func TestLoopbackListenBoundary(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8931", "127.0.0.1:0", "localhost:8931", "[::1]:8931"} {
		if err := validateLocalAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{":8931", "0.0.0.0:8931", "192.168.1.2:8931", "example.com:8931", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1"} {
		if err := validateLocalAddress(address); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
}

func TestServerPortConfiguration(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().String("listen", "127.0.0.1:8931", "")
	command.Flags().Uint32("port", 8931, "")
	t.Setenv("NB1_PORT", "invalid")
	if _, err := serverAddress(command, "127.0.0.1:8931"); err == nil {
		t.Fatal("invalid environment port accepted")
	}
	if err := command.Flags().Set("port", "8932"); err != nil {
		t.Fatal(err)
	}
	if address, err := serverAddress(command, "127.0.0.1:8931"); err != nil || address != "127.0.0.1:8932" {
		t.Fatalf("explicit port precedence: %s %v", address, err)
	}
	if err := command.Flags().Set("listen", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if _, err := serverAddress(command, "127.0.0.1:0"); err == nil {
		t.Fatal("listen and port conflict accepted")
	}
}

type runtimeOutput struct{ line chan []byte }

func (w runtimeOutput) Write(b []byte) (int, error) {
	w.line <- append([]byte(nil), b...)
	return len(b), nil
}

type lifecycleDriver struct{ backend *lifecycleBackend }

func (d lifecycleDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type lifecycleBackend struct {
	mu       sync.Mutex
	lastStop string
	closed   bool
	set      chan struct{}
	once     sync.Once
}

func (*lifecycleBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "test-device", Capabilities: []string{"set"}}
}
func (b *lifecycleBackend) Set(control.Step) error { b.once.Do(func() { close(b.set) }); return nil }
func (*lifecycleBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *lifecycleBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastStop = mode
	return nil
}
func (b *lifecycleBackend) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true }

func TestDaemonContextShutdownCleansActiveJob(t *testing.T) {
	backend := &lifecycleBackend{set: make(chan struct{})}
	engine, err := control.New(lifecycleDriver{backend}, control.Options{Serials: []string{"test-device"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := runtimeOutput{line: make(chan []byte, 1)}
	done := make(chan error, 1)
	go func() { done <- serveRuntime(ctx, listener, engine, output, 2*time.Second) }()
	var address struct {
		URL string `json:"url"`
	}
	select {
	case line := <-output.line:
		if err := json.Unmarshal(line, &address); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no startup address")
	}
	client, err := daemonclient.New(address.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	requestCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for {
		state, err := client.Device(requestCtx, "test-device")
		if err != nil {
			t.Fatal(err)
		}
		if state.Connection == "online" {
			break
		}
		select {
		case <-requestCtx.Done():
			t.Fatal("runtime did not connect")
		case <-time.After(time.Millisecond):
		}
	}
	job, err := client.Submit(requestCtx, "test-device", control.Program{Steps: []control.Step{{Color: "#ff0000", HoldMS: 30000}}, Repeat: 1, OnFinish: "hold"}, "queue")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.set:
	case <-requestCtx.Done():
		t.Fatal("job did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon shutdown did not finish")
	}
	job, err = engine.Job(job.ID)
	if err != nil || job.Status != "cancelled" {
		t.Fatalf("shutdown job=%v err=%v", job, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.closed || backend.lastStop != "off" {
		t.Fatalf("closed=%t stop=%q", backend.closed, backend.lastStop)
	}
}
