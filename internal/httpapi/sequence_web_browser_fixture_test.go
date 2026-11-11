package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

type browserFixtureBackend struct {
	mu                    sync.Mutex
	one, two              string
	jobPosts, sets, reads int
	stops                 int
}

func (*browserFixtureBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "fixture-0001", Product: "SOFTWARE FIXTURE (no USB)",
		Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold", "observe", "cancel"}}
}
func (b *browserFixtureBackend) Set(step control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sets++
	if step.LED == 0 || step.LED == 1 {
		b.one = step.Color
	}
	if step.LED == 0 || step.LED == 2 {
		b.two = step.Color
	}
	return nil
}
func (b *browserFixtureBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	return control.Observation{LED1: b.one, LED2: b.two, SampledAt: time.Now().UTC()}, nil
}
func (b *browserFixtureBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stops++
	if mode == "off" {
		b.one, b.two = "#000000", "#000000"
	}
	return nil
}
func (*browserFixtureBackend) Close() {}

type browserFixtureDriver struct{ backend *browserFixtureBackend }

func (d browserFixtureDriver) Open(string) (control.Backend, error) { return d.backend, nil }

// This opt-in test is a finite browser-development server. All device I/O is
// simulated; the normal nb1 executable receives no fixture option.
func TestSequenceWebBrowserFixture(t *testing.T) {
	dist := os.Getenv("NB1_V1_WEB_FIXTURE_DIST")
	if dist == "" {
		t.Skip("set NB1_V1_WEB_FIXTURE_DIST for the disposable browser fixture")
	}
	info, err := os.Lstat(dist)
	if err != nil || !filepath.IsAbs(dist) || !info.IsDir() {
		t.Fatal("fixture dist must be an absolute regular directory")
	}
	seconds := 180
	if text := os.Getenv("NB1_V1_WEB_FIXTURE_SECONDS"); text != "" {
		seconds, err = strconv.Atoi(text)
		if err != nil || seconds < 1 || seconds > 900 {
			t.Fatal("fixture duration must be 1 to 900 seconds")
		}
	}
	backend := &browserFixtureBackend{one: "#000000", two: "#000000"}
	engine, err := control.New(browserFixtureDriver{backend}, control.Options{Serials: []string{"fixture-0001"}, PollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	api := httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(filepath.Join(t.TempDir(), "library")))
	stop := make(chan struct{})
	var stopOnce sync.Once
	writeJSON := func(w http.ResponseWriter, value interface{}) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
			http.Error(w, "fixture is local and same-origin only", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/__fixture/stats" && r.Method == "GET" && r.URL.RawQuery == "" {
			backend.mu.Lock()
			stats := map[string]interface{}{"software_fixture": true, "job_posts": backend.jobPosts, "set_calls": backend.sets, "read_calls": backend.reads, "stop_calls": backend.stops}
			backend.mu.Unlock()
			writeJSON(w, stats)
			return
		}
		if r.URL.Path == "/__fixture/shutdown" && r.Method == "POST" {
			writeJSON(w, map[string]bool{"stopping": true})
			stopOnce.Do(func() { close(stop) })
			return
		}
		if r.URL.Path == "/demo" || strings.HasPrefix(r.URL.Path, "/demo/") {
			if r.Method != "GET" {
				w.Header().Set("Allow", "GET")
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
				http.Error(w, "static fixture accepts no body or query", http.StatusBadRequest)
				return
			}
			if r.URL.Path == "/demo" {
				http.Redirect(w, r, "/demo/", http.StatusTemporaryRedirect)
				return
			}
			for _, asset := range sequenceWebAssets {
				if asset.path != r.URL.Path {
					continue
				}
				path := filepath.Join(dist, asset.file)
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Size() > 256*1024 {
					http.Error(w, "fixture asset unavailable", http.StatusInternalServerError)
					return
				}
				file, err := os.Open(path)
				if err != nil {
					http.Error(w, "fixture asset unavailable", http.StatusInternalServerError)
					return
				}
				defer file.Close()
				w.Header().Set("Content-Type", asset.contentType)
				w.Header().Set("Content-Security-Policy", sequenceWebCSP)
				w.Header().Set("Referrer-Policy", "no-referrer")
				_, _ = io.Copy(w, io.LimitReader(file, 256*1024))
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v1/jobs" {
			backend.mu.Lock()
			backend.jobPosts++
			backend.mu.Unlock()
		}
		api.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer server.Close()
	expires := time.Now().Add(time.Duration(seconds) * time.Second)
	ready, err := json.Marshal(map[string]interface{}{"software_fixture": true, "origin": origin, "demo": origin + "/demo/", "serial": "fixture-0001", "stats": origin + "/__fixture/stats", "shutdown": origin + "/__fixture/shutdown", "expires_at": expires.UTC(), "dist": dist})
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("NB1_V1_WEB_FIXTURE_READY_FILE"); path != "" {
		if err := os.WriteFile(path, append(ready, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("NB1_V1_WEB_FIXTURE " + string(ready))
	timer := time.NewTimer(time.Until(expires))
	defer timer.Stop()
	select {
	case <-stop:
	case <-timer.C:
	case err := <-served:
		if err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	}
}
