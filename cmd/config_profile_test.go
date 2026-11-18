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

func executeControlSelection(t *testing.T, endpoint string, stderr io.Writer, args ...string) (string, error) {
	t.Helper()
	command := newSequenceControlCommand()
	command.SilenceErrors, command.SilenceUsage = true, true
	// The production root owns this persistent flag.
	command.PersistentFlags().String("device", "", "preferred device")
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(stderr)
	command.SetArgs(append([]string{"--endpoint", endpoint}, args...))
	err := command.Execute()
	return stdout.String(), err
}

func TestControlAutomaticDeviceSelection(t *testing.T) {
	device := func(serial, connection string) control.DeviceState {
		return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial}, Connection: connection}
	}
	for _, tc := range []struct {
		name      string
		devices   []control.DeviceState
		selected  string
		listError string
	}{
		{"single-online", []control.DeviceState{device("20000002", "online")}, "20000002", ""},
		{"single-connection-omitted", []control.DeviceState{device("20000002", "")}, "20000002", ""},
		{"two-online-reversed", []control.DeviceState{device("20000002", "online"), device("20000001", "online")}, "20000001", ""},
		{"online-before-lower-offline", []control.DeviceState{device("20000001", "offline"), device("20000002", "online")}, "20000002", ""},
		{"online-before-lower-busy", []control.DeviceState{device("20000001", "busy"), device("20000002", "online")}, "20000002", ""},
		{"online-before-unknown", []control.DeviceState{device("20000001", "future-state"), device("20000002", "online")}, "20000002", ""},
		{"all-offline-reversed", []control.DeviceState{device("20000002", "offline"), device("20000001", "offline")}, "20000001", ""},
		{"no-online-connecting-and-error", []control.DeviceState{device("20000002", "connecting"), device("20000001", "error")}, "20000001", ""},
		{"online-active-job-keeps-target", []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "20000001"}, Connection: "online", ActiveJob: "j_existing"}, device("20000002", "online")}, "20000001", ""},
		{"empty", []control.DeviceState{}, "", "no configured devices"},
		{"missing-serial", []control.DeviceState{device("", "online")}, "", "invalid device list"},
		{"duplicate-serial", []control.DeviceState{device("20000001", "online"), device("20000001", "offline")}, "", "invalid device list"},
	} {
		for _, verb := range []string{"state", "run"} {
			t.Run(tc.name+"/"+verb, func(t *testing.T) {
				profileForTest(t, "{}\n")
				t.Setenv("NO_COLOR", "1")
				var targets []string
				var listCalls int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" && r.URL.Path == "/v1/devices" {
						listCalls++
						sequenceControlJSON(w, 200, tc.devices)
						return
					}
					if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/devices/") {
						selected := strings.TrimPrefix(r.URL.Path, "/v1/devices/")
						targets = append(targets, selected)
						sequenceControlJSON(w, 200, device(selected, "offline"))
						return
					}
					if r.Method != "POST" || r.URL.Path != "/v1/jobs" {
						t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
						w.WriteHeader(500)
						return
					}
					var input struct{ ID, Serial string }
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					targets = append(targets, input.Serial)
					job := sequenceControlJob(input.ID, "succeeded")
					job.Serial = input.Serial
					sequenceControlJSON(w, 202, job)
				}))
				defer server.Close()
				args := []string{verb}
				if verb == "run" {
					args = append(args, sequenceControlFile(t, sequenceControlInput))
				}
				var stderr bytes.Buffer
				stdout, err := executeControlSelection(t, server.URL, &stderr, args...)
				if tc.listError != "" {
					if err == nil || !strings.Contains(err.Error(), tc.listError) || len(targets) != 0 || stdout != "" || stderr.Len() != 0 {
						t.Fatalf("invalid directory: stdout=%q stderr=%q err=%v targets=%v", stdout, stderr.String(), err, targets)
					}
					return
				}
				var output struct{ Serial string }
				decoder := json.NewDecoder(strings.NewReader(stdout))
				if err != nil || decoder.Decode(&output) != nil || output.Serial != tc.selected || decoder.Decode(&struct{}{}) != io.EOF {
					t.Fatalf("stdout is not one selected JSON result: %q err=%v", stdout, err)
				}
				if listCalls != 1 || len(targets) != 1 || targets[0] != tc.selected {
					t.Fatalf("directory=%d targets=%v want=%s", listCalls, targets, tc.selected)
				}
				if len(tc.devices) > 1 {
					for _, text := range []string{"2 configured devices", "using " + tc.selected, "--device SERIAL", "INFO"} {
						if !strings.Contains(stderr.String(), text) {
							t.Fatalf("selection notice lacks %q: %q", text, stderr.String())
						}
					}
					if strings.Contains(stderr.String(), "\x1b[") || strings.Count(stderr.String(), "configured devices") != 1 {
						t.Fatalf("notice must be plain and emitted once: %q", stderr.String())
					}
				} else if stderr.Len() != 0 {
					t.Fatalf("single device emitted a multi-device notice: %q", stderr.String())
				}
			})
		}
	}
}

