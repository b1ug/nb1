package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
)

type fakeService struct {
	submitCount int
	fail        error
}

func (s *fakeService) Devices() []control.DeviceState {
	return []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "20005601"}, Connection: "online"}}
}
func (s *fakeService) Device(serial string) (control.DeviceState, error) {
	if s.fail != nil {
		return control.DeviceState{}, s.fail
	}
	if serial != "20005601" {
		return control.DeviceState{}, control.ErrNotFound
	}
	return s.Devices()[0], nil
}
func (s *fakeService) Submit(_ context.Context, serial string, p control.Program, policy string) (control.Job, error) {
	s.submitCount++
	if s.fail != nil {
		return control.Job{}, s.fail
	}
	return control.Job{ID: "job-1", Device: serial, Status: "queued", Program: p}, nil
}
func (s *fakeService) Job(id string) (control.Job, error) {
	if s.fail != nil {
		return control.Job{}, s.fail
	}
	return control.Job{ID: id, Device: "20005601", Status: "running"}, nil
}
func (s *fakeService) Cancel(_ context.Context, id string) (control.Job, error) {
	if s.fail != nil {
		return control.Job{}, s.fail
	}
	return control.Job{ID: id, Device: "20005601", Status: "cancelling"}, nil
}

func TestClientAndHTTPService(t *testing.T) {
	service := &fakeService{}
	server := httptest.NewServer(httpapi.New(service))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	devices, err := client.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Serial != "20005601" {
		t.Fatalf("devices: %v %v", devices, err)
	}
	device, err := client.Device(ctx, "20005601")
	if err != nil || device.Connection != "online" {
		t.Fatalf("device: %v %v", device, err)
	}
	p := control.Program{Steps: []control.Step{{LED: 1, Color: "#ff0000", HoldMS: 20}}, Repeat: 1, OnFinish: "off"}
	job, err := client.Submit(ctx, "20005601", p, "replace")
	if err != nil || job.Status != "queued" || job.Program.Steps[0].LED != 1 {
		t.Fatalf("submit: %v %v", job, err)
	}
	job, err = client.Job(ctx, job.ID)
	if err != nil || job.Status != "running" {
		t.Fatalf("job: %v %v", job, err)
	}
	job, err = client.Cancel(ctx, job.ID)
	if err != nil || job.Status != "cancelling" {
		t.Fatalf("cancel must preserve nonterminal status: %v %v", job, err)
	}
	if service.submitCount != 1 {
		t.Fatalf("submit calls: %d", service.submitCount)
	}
}

func TestStrictRequestBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
	}{
		{"unknown envelope", `{"program":{},"raw_usb":true}`, "application/json", 400},
		{"unknown step", `{"program":{"steps":[{"color":"#ff0000","raw":true}]}}`, "application/json", 400},
		{"trailing object", `{"program":{}} {}`, "application/json", 400},
		{"trailing malformed", `{"program":{}} x`, "application/json", 400},
		{"null", `null`, "application/json", 400},
		{"array", `[]`, "application/json", 400},
		{"form", `program=test`, "application/x-www-form-urlencoded", 415},
		{"too large", `{"program":{}}` + strings.Repeat(" ", httpapi.MaxBodyBytes), "application/json", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeService{}
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8931/v1/devices/20005601/jobs", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.kind)
			w := httptest.NewRecorder()
			httpapi.New(service).ServeHTTP(w, r)
			if w.Code != tc.status || service.submitCount != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, service.submitCount, w.Body)
			}
		})
	}
}

