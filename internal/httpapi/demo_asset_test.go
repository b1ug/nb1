package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/webui"
)

type failedDemoRandomness struct{}

func (failedDemoRandomness) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}

func TestDemoHTMLOnlyChangesItsNonceMeta(t *testing.T) {
	source := []byte("<!doctype html><head>" + demoStyleNonceMeta + "</head><body>Keep this content unchanged.</body>")
	before := append([]byte(nil), source...)
	randomBytes := bytes.Repeat([]byte{0xab}, 24)
	got, policy, err := renderDemoHTML(source, bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatal(err)
	}
	nonce := base64.StdEncoding.EncodeToString(randomBytes)
	want := bytes.Replace(source, []byte(demoStyleNoncePlaceholder), []byte(nonce), 1)
	if !bytes.Equal(got, want) || !bytes.Equal(source, before) {
		t.Fatal("rendering changed template bytes beyond the nonce or mutated its source")
	}
	if policy != strings.Replace(demoContentSecurityPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1) ||
		strings.Contains(policy, "unsafe-inline") || strings.Contains(policy, "unsafe-eval") {
		t.Fatalf("unexpected nonce policy: %s", policy)
	}
}

func TestDemoHTMLFailuresReturnJSONWithoutServingHTML(t *testing.T) {
	valid := "<!doctype html><head>" + demoStyleNonceMeta + "</head><body>private template</body>"
	for _, test := range []struct {
		name, html string
		random     io.Reader
	}{
		{"missing", "<!doctype html><head></head>", bytes.NewReader(make([]byte, 24))},
		{"duplicate-meta", valid + demoStyleNonceMeta, bytes.NewReader(make([]byte, 24))},
		{"duplicate-placeholder", valid + demoStyleNoncePlaceholder, bytes.NewReader(make([]byte, 24))},
		{"outside-meta", "<body>" + demoStyleNoncePlaceholder + "</body>", bytes.NewReader(make([]byte, 24))},
		{"duplicate-name", valid + `<meta name='nb1-style-nonce' content='other'>`, bytes.NewReader(make([]byte, 24))},
		{"noncanonical-meta", `<meta content="__NB1_STYLE_NONCE__" name="nb1-style-nonce">`, bytes.NewReader(make([]byte, 24))},
		{"random-error", valid, failedDemoRandomness{}},
		{"short-randomness", valid, bytes.NewReader(make([]byte, 23))},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeDemoAsset(response, "text/html; charset=utf-8", []byte(test.html), test.random)
			if response.Code != http.StatusInternalServerError || response.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
				strings.Contains(response.Body.String(), "<") || strings.Contains(response.Body.String(), demoStyleNoncePlaceholder) {
				t.Fatalf("unprotected HTML escaped a failed render: %d %v %s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestNonHTMLDemoAssetDoesNotUseRandomnessOrChangeBytes(t *testing.T) {
	data := []byte(`const marker = "__NB1_STYLE_NONCE__";`)
	response := httptest.NewRecorder()
	writeDemoAsset(response, "text/javascript; charset=utf-8", data, failedDemoRandomness{})
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), data) || response.Header().Get("Content-Security-Policy") != demoContentSecurityPolicy {
		t.Fatal("non-HTML asset changed bytes or needed a nonce", response.Code, response.Header())
	}
}

func TestNightlabWASMCapabilityOnlyAppliesToHTML(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=utf-8", "text/javascript; charset=utf-8", "application/wasm"} {
		t.Run(contentType, func(t *testing.T) {
			data := []byte("<!doctype html><head>" + demoStyleNonceMeta + "</head><body>local resources</body>")
			response := httptest.NewRecorder()
			writeDemoAsset(response, contentType, data, bytes.NewReader(make([]byte, 24)), true)
			policy := response.Header().Get("Content-Security-Policy")
			if response.Code != http.StatusOK || strings.Contains(policy, "'unsafe-eval'") || strings.Contains(policy, "'unsafe-inline'") {
				t.Fatalf("unsafe or unsuccessful response: %d %s", response.Code, policy)
			}
			if strings.Contains(policy, "'wasm-unsafe-eval'") != (contentType == "text/html; charset=utf-8") {
				t.Fatalf("unexpected WASM capability scope: %s", policy)
			}
		})
	}
}

func TestNightlabRoutesReturnDecodedAssetsWithoutDeviceAccess(t *testing.T) {
	if !webui.IsNightlab() {
		t.Skip("the embedded profile is legacy")
	}
	// A nil Service makes any accidental API/device dependency fail immediately.
	handler := New(nil, WithSequenceAPI())
	for name := range nightlabDemoAssetTypes {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/demo/"+name, nil))
			want, err := webui.ReadFile(name)
			if err != nil || response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "" {
				t.Fatalf("decoded asset response: %d %v %v", response.Code, response.Header(), err)
			}
			policy := response.Header().Get("Content-Security-Policy")
			if strings.HasSuffix(name, ".html") {
				prefix := "style-src 'self' 'nonce-"
				parts := strings.SplitN(policy, prefix, 2)
				if len(parts) != 2 {
					t.Fatal("HTML lacks its style nonce")
				}
				nonce := strings.SplitN(parts[1], "'", 2)[0]
				want = bytes.Replace(want, []byte(demoStyleNoncePlaceholder), []byte(nonce), 1)
				if !strings.Contains(policy, "script-src 'self' 'wasm-unsafe-eval'") {
					t.Fatal("Nightlab HTML lacks scoped WASM capability")
				}
			} else if policy != demoContentSecurityPolicy {
				t.Fatal("non-HTML policy was broadened", policy)
			}
			if !bytes.Equal(response.Body.Bytes(), want) || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("asset bytes or response boundaries changed")
			}
		})
	}
	for name, wantType := range map[string]string{
		"nightlab/studio-3d.js":                                      "text/javascript; charset=utf-8",
		"nightlab/compiler/nb1.wasm":                                 "application/wasm",
		"nightlab/model-assets/blink1mk2_enclosure_diffuser_v3k.stl": "model/stl",
		"nightlab/model.html":                                        "text/html; charset=utf-8",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/demo/"+name, nil))
		if response.Header().Get("Content-Type") != wantType {
			t.Fatalf("critical MIME %s: %v", name, response.Header())
		}
	}
	for _, path := range []string{"/demo/nightlab/", "/demo/nightlab/private.env", "/demo/nightlab/compiler/", "/demo/nightlab/compiler/../compiler/nb1.wasm", "/demo/nightlab/studio-3d.js.gz",
		"/demo/nightlab/docs/INTEGRATION.md", "/demo/nightlab/model-assets/project-led-faces.txt", "/demo/nightlab/screenshots/joint-capture.json",
		"/demo/nightlab/hardware-checks/led-face-20261114T143506Z.json"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("unlisted Nightlab path accepted: %s HTTP %d", path, response.Code)
		}
	}
}
