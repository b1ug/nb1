package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/libraryv1"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/sequence"
)

type sequenceDirectTestBackend struct {
	mu                       sync.Mutex
	led1, led2               string
	writes, closes           int
	stopModes                []string
	lease                    *ownership.Lease
	entered, release, closed chan struct{}
	once                     sync.Once
}

func (*sequenceDirectTestBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20000001", Capabilities: []string{"set", "program", "raw-rgb", "hold", "mk2-basic"}}
}
func (b *sequenceDirectTestBackend) Set(s control.Step) error {
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes++
	if s.LED == 0 || s.LED == 1 {
		b.led1 = s.Color
	}
	if s.LED == 0 || s.LED == 2 {
		b.led2 = s.Color
	}
	return nil
}
func (b *sequenceDirectTestBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now().UTC()}, nil
}
func (b *sequenceDirectTestBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopModes = append(b.stopModes, mode)
	if mode == "off" {
		b.led1, b.led2 = "#000000", "#000000"
	}
	return nil
}
func (b *sequenceDirectTestBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	if b.lease != nil {
		_ = b.lease.Close()
	}
	if b.closed != nil {
		close(b.closed)
	}
}

type sequenceDirectTestDriver struct {
	open func(string) (control.Backend, error)
}

func (d sequenceDirectTestDriver) Open(serial string) (control.Backend, error) { return d.open(serial) }

func sequenceDirectProfile(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lib := filepath.Join(root, "library")
	path := filepath.Join(root, "config.yaml")
	data := "device: \"20000001\"\nlibrary_dir: " + lib + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NB1_CONFIG", path)
	t.Setenv("NB1_DEVICE", "")
	t.Setenv("NB1_LIBRARY_DIR", "")
	t.Setenv("NB1_LOCK_DIR", "")
	return lib
}
func executeSequenceDirect(ctx context.Context, driver control.Driver, args ...string) (string, string, error) {
	cmd := newSequenceControlCommandWithDriver(func(string) control.Driver { return driver })
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var out, diagnostic bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostic)
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), diagnostic.String(), err
}
func directOutput(t *testing.T, raw string) sequenceDirectResult {
	t.Helper()
	var result sequenceDirectResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("invalid direct output: %s (%v)", raw, err)
	}
	return result
}

