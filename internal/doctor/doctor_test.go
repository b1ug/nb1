package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/b1ug/nb1/internal/control"
)

var testTime = time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)

func testBuild() Build {
	return buildFromInfo(&debug.BuildInfo{GoVersion: "go1.18.10", Main: debug.Module{Path: "github.com/b1ug/nb1", Version: "(devel)"}}, true, true)
}

func onlineState(serial string) control.DeviceState {
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial, Firmware: 204}, Connection: "online", Observed: &control.Observation{
		LED1: "#000000", LED2: "#000000", SampledAt: testTime.Add(-time.Second),
	}}
}

func fakeDaemon(t *testing.T, health string, devices interface{}) (*httptest.Server, *int32) {
	t.Helper()
	deviceReads := new(int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Body != nil && r.ContentLength > 0 {
			t.Errorf("doctor attempted a non-read-only request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			io.WriteString(w, health)
		case "/v1/devices":
			atomic.AddInt32(deviceReads, 1)
			if raw, ok := devices.(string); ok {
				io.WriteString(w, raw)
			} else {
				json.NewEncoder(w).Encode(devices)
			}
		default:
			t.Errorf("doctor requested unexpected endpoint %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, deviceReads
}

func TestConfiguredTargetsAndReadOnlyRequests(t *testing.T) {
	first, second := onlineState("a"), onlineState("b")
	second.ActiveJob, second.QueueDepth = "job_existing", 2
	server, deviceReads := fakeDaemon(t, `{"status":"ok","api_version":"v1"}`, []control.DeviceState{second, first})
	report, err := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.Selection != "daemon_configured" || len(report.Devices) != 2 || report.Devices[0].Serial != "a" || report.Devices[1].QueueDepth != 2 || report.Devices[1].ActiveJob != "job_existing" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Daemon.HTTPResponse != "received" || report.Daemon.APIVersion != "v1" || report.Devices[0].Observed.State != "fresh" || *report.Devices[0].Observed.AgeMS != 1000 || atomic.LoadInt32(deviceReads) != 1 {
		t.Fatalf("health/observation not confirmed: %+v", report)
	}
	// A client built without native HID support can still diagnose the daemon;
	// this build capability must not be confused with device connectivity.
	build := testBuild()
	build.CGOEnabled, build.NativeHIDSupported = false, false
	report, err = collect(context.Background(), Options{Endpoint: server.URL, Serial: "b"}, build, func() time.Time { return testTime })
	if err != nil || report.Status != "ok" || len(report.Devices) != 1 || report.Devices[0].Serial != "b" || report.Selection != "explicit_serial" {
		t.Fatalf("explicit serial/HTTP-only build: %+v %v", report, err)
	}
}

func TestDeviceProblemsAreDistinctFromHTTPHealth(t *testing.T) {
	for _, test := range []struct{ connection, code string }{
		{"offline", "device_offline"}, {"busy", "device_busy"}, {"connecting", "device_connecting"}, {"error", "device_error"},
		{"closing", "device_closing"}, {"closed", "device_closed"}, {"future-state", "device_state_unknown"},
	} {
		t.Run(test.connection, func(t *testing.T) {
			state := onlineState("target")
			state.Connection, state.LastError = test.connection, "example backend message"
			server, _ := fakeDaemon(t, `{"status":"ok","api_version":"v1"}`, []control.DeviceState{state})
			report, err := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
			if err != nil || report.Status != "attention" || report.Daemon.State != "ready" || report.Issues[0].Code != test.code || report.Devices[0].LastError != state.LastError {
				t.Fatalf("HTTP/device health conflated: %+v %v", report, err)
			}
			if test.connection == "busy" && !strings.Contains(report.Issues[0].Advice, "do not delete it to take over the device") {
				t.Fatal("busy advice must preserve the cooperative lock file")
			}
			var text bytes.Buffer
			if err := WriteText(&text, report); err != nil || strings.ContainsFunc(text.String(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Fatalf("default diagnostics are not English: %s %v", text.String(), err)
			}
		})
	}
}

func TestSnapshotFreshnessAndMissingTargets(t *testing.T) {
	for _, test := range []struct {
		name, code string
		observed   *control.Observation
	}{
		{"missing", "observation_missing", nil},
		{"zero", "observation_missing", &control.Observation{}},
		{"stale", "observation_stale", &control.Observation{SampledAt: testTime.Add(-6 * time.Second)}},
		{"future", "observation_clock_skew", &control.Observation{SampledAt: testTime.Add(2 * time.Second)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := onlineState("configured")
			state.Observed = test.observed
			server, _ := fakeDaemon(t, `{"status":"ok","api_version":"v1"}`, []control.DeviceState{state})
			report, err := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
			if err != nil || report.Status != "attention" || report.Issues[0].Code != test.code {
				t.Fatalf("snapshot problem not diagnosed: %+v %v", report, err)
			}
			report, err = collect(context.Background(), Options{Endpoint: server.URL, Serial: "absent"}, testBuild(), func() time.Time { return testTime })
			if err != nil || report.Issues[0].Code != "target_not_configured" || len(report.Devices) != 0 {
				t.Fatalf("selected an unintended device: %+v %v", report, err)
			}
		})
	}
	if got := snapshot(&control.Observation{SampledAt: testTime.Add(500 * time.Millisecond)}, testTime); got.State != "fresh" || *got.AgeMS != 0 {
		t.Fatalf("subsecond clock adjustment: %+v", got)
	}
	server, _ := fakeDaemon(t, `{"status":"ok","api_version":"v1"}`, []control.DeviceState{})
	report, _ := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
	if report.Issues[0].Code != "no_configured_devices" {
		t.Fatalf("empty targets: %+v", report)
	}
}

func TestHealthVersionAndInvalidDeviceResponses(t *testing.T) {
	for _, test := range []struct{ name, health, devices, state, code string }{
		{"version", `{"status":"ok","api_version":"v2"}`, "[]", "incompatible", "api_version_mismatch"},
		{"missing_version", `{"status":"ok"}`, "[]", "incompatible", "api_version_mismatch"},
		{"unhealthy", `{"status":"bad","api_version":"v1"}`, "[]", "unhealthy", "daemon_unhealthy"},
		{"bad_health_json", `not json`, "[]", "unavailable", "daemon_unavailable"},
		{"null_devices", `{"status":"ok","api_version":"v1"}`, "null", "devices_error", "device_list_unavailable"},
		{"bad_devices", `{"status":"ok","api_version":"v1"}`, "{}", "devices_error", "device_list_unavailable"},
		{"bad_serial", `{"status":"ok","api_version":"v1"}`, `[{"serial":"bad/serial","connection":"online"}]`, "devices_error", "invalid_device_state"},
		{"duplicate", `{"status":"ok","api_version":"v1"}`, `[{"serial":"a"},{"serial":"a"}]`, "devices_error", "invalid_device_state"},
		{"negative_queue", `{"status":"ok","api_version":"v1"}`, `[{"serial":"a","queue_depth":-1}]`, "devices_error", "invalid_device_state"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, reads := fakeDaemon(t, test.health, test.devices)
			report, err := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
			if err != nil || report.Status != "attention" || report.Daemon.State != test.state || report.Issues[0].Code != test.code {
				t.Fatalf("unexpected response diagnosis: %+v %v", report, err)
			}
			if test.state != "devices_error" && atomic.LoadInt32(reads) != 0 {
				t.Fatal("device API was queried after health/version failed")
			}
			if report.Daemon.HTTPResponse != "received" {
				t.Fatalf("HTTP responses, including invalid JSON, must be distinguished from transport failure: %+v", report.Daemon)
			}
		})
	}
}

func TestBudgetIncludesUnresponsiveHealthAndDeviceRead(t *testing.T) {
	for _, slowPath := range []string{"/healthz", "/v1/devices"} {
		t.Run(slowPath, func(t *testing.T) {
			var requests int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requests, 1)
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation: %s", r.Method)
				}
				if r.URL.Path == slowPath {
					<-r.Context().Done()
					return
				}
				io.WriteString(w, `{"status":"ok","api_version":"v1"}`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			report, err := collect(ctx, Options{Endpoint: server.URL, Serial: "target"}, testBuild(), func() time.Time { return testTime })
			if err != nil || report.Status != "attention" || time.Since(started) > time.Second {
				t.Fatalf("unbounded probe: %+v %v", report, err)
			}
			expected := int32(1)
			if slowPath == "/v1/devices" {
				expected = 2
			}
			if atomic.LoadInt32(&requests) != expected {
				t.Fatalf("unexpected retry count: %d", requests)
			}
		})
	}
}

func TestUnavailableEndpointDoesNotAssertHardwareFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()
	report, err := collect(context.Background(), Options{Endpoint: endpoint, Serial: "target"}, testBuild(), func() time.Time { return testTime })
	if err != nil || report.Issues[0].Code != "daemon_unavailable" || len(report.Devices) != 0 || report.Daemon.HTTPResponse != "unconfirmed" || !strings.Contains(report.Issues[0].Advice, "nb1 server --devices target") || !strings.Contains(report.Issues[0].Message, "does not establish that the hardware") {
		t.Fatalf("unavailable service misdiagnosed: %+v %v", report, err)
	}
}

func TestRejectedOptionsDoNotEchoCredentials(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "http://user:secret@localhost", "http://user:secret@bad%host", "https://localhost", "http://localhost/path"} {
		_, err := collect(context.Background(), Options{Endpoint: endpoint}, testBuild(), func() time.Time { return testTime })
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid/private endpoint: %v", err)
		}
	}
	for _, serial := range []string{"bad/serial", " ", strings.Repeat("a", 129), "target\n"} {
		if _, err := collect(context.Background(), Options{Serial: serial}, testBuild(), func() time.Time { return testTime }); err == nil {
			t.Fatalf("accepted invalid serial %q", serial)
		}
	}
}

