package httpapi_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/webui"
)

const sequenceWebCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"

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
}

func assertSequenceWebHeaders(t *testing.T, header http.Header, contentType string) {
	t.Helper()
	if header.Get("Content-Type") != contentType || header.Get("Content-Security-Policy") != sequenceWebCSP || header.Get("Cache-Control") != "no-store" || header.Get("Referrer-Policy") != "no-referrer" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("static response headers: %v", header)
	}
}

func TestSequenceWebRoutesServeVerifiedAssets(t *testing.T) {
	// Every service method panics, including read methods.
	handler := httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI())
	for _, asset := range sequenceWebAssets {
		t.Run(asset.file, func(t *testing.T) {
			response := previewRequest(handler, http.MethodGet, asset.path, "")
			want, err := webui.ReadFile(asset.file)
			if err != nil || response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
				t.Fatalf("asset %s: HTTP %d, read error %v", asset.path, response.Code, err)
			}
			assertSequenceWebHeaders(t, response.Header(), asset.contentType)
		})
	}
}

func TestSequenceWebSecurityBoundaries(t *testing.T) {
	handler := httpapi.New(noPreviewIO{}, httpapi.WithSequenceAPI())
	for _, path := range []string{"/demo", "/demo/", "/demo/orbit.html", "/demo/patterns.html", "/demo/locales.js"} {
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
		if err != nil || readErr != nil || response.StatusCode != http.StatusOK || !bytes.Equal(data, want) {
			t.Fatalf("real HTTP asset %s: %d %v %v", asset.path, response.StatusCode, err, readErr)
		}
		assertSequenceWebHeaders(t, response.Header, asset.contentType)
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
