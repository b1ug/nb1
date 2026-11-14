package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

func executePreviewCommand(ctx context.Context, args ...string) (string, error) {
	command := newControlCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func previewFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preview.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestControlSoftwarePreviewDirectDoesNotAcquireOwnership(t *testing.T) {
	// A regular file cannot serve as a device lock directory. Both valid direct
	// commands must nevertheless succeed: they never enter the ownership path.
	blockedLockDir := previewFile(t, "not a lock directory")
	input := previewFile(t, `{"kind":"pulse","color":"#00A000","cycles":2,"period_ms":800}`)
	output, err := executePreviewCommand(context.Background(), "--mode", "direct", "--endpoint", "not-a-daemon-origin", "--lock-dir", blockedLockDir, "preview", "--effect", input)
	if err != nil {
		t.Fatalf("pure direct preview reached client/ownership: %v", err)
	}
	var result control.PreviewResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if err := control.ValidatePreviewResult(result, "effect", nil); err != nil || result.NominalDurationMS != 1600 || result.TotalSteps != 6 {
		t.Fatalf("direct preview: %#v %v", result, err)
	}
	output, err = executePreviewCommand(context.Background(), "--mode", "direct", "--endpoint", "not-a-daemon-origin", "--lock-dir", blockedLockDir, "capabilities")
	if err != nil {
		t.Fatalf("pure local capabilities reached client/ownership: %v", err)
	}
	var catalog control.CapabilityCatalog
	if err := json.Unmarshal([]byte(output), &catalog); err != nil || !reflect.DeepEqual(catalog, control.Capabilities()) {
		t.Fatalf("direct catalog: %#v %v", catalog, err)
	}
	data, err := os.ReadFile(blockedLockDir)
	if err != nil || string(data) != "not a lock directory" {
		t.Fatal("software command changed supplied lock path")
	}
}

func TestControlSoftwarePreviewArgumentGuardsPrecedeFilesAndNetwork(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	for _, args := range [][]string{
		{"preview"}, {"preview", "positional"},
		{"preview", "--program", ""},
		{"preview", "--program", "missing", "--effect", "missing"},
		{"preview", "--effect", "missing", "--serial", ""},
		{"preview", "--effect", "missing", "--serial", "../bad"},
		{"--mode", "direct", "preview", "--effect", "missing", "--serial", "target"},
		{"--mode", "direct", "preview", "--effect", "missing", "--serial", ""},
		{"preview", "--effect", "missing", "--policy", "replace"},
		{"capabilities", "--policy", "reject"},
		{"preview", "--effect", "missing", "--request-id", ""},
		{"preview", "--effect", "missing", "--expected-daemon-instance-id", ""},
		{"capabilities", "--request-id", "x", "--expected-daemon-instance-id", "y"},
		{"capabilities", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "must-not-exist")
			full := append([]string{"--endpoint", server.URL, "--lock-dir", lockDir}, args...)
			output, err := executePreviewCommand(context.Background(), full...)
			if err == nil || strings.Contains(err.Error(), "no such file") || output != "" {
				t.Fatalf("guard did not reject before file/client: %s %v", output, err)
			}
			if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
				t.Fatalf("guard touched lock path: %v", err)
			}
		})
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("guard made %d daemon calls", calls)
	}
}

func TestControlSoftwarePreviewForwardsIntentOnly(t *testing.T) {
	var calls int32
	captured := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/v1/programs/preview" || r.Method != "POST" {
			t.Errorf("unexpected route: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var received map[string]json.RawMessage
		if err := json.Unmarshal(data, &received); err != nil {
			t.Error(err)
			return
		}
		var input httpapi.PreviewRequest
		if err := json.Unmarshal(data, &input); err != nil {
			t.Error(err)
			return
		}
		result, err := control.Preview(input.Program, input.Effect)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if input.Serial != nil {
			result = control.WithDeviceSnapshot(result, *input.Serial, nil)
		}
		captured <- received
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	path := previewFile(t, `{"kind":"pulse","color":"#00a000","cycles":2,"period_ms":800}`)
	output, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "preview", "--effect", path, "--serial", "target")
	if err != nil {
		t.Fatal(err)
	}
	var result control.PreviewResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	serial := "target"
	if err := control.ValidatePreviewResult(result, "effect", &serial); err != nil || result.NominalDurationMS != 1600 || result.Validation.Device != "unknown" {
		t.Fatalf("preview output: %#v %v", result, err)
	}
	received := <-captured
	if atomic.LoadInt32(&calls) != 1 || len(received) != 2 || len(received["effect"]) == 0 || string(received["serial"]) != `"target"` {
		t.Fatalf("CLI compiled or added execution fields: %s calls=%d", received, calls)
	}
}

func TestControlSoftwarePreviewInputUsesStrictExistingReaders(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{"kind":"steady","color":"red"}`,
		`{"kind":"steady","color":"#000000","color":"#ffffff"}`,
		`{"Kind":"steady","color":"#000000"}`,
		`{"kind":"steady","color":"#000000","fade_ms":null}`,
		`{"kind":"steady","color":"#000000","policy":"reject"}`,
		`{"kind":"steady","color":"#000000"}{}`,
	} {
		path := previewFile(t, body)
		if output, err := executePreviewCommand(context.Background(), "--mode", "direct", "preview", "--effect", path); err == nil || output != "" {
			t.Fatalf("invalid direct preview: %s %s %v", body, output, err)
		}
	}
	program := previewFile(t, `{"steps":[{"color":"#102030","fade_ms":100}],"on_finish":"hold"}`)
	output, err := executePreviewCommand(context.Background(), "--mode", "direct", "preview", "--program", program)
	if err != nil || !strings.Contains(output, `"input_kind": "program"`) || !strings.Contains(output, `"nominal_duration_ms": 110`) {
		t.Fatalf("direct Program normalization: %s %v", output, err)
	}
}

func TestControlSoftwarePreviewNoFallbackAndCallerTimeout(t *testing.T) {
	path := previewFile(t, `{"kind":"steady","color":"#102030"}`)
	for _, status := range []int{404, 501} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(status) }))
		for _, args := range [][]string{{"capabilities"}, {"preview", "--effect", path}} {
			output, err := executePreviewCommand(context.Background(), append([]string{"--endpoint", server.URL}, args...)...)
			if err == nil || !strings.Contains(err.Error(), "unsupported") || output != "" {
				t.Fatalf("older daemon silently succeeded: %s %v", output, err)
			}
		}
		if atomic.LoadInt32(&calls) != 2 {
			t.Fatalf("older daemon was retried: %d", calls)
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	output, err := executePreviewCommand(ctx, "--endpoint", server.URL, "preview", "--effect", path)
	if !errors.Is(err, context.DeadlineExceeded) || output != "" {
		t.Fatalf("caller timeout lost: %s %v", output, err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := executePreviewCommand(cancelled, "--mode", "direct", "preview", "--program", "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled direct preview read input: %v", err)
	}
}