func TestErrorCodesAndMethods(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{control.ErrInvalid, 400}, {control.ErrNotFound, 404}, {fmt.Errorf("wrapped: %w", control.ErrBusy), 409}, {control.ErrQueueFull, 409}, {control.ErrOffline, 503}, {control.ErrClosed, 503}, {fmt.Errorf("backend failed"), 500},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/devices/20005601", nil)
			w := httptest.NewRecorder()
			httpapi.New(&fakeService{fail: tc.err}).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
		})
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"POST", "/healthz", 405}, {"DELETE", "/v1/devices", 405}, {"GET", "/v1/jobs/id/cancel", 405}, {"GET", "/v1/unknown", 404}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil)
		w := httptest.NewRecorder()
		httpapi.New(&fakeService{}).ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestLocalBrowserBoundary(t *testing.T) {
	for _, tc := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:8931", "", 200}, {"localhost:8931", "http://localhost:8931", 200}, {"[::1]:8931", "", 200},
		{"attacker.example:8931", "", 403}, {"127.0.0.1:8931", "http://attacker.example", 403}, {"127.0.0.1:8931", "http://127.0.0.1:4173", 403}, {"127.0.0.1:8931", "null", 403}, {"127.0.0.1:8931", "https://127.0.0.1:8931", 403},
	} {
		t.Run(tc.host+tc.origin, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1/healthz", nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			httpapi.New(&fakeService{}).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d body=%s", w.Code, w.Body)
			}
			var body interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBrowserPresence(t *testing.T) {
	// A nil service makes any accidental device lookup fail the test.
	h := httpapi.New(nil, httpapi.WithSequenceAPI())
	for _, origin := range []string{
		"", "https://b1ug.github.io", "http://127.0.0.1:8931", "http://localhost",
		"https://localhost:4173", "http://127.0.0.2", "http://[::1]", "http://[::1]:65535",
	} {
		t.Run("read "+origin, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:8931/v1/presence", nil)
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var marker map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &marker); err != nil || len(marker) != 2 ||
				marker["service"] != "nb1" || marker["api_version"] != "v1" || w.Code != 200 {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "" ||
				w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Vary") != "Origin" {
				t.Fatalf("headers %v", w.Header())
			}
		})
	}
	for _, origin := range []string{
		"null", "https://example.com", "http://b1ug.github.io", "https://b1ug.github.io:444",
		"https://b1ug.github.io.evil.example", "http://localhost.example", "http://192.168.1.2",
		"http://0.0.0.0", "https://name@localhost", "http://evil.example@localhost",
		"http://localhost/", "http://localhost?", "http://localhost#", "http://localhost:0",
		"http://localhost:", "http://localhost:65536", "http://localhost:bad", "file://localhost",
		"http://[localhost]", "http://[::1%25zone]", "http://localhost http://127.0.0.1",
	} {
		t.Run("deny "+origin, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1/v1/presence", nil)
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("got %d %v", w.Code, w.Header())
			}
		})
	}
	for _, tc := range []struct {
		method, host, path, body string
		want                     int
	}{
		{"GET", "remote.example", "/v1/presence", "", 403},
		{"GET", "localhost", "/v1/presence?device=test", "", 400},
		{"GET", "localhost", "/v1/presence", "{}", 400},
		{"HEAD", "localhost", "/v1/presence", "", 405},
		{"POST", "localhost", "/v1/presence", "", 405},
		{"OPTIONS", "localhost", "/v1/presence", "", 405},
		{"GET", "localhost", "/v1/presence/", "", 403},
		{"GET", "localhost", "/v1/%70resence", "", 403},
		{"GET", "localhost", "/v1/devices", "", 403},
		{"POST", "localhost", "/v1/jobs", "{}", 403},
		{"GET", "localhost", "/healthz", "", 403},
	} {
		t.Run(tc.method+" "+tc.host+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", "https://b1ug.github.io")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("got %d %v", w.Code, w.Header())
			}
		})
	}
	t.Run("duplicate origins", func(t *testing.T) {
		r := httptest.NewRequest("GET", "http://localhost/v1/presence", nil)
		r.Header.Add("Origin", "https://b1ug.github.io")
		r.Header.Add("Origin", "https://other.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("got %d %v", w.Code, w.Header())
		}
	})
}

// Any accidental call other than Devices fails through the nil embedded service.
type cachedDeviceService struct {
	control.Service
	states []control.DeviceState
	reads  int
}

func (s *cachedDeviceService) Devices() []control.DeviceState {
	s.reads++
	return s.states
}

func TestBrowserServiceStatus(t *testing.T) {
	mixed := &cachedDeviceService{}
	for _, state := range []string{"online", "online", "connecting", "offline", "busy", "error", "closing", "closed"} {
		mixed.states = append(mixed.states, control.DeviceState{
			DeviceInfo: control.DeviceInfo{Serial: "private-id", Path: "private-path"},
			Connection: state, ActiveJob: "private-job", LastError: "private-error",
		})
	}
	for _, tc := range []struct {
		name    string
		cache   *cachedDeviceService
		devices any
	}{
		{"mixed states", mixed, map[string]any{"configured": float64(8), "online": float64(2)}},
		{"empty snapshot", &cachedDeviceService{states: []control.DeviceState{}}, map[string]any{"configured": float64(0), "online": float64(0)}},
		{"missing snapshot", &cachedDeviceService{}, nil},
		{"missing service", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var service control.Service
			if tc.cache != nil {
				service = tc.cache
			}
			h := httpapi.New(service, httpapi.WithSequenceAPI())
			r := httptest.NewRequest("GET", "http://localhost/v1/service-status", nil)
			r.Header.Set("Origin", "https://b1ug.github.io")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var got map[string]any
			want := map[string]any{"service": "nb1", "api_version": "v1", "health": "ok", "devices": tc.devices}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, want) || w.Code != 200 {
				t.Fatalf("got %d %s, want %+v", w.Code, w.Body.String(), want)
			}
			if tc.cache != nil && tc.cache.reads != 1 {
				t.Fatalf("read cache %d times", tc.cache.reads)
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "https://b1ug.github.io" || w.Header().Get("Access-Control-Allow-Credentials") != "" || w.Header().Get("Vary") != "Origin" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("headers %v", w.Header())
			}
		})
	}
	t.Run("only an allowed status GET reads the cache", func(t *testing.T) {
		cache := &cachedDeviceService{}
		h := httpapi.New(cache)
		for _, tc := range []struct {
			method, url, origin, body string
			code                      int
		}{
			{"GET", "http://localhost/v1/presence", "https://b1ug.github.io", "", 200},
			{"GET", "http://localhost/v1/service-status", "https://example.com", "", 403},
			{"GET", "http://example.com/v1/service-status", "https://b1ug.github.io", "", 403},
			{"GET", "http://localhost/v1/service-status?query=1", "http://localhost:4173", "", 400},
			{"GET", "http://localhost/v1/service-status", "http://localhost:4173", "{}", 400},
			{"OPTIONS", "http://localhost/v1/service-status", "https://b1ug.github.io", "", 405},
			{"POST", "http://localhost/v1/service-status", "https://b1ug.github.io", "", 405},
			{"GET", "http://localhost/v1/%73ervice-status", "https://b1ug.github.io", "", 403},
		} {
			r := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code || (w.Code != 200 && w.Header().Get("Access-Control-Allow-Origin") != "") || cache.reads != 0 {
				t.Errorf("%s %s from %s: status=%d, headers=%v, cache reads=%d", tc.method, tc.url, tc.origin, w.Code, w.Header(), cache.reads)
			}
		}
	})
}
