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
)

func TestControlPatternsV2ListDoesNotCreatePreviewPrepareOrPlay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	handler := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	text, err := executePreviewCommand(context.Background(), "--endpoint", server.URL, "patterns-v2", "list")
	var value daemonclient.PatternListV2
	if err != nil || json.Unmarshal([]byte(text), &value) != nil || value.Patterns == nil || len(value.Patterns) != 0 || value.Library.Path != dir || len(requests) != 1 || requests[0] != "GET /v2/patterns" {
		t.Fatal(text, err, requests)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("list created a local library", err)
	}
}

func TestControlPatternsV2ListGuardsWithoutNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"--mode", "direct", "patterns-v2", "list"},
		{"patterns-v2", "list", "extra"},
		{"patterns-v2", "list", "--policy", "reject"},
		{"patterns-v2", "list", "--request-id", "original", "--expected-daemon-instance-id", "instance"},
		{"patterns-v2", "list", "--wait=false"},
	} {
		command := newControlCommand()
		command.PersistentFlags().Bool("wait", false, "fixture inherited flag")
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SilenceErrors, command.SilenceUsage = true, true
		command.SetArgs(append([]string{"--endpoint", "http://127.0.0.1:1"}, args...))
		err := command.ExecuteContext(context.Background())
		if err == nil || strings.Contains(err.Error(), "contact daemon") || output.Len() != 0 {
			t.Fatal("invalid list flags reached network or printed data", args, output.String(), err)
		}
	}
}
