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
