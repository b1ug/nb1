package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/doctor"
)

func TestDoctorJSONAndExitStatusWithoutUSBFallback(t *testing.T) {
	for _, connection := range []string{"online", "offline"} {
		t.Run(connection, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("doctor submitted a mutation: %s", r.Method)
				}
				switch r.URL.Path {
				case "/healthz":
					json.NewEncoder(w).Encode(map[string]string{"status": "ok", "api_version": "v1"})
				case "/v1/devices":
					json.NewEncoder(w).Encode([]control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "test-target"}, Connection: connection, Observed: &control.Observation{SampledAt: time.Now().UTC()}}})
				default:
					t.Errorf("unexpected route: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
			t.Setenv("NB1_LOCK_DIR", lockDir)
			command := newDoctorCommand()
			var output, errors bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&errors)
			command.SilenceErrors, command.SilenceUsage = true, true
			command.SetArgs([]string{"--json", "--endpoint", server.URL, "--serial", "test-target"})
			err := command.Execute()
			if (err != nil) != (connection == "offline") {
				t.Fatalf("wrong exit result for %s: %v", connection, err)
			}
			var report doctor.Report
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatalf("stdout must remain a complete JSON report: %s %v", output.String(), err)
			}
			if report.SchemaVersion != 1 || len(report.Devices) != 1 || report.Devices[0].Connection != connection || errors.Len() != 0 {
				t.Fatalf("bad JSON/stdout contract: %+v stderr=%q", report, errors.String())
			}
			if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
				t.Fatalf("doctor reached hardware ownership: %v", err)
			}
		})
	}
}

func TestDoctorUnavailableKeepsReportAndDoesNotCreateLock(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("NB1_LOCK_DIR", lockDir)
	command := newDoctorCommand()
	command.SilenceErrors, command.SilenceUsage = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"--endpoint", endpoint, "--serial", "target"})
	if err := command.Execute(); err == nil || !strings.Contains(output.String(), "nb1 server --devices target") {
		t.Fatalf("unavailable daemon was not explained: err=%v output=%s", err, output.String())
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("doctor silently fell back to USB: %v", err)
	}
}