func TestBuildMetadataReflectsResolvedModulesWithoutLocalPaths(t *testing.T) {
	info := &debug.BuildInfo{GoVersion: "go1.18.10", Main: debug.Module{Path: "github.com/b1ug/nb1", Version: "(devel)"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}, {Key: "CGO_CFLAGS", Value: "PRIVATE_SECRET"}},
		Deps: []*debug.Module{
			{Path: "github.com/b1ug/blink1-go", Version: "v0.0.1", Replace: &debug.Module{Path: "/Users/private/PRIVATE_SECRET"}},
			{Path: "github.com/b1ug/gid", Version: "v0.0.2", Replace: &debug.Module{Path: "github.com/b1ug/gid", Version: "v0.0.3-candidate"}},
		}}
	build := buildFromInfo(info, true, true)
	if build.Revision != "abc123" || build.Modified != "true" || build.Dependencies[0].Source != "local_unversioned" || build.Dependencies[0].Version != "" || build.Dependencies[0].RequestedVersion != "v0.0.1" || build.Dependencies[1].Version != "v0.0.3-candidate" {
		t.Fatalf("misrepresented built modules: %+v", build)
	}
	encoded, err := json.Marshal(build)
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE_SECRET")) {
		t.Fatalf("build settings/local paths leaked: %s %v", encoded, err)
	}
	if got := buildFromInfo(nil, false, false); got.InfoAvailable || got.Dependencies[0].Source != "unavailable" {
		t.Fatalf("missing build info invented a version: %+v", got)
	}
}

