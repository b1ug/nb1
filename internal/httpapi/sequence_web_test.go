package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/webui"
)

const sequenceWebCSP = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"

var sequenceWebAssets = []struct{ path, file, contentType string }{
	{"/demo/", "index.html", "text/html; charset=utf-8"},
	{"/demo/demo.js", "demo.js", "text/javascript; charset=utf-8"},
	{"/demo/demo.css", "demo.css", "text/css; charset=utf-8"},
	{"/demo/orbit.html", "orbit.html", "text/html; charset=utf-8"},
	{"/demo/orbit.js", "orbit.js", "text/javascript; charset=utf-8"},
	{"/demo/orbit.css", "orbit.css", "text/css; charset=utf-8"},
	{"/demo/patterns.html", "patterns.html", "text/html; charset=utf-8"},
	{"/demo/patterns.js", "patterns.js", "text/javascript; charset=utf-8"},
	{"/demo/patterns.css", "patterns.css", "text/css; charset=utf-8"},
	{"/demo/i18n.js", "i18n.js", "text/javascript; charset=utf-8"},
	{"/demo/locales.js", "locales.js", "text/javascript; charset=utf-8"},
	{"/demo/action-icons.js", "action-icons.js", "text/javascript; charset=utf-8"},
	{"/demo/locale-menu.css", "locale-menu.css", "text/css; charset=utf-8"},
	{"/demo/typography.css", "typography.css", "text/css; charset=utf-8"},
	{"/demo/json-editor.js", "json-editor.js", "text/javascript; charset=utf-8"},
	{"/demo/json-editor.css", "json-editor.css", "text/css; charset=utf-8"},
	{"/demo/editor-LICENSES.txt", "editor-LICENSES.txt", "text/plain; charset=utf-8"},
	{"/demo/assets/nb1-icon.png", "assets/nb1-icon.png", "image/png"},
	{"/demo/assets/nb1-mascot.webp", "assets/nb1-mascot.webp", "image/webp"},
	{"/demo/fonts/fredoka-700-latin.woff2", "fonts/fredoka-700-latin.woff2", "font/woff2"},
	{"/demo/fonts/fredoka-700-latin-ext.woff2", "fonts/fredoka-700-latin-ext.woff2", "font/woff2"},
	{"/demo/fonts/mplusround-500-latin.woff2", "fonts/mplusround-500-latin.woff2", "font/woff2"},
	{"/demo/fonts/mplusround-700-latin.woff2", "fonts/mplusround-700-latin.woff2", "font/woff2"},
	{"/demo/fonts/Fredoka-OFL.txt", "fonts/Fredoka-OFL.txt", "text/plain; charset=utf-8"},
	{"/demo/fonts/MPLUSRounded1c-OFL.txt", "fonts/MPLUSRounded1c-OFL.txt", "text/plain; charset=utf-8"},
}

func assertSequenceWebHeaders(t *testing.T, header http.Header, contentType string) string {
	t.Helper()
	policy := header.Get("Content-Security-Policy")
	wantPolicy, nonce := sequenceWebCSP, ""
	if contentType == "text/html; charset=utf-8" {
		match := regexp.MustCompile(`style-src 'self' 'nonce-([A-Za-z0-9+/]{32})'`).FindStringSubmatch(policy)
		if len(match) != 2 {
			t.Fatalf("HTML response lacks a protected style nonce: %s", policy)
		}
		nonce = match[1]
		decoded, err := base64.StdEncoding.DecodeString(nonce)
		if err != nil || len(decoded) != 24 {
			t.Fatalf("invalid style nonce: %q", nonce)
		}
		wantPolicy = strings.Replace(sequenceWebCSP, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
	}
	if header.Get("Content-Type") != contentType || policy != wantPolicy || header.Get("Cache-Control") != "no-store" || header.Get("Referrer-Policy") != "no-referrer" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("static response headers: %v", header)
	}
	return nonce
}

func assertSequenceWebAssetBytes(t *testing.T, header http.Header, contentType string, got, source []byte) {
	t.Helper()
	nonce := assertSequenceWebHeaders(t, header, contentType)
	want := source
	if nonce != "" {
		placeholder := []byte(`<meta name="nb1-style-nonce" content="__NB1_STYLE_NONCE__">`)
		if bytes.Count(source, placeholder) != 1 || bytes.Count(source, []byte("__NB1_STYLE_NONCE__")) != 1 {
			t.Fatal("verified HTML does not have exactly one canonical nonce placeholder")
		}
		replacement := []byte(`<meta name="nb1-style-nonce" content="` + nonce + `">`)
		want = bytes.Replace(source, placeholder, replacement, 1)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("asset bytes differ beyond the HTML nonce meta")
	}
}

