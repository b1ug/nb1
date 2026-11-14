package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/httpapi"
)

func writeEffectFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "effect.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestControlEffectDaemonSendsIntentAndReceivesCompiledProgram(t *testing.T) {
	s := &commandService{}
	var mu sync.Mutex
	var envelope map[string]json.RawMessage
	handler := httpapi.New(s)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(data))
			mu.Lock()
			err = json.Unmarshal(data, &envelope)
			mu.Unlock()
			if err != nil {
				t.Error(err)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	path := writeEffectFile(t, `{"kind":"pulse","color":"#AaBbCc","cycles":2,"period_ms":800}`)
	output, err := executeReceiptCommand("--endpoint", server.URL, "effect", "20005601", path, "--policy", "queue")
	if err != nil || !strings.Contains(output, `"fade_ms": 380`) || !strings.Contains(output, `"repeat": 2`) || atomic.LoadInt32(&s.submitted) != 1 {
		t.Fatalf("effect CLI: %s %v calls=%d", output, err, s.submitted)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := envelope["effect"]; !ok {
		t.Fatal("daemon command did not send high-level intent")
	}
	if _, ok := envelope["program"]; ok {
		t.Fatal("daemon command compiled intent into a second raw payload")
	}
	if string(envelope["policy"]) != `"queue"` {
		t.Fatalf("policy lost: %s", envelope["policy"])
	}
}

func TestControlEffectUsesPreparedBindingAndReceiptWait(t *testing.T) {
	previous := waitComplete
	waitComplete = true
	defer func() { waitComplete = previous }()
	s := &receiptCommandService{}
	server := httptest.NewServer(httpapi.New(s))
	defer server.Close()
	path := writeEffectFile(t, `{"kind":"steady","color":"#102030"}`)
	output, err := executeReceiptCommand("--endpoint", server.URL, "effect", "20005601", path, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err != nil || !strings.Contains(output, `"status": "succeeded"`) || atomic.LoadInt32(&s.keyed) != 1 || atomic.LoadInt32(&s.lookups) == 0 || atomic.LoadInt32(&s.jobLookups) != 0 {
		t.Fatalf("effect did not reuse receipts/wait: %s %v keyed=%d lookup=%d jobs=%d", output, err, s.keyed, s.lookups, s.jobLookups)
	}
}

func TestControlEffectInvalidDirectInputPrecedesOwnership(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"kind":"unknown","color":"#102030"}`,
		`{"kind":"steady","color":"#102030","fade_ms":null}`,
		`{"kind":"steady","color":"#102030","cycles":0}`,
		`{"kind":"pulse","color":"#102030","fade_ms":0}`,
		`{"kind":"pulse","color":"#102030","cycles":0}`,
		`{"kind":"pulse","color":"#102030","period_ms":61}`,
		`{"kind":"steady","color":"#102030","led":3}`,
		`{"kind":"steady","color":"red"}`,
		`{"kind":"steady","color":"#102030","co\u006cor":"#102030"}`,
		`{"Kind":"steady","color":"#102030"}`,
		`{"kind":"steady","color":"#102030","unknown":1}`,
		`{"kind":"steady","color":"#102030"} {}`,
	} {
		path := writeEffectFile(t, body)
		lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
		_, err := executeReceiptCommand("--mode", "direct", "--lock-dir", lockDir, "effect", "not-a-device", path)
		if err == nil {
			t.Fatalf("invalid direct effect accepted: %s", body)
		}
		if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
			t.Fatalf("invalid effect reached ownership: %s: %v", body, err)
		}
	}
	lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
	_, err := executeReceiptCommand("--mode", "direct", "--lock-dir", lockDir, "effect", "not-a-device", "nonexistent.json", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err == nil || !strings.Contains(err.Error(), "request receipts belong to the daemon") {
		t.Fatalf("direct receipt not rejected before file/hardware: %v", err)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("direct receipt reached ownership: %v", err)
	}
}

func TestControlEffectInputSizeLimit(t *testing.T) {
	path := writeEffectFile(t, `{"kind":"steady","color":"#102030"}`+strings.Repeat(" ", httpapi.MaxBodyBytes))
	if _, err := readControlEffect(path); err == nil || !strings.Contains(err.Error(), "effect file exceeds 1 MiB") {
		t.Fatalf("oversized effect accepted: %v", err)
	}
}
