package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func snapshotV2Known(t *testing.T) []byte {
	t.Helper()
	return clientPatternV2Fixture(t, "requests/import-known.snapshot.json")
}

func snapshotV2Response(t *testing.T) PatternWriteResultV2 {
	t.Helper()
	input := snapshotV2Known(t)
	h := httpapi.New(nil, httpapi.WithPatternDirectoryV2(filepath.Join(t.TempDir(), "library")))
	r := httptest.NewRequest("POST", "http://127.0.0.1/v2/patterns/import", bytes.NewReader(input))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var value PatternWriteResultV2
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return value
}

func TestPatternV2SnapshotClientRealHTTPCreateOnlyRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	h := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
	calls := make(chan string, 16)
	input := append(snapshotV2Known(t), '\n', ' ')
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- r.Method + " " + r.URL.RequestURI()
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			if !bytes.Equal(body, input) {
				t.Error("original snapshot bytes changed")
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		h.ServeHTTP(w, r)
	}))
	defer s.Close()
	c, _ := New(s.URL)
	defer c.Close()
	empty, err := c.ExportPatternsV2(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read created missing library", err)
	}
	if got, err := patternsv2.DecodeSnapshot(empty); err != nil || len(got) != 0 {
		t.Fatal(err)
	}
	expected, _ := patternsv2.DecodeSnapshot(input)
	for pass := 0; pass < 2; pass++ {
		value, err := c.ImportPatternsV2(context.Background(), input)
		if err != nil || len(value.Records) != len(expected) || value.Total != len(expected) {
			t.Fatal(value, err)
		}
		if pass == 0 && len(value.Created) != len(expected) || pass == 1 && len(value.Existing) != len(expected) {
			t.Fatal(value)
		}
	}
	data, err := c.ExportPatternsV2(context.Background(), "")
	canonical, _ := patternsv2.EncodeSnapshot(expected)
	if err != nil || !bytes.Equal(data, canonical) {
		t.Fatal("export changed records", err)
	}
	for _, record := range expected {
		data, err := c.ExportPatternsV2(context.Background(), record.ID)
		one, _ := patternsv2.EncodeSnapshot([]patternsv2.Record{record})
		if err != nil || !bytes.Equal(data, one) {
			t.Fatal(record.ID, err)
		}
	}
	if len(calls) != 4+len(expected) {
		t.Fatal("unexpected extra preview/prepare/job calls", len(calls))
	}
	for len(calls) > 0 {
		call := <-calls
		if !strings.HasPrefix(call, "GET /v2/patterns") && call != "POST /v2/patterns/import" {
			t.Fatal(call)
		}
	}
}

