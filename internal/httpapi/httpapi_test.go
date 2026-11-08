package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
