package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/webui"
)

func TestVerifiedWebBundleKeepsExistingRoutesAndPolicy(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	for path, file := range map[string]string{
		"/demo/": "index.html", "/demo/demo.js": "demo.js", "/demo/demo.css": "demo.css",
		"/demo/orbit.html": "orbit.html", "/demo/orbit.js": "orbit.js", "/demo/orbit.css": "orbit.css",
	} {
		response := previewRequest(h, http.MethodGet, path, "")
		want, err := webui.ReadFile(file)
		if err != nil || response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
			t.Fatalf("%s does not serve the exact verified asset: %d %v", path, response.Code, err)
		}
		if response.Header().Get("Content-Security-Policy") != sequenceWebCSP || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s changed the static resource policy: %v", path, response.Header())
		}
	}
	for _, path := range []string{"/demo/manifest.json", "/demo/manifest.lock.json", "/demo/LICENSE.txt", "/demo/../manifest.lock.json", "/demox/index.html"} {
		if response := previewRequest(h, http.MethodGet, path, ""); response.Code != http.StatusNotFound {
			t.Fatalf("unexpected resource %s was exposed: %d", path, response.Code)
		}
	}
}

func TestPatternWebResourcesUseTheSelectedBundleProfileAndSameGuards(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	for path, file := range map[string]string{"/demo/patterns.html": "patterns.html", "/demo/patterns.js": "patterns.js", "/demo/patterns.css": "patterns.css"} {
		w := previewRequest(h, http.MethodGet, path, "")
		data, err := webui.ReadFile(file)
		if err == nil {
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) || w.Header().Get("Content-Security-Policy") != sequenceWebCSP {
				t.Fatal(path, w.Code, w.Header())
			}
		} else if errors.Is(err, fs.ErrNotExist) && webui.Validate() == nil {
			if w.Code != 404 {
				t.Fatal("valid legacy bundle must not report corruption for its absent Pattern page", path, w.Code)
			}
		} else {
			t.Fatal("invalid embedded bundle", err)
		}
		if w := previewRequest(h, http.MethodPost, path, ""); w.Code != 405 || w.Header().Get("Allow") != "GET" {
			t.Fatal(path, w.Code, w.Header())
		}
		if w := previewRequest(h, http.MethodGet, path+"?", ""); w.Code != 400 {
			t.Fatal("resource accepted query", path, w.Code)
		}
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		r.Header.Set("Origin", "https://untrusted.invalid")
		blocked := httptest.NewRecorder()
		h.ServeHTTP(blocked, r)
		if blocked.Code != 403 {
			t.Fatal("resource bypassed same-origin guard", path, blocked.Code)
		}
	}
}

func TestBilingualWebResourcesKeepStaticGuardsWithoutBackendCalls(t *testing.T) {
	// Every backend method panics: serving locale assets must remain pure.
	h := httpapi.New(noPreviewIO{})
	for _, name := range []string{"i18n.js", "locales.js"} {
		t.Run(name, func(t *testing.T) {
			path := "/demo/" + name
			want, err := webui.ReadFile(name)
			response := previewRequest(h, http.MethodGet, path, "")
			if err != nil || response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatal("locale asset differs from verified bundle", response.Code, err)
			}
			if response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || response.Header().Get("Content-Security-Policy") != sequenceWebCSP || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("locale asset policy changed", response.Header())
			}
			for _, method := range []string{http.MethodPost, http.MethodHead, http.MethodPut} {
				if w := previewRequest(h, method, path, ""); w.Code != 405 || w.Header().Get("Allow") != "GET" {
					t.Fatal("locale resource accepted method", method, w.Code)
				}
			}
			if w := previewRequest(h, http.MethodGet, path+"?", ""); w.Code != 400 {
				t.Fatal("locale resource accepted query", w.Code)
			}
			if w := previewRequest(h, http.MethodGet, path, "body"); w.Code != 400 {
				t.Fatal("locale resource accepted body", w.Code)
			}
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
			r.Header.Set("Origin", "https://untrusted.invalid")
			blocked := httptest.NewRecorder()
			h.ServeHTTP(blocked, r)
			if blocked.Code != 403 {
				t.Fatal("locale resource bypassed origin guard", blocked.Code)
			}
		})
	}
	for _, path := range []string{"/demo/locales/en.js", "/demo/locales.json", "/demo/i18n.js.map", "/demo/locales.js.map"} {
		if w := previewRequest(h, http.MethodGet, path, ""); w.Code != 404 {
			t.Fatal("unknown locale resource exposed", path, w.Code)
		}
	}
}

