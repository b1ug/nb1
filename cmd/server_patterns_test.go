package cmd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternstore"
)

func TestRuntimeForwardsPatternOptionWithoutCreatingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	v2Dir := filepath.Join(t.TempDir(), "library-v2")
	backend := &lifecycleBackend{set: make(chan struct{})}
	engine, err := control.New(lifecycleDriver{backend}, control.Options{Serials: []string{"test-device"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := runtimeOutput{line: make(chan []byte, 64)}
	done := make(chan error, 1)
	go func() {
		done <- serveRuntime(ctx, listener, engine, output, 2*time.Second,
			httpapi.WithPatternDirectory(dir), httpapi.WithPatternDirectoryV2(v2Dir))
	}()
	var started struct {
		URL string `json:"url"`
	}
	select {
	case line := <-output.line:
		if err = json.Unmarshal(line, &started); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not announce local fixture listener")
	}
	response, err := http.Get(started.URL + "/v1/patterns")
	if err != nil {
		t.Fatal(err)
	}
	var list httpapi.PatternList
	err = json.NewDecoder(response.Body).Decode(&list)
	response.Body.Close()
	if response.StatusCode != 200 || err != nil || list.Library.Path != dir {
		t.Fatal(response.StatusCode, list, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("runtime startup/read created library: %v", err)
	}
	response, err = http.Get(started.URL + "/v2/patterns")
	if err != nil {
		t.Fatal(err)
	}
	var v2List struct {
		Library  struct{ Path string }
		Patterns []json.RawMessage
	}
	err = json.NewDecoder(response.Body).Decode(&v2List)
	response.Body.Close()
	if response.StatusCode != 200 || err != nil || v2List.Library.Path != v2Dir || len(v2List.Patterns) != 0 {
		t.Fatal(response.StatusCode, v2List, err)
	}
	if _, err = os.Stat(v2Dir); !os.IsNotExist(err) {
		t.Fatalf("runtime startup/read created v2 library: %v", err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fixture runtime did not stop")
	}
}

func TestServerPatternDefaultConfigurationIsPure(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	want, err := patternstore.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(nil, httpapi.WithPatternDirectory(""))
	if _, err = os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("configuration created directory: %v", err)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/patterns", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var value httpapi.PatternList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Library.Path != want || len(value.Patterns) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("read created directory: %v", err)
	}
}
