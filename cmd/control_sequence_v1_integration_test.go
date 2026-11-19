package cmd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/triggerv1"
)

type sequenceControlDriver struct{ backend *sequenceControlBackend }

func (d sequenceControlDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type sequenceControlBackend struct {
	mu       sync.Mutex
	setCalls int
	led1     string
	led2     string
}

func (*sequenceControlBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20000001", Capabilities: []string{"set", "program", "raw-rgb", "hold", "mk2-basic"}}
}

func (b *sequenceControlBackend) Set(step control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setCalls++
	if step.LED == 0 || step.LED == 1 {
		b.led1 = step.Color
	}
	if step.LED == 0 || step.LED == 2 {
		b.led2 = step.Color
	}
	return nil
}

func (b *sequenceControlBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now().UTC()}, nil
}

func (b *sequenceControlBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "off" {
		b.led1, b.led2 = "#000000", "#000000"
	}
	return nil
}

func (*sequenceControlBackend) Close() {}

func TestSequenceControlV1ComposesWithHTTPAndEngine(t *testing.T) {
	t.Setenv("NB1_DEVICE", "")
	backend := &sequenceControlBackend{led1: "#000000", led2: "#000000"}
	engine, err := control.New(sequenceControlDriver{backend}, control.Options{Serials: []string{"20000001"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
	for {
		state, err := engine.Device("20000001")
		if err != nil {
			t.Fatal(err)
		}
		if state.Connection == "online" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fake device did not connect")
		case <-time.After(time.Millisecond):
		}
	}
	libraryDir := filepath.Join(t.TempDir(), "library")
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(libraryDir), httpapi.WithSequenceRouting("20000001", false, libraryDir)))
	defer server.Close()
	path := sequenceControlFile(t, sequenceControlInput)
	preview, err := executeSequenceControl(t, server.URL, "preview", path, "--serial", "20000001")
	if err != nil {
		t.Fatalf("CLI preview through real handler failed: %v", err)
	}
	var result struct {
		TotalSteps  int `json:"total_steps"`
		DeviceCheck struct {
			Status string `json:"status"`
		} `json:"device_check"`
	}
	if err := json.Unmarshal([]byte(preview), &result); err != nil || result.TotalSteps != 1 || result.DeviceCheck.Status != "supported" {
		t.Fatalf("invalid real preview %s, %v", preview, err)
	}
	backend.mu.Lock()
	previewWrites := backend.setCalls
	backend.mu.Unlock()
	if previewWrites != 0 {
		t.Fatalf("preview issued %d Set calls", previewWrites)
	}
	var accepted control.SequenceJob
	for attempt := 0; attempt < 2; attempt++ {
		output, err := executeSequenceControl(t, server.URL, "run", "20000001", path, "--id", "composition-job", "--wait")
		if err != nil {
			t.Fatalf("CLI run attempt %d failed: %v", attempt, err)
		}
		var job control.SequenceJob
		if err := json.Unmarshal([]byte(output), &job); err != nil || job.ID != "composition-job" || job.Status != "succeeded" || job.CompletedSteps != 1 {
			t.Fatalf("invalid real Job %s, %v", output, err)
		}
		if attempt == 0 {
			accepted = job
		} else if !accepted.CreatedAt.Equal(job.CreatedAt) || accepted.Serial != job.Serial {
			t.Fatalf("repeat submission changed the accepted Job: %+v then %+v", accepted, job)
		}
	}
	state := func(expected int) {
		t.Helper()
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if backend.setCalls != expected || backend.led1 != "#000000" || backend.led2 != "#000000" {
			t.Fatalf("Set calls=%d, final RGB=%s/%s", backend.setCalls, backend.led1, backend.led2)
		}
	}
	state(1)
	if _, err := executeSequenceControl(t, server.URL, "patterns", "save", "ready", path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libraryDir, "triggers.json"), []byte(`{"schema_version":1,"triggers":{"build.done":{"pattern_id":"ready"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := executeSequenceControl(t, server.URL, "triggers"); err != nil || !strings.Contains(output, "build.done") {
		t.Fatalf("CLI trigger listing: %s %v", output, err)
	}
	if output, err := executeSequenceControl(t, server.URL, "triggers", "--check"); err != nil || !strings.Contains(output, `"issues": []`) {
		t.Fatalf("CLI clean trigger report: %s %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(libraryDir, "triggers.json"), []byte(`{"schema_version":1,"triggers":{"build.done":{"pattern_id":"ready"},"dangling":{"pattern_id":"absent"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := executeSequenceControl(t, server.URL, "triggers", "--check")
	var report triggerv1.CheckReport
	if err == nil || !strings.Contains(err.Error(), "trigger check found issues") ||
		json.Unmarshal([]byte(output), &report) != nil || len(report.Items) != 2 ||
		len(report.Items[0].Issues) != 0 || len(report.Items[1].Issues) != 1 ||
		report.Items[1].Issues[0].Code != "pattern_not_found" {
		t.Fatalf("CLI trigger check with known issue: %s %v", output, err)
	}
	state(1)
	for _, args := range [][]string{
		{"emit", "build.done", "--id", "trigger-job", "--wait"},
		{"run", path, "--daemon-default", "--id", "default-job", "--wait"},
	} {
		output, err := executeSequenceControl(t, server.URL, args...)
		var job control.SequenceJob
		if err != nil || json.Unmarshal([]byte(output), &job) != nil || job.Status != "succeeded" || job.Serial != "20000001" || job.TargetSource != "default" {
			t.Fatalf("CLI routed playback %v: %s %v", args[0], output, err)
		}
	}
	state(3)
}