func TestControlPreferredSelectionDoesNotListOrSubstituteDevices(t *testing.T) {
	for _, tc := range []struct {
		name, config, env, flag, explicit, selected string
	}{
		{"config", "20000001", "", "", "", "20000001"},
		{"env-over-config", "20000001", "20000002", "", "", "20000002"},
		{"flag-over-env", "20000001", "20000002", "20000003", "", "20000003"},
		{"positional-over-flag", "20000001", "20000002", "20000003", "20000004", "20000004"},
	} {
		for _, verb := range []string{"state", "run"} {
			t.Run(tc.name+"/"+verb, func(t *testing.T) {
				profileForTest(t, "device: \""+tc.config+"\"\n")
				t.Setenv("NB1_DEVICE", tc.env)
				var calls int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path == "/v1/devices" {
						t.Error("preferred target queried the directory")
					}
					if verb == "state" && r.URL.Path != "/v1/devices/"+tc.selected {
						t.Errorf("wrong target %s", r.URL.Path)
					}
					if verb == "run" {
						var input struct{ Serial string }
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Serial != tc.selected {
							t.Errorf("wrong submission %+v %v", input, err)
						}
					}
					sequenceControlJSON(w, 503, map[string]any{"error": map[string]string{"code": "unavailable", "message": "device offline"}})
				}))
				defer server.Close()
				args := []string{verb}
				if tc.explicit != "" {
					args = append(args, tc.explicit)
				}
				if verb == "run" {
					args = append(args, sequenceControlFile(t, sequenceControlInput))
				}
				if tc.flag != "" {
					args = append(args, "--device", tc.flag)
				}
				var stderr bytes.Buffer
				stdout, err := executeControlSelection(t, server.URL, &stderr, args...)
				if err == nil || !strings.Contains(err.Error(), "device offline") || stdout != "" || stderr.Len() != 0 || calls != 1 {
					t.Fatalf("preferred offline: stdout=%q stderr=%q calls=%d err=%v", stdout, stderr.String(), calls, err)
				}
			})
		}
	}
}

func TestControlAutomaticSelectionDoesNotRetryAnotherDevice(t *testing.T) {
	for _, tc := range []struct {
		name, code, message string
		status              int
	}{
		{"busy", "device_busy", "device busy", http.StatusConflict},
		{"went-offline", "unavailable", "device offline", http.StatusServiceUnavailable},
		{"no-longer-configured", "not_found", "device not found", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profileForTest(t, "{}\n")
			var requests, posts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method == "GET" && r.URL.Path == "/v1/devices" {
					sequenceControlJSON(w, 200, []control.DeviceState{
						{DeviceInfo: control.DeviceInfo{Serial: "20000002"}, Connection: "online"},
						{DeviceInfo: control.DeviceInfo{Serial: "20000001"}, Connection: "online", ActiveJob: "j_existing"},
					})
					return
				}
				posts++
				var input struct{ Serial string }
				if r.Method != "POST" || r.URL.Path != "/v1/jobs" || json.NewDecoder(r.Body).Decode(&input) != nil || input.Serial != "20000001" {
					t.Errorf("selection changed after lookup: %s %s %+v", r.Method, r.URL.Path, input)
				}
				sequenceControlJSON(w, tc.status, map[string]any{"error": map[string]string{"code": tc.code, "message": tc.message}})
			}))
			defer server.Close()
			var stderr bytes.Buffer
			stdout, err := executeControlSelection(t, server.URL, &stderr, "run", sequenceControlFile(t, sequenceControlInput))
			if err == nil || !strings.Contains(err.Error(), tc.message) || stdout != "" || requests != 2 || posts != 1 || strings.Count(stderr.String(), "configured devices") != 1 {
				t.Fatalf("rejected target changed or was retried: stdout=%q stderr=%q err=%v HTTP=%d POST=%d", stdout, stderr.String(), err, requests, posts)
			}
		})
	}
}

type selectionNoticeWriter struct {
	err   error
	short bool
	calls int
}

func (w *selectionNoticeWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.short {
		return len(data) - 1, nil
	}
	return 0, w.err
}

func TestControlSelectionNoticeFailurePreventsTargetRequest(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "write-error", true: "short-write"}[short], func(t *testing.T) {
			profileForTest(t, "{}\n")
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/devices" {
					t.Errorf("request after failed notice: %s %s", r.Method, r.URL.Path)
				}
				sequenceControlJSON(w, 200, []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "20000002"}}, {DeviceInfo: control.DeviceInfo{Serial: "20000001"}}})
			}))
			defer server.Close()
			want := errors.New("selection output failed")
			if short {
				want = io.ErrShortWrite
			}
			stderr := &selectionNoticeWriter{err: want, short: short}
			stdout, err := executeControlSelection(t, server.URL, stderr, "run", sequenceControlFile(t, sequenceControlInput))
			if !errors.Is(err, want) || calls != 1 || stderr.calls != 1 || stdout != "" {
				t.Fatalf("notice failure repeated or admitted: stdout=%q err=%v HTTP=%d writes=%d", stdout, err, calls, stderr.calls)
			}
		})
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
