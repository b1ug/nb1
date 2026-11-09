package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

func compilerPreview(t *testing.T) (httpapi.PreviewRequest, control.PreviewResult) {
	t.Helper()
	cycles, period := 2, 800
	effect := control.Effect{Kind: "pulse", Color: "#00a000", Cycles: &cycles, PeriodMS: &period}
	result, err := control.Preview(nil, &effect)
	if err != nil {
		t.Fatal(err)
	}
	return httpapi.PreviewRequest{Effect: &effect}, result
}

func compilerJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCompilerClientUsesReadOnlyRoutes(t *testing.T) {
	input, expected := compilerPreview(t)
	serial := "test-device_1"
	input.Serial = &serial
	expected = control.WithDeviceSnapshot(expected, serial, nil)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/v1/capabilities":
			if r.Method != "GET" || r.URL.RawQuery != "" || r.ContentLength > 0 {
				t.Errorf("catalog request: %s %s content=%d", r.Method, r.URL, r.ContentLength)
			}
			_ = json.NewEncoder(w).Encode(control.Capabilities())
		case "/v1/programs/preview":
			if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("preview request: %s %s headers=%v", r.Method, r.URL, r.Header)
			}
			var actual map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
				t.Error(err)
			}
			if len(actual) != 2 || string(actual["serial"]) != `"test-device_1"` || len(actual["effect"]) == 0 {
				t.Errorf("preview envelope must preserve effect/serial without policy or receipts: %s", actual)
			}
			_ = json.NewEncoder(w).Encode(expected)
		default:
			t.Errorf("unexpected execution route: %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	catalog, err := client.Capabilities(context.Background())
	if err != nil || !reflect.DeepEqual(catalog, control.Capabilities()) {
		t.Fatalf("catalog: %#v %v", catalog, err)
	}
	result, err := client.Preview(context.Background(), input)
	if err != nil || !reflect.DeepEqual(result, expected) || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("preview: %#v %v calls=%d", result, err, calls)
	}
}

func TestCompilerClientRequiresExactStatusAndStrictResponse(t *testing.T) {
	input, preview := compilerPreview(t)
	previewJSON, catalogJSON := compilerJSON(t, preview), compilerJSON(t, control.Capabilities())
	tests := []struct {
		name, route, body, contentType string
		status                         int
	}{
		{"preview202", "preview", previewJSON, "application/json", 202},
		{"catalog202", "catalog", catalogJSON, "application/json", 202},
		{"preview204", "preview", "", "application/json", 204},
		{"wrongContentType", "preview", previewJSON, "text/plain", 200},
		{"missingContentType", "catalog", catalogJSON, "", 200},
		{"unknownField", "preview", strings.TrimSuffix(previewJSON, "}") + `,"job":{}}`, "application/json", 200},
		{"noncanonicalCase", "preview", strings.Replace(previewJSON, `"schema_version"`, `"Schema_version"`, 1), "application/json", 200},
		{"duplicateEscaped", "preview", strings.TrimSuffix(previewJSON, "}") + `,"schema_\u0076ersion":1}`, "application/json", 200},
		{"null", "preview", "null", "application/json", 200},
		{"trailing", "preview", previewJSON + "{}", "application/json", 200},
		{"wrongDuration", "preview", strings.Replace(previewJSON, `"nominal_duration_ms":1600`, `"nominal_duration_ms":1`, 1), "application/json", 200},
		{"wrongSteps", "preview", strings.Replace(previewJSON, `"total_steps":6`, `"total_steps":5`, 1), "application/json", 200},
		{"wrongInputKind", "preview", strings.Replace(previewJSON, `"input_kind":"effect"`, `"input_kind":"program"`, 1), "application/json", 200},
		{"noncanonicalOmission", "preview", strings.Replace(previewJSON, `"device":"not_requested"`, `"device":"not_requested","reason":""`, 1), "application/json", 200},
		{"missingZeroLED", "preview", strings.Replace(previewJSON, `"led":0,`, ``, 1), "application/json", 200},
		{"noncanonicalProgram", "preview", strings.ReplaceAll(previewJSON, "#00a000", "#00A000"), "application/json", 200},
		{"unknownContract", "catalog", strings.Replace(catalogJSON, "nb1-program-effect/v1", "future/v2", 1), "application/json", 200},
		{"missingFalseField", "catalog", strings.Replace(catalogJSON, `"pattern_library":false,`, ``, 1), "application/json", 200},
		{"missingFalseUpload", "catalog", strings.Replace(catalogJSON, `"hardware_pattern_upload":false,`, ``, 1), "application/json", 200},
		{"oversized", "preview", previewJSON + strings.Repeat(" ", 2<<20), "application/json", 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if tc.route == "preview" {
				var out control.PreviewResult
				out, err = client.Preview(context.Background(), input)
				if !reflect.DeepEqual(out, control.PreviewResult{}) {
					t.Fatalf("invalid response exposed as preview: %#v", out)
				}
			} else {
				var out control.CapabilityCatalog
				out, err = client.Capabilities(context.Background())
				if !reflect.DeepEqual(out, control.CapabilityCatalog{}) {
					t.Fatalf("invalid response exposed as catalog: %#v", out)
				}
			}
			if err == nil || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("invalid response accepted or retried: %v calls=%d", err, calls)
			}
		})
	}
}

func TestCompilerClientOlderDaemonIsUnsupported(t *testing.T) {
	input, _ := compilerPreview(t)
	for _, status := range []int{404, 501} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("old daemon"))
		}))
		client, err := New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []func() error{
			func() error { _, err := client.Capabilities(context.Background()); return err },
			func() error { _, err := client.Preview(context.Background(), input); return err },
		} {
			err := call()
			var failure *Error
			if !errors.As(err, &failure) || failure.Status != status || !strings.HasSuffix(failure.Code, "_unsupported") || !strings.Contains(err.Error(), "no direct fallback") {
				t.Fatalf("unsupported response: %v", err)
			}
		}
		client.Close()
		server.Close()
	}
}

func TestCompilerClientPreviewInputGuardAndCallerDeadline(t *testing.T) {
	input, _ := compilerPreview(t)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invalidSerial := "../not-a-device"
	for _, invalid := range []httpapi.PreviewRequest{{}, {Program: &control.Program{}, Effect: input.Effect}, {Effect: input.Effect, Serial: &invalidSerial}} {
		if _, err := client.Preview(context.Background(), invalid); err == nil {
			t.Fatal("invalid preview input accepted")
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("input guard contacted daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.Preview(ctx, input)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("caller deadline not preserved or request retried: %v calls=%d", err, calls)
	}
}

func TestCompilerClientValidatesRequestedDeviceSnapshot(t *testing.T) {
	input, result := compilerPreview(t)
	serial := "requested"
	input.Serial = &serial
	// A valid program alone must not confirm an omitted or different target.
	for _, response := range []control.PreviewResult{result, control.WithDeviceSnapshot(result, "other", nil)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		}))
		client, err := New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Preview(context.Background(), input); err == nil {
			t.Fatal("unbound device snapshot accepted")
		}
		client.Close()
		server.Close()
	}
}
