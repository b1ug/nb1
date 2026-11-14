package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func cliPatternV2Input(t *testing.T, name string) (string, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "internal", "httpapi", "testdata", "pattern-v2", "requests", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestControlPatternsV2AuthoringWorkflowDoesNotPlayOrPrepare(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-v2")
	handler := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
	calls := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- r.Method + " " + r.URL.Path
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		return executePreviewCommand(context.Background(), append([]string{"--endpoint", server.URL, "patterns-v2"}, args...)...)
	}
	draft, _ := cliPatternV2Input(t, "preview-orbit-red-blue.json")
	text, err := run("preview-draft", draft)
	var preview daemonclient.PatternDraftPreviewV2
	if err != nil || json.Unmarshal([]byte(text), &preview) != nil || !preview.Verification.Verified {
		t.Fatal(text, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preview created library", err)
	}
	save, data := cliPatternV2Input(t, "save-orbit-red-blue.json")
	expected, err := patternsv2.PrepareSave(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		text, err := run("save", save)
		var result patternSaveOutputV2
		if err != nil || json.Unmarshal([]byte(text), &result) != nil || !result.PublicationConfirmed || result.PublicationUnknown || result.Result == nil || result.PatternRef.Revision != expected.Candidate().Record().Revision {
			t.Fatal(text, err)
		}
	}
	text, err = run("get", "orbit-red-blue")
	var got daemonclient.PatternRecordV2
	if err != nil || json.Unmarshal([]byte(text), &got) != nil {
		t.Fatal(text, err)
	}
	i, err := patternsv2.InspectRecord(got.Record)
	if err != nil || i.Record().Revision != expected.Candidate().Record().Revision || i.Record().Source.Contract != "nb1-light-action/experimental-v2" {
		t.Fatal("high-level source lost", text, err)
	}
	want := []string{"POST /v2/patterns/preview", "POST /v2/patterns", "POST /v2/patterns", "GET /v2/patterns/orbit-red-blue"}
	if len(calls) != len(want) {
		t.Fatal("unexpected requests, prepare or playback", len(calls))
	}
	for _, path := range want {
		if got := <-calls; got != path {
			t.Fatal(got, path)
		}
	}
}

func TestControlPatternsV2AuthoringGuardsBeforeFilesOrNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"--mode", "direct", "patterns-v2", "get", "id"},
		{"patterns-v2", "save", "does-not-exist", "--policy", "replace"},
		{"patterns-v2", "preview-draft", "does-not-exist", "--wait=false"},
		{"patterns-v2", "get", "id", "--request-id", ""},
		{"patterns-v2", "get", "id", "--expected-daemon-instance-id", ""},
	} {
		command := newControlCommand()
		// The application root provides this inherited flag. Register it in
		// this isolated command fixture so the guard, rather than Cobra's
		// unknown-flag rejection, is what the test actually exercises.
		command.PersistentFlags().Bool("wait", false, "fixture root flag")
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SilenceErrors, command.SilenceUsage = true, true
		command.SetArgs(append([]string{"--endpoint", "http://127.0.0.1:1"}, args...))
		err := command.ExecuteContext(context.Background())
		text := output.String()
		if err == nil || !strings.Contains(err.Error(), "Pattern v2") || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "contact daemon") || text != "" {
			t.Fatal("argument guard did not precede I/O", args, text, err)
		}
	}
}

func TestControlPatternsV2SavePrintsOriginalRefOnUnknownPublication(t *testing.T) {
	file, data := cliPatternV2Input(t, "save-orbit-red-blue.json")
	want, err := patternsv2.PrepareSave(data)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v2/patterns" {
			t.Error("unexpected execution or lookup", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "patterns-v2", "save", file)
	var result patternSaveOutputV2
	if err == nil || json.Unmarshal([]byte(text), &result) != nil || !result.PublicationUnknown || result.PublicationConfirmed || result.Result != nil || result.PatternRef.Revision != want.Candidate().Record().Revision || result.Error == "" {
		t.Fatal("uncertain save lost original identity", text, err)
	}
}
