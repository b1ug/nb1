package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/sequence"
)

const sequenceControlInput = `{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#102030"}]}]}}`

func sequenceControlFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeSequenceControl(t *testing.T, endpoint string, args ...string) (string, error) {
	t.Helper()
	command := newSequenceControlCommand()
	command.SilenceErrors, command.SilenceUsage = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(append([]string{"--endpoint", endpoint}, args...))
	err := command.Execute()
	return output.String(), err
}

func sequenceControlJob(id, status string) control.SequenceJob {
	job := control.SequenceJob{ID: id, Serial: "20000001", Status: status, TotalSteps: 1, CreatedAt: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}
	if status == "succeeded" {
		job.CompletedSteps = 1
	}
	return job
}

func sequenceControlJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestSequenceControlV1RejectsInvalidFileBeforeHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, input := range []string{
		`{"program":{}}`,
		`{"action":{"kind":"off"},"sequence":{"timing":"serial","steps":[]}}`,
		`{"action":{"kind":"set","color":"red","kind":"off"}}`,
		`{"action":{"kind":"off","cycles":1}}`,
		`{"action":{"kind":"off","led":null}}`,
		`{"action":{"kind":"fade","color":"red","duration_ms":1}}`,
		strings.Replace(sequenceControlInput, `"led":0`, `"led":0,"LED":1`, 1),
	} {
		path := sequenceControlFile(t, input)
		if _, err := executeSequenceControl(t, server.URL, "preview", path); err == nil {
			t.Errorf("accepted invalid input %s", input)
		}
		if _, err := executeSequenceControl(t, server.URL, "run", "20000001", path); err == nil {
			t.Errorf("submitted invalid input %s", input)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("invalid input sent %d HTTP requests", got)
	}
}

func TestSequenceControlV1PreviewPreservesActionAndSerial(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/preview" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			Action json.RawMessage `json:"action"`
			Serial string          `json:"serial"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(input.Action), "@status-ok") || input.Serial != "20000001" {
			t.Errorf("author input changed: %+v", input)
		}
		compiled, err := actionv1.Compile(input.Action, func(string) (string, error) { return "#102030", nil })
		if err != nil {
			t.Error(err)
		}
		sequenceControlJSON(w, http.StatusOK, daemonclient.SequencePreview{Sequence: compiled, TotalSteps: 1, NominalDurationMS: compiled.NominalDurationMS(), RequiredCapabilities: []string{"set", "raw-rgb"}, DeviceCheck: daemonclient.SequenceDeviceCheck{Status: "supported"}})
	}))
	defer server.Close()
	path := sequenceControlFile(t, `{"action":{"kind":"set","color":"@status-ok"}}`)
	output, err := executeSequenceControl(t, server.URL, "preview", path, "--serial", "20000001")
	if err != nil || !strings.Contains(output, "#102030") || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("preview = %s, %v, calls %d", output, err, calls)
	}
}

func TestSequenceControlV1RunGeneratesIDAndPollsSameJob(t *testing.T) {
	var posts, gets int32
	var submittedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&posts, 1)
			var input struct {
				ID       string            `json:"id"`
				Serial   string            `json:"serial"`
				Sequence sequence.Sequence `json:"sequence"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			submittedID = input.ID
			if !regexp.MustCompile(`^j_[0-9A-HJKMNP-TV-Z]{10}$`).MatchString(input.ID) || input.Serial != "20000001" || input.Sequence.Steps[0].Targets[0].Color != "#102030" {
				t.Errorf("invalid submitted input %+v", input)
			}
			sequenceControlJSON(w, http.StatusAccepted, sequenceControlJob(input.ID, "running"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/"+submittedID {
			atomic.AddInt32(&gets, 1)
			sequenceControlJSON(w, http.StatusOK, sequenceControlJob(submittedID, "succeeded"))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	output, err := executeSequenceControl(t, server.URL, "run", "20000001", sequenceControlFile(t, sequenceControlInput), "--wait")
	if err != nil || !strings.Contains(output, `"status": "succeeded"`) || atomic.LoadInt32(&posts) != 1 || atomic.LoadInt32(&gets) != 1 {
		t.Fatalf("run = %s, %v, POSTs %d, GETs %d", output, err, posts, gets)
	}
}

func TestSequenceControlV1LostSubmitResponseReportsOriginalIDWithoutRetry(t *testing.T) {
	var posts int32
	var capturedID atomic.Value
	capturedID.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		var input struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		capturedID.Store(input.ID)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	_, err := executeSequenceControl(t, server.URL, "run", "20000001", sequenceControlFile(t, sequenceControlInput), "--wait")
	submittedID := capturedID.Load().(string)
	if err == nil || submittedID == "" || !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), "control --endpoint "+server.URL+" job "+submittedID) || atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("lost response error = %v, ID %q, requests %d", err, submittedID, posts)
	}
	if strings.Contains(err.Error(), "Start a daemon explicitly") {
		t.Fatalf("lost response misreported as a failed connection: %v", err)
	}
}