type lightHTTPSpy struct {
	noPreviewIO
	deviceCalls int
	submitCalls int
	snapshot    control.DeviceState
	serial      string
	program     control.Program
	policy      string
	binding     control.ReceiptRequest
	submitErr   error
}

func (s *lightHTTPSpy) Device(string) (control.DeviceState, error) {
	s.deviceCalls++
	return s.snapshot, nil
}
func (s *lightHTTPSpy) SubmitWithReceipt(_ context.Context, serial string, program control.Program, policy string, binding control.ReceiptRequest) (control.ReceiptResult, error) {
	s.submitCalls++
	s.serial, s.program, s.policy, s.binding = serial, program, policy, binding
	if s.submitErr != nil {
		return control.ReceiptResult{}, s.submitErr
	}
	return control.ReceiptResult{Job: control.Job{ID: "demo-job", Device: serial, Status: "queued", Program: program}, DaemonInstanceID: binding.ExpectedDaemonInstanceID, Replayed: true}, nil
}
func (s *lightHTTPSpy) Request(string, string, string) (control.Job, error) {
	panic("new light handler must not query receipts on its own")
}

func TestLightHTTPPureCompilationUsesSharedCore(t *testing.T) {
	actions := []string{
		`{"kind":"set","color":"red"}`,
		`{"kind":"off"}`,
		`{"kind":"fade","color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50},"duration_ms":200}`,
		`{"kind":"blink","color":"#0f0","cycles":2}`,
		`{"kind":"breathe","color":"lime","period_ms":2000}`,
		`{"kind":"pulse","color":"rebeccapurple"}`,
		`{"kind":"alternate","colors":["red","blue"],"cycles":2}`,
		`{"kind":"rainbow","period_ms":3000}`,
	}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			body := `{"contract":"` + lightaction.Contract + `","action":` + action + `}`
			expected, err := lightaction.CompileRequest([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			out := previewRequest(httpapi.New(noPreviewIO{}), http.MethodPost, "/v1/light/preview", body)
			var result struct {
				Result  lightaction.Result    `json:"result"`
				Preview control.PreviewResult `json:"preview"`
			}
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || !reflect.DeepEqual(result.Result, expected) || !reflect.DeepEqual(result.Preview.Program, expected.Program) || result.Preview.NominalDurationMS != expected.NominalDurationMS || result.Preview.TotalSteps != expected.TotalSteps || result.Preview.Validation.Device != "not_requested" {
				t.Fatalf("preview = %d %s", out.Code, out.Body.String())
			}
			t.Logf("LIGHT_DEMO_FIXTURE %s", strings.TrimSpace(out.Body.String()))
		})
	}
	// A daemon or a USB implementation is not required for pure compilation.
	out := previewRequest(httpapi.New(nil), http.MethodPost, "/v1/light/preview", `{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestLightHTTPTargetPreviewReadsOnlyOneSnapshot(t *testing.T) {
	spy := &lightHTTPSpy{snapshot: control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: "demo", Capabilities: []string{"set", "program", "raw-rgb", "hold"}}, Connection: "online"}}
	out := previewRequest(httpapi.New(spy), http.MethodPost, "/v1/light/preview", `{"contract":"nb1-light-action/v1","action":{"kind":"set","color":"red"},"serial":"demo"}`)
	var result struct {
		Preview control.PreviewResult `json:"preview"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || result.Preview.Validation.Device != "supported" || spy.deviceCalls != 1 || spy.submitCalls != 0 {
		t.Fatal(out.Code, out.Body.String(), spy.deviceCalls, spy.submitCalls)
	}
}

