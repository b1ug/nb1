package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

func cliPatternFixture(t *testing.T) (*patternstore.Store, patterns.Record, []byte) {
	t.Helper()
	store, err := patternstore.New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	p := control.Program{Steps: []control.Step{{Color: "#123456"}}, Repeat: 2048, OnFinish: "hold"}
	r, err := patterns.NewRecord("saved", "Saved pattern", patterns.Source{Program: &p})
	if err != nil {
		t.Fatal(err)
	}
	data, err := patterns.EncodeSnapshot([]patterns.Record{r})
	if err != nil {
		t.Fatal(err)
	}
	return store, r, data
}

func TestControlPatternsLibraryWorkflowNoPlaybackAndNoOverwrite(t *testing.T) {
	store, record, data := cliPatternFixture(t)
	service := &lightCommandService{}
	server := httptest.NewServer(httpapi.New(service, httpapi.WithPatternStore(store)))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		return executePreviewCommand(context.Background(), append([]string{"--endpoint", server.URL, "patterns"}, args...)...)
	}
	text, err := run("import", file)
	var imported patternImportOutput
	if err != nil || json.Unmarshal([]byte(text), &imported) != nil || imported.PublicationUnknown || imported.Result == nil || len(imported.Result.Created) != 1 {
		t.Fatal(text, err)
	}
	if atomic.LoadInt32(&service.keyed) != 0 || atomic.LoadInt32(&service.submitted) != 0 || atomic.LoadInt32(&service.prepares) != 0 {
		t.Fatal("import played or prepared")
	}
	for _, args := range [][]string{{"list"}, {"info"}, {"get", record.ID}, {"preview", record.ID, "--revision", record.Revision, "--serial", "demo"}} {
		text, err := run(args...)
		if err != nil || text == "" {
			t.Fatal(args, text, err)
		}
	}
	text, err = run("export")
	if err != nil || !bytes.Equal([]byte(text), data) {
		t.Fatal(text, err)
	}
	output := filepath.Join(t.TempDir(), "snapshot.json")
	text, err = run("export", record.ID, "--output", output)
	got, _ := os.ReadFile(output)
	if err != nil || text != "" || !bytes.Equal(got, data) {
		t.Fatal(text, err)
	}
	if _, err = run("export", record.ID, "--output", output); err == nil {
		t.Fatal("existing export overwritten")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(after, data) {
		t.Fatal("existing output changed")
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "snapshot.json")
	if _, err = run("export", "--output", missingParent); err == nil {
		t.Fatal("created parent directory")
	}
	text, err = run("run", "demo", record.ID, "--revision", record.Revision)
	var result patternRunOutput
	if err != nil || json.Unmarshal([]byte(text), &result) != nil || !result.Accepted || result.OutcomeUnknown || result.Job == nil || result.Job.TotalSteps != 2048 || result.Policy != "reject" || result.RequestTicket == nil || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.keyed) != 1 || atomic.LoadInt32(&service.submitted) != 0 {
		t.Fatal(text, err)
	}
	text, err = run("run", "demo", record.ID, "--revision", record.Revision, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err != nil || atomic.LoadInt32(&service.prepares) != 1 || atomic.LoadInt32(&service.keyed) != 2 {
		t.Fatal(text, err)
	}
	if _, err = executePreviewCommand(context.Background(), "--endpoint", server.URL, "set", "demo", "#123456"); err != nil || service.policyValue() != "replace" {
		t.Fatal("old CLI default changed", err)
	}
}

