package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/control"
	"github.com/spf13/cobra"
)

func profileForTest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NB1_CONFIG", path)
	t.Setenv("NB1_DEVICE", "")
	return path
}

func TestControlUsesConfiguredDeviceAndExplicitSerialWins(t *testing.T) {
	profileForTest(t, "device: \"20000001\"\njobs:\n  terminal_limit: 2\n  terminal_ttl: 5m\n")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "POST" || r.URL.Path != "/v1/jobs" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var input struct{ ID, Serial string }
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		job := sequenceControlJob(input.ID, "succeeded")
		job.Serial = input.Serial
		sequenceControlJSON(w, http.StatusAccepted, job)
	}))
	defer server.Close()
	file := sequenceControlFile(t, sequenceControlInput)
	for _, tc := range []struct {
		args   []string
		serial string
	}{
		{[]string{"run", file}, "20000001"}, {[]string{"run", "20000002", file}, "20000002"},
	} {
		out, err := executeSequenceControl(t, server.URL, tc.args...)
		if err != nil || !strings.Contains(out, tc.serial) {
			t.Fatalf("run = %s %v", out, err)
		}
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("default selection made extra requests: %d", calls)
	}
}

func TestControlRejectsExplicitEmptySerialAndInvalidConfigBeforeHTTP(t *testing.T) {
	path := profileForTest(t, "device: \"20000001\"\n")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	file := sequenceControlFile(t, sequenceControlInput)
	for _, args := range [][]string{{"run", "", file}, {"state", ""}} {
		if _, err := executeSequenceControl(t, server.URL, args...); err == nil {
			t.Fatal("empty explicit serial selected a default")
		}
	}
	if err := os.WriteFile(path, []byte("jobs:\n  terminal_limit: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSequenceControl(t, server.URL, "run", file); err == nil {
		t.Fatal("invalid profile accepted")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid input made %d requests", calls)
	}
}

func TestControlInfersOnlyOneConfiguredDaemonDevice(t *testing.T) {
	profileForTest(t, "{}\n")
	var posts int32
	var count int32 = 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/devices" {
			devices := []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "20000001"}}}
			if atomic.LoadInt32(&count) == 2 {
				devices = append(devices, control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: "20000002"}})
			}
			sequenceControlJSON(w, 200, devices)
			return
		}
		atomic.AddInt32(&posts, 1)
		var input struct{ ID string }
		_ = json.NewDecoder(r.Body).Decode(&input)
		sequenceControlJSON(w, 202, sequenceControlJob(input.ID, "succeeded"))
	}))
	defer server.Close()
	file := sequenceControlFile(t, sequenceControlInput)
	if _, err := executeSequenceControl(t, server.URL, "run", file); err == nil || atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("ambiguous device submitted: %v posts%d", err, posts)
	}
	atomic.StoreInt32(&count, 1)
	if _, err := executeSequenceControl(t, server.URL, "run", file); err != nil || atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("one device unresolved: %v posts%d", err, posts)
	}
}

func TestConfigurationErrorPrecedesBasicUSBAndAllowAbsent(t *testing.T) {
	oldError, oldOpen, oldAbsent := configurationInitError, openBasicDevice, allowAbsent
	t.Cleanup(func() { configurationInitError, openBasicDevice, allowAbsent = oldError, oldOpen, oldAbsent })
	want := errors.New("invalid selected configuration")
	configurationInitError = want
	allowAbsent = true
	openBasicDevice = func(string) error { t.Fatal("configuration error opened USB"); return nil }
	if err := openBlink1Device(&cobra.Command{}, nil); !errors.Is(err, want) {
		t.Fatalf("configuration failure masked: %v", err)
	}
}

func TestServiceInstallCapturesProfileAndExplicitDeviceChoice(t *testing.T) {
	path := profileForTest(t, "device: \"20000001\"\nserver:\n  listen: \"127.0.0.1:8934\"\njobs:\n  terminal_limit: 2\n  terminal_ttl: 5m\n")
	fixture := serviceCLITestFixture(t, "darwin")
	var output bytes.Buffer
	if err := executeServiceCLI(fixture.management, context.Background(), &output, "install", "--json"); err != nil {
		t.Fatal(err)
	}
	got := fixture.mac.calls[0].config
	if got.ConfigFile != path || got.Listen != "127.0.0.1:8934" || len(got.Devices) != 1 || got.Devices[0] != "20000001" {
		t.Fatalf("profile not bound: %+v", got)
	}
}

func TestConfiguredServerDevicePrecedence(t *testing.T) {
	settings := appconfig.Defaults(t.TempDir())
	settings.Device = "20000001"
	settings.Server.Devices = []string{"20000002", "20000003"}
	command := &cobra.Command{}
	command.Flags().StringSlice("devices", nil, "")
	command.Flags().String("device", "", "")
	if got, err := configuredServerSerials(command, settings, nil); err != nil || len(got) != 2 {
		t.Fatalf("configured group: %v %v", got, err)
	}
	_ = command.Flags().Set("device", "20000004")
	if got, err := configuredServerSerials(command, settings, nil); err != nil || len(got) != 1 || got[0] != "20000004" {
		t.Fatalf("explicit device: %v %v", got, err)
	}
}

func TestProfileInitRejectsEmptyExplicitPathWithoutWriting(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "must-not-exist.yaml")
	t.Setenv("NB1_CONFIG", destination)
	for _, verb := range []string{"init", "path"} {
		parent := &cobra.Command{Use: "config", SilenceErrors: true, SilenceUsage: true}
		parent.PersistentFlags().String("config", "", "")
		addProfileCommands(parent)
		parent.SetOut(&bytes.Buffer{})
		parent.SetErr(&bytes.Buffer{})
		parent.SetArgs([]string{"--config", "", verb})
		if err := parent.Execute(); err == nil {
			t.Fatal("explicit empty config accepted")
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("init wrote a fallback: %v", err)
		}
	}
}