func TestPatternV2SnapshotClientPreservesReadOnlyPrecisionAndRejectsImport(t *testing.T) {
	data, err := os.ReadFile("testdata/pattern-v2-precision-future.snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := patternsv2.InspectSnapshot(data)
	if err != nil || snapshot.Records()[0].Support().Verified {
		t.Fatal(err)
	}
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "GET" || r.URL.RequestURI() != "/v2/patterns?format=snapshot" {
			t.Error(r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer s.Close()
	c, _ := New(s.URL)
	defer c.Close()
	exported, err := c.ExportPatternsV2(context.Background(), "")
	if err != nil || !bytes.Equal(exported, data) {
		t.Fatal("future source/precision lost", err)
	}
	for _, literal := range []string{"9007199254740993", "9223372036854775807", "灯🌈"} {
		if !bytes.Contains(exported, []byte(literal)) {
			t.Fatal("value changed", literal)
		}
	}
	if _, err := c.ImportPatternsV2(context.Background(), exported); !errors.Is(err, patternsv2.ErrUnsupported) {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("unsupported import contacted daemon")
	}
	for _, input := range [][]byte{[]byte(`{"schema_version":2,"schema_version":2}`), []byte(`{"schema_version":2,"kind":"nb1-pattern-snapshot/experimental-v2","records":null}`), bytes.Replace(data, []byte(`"counter"`), []byte(`"计数"`), 1)} {
		if _, err := c.ImportPatternsV2(context.Background(), input); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := c.ExportPatternsV2(context.Background(), "../bad"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("invalid input contacted daemon")
	}
}

func TestPatternV2SnapshotClientExportsUnknownRecipeReadOnly(t *testing.T) {
	input := clientPatternV2Fixture(t, "responses/export-read-only.snapshot.json")
	snapshot, err := patternsv2.InspectSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	data := snapshot.CanonicalJSON()
	unknown := false
	for _, record := range snapshot.Records() {
		unknown = unknown || !record.Support().Verified
	}
	if !unknown {
		t.Fatal("fixture has no unknown recipe")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("not read-only")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer s.Close()
	c, _ := New(s.URL)
	defer c.Close()
	got, err := c.ExportPatternsV2(context.Background(), "")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("unknown recipe lost", err)
	}
}

func TestPatternV2SnapshotClientImportUncertaintyRetainsOriginalOnce(t *testing.T) {
	input := snapshotV2Known(t)
	for _, tc := range []struct {
		name    string
		status  int
		code    string
		unknown bool
		header  string
	}{
		{"known-rejection", 409, "pattern_conflict", false, ""},
		{"known-read-only", 409, "pattern_library_read_only", false, ""},
		{"uncertain", 503, "pattern_commit_uncertain", true, ""},
		{"unknown-code", 409, "future", true, ""},
		{"wrong-code-status", 400, "pattern_conflict", true, ""},
		{"redirect", 307, "pattern_conflict", true, "Location"},
		{"contradictory-empty-header", 409, "pattern_conflict", true, "X-NB1-Replayed"},
		{"contradictory-location", 409, "pattern_conflict", true, "Location"},
		{"malformed-confirmation", 200, "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/v2/patterns/import" || !bytes.Equal(body, input) {
					t.Error("request changed")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.header != "" {
					w.Header()[http.CanonicalHeaderKey(tc.header)] = []string{""}
				}
				w.WriteHeader(tc.status)
				if tc.code == "" {
					io.WriteString(w, `{}`)
				} else {
					fmt.Fprintf(w, `{"error":{"code":%q,"message":"fixture"}}`, tc.code)
				}
			}))
			defer s.Close()
			c, _ := New(s.URL)
			defer c.Close()
			_, err := c.ImportPatternsV2(context.Background(), input)
			var retained *PatternSnapshotImportErrorV2
			var base *PatternImportError
			if !errors.As(err, &retained) || !errors.As(err, &base) || base.PublicationUnknown != tc.unknown || !bytes.Equal(retained.OriginalSnapshotJSON(), input) || len(retained.RequestedRefs()) != 2 {
				t.Fatal(err)
			}
			copy := retained.OriginalSnapshotJSON()
			copy[0] = '!'
			refs := retained.RequestedRefs()
			refs[0].ID = "changed"
			if !bytes.Equal(retained.OriginalSnapshotJSON(), input) || retained.RequestedRefs()[0].ID == "changed" {
				t.Fatal("retained identity aliases caller")
			}
			if atomic.LoadInt32(&calls) != 1 {
				t.Fatal("automatic retry", calls)
			}
		})
	}
}

func TestPatternV2SnapshotClientImportLostAcknowledgmentNoReplayBody(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	defer c.Close()
	input := snapshotV2Known(t)
	calls := 0
	c.http.Transport = executionRoundTripV2(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil || r.Method != "POST" {
			t.Error("import can be replayed")
		}
		return nil, io.ErrUnexpectedEOF
	})
	_, err := c.ImportPatternsV2(context.Background(), input)
	var base *PatternImportError
	if !errors.As(err, &base) || !base.PublicationUnknown || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestPatternV2SnapshotClientRejectsChangedImportConfirmation(t *testing.T) {
	for _, mode := range []string{"record", "record-order", "created-missing", "created-duplicate", "created-foreign", "existing-overlap", "total", "null-records", "location-empty", "instance-empty", "replayed-empty", "status", "media", "duplicate-media", "duplicate-key"} {
		t.Run(mode, func(t *testing.T) {
			value := snapshotV2Response(t)
			switch mode {
			case "record":
				value.Records[0] = value.Records[1]
			case "record-order":
				value.Records[0], value.Records[1] = value.Records[1], value.Records[0]
			case "created-missing":
				value.Created = value.Created[1:]
			case "created-duplicate":
				value.Created[1] = value.Created[0]
			case "created-foreign":
				value.Created[0] = "other"
			case "existing-overlap":
				value.Existing = value.Created
				value.Created = []string{value.Created[0]}
			case "total":
				value.Total = 0
			case "null-records":
				value.Records = nil
			}
			data, _ := json.Marshal(value)
			if mode == "duplicate-key" {
				data = append([]byte(`{"schema_version":2,`), data[1:]...)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "location-empty":
					w.Header()["Location"] = []string{""}
				case "instance-empty":
					w.Header()["X-Nb1-Daemon-Instance-Id"] = []string{""}
				case "replayed-empty":
					w.Header()["X-Nb1-Replayed"] = []string{""}
				case "status":
					w.WriteHeader(201)
				case "media":
					w.Header().Set("Content-Type", "text/plain")
				case "duplicate-media":
					w.Header().Add("Content-Type", "application/json")
				}
				w.Write(data)
			}))
			defer s.Close()
			c, _ := New(s.URL)
			defer c.Close()
			_, err := c.ImportPatternsV2(context.Background(), snapshotV2Known(t))
			var failure *PatternImportError
			if !errors.As(err, &failure) || !failure.PublicationUnknown {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestPatternV2SnapshotClientRejectsUntrustedExports(t *testing.T) {
	canonical, _ := patternsv2.EncodeSnapshot(mustSnapshotV2Records(t, snapshotV2Known(t)))
	for _, mode := range []string{"whitespace", "wrong-id", "unsorted", "tampered", "wrong-kind", "duplicate-key", "nonascii-key", "too-large", "media", "duplicate-media", "status", "location-empty", "instance-empty", "replayed-empty"} {
		t.Run(mode, func(t *testing.T) {
			data := append([]byte(nil), canonical...)
			switch mode {
			case "whitespace":
				data = append(data, '\n')
			case "unsorted":
				var value struct {
					Kind    string            `json:"kind"`
					Records []json.RawMessage `json:"records"`
					Schema  int               `json:"schema_version"`
				}
				json.Unmarshal(data, &value)
				value.Records[0], value.Records[1] = value.Records[1], value.Records[0]
				data, _ = json.Marshal(value)
			case "tampered":
				data = bytes.Replace(data, []byte(`"title":`), []byte(`"title":"tampered","title":`), 1)
			case "wrong-kind":
				data = bytes.Replace(data, []byte(patternsv2.SnapshotKind), []byte("future"), 1)
			case "duplicate-key":
				data = append([]byte(`{"schema_version":2,`), data[1:]...)
			case "nonascii-key":
				data = bytes.Replace(data, []byte(`"title"`), []byte(`"标题"`), 1)
			case "too-large":
				data = bytes.Repeat([]byte(" "), patternsv2.MaxBytes+1)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "media":
					w.Header().Set("Content-Type", "text/plain")
				case "duplicate-media":
					w.Header().Add("Content-Type", "application/json")
				case "status":
					w.WriteHeader(201)
				case "location-empty":
					w.Header()["Location"] = []string{""}
				case "instance-empty":
					w.Header()["X-Nb1-Daemon-Instance-Id"] = []string{""}
				case "replayed-empty":
					w.Header()["X-Nb1-Replayed"] = []string{""}
				}
				w.Write(data)
			}))
			defer s.Close()
			c, _ := New(s.URL)
			defer c.Close()
			id := ""
			if mode == "wrong-id" {
				id = "other"
			}
			got, err := c.ExportPatternsV2(context.Background(), id)
			if err == nil || got != nil {
				t.Fatal("untrusted export accepted", mode, err)
			}
		})
	}
}

func mustSnapshotV2Records(t *testing.T, data []byte) []patternsv2.Record {
	t.Helper()
	r, e := patternsv2.DecodeSnapshot(data)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestPatternV2SnapshotClientEmptyImportAndMixedPartitions(t *testing.T) {
	value := snapshotV2Response(t)
	expected := mustSnapshotV2Records(t, snapshotV2Known(t))
	value.Existing = []string{value.Created[0]}
	value.Created = value.Created[1:]
	if err := validatePatternImportV2(value, expected); err != nil {
		t.Fatal(err)
	}
	value = PatternWriteResultV2{SchemaVersion: 2, Contract: httpapi.PatternLibraryV2Contract, Records: []json.RawMessage{}, Created: []string{}, Existing: []string{}, Total: 4}
	if err := validatePatternImportV2(value, []patternsv2.Record{}); err != nil {
		t.Fatal(err)
	}
}