func TestSequenceWebRoutesServeVerifiedAssets(t *testing.T) {
	// Every service method panics, including read methods.
	handler := httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI())
	for _, asset := range sequenceWebAssets {
		t.Run(asset.file, func(t *testing.T) {
			response := previewRequest(handler, http.MethodGet, asset.path, "")
			want, err := webui.ReadFile(asset.file)
			if err != nil || response.Code != http.StatusOK {
				t.Fatalf("asset %s: HTTP %d, read error %v", asset.path, response.Code, err)
			}
			assertSequenceWebAssetBytes(t, response.Header(), asset.contentType, response.Body.Bytes(), want)
		})
	}
}

func TestSequenceWebSecurityBoundaries(t *testing.T) {
	handler := httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI())
	for _, path := range []string{"/demo", "/demo/", "/demo/orbit.html", "/demo/patterns.html", "/demo/locales.js", "/demo/action-icons.js", "/demo/locale-menu.css", "/demo/typography.css", "/demo/json-editor.js", "/demo/json-editor.css", "/demo/editor-LICENSES.txt", "/demo/assets/nb1-icon.png", "/demo/assets/nb1-mascot.webp", "/demo/fonts/fredoka-700-latin.woff2", "/demo/fonts/Fredoka-OFL.txt"} {
		for _, method := range []string{"HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			response := previewRequest(handler, method, path, "")
			if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET" {
				t.Fatalf("method %s %s: HTTP %d", method, path, response.Code)
			}
		}
		for _, query := range []string{"?", "?design=offline"} {
			if response := previewRequest(handler, "GET", path+query, ""); response.Code != http.StatusBadRequest {
				t.Fatalf("query %s: HTTP %d", path+query, response.Code)
			}
		}
		if response := previewRequest(handler, "GET", path, "body"); response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: HTTP %d", path, response.Code)
		}
		request := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		request.ContentLength = -1
		request.TransferEncoding = []string{"chunked"}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("chunked body %s: HTTP %d", path, response.Code)
		}
		for _, boundary := range []struct{ host, origin string }{
			{"untrusted.invalid", ""},
			{"127.0.0.1", "https://untrusted.invalid"},
			{"127.0.0.1", "http://127.0.0.1:4173"},
			{"127.0.0.1", "null"},
		} {
			request := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
			request.Host = boundary.host
			request.Header.Set("Origin", boundary.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("Host/Origin %s: HTTP %d", path, response.Code)
			}
		}
	}
	for _, path := range []string{
		"/demo/manifest.json", "/demo/manifest.lock.json", "/demo/LICENSE.txt", "/demo/index.html",
		"/demo/../manifest.lock.json", "/demo/%2e%2e/manifest.lock.json", "/demo//demo.js", "/demo/src/index.html", "/demox/index.html",
		"/demo/fonts/", "/demo/fonts/unreviewed.woff2", "/demo/fonts/../manifest.json",
		"/demo/assets/", "/demo/assets/nb1-icon.svg", "/demo/assets/unreviewed.png", "/demo/assets/unreviewed.svg", "/demo/assets/unreviewed.webp", "/demo/assets/../manifest.json",
		"/v1/programs/preview", "/v1/frame-programs/preview", "/v1/light/preview", "/v1/instance", "/v2/light/preview",
	} {
		if response := previewRequest(handler, "GET", path, ""); response.Code != http.StatusNotFound {
			t.Fatalf("unlisted route %s: HTTP %d", path, response.Code)
		}
	}
}

func TestSequenceWebStaticOverRealHTTP(t *testing.T) {
	server := httptest.NewServer(httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI()))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(server.URL + "/demo")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "/demo/" {
		t.Fatalf("canonical entry redirect: %d %v", response.StatusCode, response.Header)
	}
	for _, asset := range sequenceWebAssets {
		request, err := http.NewRequest("GET", server.URL+asset.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", server.URL)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		want, readErr := webui.ReadFile(asset.file)
		if err != nil || readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("real HTTP asset %s: %d %v %v", asset.path, response.StatusCode, err, readErr)
		}
		assertSequenceWebAssetBytes(t, response.Header, asset.contentType, data, want)
	}
	request, err := http.NewRequest("GET", server.URL+"/demo/demo.js", strings.NewReader("unexpected"))
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("real HTTP body accepted: %d", response.StatusCode)
	}
}

func TestSequenceWebHTMLResponsesHaveFreshNonces(t *testing.T) {
	handler := httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI())
	seen := map[string]bool{}
	for _, path := range []string{"/demo/", "/demo/orbit.html", "/demo/patterns.html", "/demo/"} {
		response := previewRequest(handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("HTML request failed: %d", response.Code)
		}
		nonce := assertSequenceWebHeaders(t, response.Header(), "text/html; charset=utf-8")
		if seen[nonce] {
			t.Fatal("style nonce was reused between HTML responses")
		}
		seen[nonce] = true
		if !bytes.Contains(response.Body.Bytes(), []byte(`<meta name="nb1-style-nonce" content="`+nonce+`">`)) {
			t.Fatal("nonce header and meta differ")
		}
	}
}
