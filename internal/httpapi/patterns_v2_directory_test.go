package httpapi_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternstore"
)

func TestPatternV2DirectoryLazyReadsSaveReopenAndLegacyIsolation(t *testing.T) {
	base := t.TempDir()
	legacyDir, v2Dir := filepath.Join(base, "legacy"), filepath.Join(base, "v2")
	options := []httpapi.Option{httpapi.WithPatternDirectory(legacyDir), httpapi.WithPatternDirectoryV2(v2Dir)}
	h := httpapi.New(patternV2NoIO{}, options...)
	for _, route := range []struct {
		method, path, file string
		status             int
	}{
		{"GET", "/v2/patterns", "", 200},
		{"GET", "/v2/patterns?format=snapshot", "", 200},
		{"GET", "/v2/patterns/missing", "", 404},
		{"POST", "/v2/patterns/preview", "requests/preview-orbit-red-blue.json", 200},
		{"POST", "/v2/patterns", "requests/save-revision-mismatch.json", 409},
	} {
		var body []byte
		if route.file != "" {
			body = patternV2File(t, route.file)
		}
		w := patternHTTP(h, route.method, route.path, body)
		if w.Code != route.status {
			t.Fatal(route.path, w.Code, w.Body.String())
		}
		for _, dir := range []string{legacyDir, v2Dir} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("non-write created %s: %v", dir, err)
			}
		}
	}
	// Save creates only the v2 directory. This handler's service panics on
	// device work, including state reads, admission and receipt operations.
	w := patternHTTP(h, "POST", "/v2/patterns", patternV2File(t, "requests/save-orbit-red-blue.json"))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatal("v2 save touched legacy directory", err)
	}
	v2Bytes, err := os.ReadFile(filepath.Join(v2Dir, "patterns.json"))
	if err != nil {
		t.Fatal(err)
	}
	w = patternHTTP(h, "POST", "/v1/patterns/import", patternPackage(t, "legacy", control.Program{Steps: []control.Step{{Color: "#000000"}}}))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	after, err := os.ReadFile(filepath.Join(v2Dir, "patterns.json"))
	if err != nil || !bytes.Equal(v2Bytes, after) {
		t.Fatal("legacy save changed v2 snapshot", err)
	}
	// Fresh construction opens neither library and preserves the full saved
	// high-level source, revision, recipe and frozen execution on subsequent GET.
	reopened := httpapi.New(patternV2NoIO{}, options...)
	w = patternHTTP(reopened, "GET", "/v2/patterns/orbit-red-blue", nil)
	if w.Code != 200 || !reflect.DeepEqual(patternV2JSON(t, w.Body.Bytes()), patternV2JSON(t, patternV2File(t, "responses/get-orbit-red-blue.json"))) {
		t.Fatal("reopen lost frozen record", w.Code, w.Body.String())
	}
}

func TestPatternV2DirectoryConfigurationFailureAndOptionRecovery(t *testing.T) {
	h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternDirectoryV2("relative-v2"))
	w := patternHTTP(h, "GET", "/v2/patterns", nil)
	patternV2Error(t, w, 503, "pattern_library_unavailable")
	if !strings.Contains(w.Body.String(), "relative-v2") {
		t.Fatal("missing configuration diagnostic", w.Body.String())
	}
	for _, route := range []struct {
		path string
		data []byte
	}{
		{"/v2/patterns/preview", patternV2File(t, "requests/preview-orbit-red-blue.json")},
		{"/v1/light/preview", []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`)},
	} {
		if w := patternHTTP(h, "POST", route.path, route.data); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	store := patternV2Store(t)
	for _, options := range [][]httpapi.Option{
		{httpapi.WithPatternDirectoryV2("relative-v2"), httpapi.WithPatternStoreV2(store)},
		{httpapi.WithPatternDirectoryV2("relative-v2"), httpapi.WithPatternDirectoryV2(store.Path())},
	} {
		w := patternHTTP(httpapi.New(patternV2NoIO{}, options...), "GET", "/v2/patterns", nil)
		if w.Code != http.StatusOK {
			t.Fatal("later valid option did not recover", w.Code, w.Body.String())
		}
	}
	var nilStore *patternstore.StoreV2
	for _, store := range []httpapi.PatternStoreV2{nil, nilStore, (*patternV2Spy)(nil)} {
		h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternDirectoryV2("relative-v2"), httpapi.WithPatternStoreV2(store))
		patternV2Error(t, patternHTTP(h, "GET", "/v2/patterns", nil), 501, "pattern_library_unsupported")
		w := patternHTTP(h, "POST", "/v2/patterns/preview", patternV2File(t, "requests/preview-orbit-red-blue.json"))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
