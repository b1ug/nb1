package httpapi_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/colorinput"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

func TestColorResolutionNeedsNoServiceOrDevice(t *testing.T) {
	for _, service := range []control.Service{nil, noPreviewIO{}} {
		h := httpapi.New(service)
		for _, c := range []struct{ body, kind, color, palette string }{
			{`{"color":"green"}`, "name", "#008000", "css-named-2026"},
			{`{"color":"lime"}`, "name", "#00ff00", "css-named-2026"},
			{`{"color":"#0F8"}`, "hex3", "#00ff88", ""},
			{`{"color":"#12AbEF"}`, "hex6", "#12abef", ""},
			{`{"color":{"space":"rgb8","r":0,"g":255,"b":136}}`, "rgb8", "#00ff88", ""},
			{`{"color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}}`, "hsv", "#008000", ""},
		} {
			out := previewRequest(h, http.MethodPost, "/v1/colors/resolve", c.body)
			if out.Code != 200 {
				t.Fatal(out.Code, out.Body.String())
			}
			var result colorinput.Result
			if json.Unmarshal(out.Body.Bytes(), &result) != nil || colorinput.ValidateResult(result) != nil || result.InputKind != c.kind || result.CanonicalColor != c.color || result.Palette != c.palette {
				t.Fatal(c, out.Body.String())
			}
			if out.Header().Get("Cache-Control") != "no-store" || out.Header().Get("X-Content-Type-Options") != "nosniff" || out.Header().Get("Location") != "" || out.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
				t.Fatal(out.Header())
			}
			var wire map[string]interface{}
			if json.Unmarshal(out.Body.Bytes(), &wire) != nil || wire["accepted"] != nil || wire["serial"] != nil || wire["job"] != nil {
				t.Fatal(wire)
			}
		}
	}
}

func TestColorHTTPBoundary(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	for _, c := range []struct {
		name, method, path, body, code string
		status                         int
	}{
		{"wrong method", "GET", "/v1/colors/resolve", "", "method_not_allowed", 405},
		{"query", "POST", "/v1/colors/resolve?x=1", `{"color":"red"}`, "invalid_request", 400},
		{"empty query", "POST", "/v1/colors/resolve?", `{"color":"red"}`, "invalid_request", 400},
		{"trailing slash", "POST", "/v1/colors/resolve/", `{"color":"red"}`, "not_found", 404},
		{"too big", "POST", "/v1/colors/resolve", strings.Repeat(" ", 4096) + `{}`, "body_too_large", 413},
		{"missing", "POST", "/v1/colors/resolve", `{}`, "invalid_color", 400},
		{"serial disallowed", "POST", "/v1/colors/resolve", `{"color":"red","serial":"20005601"}`, "invalid_color", 400},
		{"ticket disallowed", "POST", "/v1/colors/resolve", `{"color":"red","request_id":"req_a"}`, "invalid_color", 400},
		{"policy disallowed", "POST", "/v1/colors/resolve", `{"color":"red","policy":"replace"}`, "invalid_color", 400},
		{"duplicate", "POST", "/v1/colors/resolve", `{"color":"red","c\u006flor":"blue"}`, "invalid_color", 400},
		{"null", "POST", "/v1/colors/resolve", `{"color":null}`, "invalid_color", 400},
		{"tail", "POST", "/v1/colors/resolve", `{"color":"red"} {}`, "invalid_color", 400},
		{"float rgb", "POST", "/v1/colors/resolve", `{"color":{"space":"rgb8","r":1.0,"g":0,"b":0}}`, "invalid_color", 400},
		{"transparent", "POST", "/v1/colors/resolve", `{"color":"transparent"}`, "invalid_color", 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := previewRequest(h, c.method, c.path, c.body)
			var failure httpapi.ErrorResponse
			if out.Code != c.status || json.Unmarshal(out.Body.Bytes(), &failure) != nil || failure.Error.Code != c.code {
				t.Fatal(out.Code, out.Body.String())
			}
			if c.status == 405 && out.Header().Get("Allow") != http.MethodPost {
				t.Fatal(out.Header())
			}
		})
	}
	for _, c := range []struct {
		header, value string
		status        int
	}{
		{"Content-Type", "text/plain", 415}, {"Origin", "https://evil.example", 403}, {"Host", "evil.example", 403},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/colors/resolve", strings.NewReader(`{"color":"red"}`))
		r.Header.Set("Content-Type", "application/json")
		if c.header == "Host" {
			r.Host = c.value
		} else {
			r.Header.Set(c.header, c.value)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != c.status {
			t.Fatal(c, out.Code, out.Body.String())
		}
	}
	// Exactly 4096 bytes is permitted; transport size is independent of semantic
	// input length. A read error never reaches any device service.
	body := `{"color":"red"}`
	out := previewRequest(h, "POST", "/v1/colors/resolve", body+strings.Repeat(" ", 4096-len(body)))
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/v1/colors/resolve", nil)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.Body = io.NopCloser(colorReadFailure{})
	out = httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
}

type colorReadFailure struct{}

func (colorReadFailure) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }

func TestColorAuthoringDoesNotWidenExistingV1(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	for _, color := range []string{`"green"`, `"#0f8"`, `{"space":"rgb8","r":0,"g":255,"b":0}`} {
		body := `{"effect":{"kind":"steady","color":` + color + `}}`
		for _, route := range []string{"/v1/programs/preview", "/v1/devices/demo/jobs"} {
			out := previewRequest(h, "POST", route, body)
			if out.Code != 400 {
				t.Fatal(route, body, out.Code, out.Body.String())
			}
		}
	}
}
