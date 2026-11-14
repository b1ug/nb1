package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func cliSnapshotInputV2(t *testing.T) (string, []byte) {
	t.Helper()
	return cliPatternV2Input(t, "import-known.snapshot.json")
}

func runSnapshotCLIV2(endpoint string, args ...string) (string, error) {
	return executePreviewCommand(context.Background(), append([]string{"--endpoint", endpoint, "patterns-v2"}, args...)...)
}

func TestControlPatternsV2SnapshotsRealHTTPWithoutDeviceOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	h := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
	calls := make(chan string, 16)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- r.Method + " " + r.URL.RequestURI()
		h.ServeHTTP(w, r)
	}))
	defer s.Close()
	blockedLock := previewFile(t, "not a directory")
	file, input := cliSnapshotInputV2(t)
	text, err := runSnapshotCLIV2(s.URL, "import", file, "--lock-dir", blockedLock)
	var imported patternImportOutputV2
	if err != nil || json.Unmarshal([]byte(text), &imported) != nil || !imported.PublicationConfirmed || imported.PublicationUnknown || len(imported.Requested) != 2 || imported.Result == nil {
		t.Fatal(text, err)
	}
	digest := sha256.Sum256(input)
	if imported.SnapshotSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatal("input digest lost")
	}
	records, err := patternsv2.DecodeSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := patternsv2.EncodeSnapshot(records)
	text, err = runSnapshotCLIV2(s.URL, "export", "--lock-dir", blockedLock)
	if err != nil || !bytes.Equal([]byte(text), canonical) {
		t.Fatal("stdout export changed", err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	text, err = runSnapshotCLIV2(s.URL, "export", records[0].ID, "--output", path, "--lock-dir", blockedLock)
	if err != nil || text != "" {
		t.Fatal(text, err)
	}
	data, err := os.ReadFile(path)
	want, _ := patternsv2.EncodeSnapshot([]patternsv2.Record{records[0]})
	if err != nil || !bytes.Equal(data, want) {
		t.Fatal("single record snapshot changed", err)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("output file permission", err)
	}
	if _, err := runSnapshotCLIV2(s.URL, "export", "--output", path); err == nil {
		t.Fatal("overwrote existing output")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, want) {
		t.Fatal("existing output changed")
	}
	missing := filepath.Join(t.TempDir(), "missing", "snapshot.json")
	if _, err := runSnapshotCLIV2(s.URL, "export", "--output", missing); err == nil {
		t.Fatal("created parent directories")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("created output parent", err)
	}
	if len(calls) != 5 {
		t.Fatal("unexpected prepare/preview/execution", len(calls))
	}
	for len(calls) > 0 {
		call := <-calls
		if !strings.HasPrefix(call, "GET /v2/patterns") && call != "POST /v2/patterns/import" {
			t.Fatal(call)
		}
	}
	if after, _ := os.ReadFile(blockedLock); string(after) != "not a directory" {
		t.Fatal("entered ownership path")
	}
}

func TestControlPatternsV2SnapshotGuardsBeforeFileOrNetwork(t *testing.T) {
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer s.Close()
	for _, args := range [][]string{
		{"--mode", "direct", "patterns-v2", "import", "missing"},
		{"--mode", "direct", "patterns-v2", "export"},
		{"patterns-v2", "import", "missing", "--policy", "reject"},
		{"patterns-v2", "export", "--policy", "replace"},
		{"patterns-v2", "import", "missing", "--wait=false"},
		{"patterns-v2", "export", "--wait=false"},
		{"patterns-v2", "import", "missing", "--request-id", ""},
		{"patterns-v2", "export", "--expected-daemon-instance-id", ""},
	} {
		command := newControlCommand()
		command.PersistentFlags().Bool("wait", false, "fixture")
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SilenceErrors = true
		command.SilenceUsage = true
		command.SetArgs(append([]string{"--endpoint", s.URL}, args...))
		err := command.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), "Pattern v2") || strings.Contains(err.Error(), "no such file") || output.Len() != 0 {
			t.Fatal(args, output.String(), err)
		}
	}
	for _, args := range [][]string{{"export", ""}, {"export", "--output", ""}, {"export", "../bad"}, {"import"}, {"import", "extra", "extra"}} {
		if _, err := runSnapshotCLIV2(s.URL, args...); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("guard contacted daemon")
	}
}

func TestControlPatternsV2ImportRetainsExactInputOnLostConfirmation(t *testing.T) {
	file, input := cliSnapshotInputV2(t)
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, input) || r.URL.Path != "/v2/patterns/import" {
			t.Error("request changed")
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer s.Close()
	text, err := runSnapshotCLIV2(s.URL, "import", file)
	var out patternImportOutputV2
	if err == nil || json.Unmarshal([]byte(text), &out) != nil || !out.PublicationUnknown || out.PublicationConfirmed || out.OriginalSnapshotJSON != string(input) || len(out.Requested) != 2 || out.Result != nil {
		t.Fatal(text, err)
	}
	var retained *daemonclient.PatternSnapshotImportErrorV2
	if !errors.As(err, &retained) || !bytes.Equal(retained.OriginalSnapshotJSON(), input) {
		t.Fatal("typed original input lost", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("automatic retry", calls)
	}
}

func TestControlPatternsV2SnapshotInvalidResponseNeverCreatesOutput(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "new.json")
	if _, err := runSnapshotCLIV2(s.URL, "export", "--output", path); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unvalidated response created output", err)
	}
}

func TestControlPatternsV2ReadOnlyPrecisionExportNeverImports(t *testing.T) {
	data, err := os.ReadFile("../internal/daemonclient/testdata/pattern-v2-precision-future.snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "GET" {
			t.Error("not readonly", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "future.json")
	if _, err := runSnapshotCLIV2(s.URL, "export", "--output", path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) {
		t.Fatal("precision/future source changed")
	}
	if _, err := runSnapshotCLIV2(s.URL, "import", path); !errors.Is(err, patternsv2.ErrUnsupported) {
		t.Fatal("read-only snapshot imported", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("unsupported import contacted daemon")
	}
}

func TestControlPatternsV2SnapshotInputRegularAndBounded(t *testing.T) {
	dir := t.TempDir()
	file, _ := cliSnapshotInputV2(t)
	link := filepath.Join(dir, "input-link.json")
	if err := os.Symlink(file, link); err == nil {
		if _, err := readPatternSnapshotFileV2(link); err == nil {
			t.Fatal("symlink input accepted")
		}
	}
	if _, err := readPatternSnapshotFileV2(dir); err == nil {
		t.Fatal("directory input accepted")
	}
	large := filepath.Join(dir, "large.json")
	if err := os.WriteFile(large, bytes.Repeat([]byte(" "), patternsv2.MaxBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPatternSnapshotFileV2(large); err == nil {
		t.Fatal("oversized input accepted")
	}
	text := "keep"
	target := filepath.Join(dir, "target.json")
	os.WriteFile(target, []byte(text), 0600)
	outputLink := filepath.Join(dir, "output-link.json")
	if err := os.Symlink(target, outputLink); err == nil {
		if err := writePatternExport(io.Discard, outputLink, []byte("replace")); err == nil {
			t.Fatal("symlink output overwritten")
		}
		got, _ := os.ReadFile(target)
		if string(got) != text {
			t.Fatal("symlink target changed")
		}
	}
}