func TestSequenceDirectPreflightNeverOpensDevice(t *testing.T) {
	sequenceDirectProfile(t)
	var opens int32
	driver := sequenceDirectTestDriver{open: func(string) (control.Backend, error) {
		atomic.AddInt32(&opens, 1)
		return nil, errors.New("must not open")
	}}
	valid := sequenceControlFile(t, `{"action":{"kind":"set","color":"red"}}`)
	invalid := sequenceControlFile(t, `{"action":{"kind":"set","color":"red","cycles":2}}`)
	alias := sequenceControlFile(t, `{"action":{"kind":"set","color":"@missing"}}`)
	pattern := sequenceControlFile(t, `{"pattern_id":"missing"}`)
	for _, args := range [][]string{
		{"--direct", "preview", valid},
		{"--direct", "job", "j_7K3XJNY9ZM"},
		{"--direct", "--endpoint", "http://127.0.0.1:1", "run", valid},
		{"--direct", "run", invalid},
		{"--direct", "run", alias},
		{"--direct", "run", pattern},
		{"--direct", "run", valid, "--id", ""},
		{"--direct", "run", valid, "--id", "j_7K3XJNY9ZM"},
		{"--direct", "run", valid, "--lock-dir", "relative"},
		{"--direct", "run", valid, "--shutdown-timeout", "0"},
		{"--direct", "run", valid, "--timeout", "0"},
	} {
		if _, _, err := executeSequenceDirect(context.Background(), driver, args...); err == nil {
			t.Fatalf("accepted invalid direct command: %v", args)
		}
	}
	path := os.Getenv("NB1_CONFIG")
	if err := os.WriteFile(path, []byte("device: \"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", valid); err == nil || !strings.Contains(err.Error(), "requires a serial") {
		t.Fatalf("missing serial: %v", err)
	}
	if atomic.LoadInt32(&opens) != 0 {
		t.Fatal("preflight opened a device")
	}
}

func TestSequenceDirectFreezesLocalColorAndReleasesHold(t *testing.T) {
	lib := sequenceDirectProfile(t)
	store := libraryv1.New(lib)
	if _, err := store.SaveColor("review-red", "#ff0000"); err != nil {
		t.Fatal(err)
	}
	backend := &sequenceDirectTestBackend{led1: "#000000", led2: "#000000"}
	driver := sequenceDirectTestDriver{open: func(string) (control.Backend, error) {
		// Changing the library after Open must not change this frozen execution.
		if _, err := store.SaveColor("review-red", "#0000ff"); err != nil {
			return nil, err
		}
		return backend, nil
	}}
	file := sequenceControlFile(t, `{"action":{"kind":"set","color":"@review-red"}}`)
	raw, _, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", file)
	if err != nil {
		t.Fatal(err)
	}
	result := directOutput(t, raw)
	if result.Mode != "direct" || !result.EngineClosed || result.Job == nil || result.Job.Status != "succeeded" || result.Job.CompletedSteps != 1 {
		t.Fatalf("result: %+v", result)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.writes != 1 || backend.closes != 1 || backend.led1 != "#ff0000" || backend.led2 != "#ff0000" {
		t.Fatalf("hold/freeze failed: %+v", backend)
	}
	if len(backend.stopModes) != 2 || backend.stopModes[0] != "hold" || backend.stopModes[1] != "hold" {
		t.Fatalf("duplicate shutdown output: %v", backend.stopModes)
	}
}

func TestSequenceDirectRunsLocalPatternAndInterval(t *testing.T) {
	lib := sequenceDirectProfile(t)
	store := libraryv1.New(lib)
	compiled, err := sequence.Decode([]byte(`{"timing":"interval","initial":[{"led":1,"color":"#ff0000"},{"led":2,"color":"#0000ff"}],"steps":[{"targets":[{"led":1,"color":"#0000ff"},{"led":2,"color":"#ff0000"}],"fade_ms":50,"interval_ms":100}],"on_finish":"off"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SavePattern(libraryv1.Pattern{ID: "review-interval", Sequence: compiled}); err != nil {
		t.Fatal(err)
	}
	backend := &sequenceDirectTestBackend{led1: "#000000", led2: "#000000"}
	driver := sequenceDirectTestDriver{open: func(string) (control.Backend, error) { return backend, nil }}
	file := sequenceControlFile(t, `{"pattern_id":"review-interval"}`)
	raw, _, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", file)
	if err != nil {
		t.Fatal(err)
	}
	result := directOutput(t, raw)
	if result.Job == nil || result.Job.Status != "succeeded" || !result.EngineClosed {
		t.Fatalf("result: %+v", result)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.writes != 4 || backend.closes != 1 || backend.led1 != "#000000" || backend.led2 != "#000000" {
		t.Fatalf("interval/Pattern did not use core executor: %+v", backend)
	}
}

func TestSequenceDirectTimeoutCancelsAndCloses(t *testing.T) {
	sequenceDirectProfile(t)
	backend := &sequenceDirectTestBackend{led1: "#000000", led2: "#000000"}
	driver := sequenceDirectTestDriver{open: func(string) (control.Backend, error) { return backend, nil }}
	file := sequenceControlFile(t, `{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#ff0000"}],"hold_ms":5000}],"on_finish":"hold"}}`)
	raw, diagnostic, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", file, "--timeout", "80ms")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	result := directOutput(t, raw)
	if result.Job == nil || result.Job.Status != "cancelled" || !result.EngineClosed {
		t.Fatalf("cancellation result: %+v", result)
	}
	if !strings.Contains(diagnostic, "cleanup") {
		t.Fatal("missing interruption diagnostic")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.closes != 1 || backend.led1 != "#000000" || backend.led2 != "#000000" {
		t.Fatalf("cancel cleanup failed: %+v", backend)
	}
}

func TestSequenceDirectBlockedNativeKeepsLeaseUntilActualClose(t *testing.T) {
	sequenceDirectProfile(t)
	lockDir := filepath.Join(t.TempDir(), "locks")
	backend := &sequenceDirectTestBackend{led1: "#000000", led2: "#000000", entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(backend.release) })
	driver := sequenceDirectTestDriver{open: func(serial string) (control.Backend, error) {
		lease, err := ownership.Acquire(lockDir, serial)
		backend.lease = lease
		return backend, err
	}}
	file := sequenceControlFile(t, `{"action":{"kind":"set","color":"red"}}`)
	start := time.Now()
	raw, _, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", file, "--timeout", "80ms", "--shutdown-timeout", "30ms")
	if err == nil || !strings.Contains(err.Error(), "cleanup unconfirmed") {
		t.Fatalf("native timeout: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("blocked native call prevented bounded response")
	}
	result := directOutput(t, raw)
	if result.EngineClosed || result.Job == nil || result.Job.Status != "cancelling" {
		t.Fatalf("claimed native cleanup: %+v", result)
	}
	if lease, err := ownership.Acquire(lockDir, "20000001"); !errors.Is(err, ownership.ErrBusy) {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("ownership was released during native I/O: %v", err)
	}
	backend.mu.Lock()
	closed := backend.closes
	backend.mu.Unlock()
	if closed != 0 {
		t.Fatal("backend closed before native Set returned")
	}
	releaseOnce.Do(func() { close(backend.release) })
	select {
	case <-backend.closed:
	case <-time.After(time.Second):
		t.Fatal("native cleanup did not settle after release")
	}
	lease, err := ownership.Acquire(lockDir, "20000001")
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}

func TestSequenceDirectBusyOwnerIsNeverOpenedOrStopped(t *testing.T) {
	sequenceDirectProfile(t)
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := ownership.Acquire(dir, "20000001")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	var writes int32
	driver := sequenceDirectTestDriver{open: func(serial string) (control.Backend, error) {
		other, err := ownership.Acquire(dir, serial)
		if err != nil {
			if errors.Is(err, ownership.ErrBusy) {
				return nil, control.ErrBusy
			}
			return nil, err
		}
		other.Close()
		atomic.AddInt32(&writes, 1)
		return nil, errors.New("unexpected acquired ownership")
	}}
	file := sequenceControlFile(t, `{"action":{"kind":"off"}}`)
	raw, _, err := executeSequenceDirect(context.Background(), driver, "--direct", "run", file)
	if !errors.Is(err, control.ErrBusy) {
		t.Fatalf("expected busy: %s %v", raw, err)
	}
	result := directOutput(t, raw)
	if result.Job != nil || !result.EngineClosed || atomic.LoadInt32(&writes) != 0 {
		t.Fatalf("busy owner was disturbed: %+v", result)
	}
}
