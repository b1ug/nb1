package remoteaccess

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testToken     = "synthetic-Remote-Token-v1-xzxltjdst57k"
	testAuthority = "light.example.test:8933"
)

func TestHandler(t *testing.T) {
	type testCase struct {
		name        string
		method      string
		path        string
		host        string
		insecure    bool
		credentials []string
		origins     []string
		forwarded   bool
		wantStatus  int
		wantCode    string
		wantHandler string
	}
	valid := []string{"Bearer " + testToken}
	for _, tt := range []testCase{
		{name: "api_get", method: "GET", path: "/v1/devices", credentials: valid, wantStatus: 200, wantHandler: "api"},
		{name: "api_root", method: "GET", path: "/v1", credentials: valid, wantStatus: 200, wantHandler: "api"},
		{name: "api_post", method: "POST", path: "/v1/actions", credentials: valid, wantStatus: 200, wantHandler: "api"},
		{name: "mcp_get", method: "GET", path: "/mcp", credentials: valid, wantStatus: 200, wantHandler: "mcp"},
		{name: "mcp_post", method: "POST", path: "/mcp", credentials: valid, wantStatus: 200, wantHandler: "mcp"},
		{name: "mcp_delete", method: "DELETE", path: "/mcp", credentials: valid, wantStatus: 200, wantHandler: "mcp"},
		{name: "uppercase_host", method: "GET", path: "/mcp", host: strings.ToUpper(testAuthority), credentials: valid, wantStatus: 200, wantHandler: "mcp"},
		{name: "bearer_case_ascii_space", method: "POST", path: "/mcp", credentials: []string{"bEaReR  " + testToken}, wantStatus: 200, wantHandler: "mcp"},
		{name: "same_origin", method: "GET", path: "/mcp", origins: []string{"https://" + testAuthority}, credentials: valid, wantStatus: 200, wantHandler: "mcp"},
		{name: "api_get_without_token", method: "GET", path: "/v1/devices", wantStatus: 401, wantCode: "unauthorized"},
		{name: "api_post_wrong_token", method: "POST", path: "/v1/actions", credentials: []string{"Bearer " + strings.Repeat("z", len(testToken))}, wantStatus: 401, wantCode: "unauthorized"},
		{name: "mcp_get_without_token", method: "GET", path: "/mcp", wantStatus: 401, wantCode: "unauthorized"},
		{name: "mcp_post_duplicate_token", method: "POST", path: "/mcp", credentials: []string{valid[0], valid[0]}, wantStatus: 401, wantCode: "unauthorized"},
		{name: "mcp_post_comma_joined_token", method: "POST", path: "/mcp", credentials: []string{valid[0] + ", " + valid[0]}, wantStatus: 401, wantCode: "unauthorized"},
		{name: "mcp_post_nonbearer", method: "POST", path: "/mcp", credentials: []string{"Basic " + testToken}, wantStatus: 401, wantCode: "unauthorized"},
		{name: "mcp_post_tab", method: "POST", path: "/mcp", credentials: []string{"Bearer\t" + testToken}, wantStatus: 401, wantCode: "unauthorized"},
		{name: "wrong_host_ignores_forwarded", method: "GET", path: "/mcp", host: "other.example.test:8933", credentials: valid, forwarded: true, wantStatus: 403, wantCode: "invalid_host"},
		{name: "missing_host_port", method: "GET", path: "/mcp", host: "light.example.test", credentials: valid, wantStatus: 403, wantCode: "invalid_host"},
		{name: "plain_http_ignores_forwarded", method: "GET", path: "/mcp", insecure: true, credentials: valid, forwarded: true, wantStatus: 403, wantCode: "tls_required"},
		{name: "wrong_origin", method: "GET", path: "/mcp", origins: []string{"https://other.example.test:8933"}, credentials: valid, wantStatus: 403, wantCode: "invalid_origin"},
		{name: "non_https_origin", method: "POST", path: "/v1/actions", origins: []string{"http://" + testAuthority}, credentials: valid, wantStatus: 403, wantCode: "invalid_origin"},
		{name: "duplicate_origin", method: "POST", path: "/mcp", origins: []string{"https://" + testAuthority, "https://" + testAuthority}, credentials: valid, wantStatus: 403, wantCode: "invalid_origin"},
		{name: "opaque_origin", method: "POST", path: "/mcp", origins: []string{"null"}, credentials: valid, wantStatus: 403, wantCode: "invalid_origin"},
		{name: "origin_with_path", method: "POST", path: "/mcp", origins: []string{"https://" + testAuthority + "/demo"}, credentials: valid, wantStatus: 403, wantCode: "invalid_origin"},
		{name: "workbench_off_route", method: "GET", path: "/demo/", wantStatus: 404, wantCode: "not_found"},
		{name: "route_boundary", method: "GET", path: "/v1test", credentials: valid, wantStatus: 404, wantCode: "not_found"},
		{name: "mcp_exact_path", method: "GET", path: "/mcp/", credentials: valid, wantStatus: 404, wantCode: "not_found"},
		{name: "api_path_traversal", method: "GET", path: "/v1/%2e%2e/demo", credentials: valid, wantStatus: 404, wantCode: "not_found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(name string) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != tt.method || r.URL.Path != tt.path {
						t.Errorf("changed downstream method or path: %s %s", r.Method, r.URL.Path)
					}
					if len(headerValues(r.Header, "Authorization")) != 0 {
						t.Error("forwarded credential to downstream handler")
					}
					w.Header().Set("X-Test-Handler", name)
					w.WriteHeader(http.StatusOK)
				})
			}
			req := httptest.NewRequest(tt.method, "https://"+testAuthority+tt.path, nil)
			if tt.host != "" {
				req.Host = tt.host
			}
			if tt.insecure {
				req.TLS = nil
			}
			for _, auth := range tt.credentials {
				req.Header.Add("Authorization", auth)
			}
			for _, origin := range tt.origins {
				req.Header.Add("Origin", origin)
			}
			if tt.forwarded {
				req.Header.Set("Forwarded", "host="+testAuthority+";proto=https")
				req.Header.Set("X-Forwarded-Host", testAuthority)
				req.Header.Set("X-Forwarded-Proto", "https")
			}

			rec := httptest.NewRecorder()
			Handler(testAuthority, testToken, handler("api"), handler("mcp")).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || rec.Header().Get("X-Test-Handler") != tt.wantHandler {
				t.Errorf("status=%d handler=%q body=%s", rec.Code, rec.Header().Get("X-Test-Handler"), rec.Body)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("missing no-store")
			}
			if tt.wantCode == "" {
				return
			}
			var body struct {
				Error errorBody `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != tt.wantCode || body.Error.Message == "" {
				t.Errorf("error body=%s parse error=%v", rec.Body, err)
			}
			if rec.Header().Get("Content-Type") != "application/json" {
				t.Error("unexpected error content type")
			}
			if tt.wantStatus == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != `Bearer realm="nb1"` {
				t.Error("unauthorized response lacks challenge")
			}
			if strings.Contains(rec.Body.String(), testToken) {
				t.Error("error body exposed the credential")
			}
		})
	}
}

func TestDefaultHTTPSPort(t *testing.T) {
	handler := Handler("lamp.example", testToken, http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, host := range []string{"lamp.example", "lamp.example:443"} {
		for _, origin := range []string{"https://lamp.example", "https://lamp.example:443"} {
			r := httptest.NewRequest(http.MethodPost, "https://"+host+"/mcp", nil)
			r.Header.Set("Origin", origin)
			r.Header.Set("Authorization", "Bearer "+testToken)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusNoContent {
				t.Errorf("Host %s Origin %s: %d", host, origin, w.Code)
			}
		}
	}
}

func TestHandlerFailsClosedOnInvalidServerToken(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("called downstream handler") })
	req := httptest.NewRequest("GET", "https://"+testAuthority+"/v1", nil)
	req.Header.Set("Authorization", "Bearer too-short")
	rec := httptest.NewRecorder()
	Handler(testAuthority, "too-short", next, nil).ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestProtectLocal(t *testing.T) {
	valid := "Bearer " + testToken
	unexpected := "Bearer " + strings.Repeat("x", len(testToken))
	for _, tt := range []struct {
		name, method, path   string
		token, authorization string
		duplicate            bool
		wantStatus           int
		wantCode             string
	}{
		{name: "liveness", method: "GET", path: "/healthz", token: testToken, wantStatus: 204},
		{name: "liveness_does_not_forward_credentials", method: "GET", path: "/healthz", token: testToken, authorization: unexpected, wantStatus: 204},
		{name: "post_health_requires_token", method: "POST", path: "/healthz", token: testToken, wantStatus: 401, wantCode: "unauthorized"},
		{name: "health_descendant_requires_token", method: "GET", path: "/healthz/other", token: testToken, wantStatus: 401, wantCode: "unauthorized"},
		{name: "get_requires_token", method: "GET", path: "/v1/devices", token: testToken, wantStatus: 401, wantCode: "unauthorized"},
		{name: "post_wrong_token", method: "POST", path: "/v1/jobs", token: testToken, authorization: unexpected, wantStatus: 401, wantCode: "unauthorized"},
		{name: "get", method: "GET", path: "/v1/devices", token: testToken, authorization: valid, wantStatus: 204},
		{name: "post", method: "POST", path: "/v1/jobs", token: testToken, authorization: valid, wantStatus: 204},
		{name: "v2_requires_token", method: "PUT", path: "/v2/patterns/example", token: testToken, wantStatus: 401, wantCode: "unauthorized"},
		{name: "v2_mutation", method: "PUT", path: "/v2/patterns/example", token: testToken, authorization: valid, wantStatus: 204},
		{name: "duplicate_case_variant_headers", method: "GET", path: "/v1/devices", token: testToken, authorization: valid, duplicate: true, wantStatus: 401, wantCode: "unauthorized"},
		{name: "invalid_configured_token", method: "GET", path: "/v1/devices", token: "short", authorization: "Bearer short", wantStatus: 401, wantCode: "unauthorized"},
		{name: "workbench_unauthorized", method: "GET", path: "/demo", token: testToken, wantStatus: 403, wantCode: "workbench_unavailable"},
		{name: "workbench_authorized", method: "GET", path: "/demo/?skin=nightlab", token: testToken, authorization: valid, wantStatus: 403, wantCode: "workbench_unavailable"},
		{name: "workbench_asset_authorized", method: "GET", path: "/demo/nightlab/studio.js", token: testToken, authorization: valid, wantStatus: 403, wantCode: "workbench_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://127.0.0.1:8931"+tt.path, nil)
			if tt.authorization != "" {
				req.Header["authorization"] = []string{tt.authorization}
			}
			if tt.duplicate {
				req.Header.Set("Authorization", valid)
			}
			req.Header.Set("X-Test-Header", "keep")
			rec := httptest.NewRecorder()
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != req.Method || r.URL.String() != req.URL.String() || r.Header.Get("X-Test-Header") != "keep" {
					t.Error("unrelated request data changed")
				}
				if len(headerValues(r.Header, "Authorization")) != 0 {
					t.Error("downstream handler received credentials")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			ProtectLocal(tt.token, next).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
			}
			if tt.authorization != "" {
				original := req.Header["authorization"]
				if len(original) != 1 || original[0] != tt.authorization {
					t.Error("original Authorization header changed")
				}
			}
			if tt.wantCode == "" {
				return
			}
			var body struct {
				Error errorBody `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != tt.wantCode ||
				body.Error.Message == "" || strings.Contains(rec.Body.String(), testToken) {
				t.Errorf("error body=%s parse error=%v", rec.Body, err)
			}
			challenge := rec.Header().Get("WWW-Authenticate")
			if tt.wantStatus == 401 && challenge != `Bearer realm="nb1"` || tt.wantStatus == 403 && challenge != "" {
				t.Errorf("unexpected challenge %q", challenge)
			}
		})
	}
}

func TestGenerateAndLoadToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-token")
	if err := GenerateTokenFile(path); err != nil {
		t.Fatal(err)
	}
	token, err := LoadToken(path)
	randomBytes, decodeErr := base64.RawURLEncoding.DecodeString(token)
	if err != nil || decodeErr != nil || len(randomBytes) != 32 {
		t.Fatalf("generated token did not round trip: load=%v decode=%v length=%d", err, decodeErr, len(randomBytes))
	}
	if err := GenerateTokenFile(path); err == nil {
		t.Fatal("existing token was accepted for generation")
	}
	if kept, err := LoadToken(path); err != nil || kept != token {
		t.Fatalf("original token was not preserved: %v", err)
	}

	otherPath := filepath.Join(t.TempDir(), "remote-token")
	if err := GenerateTokenFile(otherPath); err != nil {
		t.Fatal(err)
	}
	if other, err := LoadToken(otherPath); err != nil || other == token {
		t.Fatalf("second token is not independently generated: %v", err)
	}
}

func TestLoadTokenFormat(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		valid   bool
	}{
		{name: "no_newline", content: testToken, valid: true},
		{name: "CRLF", content: testToken + "\r\n", valid: true},
		{name: "RFC6750", content: testToken + "+/._~==", valid: true},
		{name: "too_short", content: "short"},
		{name: "too_long", content: strings.Repeat("a", maxTokenFileSize+1)},
		{name: "prefix", content: "Bearer " + testToken},
		{name: "space", content: testToken + " "},
		{name: "newline", content: testToken + "\n" + testToken},
		{name: "blank_line", content: testToken + "\n\n"},
		{name: "non_ascii", content: testToken + "文"},
		{name: "misplaced_padding", content: testToken + "=another"},
		{name: "only_padding", content: strings.Repeat("=", 32)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "remote-token")
			if err := GenerateTokenFile(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadToken(path)
			if tt.valid {
				if err != nil || got != strings.TrimRight(tt.content, "\r\n") {
					t.Fatalf("valid token rejected or changed: %v", err)
				}
			} else if got != "" || err == nil || strings.Contains(err.Error(), tt.content) {
				t.Fatalf("invalid token accepted or echoed: %v", err)
			}
		})
	}
}

func TestTokenFileErrorsDoNotEchoPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, testToken)
	for _, err := range []error{
		func() error { _, err := LoadToken(missing); return err }(),
		GenerateTokenFile(filepath.Join(missing, "token")),
	} {
		if err == nil || strings.Contains(err.Error(), testToken) {
			t.Fatalf("unsafe or missing error: %v", err)
		}
	}
	if _, err := LoadToken(dir); err == nil {
		t.Fatal("directory accepted as a token")
	}
}