func TestControlPatternsGuardsBeforeFileNetworkAndOwnership(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	ref := "sha256:" + strings.Repeat("0", 64)
	for _, args := range [][]string{{"export", ""}, {"export", "--output", ""}} {
		if _, err := executePreviewCommand(context.Background(), append([]string{"--endpoint", server.URL, "patterns"}, args...)...); err == nil {
			t.Fatal("accepted explicitly empty export target", args)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("empty export target reached HTTP")
	}
	for _, args := range [][]string{
		{"--mode", "direct", "patterns", "info"}, {"--mode", "direct", "patterns", "import", "missing"}, {"--mode", "direct", "patterns", "run", "demo", "saved", "--revision", ref},
		{"patterns", "run", "demo", "saved"}, {"patterns", "run", "demo", "saved", "--revision", "latest"}, {"patterns", "run", "demo", "saved", "--revision", ref, "--policy", "replace"}, {"patterns", "run", "demo", "saved", "--revision", ref, "--policy", "queue"},
		{"patterns", "run", "demo", "saved", "--revision", ref, "--request-id", "x"}, {"patterns", "get", "saved", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance}, {"patterns", "list", "--policy", "reject"},
	} {
		lock := filepath.Join(t.TempDir(), "never-created")
		all := append([]string{"--endpoint", server.URL, "--lock-dir", lock}, args...)
		text, err := executePreviewCommand(context.Background(), all...)
		if err == nil || text != "" || strings.Contains(err.Error(), "no such file") {
			t.Fatal(args, text, err)
		}
		if _, err = os.Stat(lock); !os.IsNotExist(err) {
			t.Fatal("guard acquired ownership", err)
		}
	}
	for _, input := range []string{`{"schema_version":1e0,"patterns":[]}`, `{"schema_version":1,"patterns":null}`, strings.Repeat(" ", patterns.MaxPackageBytes+1)} {
		path := previewFile(t, input)
		if text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "patterns", "import", path); err == nil || text != "" {
			t.Fatal(text, err)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal(calls)
	}
}

func TestControlPatternWaitUsesOriginalJobEvenWhenLibraryDisappears(t *testing.T) {
	previous := waitComplete
	waitComplete = true
	defer func() { waitComplete = previous }()
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			store, record, data := cliPatternFixture(t)
			if _, err := store.Import(data); err != nil {
				t.Fatal(err)
			}
			service := &lightCommandService{running: failure}
			handler := httpapi.New(service, httpapi.WithPatternStore(store))
			var previews, writes, lookups int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/programs/preview" {
					atomic.AddInt32(&previews, 1)
				}
				if strings.Contains(r.URL.Path, "/requests/") {
					if atomic.AddInt32(&lookups, 1) > 1 && failure {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":{"code":"unavailable","message":"fixture unavailable"}}`)
						return
					}
				}
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/jobs") {
					atomic.AddInt32(&writes, 1)
					handler.ServeHTTP(w, r)
					if err := os.WriteFile(filepath.Join(store.Path(), "patterns.json"), []byte("corrupt after admission"), 0600); err != nil {
						t.Error(err)
					}
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "patterns", "run", "demo", record.ID, "--revision", record.Revision)
			var value patternRunOutput
			if json.Unmarshal([]byte(text), &value) != nil || !value.Accepted || value.Job == nil || value.RequestTicket == nil || atomic.LoadInt32(&previews) != 1 || atomic.LoadInt32(&writes) != 1 || atomic.LoadInt32(&service.history) != 0 || atomic.LoadInt32(&service.cancels) != 0 {
				t.Fatal(text, err)
			}
			if failure {
				if err == nil || !value.OutcomeUnknown || value.Job.Status != "running" || value.Job.ID != "job-light" {
					t.Fatal(text, err)
				}
			} else if err != nil || value.OutcomeUnknown || value.Job.Status != "succeeded" {
				t.Fatal(text, err)
			}
		})
	}
}

func TestControlPatternLostSubmissionAndImportResponsesNeverRetry(t *testing.T) {
	for _, operation := range []string{"run", "import"} {
		t.Run(operation, func(t *testing.T) {
			store, record, data := cliPatternFixture(t)
			service := &lightCommandService{}
			if operation == "run" {
				if _, err := store.Import(data); err != nil {
					t.Fatal(err)
				}
			}
			handler := httpapi.New(service, httpapi.WithPatternStore(store))
			var writes int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/jobs") || r.URL.Path == "/v1/patterns/import") {
					atomic.AddInt32(&writes, 1)
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, r)
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			args := []string{"--endpoint", server.URL, "patterns", "run", "demo", record.ID, "--revision", record.Revision}
			if operation == "import" {
				file := filepath.Join(t.TempDir(), "package.json")
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"--endpoint", server.URL, "patterns", "import", file}
			}
			text, err := executePreviewCommand(context.Background(), args...)
			if err == nil || atomic.LoadInt32(&writes) != 1 {
				t.Fatal(text, err, writes)
			}
			if operation == "run" {
				var value patternRunOutput
				if json.Unmarshal([]byte(text), &value) != nil || value.Accepted || !value.OutcomeUnknown || value.RequestTicket == nil || atomic.LoadInt32(&service.keyed) != 1 {
					t.Fatal(text, err)
				}
			} else {
				var value patternImportOutput
				if json.Unmarshal([]byte(text), &value) != nil || !value.PublicationUnknown || value.Result != nil || len(value.Requested) != 1 || value.Requested[0].Revision != record.Revision || atomic.LoadInt32(&service.keyed) != 0 {
					t.Fatal(text, err)
				}
			}
		})
	}
}
