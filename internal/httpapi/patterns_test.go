package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
)

func patternHTTP(h http.Handler, method, path string, data []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(data))
	if data != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func patternStore(t *testing.T) *patternstore.Store {
	t.Helper()
	s, err := patternstore.New(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func patternPackage(t *testing.T, id string, program control.Program) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{"schema_version": 1, "patterns": []interface{}{map[string]interface{}{"id": id, "title": "Pattern " + id, "program": program}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func patternRecord(t *testing.T, store *patternstore.Store, id string, program control.Program) patterns.Record {
	t.Helper()
	r, err := store.Import(patternPackage(t, id, program))
	if err != nil {
		t.Fatal(err)
	}
	return r.Records[0]
}

func patternBody(t *testing.T, record patterns.Record, extra map[string]interface{}) []byte {
	t.Helper()
	v := map[string]interface{}{"pattern_ref": httpapi.PatternRef{ID: record.ID, Revision: record.Revision}}
	for k, x := range extra {
		v[k] = x
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPatternLibraryEmptyAndEightFrozenActionRoundTrips(t *testing.T) {
	s := patternStore(t)
	h := httpapi.New(noPreviewIO{}, httpapi.WithPatternStore(s))
	for _, path := range []string{"/v1/patterns", "/v1/patterns?format=snapshot"} {
		w := patternHTTP(h, "GET", path, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created library: %v", err)
	}
	actions := []string{`{"kind":"set","color":"red"}`, `{"kind":"off"}`, `{"kind":"fade","color":"blue","duration_ms":100}`, `{"kind":"blink","color":"lime"}`, `{"kind":"breathe","color":"aqua"}`, `{"kind":"pulse","color":"gold"}`, `{"kind":"alternate"}`, `{"kind":"rainbow"}`}
	for _, action := range actions {
		compiled, err := lightaction.CompileJSON([]byte(action))
		if err != nil {
			t.Fatal(err)
		}
		id := compiled.Action.Kind
		body := patternPackage(t, id, compiled.Program)
		w := patternHTTP(h, "POST", "/v1/patterns/import", body)
		var imported patternstore.ImportResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &imported) != nil || len(imported.Created) != 1 || len(imported.Records) != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
		r := imported.Records[0]
		if r.Source.Program == nil || r.Source.Effect != nil || !reflect.DeepEqual(r.CompiledProgram, compiled.Program) {
			t.Fatalf("action source changed: %+v", r)
		}
		before, err := os.Stat(filepath.Join(s.Path(), "patterns.json"))
		if err != nil {
			t.Fatal(err)
		}
		again := patternHTTP(h, "POST", "/v1/patterns/import", body)
		if again.Code != 200 || json.Unmarshal(again.Body.Bytes(), &imported) != nil || len(imported.Created) != 0 || !reflect.DeepEqual(imported.Existing, []string{id}) {
			t.Fatal(again.Code, again.Body.String())
		}
		after, err := os.Stat(filepath.Join(s.Path(), "patterns.json"))
		if err != nil || !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("idempotent import rewrote snapshot: %v", err)
		}
		get := patternHTTP(h, "GET", "/v1/patterns/"+id, nil)
		var got patterns.Record
		if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, r) {
			t.Fatal(get.Code, get.Body.String())
		}
		one := patternHTTP(h, "GET", "/v1/patterns/"+id+"?format=snapshot", nil)
		want, _ := patterns.EncodeSnapshot([]patterns.Record{r})
		if one.Code != 200 || !bytes.Equal(one.Body.Bytes(), want) {
			t.Fatal(one.Code, one.Body.String())
		}
		restored, err := patternStore(t).Import(one.Body.Bytes())
		if err != nil || !reflect.DeepEqual(restored.Records, []patterns.Record{r}) {
			t.Fatalf("single restore: %v %+v", err, restored)
		}
	}
	w := patternHTTP(h, "GET", "/v1/patterns", nil)
	var list httpapi.PatternList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || list.Contract != httpapi.PatternLibraryContract || list.Library.Path != s.Path() || list.Library.State != "ready" || !list.Library.CreateOnly || len(list.Patterns) != 8 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i, summary := range list.Patterns {
		if i > 0 && list.Patterns[i-1].ID >= summary.ID {
			t.Fatal("unsorted summaries")
		}
		if summary.TotalSteps < 1 || summary.NominalDurationMS < 1 {
			t.Fatal(summary)
		}
	}
	export := patternHTTP(h, "GET", "/v1/patterns?format=snapshot", nil)
	want, err := s.Export()
	if err != nil || !bytes.Equal(want, export.Body.Bytes()) {
		t.Fatalf("export is not canonical: %v", err)
	}
	reopened, err := patternstore.New(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	all, err := reopened.List()
	if err != nil || len(all) != 8 {
		t.Fatal(err, len(all))
	}
	// Endpoint-like names are ordinary valid IDs for GET.
	for _, id := range []string{"import", "export", "library"} {
		patternRecord(t, s, id, control.Program{Steps: []control.Step{{Color: "#000000"}}})
		if w := patternHTTP(h, "GET", "/v1/patterns/"+id, nil); w.Code != 200 {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
}

func TestPatternImportRejectsWholeInvalidInputWithoutCreatingLibrary(t *testing.T) {
	valid := `{"id":"first","title":"First","program":{"steps":[{"color":"#000000"}]}}`
	cases := []string{
		`{"schema_version":1,"patterns":[` + valid + `,{"id":"bad","title":"Bad","program":{"steps":[]}}]}`,
		`{"schema_version":1,"patterns":[` + valid + `,` + valid + `]}`,
		`{"schema_version":1.0,"patterns":[]}`, `{"schema_version":1e0,"patterns":[]}`,
		`{"schema_version":1,"patterns":[{"id":"x","title":"X","program":{"steps":[{"color":"#000000"}],"repeat":-0}}]}`,
		`{"schema_version":1,"schema_version":1,"patterns":[]}`, `{"schema_version":1,"patterns":null}`,
		`{"schema_version":1,"patterns":[],"unknown":true}`, `{"schema_version":1,"patterns":[]} {}`,
		"{\"schema_version\":1,\"patterns\":[{\"id\":\"x\",\"title\":\"\xff\",\"program\":{\"steps\":[{\"color\":\"#000000\"}]}}]}",
	}
	for i, body := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := patternStore(t)
			h := httpapi.New(noPreviewIO{}, httpapi.WithPatternStore(s))
			w := patternHTTP(h, "POST", "/v1/patterns/import", []byte(body))
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid package created directory: %v", err)
			}
		})
	}
	s := patternStore(t)
	h := httpapi.New(noPreviewIO{}, httpapi.WithPatternStore(s))
	large := patternHTTP(h, "POST", "/v1/patterns/import", bytes.Repeat([]byte(" "), patterns.MaxPackageBytes+1))
	if large.Code != 413 || !strings.Contains(large.Body.String(), "pattern_package_too_large") || large.Header().Get("Connection") != "close" {
		t.Fatal(large.Code, large.Body.String())
	}
	p := control.Program{Steps: []control.Step{{Color: "#123456"}}}
	patternRecord(t, s, "exists", p)
	before, _ := s.Export()
	changed := `{"schema_version":1,"patterns":[` + valid + `,{"id":"exists","title":"Changed","program":{"steps":[{"color":"#654321"}]}}]}`
	w := patternHTTP(h, "POST", "/v1/patterns/import", []byte(changed))
	after, _ := s.Export()
	if w.Code != 409 || !bytes.Equal(before, after) {
		t.Fatal(w.Code, w.Body.String())
	}
}

type patternFailureStore struct {
	httpapi.PatternStore
	err   error
	calls int
}

func (s *patternFailureStore) List() ([]patterns.Record, error) { s.calls++; return nil, s.err }
func (s *patternFailureStore) Get(string) (patterns.Record, error) {
	s.calls++
	return patterns.Record{}, s.err
}
func (s *patternFailureStore) Resolve(string, string) (patterns.Record, error) {
	s.calls++
	return patterns.Record{}, s.err
}
func (s *patternFailureStore) Export() ([]byte, error) { s.calls++; return nil, s.err }
func (s *patternFailureStore) Import([]byte) (patternstore.ImportResult, error) {
	s.calls++
	return patternstore.ImportResult{Created: []string{"must-not-leak"}, Total: 1}, s.err
}

func TestPatternErrorsAreExplicitAndDoNotDisableLegacyLight(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{patternstore.ErrInvalid, 400, "invalid_pattern_request"}, {patternstore.ErrLimit, 409, "pattern_library_limit"}, {patternstore.ErrNotFound, 404, "pattern_not_found"}, {patternstore.ErrConflict, 409, "pattern_conflict"}, {patternstore.ErrRevisionConflict, 409, "pattern_revision_conflict"}, {patternstore.ErrBusy, 409, "pattern_library_busy"}, {patternstore.ErrUnsupported, 501, "pattern_library_unsupported"}, {patternstore.ErrCorrupt, 503, "pattern_library_unavailable"}, {patternstore.ErrUnsafe, 503, "pattern_library_unavailable"}, {errors.New("local I/O failed"), 503, "pattern_library_unavailable"}, {patternstore.ErrCommitUncertain, 503, "pattern_commit_uncertain"},
	} {
		t.Run(tc.code+tc.err.Error(), func(t *testing.T) {
			s := &patternFailureStore{PatternStore: patternStore(t), err: tc.err}
			h := httpapi.New(noPreviewIO{}, httpapi.WithPatternStore(s))
			w := patternHTTP(h, "POST", "/v1/patterns/import", []byte(`{"schema_version":1,"patterns":[]}`))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "must-not-leak") || strings.Contains(w.Body.String(), `"created"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.err == patternstore.ErrCommitUncertain && !strings.Contains(w.Body.String(), "publication may have succeeded") {
				t.Fatal(w.Body.String())
			}
			light := patternHTTP(h, "POST", "/v1/light/preview", []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`))
			if light.Code != 200 {
				t.Fatal(light.Code, light.Body.String())
			}
			catalog := patternHTTP(h, "GET", "/v1/capabilities", nil)
			var c control.CapabilityCatalog
			if catalog.Code != 200 || json.Unmarshal(catalog.Body.Bytes(), &c) != nil || c.PatternLibrary {
				t.Fatal(catalog.Code, catalog.Body.String())
			}
		})
	}
	for _, option := range []httpapi.Option{nil, httpapi.WithPatternDirectory("relative/path")} {
		h := httpapi.New(nil, option)
		w := patternHTTP(h, "GET", "/v1/patterns", nil)
		if w.Code != 501 && w.Code != 503 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := patternHTTP(h, "POST", "/v1/light/preview", []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off"}}`)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	misconfigured := httpapi.New(nil, httpapi.WithPatternDirectory("relative/path"))
	w := patternHTTP(misconfigured, "GET", "/v1/patterns", nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "relative/path") {
		t.Fatal("configuration diagnostic lost original path", w.Code, w.Body.String())
	}
}

func TestPatternRoutesStrictQueriesBodiesHeadersAndOrigin(t *testing.T) {
	s := patternStore(t)
	h := httpapi.New(noPreviewIO{}, httpapi.WithPatternStore(s))
	for _, path := range []string{"/v1/patterns?format=", "/v1/patterns?format=snapshot&format=snapshot", "/v1/patterns?other=x", "/v1/patterns?Format=snapshot", "/v1/patterns?format=%zz", "/v1/patterns/a/b"} {
		w := patternHTTP(h, "GET", path, nil)
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := patternHTTP(h, "GET", "/v1/patterns", []byte(`{}`)); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, kind := range []string{"duplicate", "wrong", "query", "origin", "host"} {
		t.Run(kind, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1/v1/patterns/import", strings.NewReader(`{"schema_version":1,"patterns":[]}`))
			r.Header.Set("Content-Type", "application/json")
			want := 415
			switch kind {
			case "duplicate":
				r.Header.Add("Content-Type", "application/json")
			case "wrong":
				r.Header.Set("Content-Type", "text/plain")
			case "query":
				r.URL.RawQuery = "x=1"
				want = 400
			case "origin":
				r.Header.Set("Origin", "https://evil.invalid")
				want = 403
			case "host":
				r.Host = "evil.invalid"
				want = 403
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected request created library: %v", err)
	}
}

type patternSubmitSpy struct {
	fakeService
	store   *patternstore.Store
	calls   int
	seen    control.Program
	binding control.ReceiptRequest
}

func (s *patternSubmitSpy) Instance() control.InstanceInfo { return control.InstanceInfo{} }
func (s *patternSubmitSpy) Request(string, string, string) (control.Job, error) {
	return control.Job{}, control.ErrRequestNotFound
}
func (s *patternSubmitSpy) SubmitWithReceipt(_ context.Context, serial string, p control.Program, policy string, b control.ReceiptRequest) (control.ReceiptResult, error) {
	s.calls++
	s.seen = p
	s.binding = b
	// A second instance can obtain the lock during admission, proving Resolve
	// finished its short transaction before the executor is called.
	other, _ := patternstore.New(s.store.Path())
	if _, err := other.List(); err != nil {
		return control.ReceiptResult{}, err
	}
	return control.ReceiptResult{Job: control.Job{ID: "job-pattern", Device: serial, Program: p, Status: "queued"}, DaemonInstanceID: b.ExpectedDaemonInstanceID}, nil
}

func TestPatternRefFrozenPreviewAndSingleReceiptSubmission(t *testing.T) {
	s := patternStore(t)
	record := patternRecord(t, s, "saved", control.Program{Steps: []control.Step{{Color: "#AABBCC"}}, OnFinish: "hold"})
	spy := &patternSubmitSpy{store: s}
	h := httpapi.New(spy, httpapi.WithPatternStore(s))
	preview := patternHTTP(h, "POST", "/v1/programs/preview", patternBody(t, record, nil))
	var value httpapi.PatternPreview
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &value) != nil || value.PatternRef.Revision != record.Revision || !reflect.DeepEqual(value.Preview.Program, record.CompiledProgram) || value.Preview.InputKind != "program" || spy.calls != 0 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	extra := map[string]interface{}{"policy": "reject", "request_id": receiptID, "expected_daemon_instance_id": receiptInstance}
	w := patternHTTP(h, "POST", "/v1/devices/20005601/jobs", patternBody(t, record, extra))
	if w.Code != 202 || spy.calls != 1 || spy.submitCount != 0 || !reflect.DeepEqual(spy.seen, record.CompiledProgram) || spy.binding.RequestID != receiptID || spy.binding.ExpectedDaemonInstanceID != receiptInstance || w.Header().Get("X-NB1-Replayed") != "false" {
		t.Fatal(w.Code, w.Body.String(), spy)
	}
	for _, change := range []map[string]interface{}{{"program": record.CompiledProgram}, {"effect": control.Effect{Kind: "steady", Color: "#aabbcc"}}, {"policy": "replace"}, {"policy": "queue"}, {"policy": ""}, {"request_id": "invalid"}, {"expected_daemon_instance_id": "invalid"}} {
		args := map[string]interface{}{}
		for k, v := range extra {
			args[k] = v
		}
		for k, v := range change {
			args[k] = v
		}
		w := patternHTTP(h, "POST", "/v1/devices/20005601/jobs", patternBody(t, record, args))
		if w.Code != 400 || spy.calls != 1 || spy.submitCount != 0 {
			t.Fatal(w.Code, w.Body.String(), spy.calls)
		}
	}
	bad := record
	bad.Revision = "sha256:" + strings.Repeat("0", 64)
	w = patternHTTP(h, "POST", "/v1/devices/20005601/jobs", patternBody(t, bad, extra))
	if w.Code != 409 || spy.calls != 1 || !strings.Contains(w.Body.String(), "query that receipt") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{"pattern_ref":{"id":"saved"}}`, `{"pattern_ref":{"id":"saved","revision":"latest"}}`, `{"pattern_ref":null}`, `{"pattern_ref":{"id":"saved","id":"saved","revision":"x"}}`, `{"pattern_ref":{"id":"saved","revision":"x","path":"/tmp/x"}}`} {
		w := patternHTTP(h, "POST", "/v1/programs/preview", []byte(body))
		if w.Code != 400 || spy.calls != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, args := range []map[string]interface{}{{}, {"policy": "reject"}, {"policy": "reject", "request_id": receiptID}, {"policy": "reject", "expected_daemon_instance_id": receiptInstance}} {
		w := patternHTTP(h, "POST", "/v1/devices/20005601/jobs", patternBody(t, record, args))
		if w.Code != 400 || spy.calls != 1 || spy.submitCount != 0 {
			t.Fatal(w.Code, w.Body.String(), spy.calls)
		}
	}
	for _, path := range []string{"/v1/programs/preview", "/v1/devices/20005601/jobs"} {
		args := map[string]interface{}(nil)
		if strings.HasSuffix(path, "/jobs") {
			args = extra
		}
		req := httptest.NewRequest("POST", "http://127.0.0.1"+path, bytes.NewReader(patternBody(t, record, args)))
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 || spy.calls != 1 || spy.submitCount != 0 {
			t.Fatal(w.Code, w.Body.String(), spy.calls)
		}
	}
}

func TestPatternTargetPreviewUsesOnlySnapshotAndUnavailableLibraryAllowsRawJobs(t *testing.T) {
	s := patternStore(t)
	r := patternRecord(t, s, "saved", control.Program{Steps: []control.Step{{Color: "#abcdef"}}})
	snapshot := &previewSnapshot{snapshot: control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: "demo", Capabilities: []string{"set", "program", "raw-rgb"}}, Connection: "online"}}
	h := httpapi.New(snapshot, httpapi.WithPatternStore(s))
	w := patternHTTP(h, "POST", "/v1/programs/preview", patternBody(t, r, map[string]interface{}{"serial": "demo"}))
	var preview httpapi.PatternPreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || snapshot.calls != 1 || preview.Preview.Validation.Device != "supported" {
		t.Fatal(w.Code, w.Body.String(), snapshot.calls)
	}
	failure := &patternFailureStore{PatternStore: s, err: patternstore.ErrCorrupt}
	service := &fakeService{}
	h = httpapi.New(service, httpapi.WithPatternStore(failure))
	w = patternHTTP(h, "POST", "/v1/devices/20005601/jobs", []byte(`{"program":{"steps":[{"color":"#123456"}]}}`))
	if w.Code != 202 || service.submitCount != 1 || failure.calls != 0 {
		t.Fatal(w.Code, w.Body.String(), service.submitCount, failure.calls)
	}
}