func TestReportJSONAndTextOnAttention(t *testing.T) {
	server, _ := fakeDaemon(t, `{"status":"ok","api_version":"v1"}`, []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "offline"}, Connection: "offline"}})
	report, err := collect(context.Background(), Options{Endpoint: server.URL}, testBuild(), func() time.Time { return testTime })
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["schema_version"] != float64(1) || parsed["status"] != "attention" || parsed["captured_at"] != testTime.Format(time.RFC3339) {
		t.Fatalf("versioned report contract: %s", encoded)
	}
	device := parsed["devices"].([]interface{})[0].(map[string]interface{})
	observed := device["observed"].(map[string]interface{})
	if observed["sampled_at"] != nil || observed["age_ms"] != nil || device["queue_depth"] != float64(0) || parsed["issues"] == nil {
		t.Fatalf("missing values/empty arrays lost stable types: %s", encoded)
	}
	var text bytes.Buffer
	if err := WriteText(&text, report); err != nil || !strings.Contains(text.String(), "An HTTP response verifies only the service") || !strings.Contains(text.String(), "device_offline") || !strings.Contains(text.String(), "read-only diagnostics") {
		t.Fatalf("text report missing actionable distinction: %s %v", text.String(), err)
	}
}

func TestModuleLabelsUseEnglishAndPreserveRequestedValues(t *testing.T) {
	for _, test := range []struct {
		module Module
		want   string
	}{
		{Module{Source: "unavailable"}, "build information unavailable"},
		{Module{Source: "local_unversioned", RequestedVersion: "v0.0.1"}, "local workspace/replacement, actual version unversioned (requested=v0.0.1)"},
		{Module{Source: "replacement", Version: "v0.0.2", RequestedVersion: "v0.0.1"}, "v0.0.2 (replacement, requested=v0.0.1)"},
	} {
		if got := moduleLabel(test.module); got != test.want {
			t.Fatalf("module label changed identity or language: got %q, want %q", got, test.want)
		}
	}
}