func TestLightHTTPSubmissionUsesOriginalReceiptAndCompiledProgram(t *testing.T) {
	spy := &lightHTTPSpy{}
	action := `{"kind":"alternate","colors":["red","blue"],"cycles":2}`
	expected, err := lightaction.CompileJSON([]byte(action))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"contract":"nb1-light-action/v1","action":` + action + `,"policy":"reject","request_id":"original-ticket","expected_daemon_instance_id":"original-instance"}`
	out := previewRequest(httpapi.New(spy), http.MethodPost, "/v1/devices/demo/light", body)
	var job control.Job
	if out.Code != 202 || json.Unmarshal(out.Body.Bytes(), &job) != nil || !reflect.DeepEqual(job.Program, expected.Program) || spy.submitCalls != 1 || spy.deviceCalls != 0 || spy.serial != "demo" || spy.policy != "reject" || spy.binding.RequestID != "original-ticket" || spy.binding.ExpectedDaemonInstanceID != "original-instance" {
		t.Fatal(out.Code, out.Body.String(), spy)
	}
	if out.Header().Get("X-NB1-Replayed") != "true" || out.Header().Get("X-NB1-Daemon-Instance-ID") != "original-instance" || out.Header().Get("Location") != "/v1/devices/demo/requests/original-ticket?expected_daemon_instance_id=original-instance" {
		t.Fatal(out.Header())
	}
	spy.submitErr = control.ErrBusy
	out = previewRequest(httpapi.New(spy), http.MethodPost, "/v1/devices/demo/light", body)
	if out.Code != 409 || !strings.Contains(out.Body.String(), `"busy"`) {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestLightHTTPRejectsAmbiguousInputBeforeExecution(t *testing.T) {
	valid := `{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"action":{"kind":"off"}}`, `{"contract":"v2","action":{"kind":"off"}}`,
		`{"contract":"nb1-light-action/v1","action":null}`, `{"contract":"nb1-light-action/v1","Action":{"kind":"off"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off"},"a\u0063tion":{"kind":"off"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":"red","led":1.0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":"red","led":1e0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":"red","led":-0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":"red","color":"blue"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off"},"serial":null}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off"},"policy":"reject"}`, valid + `{}`,
	} {
		out := previewRequest(httpapi.New(noPreviewIO{}), http.MethodPost, "/v1/light/preview", body)
		if out.Code != 400 {
			t.Fatalf("invalid preview accepted: %s => %d %s", body, out.Code, out.Body.String())
		}
	}
	for _, suffix := range []string{``, `,"policy":"reject"`, `,"policy":"replace","request_id":"r","expected_daemon_instance_id":"i"`, `,"policy":"reject","request_id":"","expected_daemon_instance_id":"i"`, `,"policy":"reject","request_id":"r"`} {
		spy := &lightHTTPSpy{}
		body := strings.TrimSuffix(valid, "}") + suffix + "}"
		out := previewRequest(httpapi.New(spy), http.MethodPost, "/v1/devices/demo/light", body)
		if out.Code != 400 || spy.submitCalls != 0 {
			t.Fatal(out.Code, out.Body.String(), spy.submitCalls)
		}
	}
	withoutReceipts := previewRequest(httpapi.New(noPreviewIO{}), http.MethodPost, "/v1/devices/demo/light", strings.TrimSuffix(valid, "}")+`,"policy":"reject","request_id":"r","expected_daemon_instance_id":"i"}`)
	if withoutReceipts.Code != 501 {
		t.Fatal(withoutReceipts.Code, withoutReceipts.Body.String())
	}
}

func TestLightHTTPTransportAndEmbeddedAssets(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	body := `{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/light/preview", "", 405}, {"POST", "/v1/light/preview?", body, 400}, {"POST", "/v1/light/preview/", body, 404},
		{"POST", "/v1/devices/demo/light?x=1", body, 400}, {"POST", "/v1/devices/bad%20serial/light", body, 400},
		{"POST", "/v1/light/preview", strings.Repeat(" ", lightaction.MaxRequestBytes) + body, 413},
		{"GET", "/demo/", "", 200}, {"GET", "/demo/demo.js", "", 200}, {"GET", "/demo/demo.css", "", 200},
		{"GET", "/demo/../httpapi.go", "", 404}, {"GET", "/demo/unknown", "", 404}, {"POST", "/demo/", "{}", 405}, {"GET", "/demo/?", "", 400}, {"GET", "/demo/", "{}", 400},
	} {
		out := previewRequest(h, test.method, test.path, test.body)
		if out.Code != test.status {
			t.Fatalf("%s %s => %d %s", test.method, test.path, out.Code, out.Body.String())
		}
		if test.status == 200 && strings.HasPrefix(test.path, "/demo/") {
			if out.Header().Get("Content-Security-Policy") == "" || out.Header().Get("Cache-Control") != "no-store" || out.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal(out.Header())
			}
		}
	}
	for _, path := range []string{"/demo/", "/v1/light/preview"} {
		for _, origin := range []string{"http://evil.example", "null"} {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, strings.NewReader(body))
			req.Header.Set("Origin", origin)
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != http.StatusForbidden {
				t.Fatal(path, origin, out.Code)
			}
		}
	}
	spy := &lightHTTPSpy{}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/light/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	out := httptest.NewRecorder()
	httpapi.New(spy).ServeHTTP(out, req)
	if out.Code != 415 || spy.submitCalls != 0 || spy.deviceCalls != 0 {
		t.Fatal(out.Code, out.Body.String())
	}
}
