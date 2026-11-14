package cmd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
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
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithSequenceAPI()))
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
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.setCalls != 1 || backend.led1 != "#000000" || backend.led2 != "#000000" {
		t.Fatalf("Set calls=%d, final RGB=%s/%s", backend.setCalls, backend.led1, backend.led2)
	}
}
