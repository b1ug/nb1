package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// Also advertise both execution extensions, so the tests do not accidentally
// pass simply because the handler cannot find an executor interface.
type patternV2NoIO struct{ noPreviewIO }

func (patternV2NoIO) SubmitWithReceipt(context.Context, string, control.Program, string, control.ReceiptRequest) (control.ReceiptResult, error) {
	panic("pattern library submitted a sequential job")
}
func (patternV2NoIO) Request(string, string, string) (control.Job, error) {
	panic("pattern library queried a receipt")
}
func (patternV2NoIO) SubmitFrameWithReceipt(context.Context, string, control.FrameProgram, string, control.ReceiptRequest) (control.FrameReceiptResult, error) {
	panic("pattern library submitted a frame job")
}
func (patternV2NoIO) FrameJob(string) (control.FrameJob, error) {
	panic("pattern library queried a frame job")
}
func (patternV2NoIO) CancelFrame(context.Context, string) (control.FrameJob, error) {
	panic("pattern library cancelled a frame job")
}
func (patternV2NoIO) FrameRequest(string, string, string) (control.FrameJob, error) {
	panic("pattern library queried a frame receipt")
}

var _ control.ReceiptService = patternV2NoIO{}
var _ control.FrameService = patternV2NoIO{}

type patternV2Spy struct {
	store *patternstore.StoreV2
	allow map[string]bool
	calls map[string]int
	input []byte
	fail  error
}

func (s *patternV2Spy) call(name string) {
	if !s.allow[name] {
		panic("unexpected Pattern v2 store call: " + name)
	}
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[name]++
}
func (s *patternV2Spy) Path() string {
	s.call("Path")
	return "/example/user-config/nb1/patterns-v2"
}
func (s *patternV2Spy) List() ([]patternsv2.Inspection, error) {
	s.call("List")
	if s.fail != nil {
		return nil, s.fail
	}
	return s.store.List()
}
func (s *patternV2Spy) Get(id string) (patternsv2.Inspection, error) {
	s.call("Get")
	if s.fail != nil {
		return patternsv2.Inspection{}, s.fail
	}
	return s.store.Get(id)
}
func (s *patternV2Spy) Resolve(string, string) (patternsv2.Record, error) {
	panic("non-execution HTTP called Resolve")
}
func (s *patternV2Spy) Export() ([]byte, error) {
	s.call("Export")
	if s.fail != nil {
		return []byte("must-not-leak"), s.fail
	}
	return s.store.Export()
}
func (s *patternV2Spy) Import(data []byte) (patternstore.ImportResultV2, error) {
	s.call("Import")
	s.input = append([]byte(nil), data...)
	if s.fail != nil {
		return patternstore.ImportResultV2{Created: []string{"must-not-leak"}, Total: 99}, s.fail
	}
	return s.store.Import(data)
}

