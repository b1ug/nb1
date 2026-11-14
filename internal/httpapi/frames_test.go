package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

const frameHTTPInstance = "daemon_11111111111111111111111111111111"
const frameHTTPRequest = "req_60000_22222222222222222222222222222222"
const frameHTTPJob = "framejob_33333333333333333333333333333333"

type frameHTTPSpy struct {
	fakeService
	frameCalls, deviceCalls, lookupCalls, cancelCalls int
	program                                           control.FrameProgram
	binding                                           control.ReceiptRequest
	policy                                            string
	frameErr                                          error
	connection                                        string
	missingCaps                                       bool
}

func (s *frameHTTPSpy) Instance() control.InstanceInfo {
	return control.InstanceInfo{DaemonInstanceID: frameHTTPInstance}
}
func (s *frameHTTPSpy) Device(serial string) (control.DeviceState, error) {
	s.deviceCalls++
	if serial != "20005601" {
		return control.DeviceState{}, control.ErrNotFound
	}
	connection := s.connection
	if connection == "" {
		connection = "online"
	}
	caps := []string{"set", "raw-rgb", "mk2-basic"}
	if s.missingCaps {
		caps = []string{"set"}
	}
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial, Capabilities: caps}, Connection: connection}, nil
}
func (s *frameHTTPSpy) snapshot() control.FrameJob {
	preview, _ := control.PreviewFrameProgram(s.program)
	return control.FrameJob{ID: frameHTTPJob, Device: "20005601", Status: "queued", Program: s.program, TotalFrames: preview.TotalFrames, PlannedTargetWrites: preview.PlannedTargetWrites, CreatedAt: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
}
func (s *frameHTTPSpy) SubmitFrameWithReceipt(_ context.Context, serial string, p control.FrameProgram, policy string, binding control.ReceiptRequest) (control.FrameReceiptResult, error) {
	s.frameCalls++
	s.program = p
	s.policy = policy
	s.binding = binding
	if s.frameErr != nil {
		return control.FrameReceiptResult{}, s.frameErr
	}
	return control.FrameReceiptResult{Job: s.snapshot(), DaemonInstanceID: frameHTTPInstance}, nil
}
func (s *frameHTTPSpy) FrameJob(id string) (control.FrameJob, error) {
	s.lookupCalls++
	if id != frameHTTPJob {
		return control.FrameJob{}, control.ErrNotFound
	}
	return s.snapshot(), s.frameErr
}
func (s *frameHTTPSpy) FrameRequest(serial, id, instance string) (control.FrameJob, error) {
	s.lookupCalls++
	if serial != "20005601" || id != frameHTTPRequest || instance != frameHTTPInstance {
		return control.FrameJob{}, control.ErrRequestNotFound
	}
	return s.snapshot(), s.frameErr
}
func (s *frameHTTPSpy) CancelFrame(_ context.Context, id string) (control.FrameJob, error) {
	s.cancelCalls++
	job, err := s.FrameJob(id)
	job.Status = "cancelling"
	return job, err
}

func frameHTTPDo(service control.Service, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8931"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	httpapi.New(service).ServeHTTP(w, r)
	return w
}
func frameHTTPJSON(t *testing.T, value interface{}) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func frameHTTPProgram(t *testing.T) control.FrameProgram {
	t.Helper()
	result, err := lightactionv2.CompileJSON([]byte(`{"kind":"orbit","colors":["red","blue"],"segments":4,"period_ms":600,"cycles":1}`))
	if err != nil {
		t.Fatal(err)
	}
	return result.Program
}

func TestFrameHTTPPreviewIsReadOnlyAndBindsCachedDevice(t *testing.T) {
	s := &frameHTTPSpy{}
	for _, test := range []struct {
		serial, connection string
		missing            bool
		available          bool
		reason             string
	}{
		{"", "", false, false, "not_selected"}, {"20005601", "", false, true, "cached_profile_supported"},
		{"20005601", "offline", false, false, "device_not_online"}, {"20005601", "", true, false, "missing_capabilities"}, {"missing", "", false, false, "device_not_configured"},
	} {
		s.connection = test.connection
		s.missingCaps = test.missing
		input := map[string]interface{}{"program": frameHTTPProgram(t)}
		if test.serial != "" {
			input["serial"] = test.serial
		}
		w := frameHTTPDo(s, "POST", "/v1/frame-programs/preview", frameHTTPJSON(t, input))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			control.FramePreview
			Device *struct {
				Serial string `json:"serial"`
			} `json:"device"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Execution.Available != test.available || result.Execution.Reason != test.reason {
			t.Fatalf("availability %#v", result.Execution)
		}
		if test.serial != "" && (result.Device == nil || result.Device.Serial != test.serial) {
			t.Fatalf("lost preview target: %#v", result.Device)
		}
	}
	if s.frameCalls != 0 || s.submitCount != 0 || s.lookupCalls != 0 || s.cancelCalls != 0 {
		t.Fatalf("preview admitted or polled a job: %#v", s)
	}
	w := frameHTTPDo(&fakeService{}, "POST", "/v1/frame-programs/preview", frameHTTPJSON(t, map[string]interface{}{"program": frameHTTPProgram(t), "serial": "20005601"}))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "frame_service_unsupported") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestFrameHTTPHighAndLowUseSameBoundFrameAdmission(t *testing.T) {
	s := &frameHTTPSpy{}
	p := frameHTTPProgram(t)
	inputs := []struct {
		path string
		body map[string]interface{}
	}{
		{"/v1/devices/20005601/frame-jobs", map[string]interface{}{"program": p}},
		{"/v2/devices/20005601/light", map[string]interface{}{"contract": lightactionv2.Contract, "action": map[string]interface{}{"kind": "orbit", "colors": []string{"red", "blue"}, "segments": 4, "period_ms": 600, "cycles": 1}}},
	}
	for _, input := range inputs {
		input.body["policy"] = "reject"
		input.body["request_id"] = frameHTTPRequest
		input.body["expected_daemon_instance_id"] = frameHTTPInstance
		w := frameHTTPDo(s, "POST", input.path, frameHTTPJSON(t, input.body))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		if !reflect.DeepEqual(s.program, p) || s.policy != "reject" || s.binding.RequestID != frameHTTPRequest || s.binding.ExpectedDaemonInstanceID != frameHTTPInstance {
			t.Fatal("admission did not preserve the frozen program and ticket")
		}
		wantLocation := "/v1/devices/20005601/frame-requests/" + frameHTTPRequest + "?expected_daemon_instance_id=" + frameHTTPInstance
		if w.Header().Get("Location") != wantLocation || w.Header().Get("X-NB1-Daemon-Instance-ID") != frameHTTPInstance || w.Header().Get("X-NB1-Replayed") != "false" {
			t.Fatal("missing exact receipt headers", w.Header())
		}
		var job control.FrameJob
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID != frameHTTPJob || !reflect.DeepEqual(job.Program, p) {
			t.Fatal(job, err)
		}
		lookup := frameHTTPDo(s, "GET", wantLocation, "")
		if lookup.Code != 200 || lookup.Header().Get("X-NB1-Daemon-Instance-ID") != frameHTTPInstance {
			t.Fatal(lookup.Code, lookup.Body.String())
		}
	}
	if s.frameCalls != 2 || s.submitCount != 0 {
		t.Fatal("wrong admission path", s.frameCalls, s.submitCount)
	}
	w := frameHTTPDo(s, "POST", "/v1/frame-jobs/"+frameHTTPJob+"/cancel", `{}`)
	if w.Code != 202 || s.cancelCalls != 1 || !strings.Contains(w.Body.String(), `"cancelling"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestFrameHTTPStrictBoundaryBeforeAdmission(t *testing.T) {
	p := frameHTTPJSON(t, frameHTTPProgram(t))
	valid := `{"program":` + p + `,"policy":"reject","request_id":"` + frameHTTPRequest + `","expected_daemon_instance_id":"` + frameHTTPInstance + `"}`
	for name, body := range map[string]string{
		"unknown":          strings.TrimSuffix(valid, "}") + `,"raw_usb":true}`,
		"duplicate":        strings.Replace(valid, `"policy":"reject"`, `"policy":"reject","policy":"reject"`, 1),
		"duplicate_nested": strings.Replace(valid, `"fade_ms":`, `"fade_ms":0,"fade_ms":`, 1),
		"unknown_nested":   strings.Replace(valid, `"fade_ms":`, `"gamma":true,"fade_ms":`, 1),
		"wrong_contract":   strings.Replace(valid, control.FrameContract, "nb1-program-effect/v1", 1),
		"null":             strings.Replace(valid, `"policy":"reject"`, `"policy":null`, 1),
		"replace":          strings.Replace(valid, `"policy":"reject"`, `"policy":"replace"`, 1),
		"unkeyed":          `{"program":` + p + `,"policy":"reject"}`,
		"tail":             valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &frameHTTPSpy{}
			w := frameHTTPDo(s, "POST", "/v1/devices/20005601/frame-jobs", body)
			if w.Code != 400 || s.frameCalls != 0 || s.submitCount != 0 {
				t.Fatal(w.Code, w.Body.String(), s.frameCalls)
			}
		})
	}
	s := &frameHTTPSpy{}
	w := frameHTTPDo(s, "POST", "/v1/devices/20005601/frame-jobs", valid+strings.Repeat(" ", control.MaxFrameProgramBytes))
	if w.Code != 413 || s.frameCalls != 0 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/v1/frame-capabilities?", "/v1/frame-programs/preview?x=1", "/v1/frame-jobs/" + frameHTTPJob + "?x=1", "/v1/devices/20005601/frame-requests/" + frameHTTPRequest + "?expected_daemon_instance_id=" + frameHTTPInstance + "&extra=x"} {
		method := "GET"
		body := ""
		if strings.Contains(path, "preview") {
			method = "POST"
			body = `{"program":` + p + `}`
		}
		w = frameHTTPDo(s, method, path, body)
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8931/v1/devices/20005601/frame-jobs", strings.NewReader(valid))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.invalid")
	w = httptest.NewRecorder()
	httpapi.New(s).ServeHTTP(w, request)
	if w.Code != 403 || s.frameCalls != 0 {
		t.Fatal("origin guard bypass", w.Code)
	}
	w = frameHTTPDo(&fakeService{}, "POST", "/v1/devices/20005601/frame-jobs", valid)
	if w.Code != 501 || !strings.Contains(w.Body.String(), "frame_unsupported") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLightV2PreviewAndLegacyRouteIsolation(t *testing.T) {
	s := &frameHTTPSpy{}
	body := `{"contract":"` + lightactionv2.Contract + `","action":{"kind":"orbit","colors":["red","blue"],"segments":4,"period_ms":600,"cycles":1},"serial":"20005601"}`
	w := frameHTTPDo(s, "POST", "/v2/light/preview", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Result  lightactionv2.Result `json:"result"`
		Preview control.FramePreview `json:"preview"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Result.Program, result.Preview.Program) || !result.Preview.Execution.Available || result.Result.TotalFrames != 4 {
		t.Fatal("v2 preview mismatch", w.Body.String())
	}
	w = frameHTTPDo(s, "POST", "/v1/light/preview", body)
	if w.Code != 400 {
		t.Fatal("v1 accepted frame action", w.Code)
	}
	w = frameHTTPDo(s, "GET", "/v1/frame-capabilities", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"software_supported":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.frameCalls != 0 || s.submitCount != 0 {
		t.Fatal("preview ran hardware")
	}
}