func TestSequenceControlUnavailableDaemonGuidesWithoutLosingJobIdentity(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + address
	file := sequenceControlFile(t, sequenceControlInput)
	for _, tc := range []struct {
		name, serial string
		args         []string
	}{
		{"run", "20000001", []string{"run", "20000001", file, "--id", "j_7K3XJNY9ZM"}},
		{"state", "20000001", []string{"state", "20000001"}},
		{"preview", "20000001", []string{"preview", file, "--serial", "20000001"}},
		{"devices", "SERIAL", []string{"devices"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executeSequenceControl(t, endpoint, tc.args...)
			var network *net.OpError
			if err == nil || !errors.As(err, &network) {
				t.Fatalf("lost underlying network error: %v", err)
			}
			for _, text := range []string{
				"cannot connect to daemon at " + endpoint,
				"nb1 server --devices " + tc.serial + " --listen " + address,
				"nb1 service start",
				"nb1 --device " + tc.serial + " read state",
			} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("missing usable guidance %q: %v", text, err)
				}
			}
			if tc.name == "run" && !strings.Contains(err.Error(), "control --endpoint "+endpoint+" job j_7K3XJNY9ZM") {
				t.Fatalf("connection error discarded the original Job query: %v", err)
			}
		})
	}
}

func TestSequenceControlV1CancelledRunReturnsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" {
			sequenceControlJSON(w, http.StatusAccepted, sequenceControlJob("cancelled-run", "running"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/cancelled-run" {
			sequenceControlJSON(w, http.StatusOK, sequenceControlJob("cancelled-run", "cancelled"))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	output, err := executeSequenceControl(t, server.URL, "run", "20000001", sequenceControlFile(t, sequenceControlInput), "--id", "cancelled-run", "--wait")
	if err == nil || !strings.Contains(err.Error(), "was cancelled") || !strings.Contains(output, `"status": "cancelled"`) {
		t.Fatalf("cancelled run = %s, %v", output, err)
	}
}

func TestSequenceControlV1RejectsNonRegularAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := readSequenceControlFile(dir); err == nil {
		t.Fatal("directory input accepted")
	}
	oversized := filepath.Join(dir, "large.json")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(sequence.MaxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSequenceControlFile(oversized); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestSequenceControlV1CancelWaitsWithoutRepeatingPost(t *testing.T) {
	var posts, gets int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs/custom-id/cancel" {
			atomic.AddInt32(&posts, 1)
			if r.ContentLength != 0 {
				t.Errorf("cancel sent a body of %d bytes", r.ContentLength)
			}
			sequenceControlJSON(w, http.StatusAccepted, sequenceControlJob("custom-id", "cancelling"))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/custom-id" {
			atomic.AddInt32(&gets, 1)
			sequenceControlJSON(w, http.StatusOK, sequenceControlJob("custom-id", "cancelled"))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	output, err := executeSequenceControl(t, server.URL, "cancel", "custom-id", "--wait")
	if err != nil || !strings.Contains(output, `"status": "cancelled"`) || atomic.LoadInt32(&posts) != 1 || atomic.LoadInt32(&gets) != 1 {
		t.Fatalf("cancel = %s, %v, POSTs %d, GETs %d", output, err, posts, gets)
	}
}