func patternV2File(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "pattern-v2", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func patternV2Store(t *testing.T) *patternstore.StoreV2 {
	t.Helper()
	s, err := patternstore.NewV2(filepath.Join(t.TempDir(), "library-v2"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func patternV2Seed(t *testing.T, s *patternstore.StoreV2, data []byte) {
	t.Helper()
	// Explicit test fixture only: unsupported records must not be publishable
	// through Import, but may already exist on disk for read-only inspection.
	if err := os.Mkdir(s.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Path(), "patterns.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func patternV2JSON(t *testing.T, data []byte) interface{} {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value interface{}
	if err := d.Decode(&value); err != nil {
		t.Fatal(err, string(data))
	}
	if err := d.Decode(new(interface{})); err != io.EOF {
		t.Fatal("extra response data", err)
	}
	return value
}
func patternV2Error(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var value httpapi.ErrorResponse
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Error.Code != code || value.Error.Message == "" {
		t.Fatalf("status=%d want=%d code=%s body=%s", w.Code, status, code, w.Body.String())
	}
	m, ok := patternV2JSON(t, w.Body.Bytes()).(map[string]interface{})
	if !ok || len(m) != 1 || m["error"] == nil || w.Header().Get("Location") != "" {
		t.Fatal("error leaked success fields or Location", w.Body.String(), w.Header())
	}
}

// Execute the previously proposed cases against the actual handler and private
// fixture store. Golden HTTP error messages are examples, not byte contracts.
func TestPatternV2PlannedHTTPGoldens(t *testing.T) {
	var plan struct {
		Cases []struct {
			Name           string            `json:"name"`
			Method         string            `json:"method"`
			Path           string            `json:"path"`
			Status         int               `json:"response_status"`
			RequestFile    string            `json:"request_body_file"`
			ResponseFile   string            `json:"response_body_file"`
			ResponseHeader map[string]string `json:"response_headers"`
			Location       string            `json:"location"`
			Canonical      bool              `json:"canonical_bytes_exact"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(patternV2File(t, "http-cases.json"), &plan); err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s := patternV2Store(t)
			switch c.Name {
			case "same record already exists":
				candidate, err := patternsv2.PrepareSave(patternV2File(t, "requests/save-orbit-red-blue.json"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Import(candidate.SnapshotJSON()); err != nil {
					t.Fatal(err)
				}
			case "read ready library", "read known record", "export known snapshot":
				if _, err := s.Import(patternV2File(t, "requests/import-known.snapshot.json")); err != nil {
					t.Fatal(err)
				}
			case "read library with unsupported item", "read unsupported record", "export read-only snapshot", "export unsupported record", "read-only blocks save":
				patternV2Seed(t, s, patternV2File(t, "responses/export-read-only.snapshot.json"))
			case "mismatched directory format":
				legacy, err := os.ReadFile("../patternsv2/testdata/design/legacy-v1.snapshot.json")
				if err != nil {
					t.Fatal(err)
				}
				patternV2Seed(t, s, legacy)
			}
			spy := &patternV2Spy{store: s, allow: make(map[string]bool)}
			if c.Method == "GET" {
				if c.Path == "/v2/patterns" {
					spy.allow["List"], spy.allow["Path"] = true, true
				} else if c.Path == "/v2/patterns?format=snapshot" {
					spy.allow["Export"] = true
				} else {
					spy.allow["Get"] = true
				}
			} else if c.Name == "new record" || c.Name == "same record already exists" || c.Name == "read-only blocks save" || c.Name == "import complete verified snapshot" || c.Name == "publication result unknown" {
				spy.allow["Import"] = true
			}
			if c.Name == "publication result unknown" {
				spy.fail = patternstore.ErrCommitUncertain
			}
			h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy))
			var request []byte
			if c.RequestFile != "" {
				request = patternV2File(t, c.RequestFile)
			}
			w := patternHTTP(h, c.Method, c.Path, request)
			want := patternV2File(t, c.ResponseFile)
			if c.Status >= 400 {
				var expected httpapi.ErrorResponse
				if err := json.Unmarshal(want, &expected); err != nil {
					t.Fatal(err)
				}
				patternV2Error(t, w, c.Status, expected.Error.Code)
			} else if c.Canonical {
				if w.Code != c.Status || !bytes.Equal(w.Body.Bytes(), want) {
					t.Fatal("canonical snapshot bytes differ", w.Code, w.Body.String())
				}
			} else if w.Code != c.Status || !reflect.DeepEqual(patternV2JSON(t, w.Body.Bytes()), patternV2JSON(t, want)) {
				t.Fatal("response differs from pre-implementation golden", w.Code, w.Body.String())
			}
			for name, value := range c.ResponseHeader {
				if w.Header().Get(name) != value {
					t.Fatal("header differs", name, w.Header())
				}
			}
			if w.Header().Get("Location") != c.Location || w.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
				t.Fatal("unexpected identity/location", w.Header())
			}
			for name := range spy.allow {
				wantCalls := 1
				if name == "Path" && c.Status >= 400 {
					wantCalls = 0
				}
				if spy.calls[name] != wantCalls {
					t.Fatal("wrong store call count", name, spy.calls)
				}
			}
			if c.Path == "/v2/patterns/import" && c.Status < 400 && !bytes.Equal(spy.input, request) {
				t.Fatal("import bytes were re-encoded")
			}
			if len(spy.allow) == 0 {
				if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("pure/invalid authoring created directory", err)
				}
			}
		})
	}
}

func TestPatternV2StrictHTTPBoundaryBeforeStore(t *testing.T) {
	preview := patternV2File(t, "requests/preview-orbit-red-blue.json")
	save := patternV2File(t, "requests/save-orbit-red-blue.json")
	for _, c := range []struct{ method, path string }{{"GET", "/v2/patterns"}, {"GET", "/v2/patterns/x"}, {"POST", "/v2/patterns"}, {"POST", "/v2/patterns/preview"}, {"POST", "/v2/patterns/import"}} {
		for _, query := range []string{"?", "?&", "?bad=1", "?format=", "?format=snapshot&format=snapshot", "?format=%ZZ"} {
			t.Run(c.method+c.path+query, func(t *testing.T) {
				h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
				var body []byte
				if c.method == "POST" {
					body = save
				}
				patternV2Error(t, patternHTTP(h, c.method, c.path+query, body), 400, "invalid_pattern_request")
			})
		}
	}
	for _, path := range []string{"/v2/patterns", "/v2/patterns/preview", "/v2/patterns/import"} {
		for _, media := range [][]string{nil, {"text/plain"}, {"application/json", "application/json"}, {"application/json, application/json"}} {
			h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
			r := httptest.NewRequest("POST", "http://127.0.0.1"+path, bytes.NewReader(preview))
			for _, value := range media {
				r.Header.Add("Content-Type", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			patternV2Error(t, w, 415, "json_required")
		}
		h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
		reader := &patternV2CountingBody{left: patternsv2.MaxBytes + 100}
		r := httptest.NewRequest("POST", "http://127.0.0.1"+path, reader)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		patternV2Error(t, w, 413, "pattern_package_too_large")
		if reader.read != patternsv2.MaxBytes+1 || !r.Close || w.Header().Get("Connection") != "close" {
			t.Fatal("oversize input not bounded/closed", reader.read, r.Close, w.Header())
		}
		r = httptest.NewRequest("POST", "http://127.0.0.1"+path, patternV2ErrorReader{})
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		patternV2Error(t, w, 400, "invalid_pattern_request")
	}
	exact := append(bytes.Repeat([]byte(" "), patternsv2.MaxBytes-len(preview)), preview...)
	if w := patternHTTP(httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{})), "POST", "/v2/patterns/preview", exact); w.Code != 200 {
		t.Fatal("exactly 1MiB valid preview rejected", w.Code, w.Body.String())
	}
	for _, length := range []int64{1, -1, 0} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/v2/patterns", nil)
		r.ContentLength = length
		if length == 0 {
			r.TransferEncoding = []string{"chunked"}
		}
		w := httptest.NewRecorder()
		httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{})).ServeHTTP(w, r)
		patternV2Error(t, w, 400, "invalid_pattern_request")
	}
	for _, bad := range []string{
		strings.Replace(string(preview), "红蓝环绕", `\ud800`, 1),
		strings.Replace(string(preview), "红蓝环绕", string([]byte{0xff}), 1),
		strings.Replace(string(preview), `"title":`, `"t\u0069tle":"duplicate","title":`, 1),
		strings.Replace(string(preview), "3000", "3000.0", 1),
		string(preview) + " {}",
	} {
		h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
		patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/preview", []byte(bad)), 400, "invalid_pattern_request")
	}
	// Host/Origin guard must run before even body reads.
	for _, origin := range []string{"http://evil.example", "null"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/v2/patterns", patternV2PanicReader{})
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{})).ServeHTTP(w, r)
		patternV2Error(t, w, 403, "local_only")
	}
	r := httptest.NewRequest("POST", "http://evil.example/v2/patterns", patternV2PanicReader{})
	w := httptest.NewRecorder()
	httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{})).ServeHTTP(w, r)
	patternV2Error(t, w, 403, "local_only")
}

type patternV2CountingBody struct{ left, read int }

func (r *patternV2CountingBody) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.left {
		n = r.left
	}
	for i := 0; i < n; i++ {
		p[i] = ' '
	}
	r.left, r.read = r.left-n, r.read+n
	return n, nil
}

type patternV2PanicReader struct{}

func (patternV2PanicReader) Read([]byte) (int, error) { panic("guard read body") }

type patternV2ErrorReader struct{}

func (patternV2ErrorReader) Read(p []byte) (int, error) {
	return copy(p, []byte("{")), io.ErrUnexpectedEOF
}

func TestPatternV2UnavailableStorageAndLegacyIsolation(t *testing.T) {
	legacy := patternStore(t)
	h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStore(legacy))
	if w := patternHTTP(h, "POST", "/v2/patterns/preview", patternV2File(t, "requests/preview-orbit-red-blue.json")); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range []struct{ method, path, file string }{
		{"GET", "/v2/patterns", ""}, {"GET", "/v2/patterns/orbit-red-blue", ""},
		{"GET", "/v2/patterns?format=snapshot", ""}, {"POST", "/v2/patterns", "requests/save-orbit-red-blue.json"},
		{"POST", "/v2/patterns/import", "requests/import-known.snapshot.json"},
	} {
		var body []byte
		if c.file != "" {
			body = patternV2File(t, c.file)
		}
		w := patternHTTP(h, c.method, c.path, body)
		patternV2Error(t, w, 501, "pattern_library_unsupported")
		if !strings.Contains(w.Body.String(), "not configured") || !strings.Contains(w.Body.String(), "preview") {
			t.Fatal("unconfigured storage response is ambiguous", w.Body.String())
		}
	}
	old := patternHTTP(h, "GET", "/v1/patterns", nil)
	var oldValue httpapi.PatternList
	if old.Code != 200 || json.Unmarshal(old.Body.Bytes(), &oldValue) != nil || oldValue.SchemaVersion != 1 || oldValue.Contract != httpapi.PatternLibraryContract {
		t.Fatal("v1 library changed", old.Code, old.Body.String())
	}
	if _, err := os.Stat(legacy.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("v2 fell back to v1 directory", err)
	}
	unknown := []byte(`{"id":"future","title":"未来","source":{"kind":"light_action","contract":"future/v3","action":{}}}`)
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/preview", unknown), 501, "pattern_source_unsupported")
}

func TestPatternV2CreateOnlyAndWholeImportFailure(t *testing.T) {
	s := patternV2Store(t)
	spy := &patternV2Spy{store: s, allow: map[string]bool{"Import": true}}
	h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy))
	w := patternHTTP(h, "POST", "/v2/patterns", patternV2File(t, "requests/save-orbit-red-blue.json"))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	r, err := patternsv2.DecodeRecord(patternV2File(t, "responses/get-orbit-red-blue.json"))
	if err == nil || r.ID != "" {
		t.Fatal("HTTP wrapper unexpectedly accepted as Record")
	}
	candidate, err := patternsv2.PrepareSave(patternV2File(t, "requests/save-orbit-red-blue.json"))
	if err != nil {
		t.Fatal(err)
	}
	r = candidate.Candidate().Record()
	changed, err := patternsv2.NewRecord(r.ID, "Changed title", r.Source)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]interface{}{"id": changed.ID, "title": changed.Title, "source": changed.Source, "expected_revision": changed.Revision})
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns", request), 409, "pattern_conflict")
	first, err := patternsv2.NewRecord("aaa-first", r.Title, r.Source)
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := patternsv2.EncodeSnapshot([]patternsv2.Record{first, changed})
	if err != nil {
		t.Fatal(err)
	}
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/import", mixed), 409, "pattern_conflict")
	after, err := s.Export()
	if err != nil || !bytes.Equal(before, after) || spy.calls["Import"] != 3 {
		t.Fatal("conflicting save/import rewrote library or retried", err, spy.calls)
	}
	if _, err := s.Get(first.ID); !errors.Is(err, patternstore.ErrNotFound) {
		t.Fatal("partial import leaked first record", err)
	}
}

func TestPatternV2UnknownSourceReadonlyAndExactExport(t *testing.T) {
	data := patternV2File(t, "responses/export-read-only.snapshot.json")
	// This fixture has integer-only numbers, no HTML characters, ASCII keys and
	// scalar UTF-8 values: encoding/json's sorted map output equals P1 canonical.
	// Rehashing a future source makes a valid opaque read-only record, not a
	// candidate verified by today's source compiler.
	var snapshot map[string]interface{}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	record := snapshot["records"].([]interface{})[0].(map[string]interface{})
	record["source"].(map[string]interface{})["contract"] = "future/v3"
	delete(record, "revision")
	payload, _ := json.Marshal(record)
	record["revision"] = fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte(patternsv2.RecordContract+"\n"), payload...)))
	data, _ = json.Marshal(snapshot)
	inspected, err := patternsv2.InspectSnapshot(data)
	if err != nil || inspected.Records()[0].Support().Reason != "unsupported_source" {
		t.Fatal("invalid future-source fixture", err)
	}
	s := patternV2Store(t)
	patternV2Seed(t, s, inspected.CanonicalJSON())
	spy := &patternV2Spy{store: s, allow: map[string]bool{"Get": true, "Export": true, "List": true, "Path": true, "Import": true}}
	h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy))
	get := patternHTTP(h, "GET", "/v2/patterns/orbit-red-blue", nil)
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"reason":"unsupported_source"`) || spy.calls["List"] != 0 {
		t.Fatal("unknown GET was promoted or looked up library state", get.Code, get.Body.String(), spy.calls)
	}
	known := patternHTTP(h, "GET", "/v2/patterns/pulse-lime", nil)
	if known.Code != 200 || !strings.Contains(known.Body.String(), `"verified":true`) || spy.calls["List"] != 0 {
		t.Fatal("another unknown item changed this record's verification", known.Code, known.Body.String(), spy.calls)
	}
	list := patternHTTP(h, "GET", "/v2/patterns", nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"state":"read_only"`) {
		t.Fatal(list.Code, list.Body.String())
	}
	export := patternHTTP(h, "GET", "/v2/patterns?format=snapshot", nil)
	if export.Code != 200 || !bytes.Equal(export.Body.Bytes(), data) {
		t.Fatal("unknown export changed bytes", export.Code, export.Body.String())
	}
	one := patternHTTP(h, "GET", "/v2/patterns/orbit-red-blue?format=snapshot", nil)
	oneSnapshot, err := patternsv2.InspectSnapshot(one.Body.Bytes())
	if one.Code != 200 || err != nil || len(oneSnapshot.Records()) != 1 || oneSnapshot.Records()[0].Support().Verified || !bytes.Equal(oneSnapshot.Records()[0].CanonicalJSON(), inspected.Records()[0].CanonicalJSON()) {
		t.Fatal("unknown single export lost frozen record", one.Code, err)
	}
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/import", data), 400, "invalid_pattern_request")
	empty, _ := patternsv2.EncodeSnapshot(nil)
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/import", empty), 409, "pattern_library_read_only")
	patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns", patternV2File(t, "requests/save-pulse-lime.json")), 409, "pattern_library_read_only")
	after, err := s.Export()
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("read-only library was rewritten", err)
	}
}

func TestPatternV2InvalidImportDoesNotCreateLibrary(t *testing.T) {
	valid := patternV2File(t, "requests/import-known.snapshot.json")
	legacy, err := os.ReadFile("../patternsv2/testdata/design/legacy-v1.snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"authoring-preview":  patternV2File(t, "requests/preview-orbit-red-blue.json"),
		"authoring-save":     patternV2File(t, "requests/save-orbit-red-blue.json"),
		"legacy-v1":          legacy,
		"unsupported-recipe": patternV2File(t, "responses/export-read-only.snapshot.json"),
		"bad-revision":       bytes.Replace(valid, []byte("sha256:95c165aeacf05ef6ec8fb815afb7f04b727162e6bb28744f20ad1fa57efb0f87"), []byte("sha256:"+strings.Repeat("0", 64)), 1),
		"duplicate":          []byte(`{"schema_version":2,"schema_version":2,"kind":"nb1-pattern-snapshot/experimental-v2","records":[]}`),
		"null":               []byte(`{"schema_version":2,"kind":"nb1-pattern-snapshot/experimental-v2","records":null}`),
		"tail":               append(append([]byte(nil), valid...), []byte(" {}")...),
		"invalid-UTF8":       bytes.Replace(valid, []byte("红蓝环绕"), []byte{0xff}, 1),
	} {
		t.Run(name, func(t *testing.T) {
			s := patternV2Store(t)
			spy := &patternV2Spy{store: s, allow: map[string]bool{"Import": true}}
			h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy))
			patternV2Error(t, patternHTTP(h, "POST", "/v2/patterns/import", input), 400, "invalid_pattern_request")
			if spy.calls["Import"] != 1 || !bytes.Equal(spy.input, input) {
				t.Fatal("HTTP preprocessed or retried the rejected package", spy.calls)
			}
			if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected frozen package created library", err)
			}
		})
	}
}

func TestPatternV2ErrorMappingAndNoPartialSuccess(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{patternstore.ErrInvalid, 400, "invalid_pattern_request"}, {patternstore.ErrLimit, 409, "pattern_library_limit"},
		{patternstore.ErrNotFound, 404, "pattern_not_found"}, {patternstore.ErrConflict, 409, "pattern_conflict"},
		{patternstore.ErrRevisionConflict, 409, "pattern_revision_conflict"}, {patternstore.ErrBusy, 409, "pattern_library_busy"},
		{patternstore.ErrUnsupported, 501, "pattern_library_unsupported"}, {patternstore.ErrReadOnly, 409, "pattern_library_read_only"},
		{patternstore.ErrFormatMismatch, 503, "pattern_library_format_mismatch"}, {patternstore.ErrUnsafe, 503, "pattern_library_unavailable"},
		{patternstore.ErrCorrupt, 503, "pattern_library_unavailable"}, {errors.New("I/O failed"), 503, "pattern_library_unavailable"},
		{patternstore.ErrCommitUncertain, 503, "pattern_commit_uncertain"},
	} {
		t.Run(c.code+c.err.Error(), func(t *testing.T) {
			for _, route := range []struct{ method, path, file, operation string }{
				{"POST", "/v2/patterns", "requests/save-orbit-red-blue.json", "Import"},
				{"POST", "/v2/patterns/import", "requests/import-known.snapshot.json", "Import"},
				{"GET", "/v2/patterns/x", "", "Get"}, {"GET", "/v2/patterns?format=snapshot", "", "Export"},
				{"GET", "/v2/patterns", "", "List"},
			} {
				spy := &patternV2Spy{allow: map[string]bool{route.operation: true}, fail: c.err}
				h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy))
				var body []byte
				if route.file != "" {
					body = patternV2File(t, route.file)
				}
				w := patternHTTP(h, route.method, route.path, body)
				patternV2Error(t, w, c.status, c.code)
				if strings.Contains(w.Body.String(), "must-not-leak") || spy.calls[route.operation] != 1 {
					t.Fatal("partial result leaked or retry occurred", w.Body.String(), spy.calls)
				}
			}
		})
	}
}

func TestPatternV2IDsMethodsAndNoExecutionRoutes(t *testing.T) {
	s := patternV2Store(t)
	base, err := patternsv2.PrepareSave(patternV2File(t, "requests/save-orbit-red-blue.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"preview", "import", "export", "capabilities", "run", "query", "cancel"} {
		r := base.Candidate().Record()
		r, err = patternsv2.NewRecord(id, r.Title, r.Source)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := patternsv2.EncodeSnapshot([]patternsv2.Record{r})
		if _, err := s.Import(b); err != nil {
			t.Fatal(err)
		}
		spy := &patternV2Spy{store: s, allow: map[string]bool{"Get": true}}
		w := patternHTTP(httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(spy)), "GET", "/v2/patterns/"+id, nil)
		if w.Code != 200 || spy.calls["Get"] != 1 {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"", "-bad", "灯", strings.Repeat("a", 65), "x/run", "x/query", "x/cancel"} {
		h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
		patternV2Error(t, patternHTTP(h, "GET", "/v2/patterns/"+id, nil), 400, "invalid_pattern_request")
	}
	for _, path := range []string{"/v2/patterns", "/v2/patterns/preview", "/v2/patterns/import", "/v2/patterns/x", "/v2/patterns/x/run", "/v2/patterns/x/cancel"} {
		for _, method := range []string{"PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			h := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(&patternV2Spy{}))
			w := patternHTTP(h, method, path, nil)
			patternV2Error(t, w, 405, "method_not_allowed")
			want := "GET"
			if path == "/v2/patterns" || path == "/v2/patterns/preview" || path == "/v2/patterns/import" {
				want = "GET, POST"
			}
			if w.Header().Get("Allow") != want {
				t.Fatal("wrong Allow", path, w.Header())
			}
		}
	}
	for _, path := range []string{"/v2/patterns/x/run", "/v2/patterns/x/cancel", "/v2/patterns/x/query"} {
		patternV2Error(t, patternHTTP(httpapi.New(patternV2NoIO{}), "POST", path, []byte(`{}`)), 405, "method_not_allowed")
	}
}

func TestPatternV2RealHandlerPrivateStoreReopenAndFormatIsolation(t *testing.T) {
	s := patternV2Store(t)
	server := httptest.NewServer(httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(s)))
	defer server.Close()
	response, err := server.Client().Post(server.URL+"/v2/patterns", "application/json", bytes.NewReader(patternV2File(t, "requests/save-orbit-red-blue.json")))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 || response.Header.Get("Location") != "/v2/patterns/orbit-red-blue" {
		t.Fatal(response.StatusCode, err, string(data))
	}
	reopened, err := patternstore.NewV2(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	get := patternHTTP(httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(reopened)), "GET", "/v2/patterns/orbit-red-blue", nil)
	if get.Code != 200 || !reflect.DeepEqual(patternV2JSON(t, get.Body.Bytes()), patternV2JSON(t, patternV2File(t, "responses/get-orbit-red-blue.json"))) {
		t.Fatal("reopen lost frozen authoring", get.Code, get.Body.String())
	}
	before, err := reopened.Export()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := patternstore.New(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	oldHandler := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStore(legacy))
	patternV2Error(t, patternHTTP(oldHandler, "POST", "/v1/patterns/import", patternPackage(t, "legacy", control.Program{Steps: []control.Step{{Color: "#000000"}}})), 503, "pattern_library_unavailable")
	after, err := reopened.Export()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("v1 overwrote v2 snapshot or read failed", err)
	}
	other := patternStore(t)
	patternRecord(t, other, "legacy", control.Program{Steps: []control.Step{{Color: "#000000"}}})
	v1bytes, err := other.Export()
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := patternstore.NewV2(other.Path())
	if err != nil {
		t.Fatal(err)
	}
	wrongHandler := httpapi.New(patternV2NoIO{}, httpapi.WithPatternStoreV2(wrong))
	patternV2Error(t, patternHTTP(wrongHandler, "GET", "/v2/patterns", nil), 503, "pattern_library_format_mismatch")
	patternV2Error(t, patternHTTP(wrongHandler, "POST", "/v2/patterns", patternV2File(t, "requests/save-orbit-red-blue.json")), 503, "pattern_library_format_mismatch")
	stillV1, err := other.Export()
	if err != nil || !bytes.Equal(v1bytes, stillV1) {
		t.Fatal("v2 overwrote v1 snapshot or read failed", err)
	}
}
