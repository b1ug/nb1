package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
)

type patternBackend struct {
	mu           sync.Mutex
	sets, closes int
	entered      chan struct{}
	release      chan struct{}
	once         sync.Once
}

func (b *patternBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20005601", Capabilities: []string{"set", "program", "raw-rgb", "hold"}}
}
func (b *patternBackend) Set(control.Step) error {
	b.mu.Lock()
	b.sets++
	b.mu.Unlock()
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}
func (b *patternBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now()}, nil
}
func (*patternBackend) Stop(string) error { return nil }
func (b *patternBackend) Close()          { b.mu.Lock(); defer b.mu.Unlock(); b.closes++ }

type patternDriver struct{ backend *patternBackend }

func (d patternDriver) Open(string) (control.Backend, error) { return d.backend, nil }

func patternNetwork(t *testing.T, client *http.Client, method, path string, body []byte) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}

func TestPatternReceiptIdentitySurvivesEquivalentInputsAndLibraryFailure(t *testing.T) {
	b := &patternBackend{entered: make(chan struct{}), release: make(chan struct{})}
	engine, err := control.New(patternDriver{b}, control.Options{Serials: []string{"20005601"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	finish := func() {
		releaseOnce.Do(func() { close(b.release) })
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(finish)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := engine.Device("20005601")
		if err == nil && snapshot.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake backend did not become online")
		}
		time.Sleep(time.Millisecond)
	}
	store := patternStore(t)
	p := control.Program{Steps: []control.Step{{Color: "#aabbcc"}}, OnFinish: "hold"}
	first := patternRecord(t, store, "first", p)
	equivalent := patternRecord(t, store, "another-title", p)
	different := patternRecord(t, store, "different", control.Program{Steps: []control.Step{{Color: "#123456"}}, OnFinish: "hold"})
	if first.Revision == equivalent.Revision {
		t.Fatal("test requires different record revisions")
	}
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithPatternStore(store)))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ticket, err := client.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	extra := map[string]interface{}{"policy": "reject", "request_id": ticket.RequestID, "expected_daemon_instance_id": ticket.ExpectedDaemonInstanceID}
	post := func(body []byte) (int, control.Job, http.Header, []byte) {
		status, data, headers := patternNetwork(t, server.Client(), "POST", server.URL+"/v1/devices/20005601/jobs", body)
		var job control.Job
		_ = json.Unmarshal(data, &job)
		return status, job, headers, data
	}
	status, job, headers, data := post(patternBody(t, first, extra))
	if status != 202 || headers.Get("X-NB1-Replayed") != "false" {
		t.Fatal(status, string(data))
	}
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first pattern did not reach fake backend")
	}
	status, replay, headers, data := post(patternBody(t, equivalent, extra))
	if status != 202 || replay.ID != job.ID || headers.Get("X-NB1-Replayed") != "true" {
		t.Fatal(status, string(data))
	}
	raw, err := client.SubmitWithReceipt(context.Background(), "20005601", p, "reject", ticket)
	if err != nil || !raw.Replayed || raw.Job.ID != job.ID {
		t.Fatalf("raw equivalence: %+v %v", raw, err)
	}
	light, _ := json.Marshal(map[string]interface{}{"contract": "nb1-light-action/v1", "action": map[string]interface{}{"kind": "set", "color": "#aabbcc"}, "policy": "reject", "request_id": ticket.RequestID, "expected_daemon_instance_id": ticket.ExpectedDaemonInstanceID})
	status, data, headers = patternNetwork(t, server.Client(), "POST", server.URL+"/v1/devices/20005601/light", light)
	var lightJob control.Job
	_ = json.Unmarshal(data, &lightJob)
	if status != 202 || lightJob.ID != job.ID || headers.Get("X-NB1-Replayed") != "true" {
		t.Fatal(status, string(data))
	}
	status, _, _, data = post(patternBody(t, different, extra))
	if status != 409 || !strings.Contains(string(data), "idempotency_conflict") {
		t.Fatal(status, string(data))
	}
	if _, err = client.SubmitWithReceipt(context.Background(), "20005601", p, "replace", ticket); err == nil || !strings.Contains(err.Error(), "idempotency_conflict") {
		t.Fatalf("different policy reused ticket: %v", err)
	}
	b.mu.Lock()
	sets := b.sets
	b.mu.Unlock()
	if sets != 1 {
		t.Fatalf("equivalent submissions executed %d sets", sets)
	}
	// Destroy only this test's saved snapshot after admission. Neither ordinary
	// Job lookup nor receipt lookup/cancel may consult the current library.
	if err = os.WriteFile(filepath.Join(store.Path(), "patterns.json"), []byte("broken snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	status, _, _, data = post(patternBody(t, first, extra))
	if status != 503 || !strings.Contains(string(data), "query that receipt") {
		t.Fatal(status, string(data))
	}
	byReceipt, err := client.Request(context.Background(), "20005601", ticket)
	if err != nil || byReceipt.ID != job.ID {
		t.Fatalf("receipt depended on library: %+v %v", byReceipt, err)
	}
	byID, err := client.Job(context.Background(), job.ID)
	if err != nil || byID.ID != job.ID {
		t.Fatalf("Job depended on library: %+v %v", byID, err)
	}
	if _, err = client.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	finish()
	b.mu.Lock()
	sets, closes := b.sets, b.closes
	b.mu.Unlock()
	if sets != 1 || closes != 1 {
		t.Fatalf("executor ownership changed: sets=%d closes=%d", sets, closes)
	}
}
