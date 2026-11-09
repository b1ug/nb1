package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternresolver"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

type publicPatternStore struct {
	httpapi.PatternStoreV2 // every non-Resolve call panics unless explicitly supplied
	record                 patternsv2.Record
	fail                   error
	calls                  int
	id, revision           string
	hook                   func()
}

func (s *publicPatternStore) Resolve(id, revision string) (patternsv2.Record, error) {
	s.calls++
	s.id, s.revision = id, revision
	if s.hook != nil {
		s.hook()
	}
	if s.fail != nil {
		return patternsv2.Record{}, s.fail
	}
	return s.record, nil
}

type publicPatternService struct {
	noPreviewIO
	submits, queries, cancels, devices int
	serial, policy, knownID            string
	ref                                patternidentity.Ref
	ticket                             control.ReceiptRequest
	envelope                           control.PatternJobEnvelope
	fail                               error
	replay                             bool
	state                              control.DeviceState
	onDevice                           func()
}

func (s *publicPatternService) SubmitPatternWithReceipt(ctx context.Context, serial string, ref patternidentity.Ref, policy string, key control.ReceiptRequest, resolve control.PatternResolver) (control.PatternReceiptResult, error) {
	s.submits++
	s.serial, s.ref, s.policy, s.ticket = serial, ref, policy, key
	if s.fail != nil {
		return control.PatternReceiptResult{}, s.fail
	}
	if !s.replay {
		r, err := resolve(ctx, ref)
		if err != nil {
			return control.PatternReceiptResult{}, err
		}
		s.envelope = publicEnvelope(r)
	}
	return control.PatternReceiptResult{Job: s.envelope, Replayed: s.replay, DaemonInstanceID: key.ExpectedDaemonInstanceID}, nil
}
func (s *publicPatternService) PatternRequest(serial string, key control.ReceiptRequest, ref patternidentity.Ref) (control.PatternJobEnvelope, error) {
	s.queries++
	s.serial, s.ref, s.ticket = serial, ref, key
	return s.envelope, s.fail
}
func (s *publicPatternService) CancelPatternRequest(ctx context.Context, serial string, key control.ReceiptRequest, ref patternidentity.Ref, known string) (control.PatternJobEnvelope, error) {
	s.cancels++
	s.serial, s.ref, s.ticket, s.knownID = serial, ref, key, known
	return s.envelope, s.fail
}
func (s *publicPatternService) Device(string) (control.DeviceState, error) {
	s.devices++
	if s.onDevice != nil {
		s.onDevice()
	}
	return s.state, s.fail
}
func publicEnvelope(r control.ResolvedPattern) control.PatternJobEnvelope {
	e := control.PatternJobEnvelope{PatternRef: r.Ref, ExecutionDigest: r.ExecutionDigest}
	if r.Execution.Program != nil {
		e.JobKind = "program"
		e.ProgramJob = &control.Job{ID: "job_" + strings.Repeat("3", 32), Device: "20005601", Status: "queued", Program: *r.Execution.Program}
	} else {
		e.JobKind = "frame"
		e.FrameJob = &control.FrameJob{ID: frameHTTPJob, Device: "20005601", Status: "queued", Program: *r.Execution.FrameProgram}
	}
	return e
}
func publicRecord(t *testing.T, kind string) patternsv2.Record {
	t.Helper()
	name := "pulse-lime"
	if kind == "frame" {
		name = "orbit-red-blue"
	}
	c, err := patternsv2.PreviewDraft(patternV2File(t, "requests/preview-"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return c.Record()
}
func publicRef(r patternsv2.Record) patternidentity.Ref {
	return patternidentity.Ref{RecordContract: r.Contract, ID: r.ID, Revision: r.Revision}
}
func publicKey() control.ReceiptRequest {
	return control.ReceiptRequest{RequestID: frameHTTPRequest, ExpectedDaemonInstanceID: frameHTTPInstance}
}
func publicSubmit(ref patternidentity.Ref) map[string]interface{} {
	return map[string]interface{}{"pattern_ref": ref, "policy": "reject", "request_id": frameHTTPRequest, "expected_daemon_instance_id": frameHTTPInstance}
}
func publicBytes(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func publicLocation(ref patternidentity.Ref) string {
	return "/v2/devices/20005601/pattern-requests/" + frameHTTPRequest + "?" + url.Values{"expected_daemon_instance_id": {frameHTTPInstance}, "record_contract": {ref.RecordContract}, "pattern_id": {ref.ID}, "revision": {ref.Revision}}.Encode()
}
func publicHeaders(t *testing.T, w *httptest.ResponseRecorder, instance string) {
	t.Helper()
	for k, v := range map[string]string{"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-NB1-Daemon-Instance-ID": instance} {
		a := w.Header().Values(k)
		if len(a) != 1 || a[0] != v {
			t.Fatal(k, a)
		}
	}
}
func publicNoCalls(t *testing.T, s *publicPatternService, store *publicPatternStore) {
	t.Helper()
	if s.submits+s.queries+s.cancels+s.devices+store.calls != 0 {
		t.Fatalf("calls service=%+v store=%d", s, store.calls)
	}
}

func TestPatternExecutionV2SubmitLocationRoundtripAndStoreFreeReceipt(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			r := publicRecord(t, kind)
			ref := publicRef(r)
			store := &publicPatternStore{record: r}
			s := &publicPatternService{}
			h := httpapi.New(s, httpapi.WithPatternStoreV2(store))
			input := publicSubmit(ref)
			for _, policy := range []string{"replace", "queue", "reject"} {
				input["policy"] = policy
				w := patternHTTP(h, "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, input))
				if w.Code != 202 || s.policy != policy || s.ref != ref || s.ticket != publicKey() || s.serial != "20005601" || store.id != ref.ID || store.revision != ref.Revision {
					t.Fatal(w.Code, w.Body.String(), s, store)
				}
				publicHeaders(t, w, frameHTTPInstance)
				if a := w.Header().Values("X-NB1-Replayed"); len(a) != 1 || a[0] != "false" {
					t.Fatal(a)
				}
				location := w.Header().Values("Location")
				if len(location) != 1 || location[0] != publicLocation(ref) {
					t.Fatal(location)
				}
				var body map[string]json.RawMessage
				if json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body) != 4 || body["pattern_ref"] == nil || body["job"] == nil || body["job_kind"] == nil || body["execution_digest"] == nil {
					t.Fatal(w.Body.String())
				}
			}
			// No valid configuration and no store at all: only the already accepted
			// envelope/receipt belongs to query/cancel/replay; no Path/Resolve fallback.
			for _, options := range [][]httpapi.Option{nil, {httpapi.WithPatternDirectoryV2("invalid-relative")}, {httpapi.WithPatternStoreV2((*publicPatternStore)(nil))}} {
				handler := httpapi.New(s, options...)
				s.replay = true
				w := patternHTTP(handler, "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, input))
				if w.Code != 202 || w.Header().Get("X-NB1-Replayed") != "true" {
					t.Fatal(w.Code, w.Body.String())
				}
				w = patternHTTP(handler, "GET", publicLocation(ref), nil)
				if w.Code != 200 || s.ref != ref || s.ticket != publicKey() {
					t.Fatal(w.Code, w.Body.String())
				}
				publicHeaders(t, w, frameHTTPInstance)
				cancel := map[string]interface{}{"pattern_ref": ref, "expected_daemon_instance_id": frameHTTPInstance}
				for _, known := range []string{"", "job_" + strings.Repeat("4", 32), frameHTTPJob} {
					if known != "" {
						cancel["confirmed_job_id"] = known
					} else {
						delete(cancel, "confirmed_job_id")
					}
					w = patternHTTP(handler, "POST", strings.Split(publicLocation(ref), "?")[0]+"/cancel", publicBytes(t, cancel))
					if w.Code != 202 || s.knownID != known {
						t.Fatal(w.Code, w.Body.String())
					}
					publicHeaders(t, w, frameHTTPInstance)
				}
			}
			if store.calls != 3 {
				t.Fatal("receipt accessed store", store.calls)
			}
		})
	}
}

func TestPatternExecutionV2StrictRawSubmitBoundary(t *testing.T) {
	ref := publicRef(publicRecord(t, "program"))
	good := string(publicBytes(t, publicSubmit(ref)))
	cases := map[string]string{
		"missing": "{}", "null": "null", "array": "[]", "trailing": good + " {}", "unknown": strings.TrimSuffix(good, "}") + `,"program":{}}`,
		"duplicate": strings.TrimSuffix(good, "}") + `,"policy":"reject"}`, "escaped-duplicate": strings.TrimSuffix(good, "}") + `,"pol\u0069cy":"reject"}`,
		"wrong-case": strings.Replace(good, `"policy"`, `"Policy"`, 1), "null-ref": strings.Replace(good, string(publicBytes(t, ref)), "null", 1),
		"nested-duplicate": strings.Replace(good, `"id":`, `"id":"first","id":`, 1),
		"unknown-ref":      strings.Replace(good, `"record_contract":`, `"verified":true,"record_contract":`, 1),
		"null-policy":      strings.Replace(good, `"reject"`, `null`, 1), "empty-policy": strings.Replace(good, `"reject"`, `""`, 1), "other-policy": strings.Replace(good, `"reject"`, `"restart"`, 1),
		"surrogate-id": strings.Replace(good, ref.ID, `\ud800`, 1), "surrogate-instance": strings.Replace(good, frameHTTPInstance, `\udc00`, 1), "surrogate-policy": strings.Replace(good, `"reject"`, `"\ud800"`, 1),
		"surrogate-key": strings.Replace(good, `"policy"`, `"\ud800"`, 1), "invalid-utf8": good + string([]byte{255}),
		"bad-ticket": strings.Replace(good, frameHTTPRequest, "req_0_"+strings.Repeat("2", 32), 1), "ticket-overflow": strings.Replace(good, frameHTTPRequest, "req_9223372036854775808_"+strings.Repeat("2", 32), 1),
		"upper-sha": strings.Replace(good, ref.Revision, "sha256:"+strings.Repeat("A", 64), 1), "unknown-contract": strings.Replace(good, ref.RecordContract, "future", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := &publicPatternService{}
			store := &publicPatternStore{}
			w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/devices/20005601/pattern-jobs", []byte(body))
			patternV2Error(t, w, 400, "invalid_pattern_request")
			publicNoCalls(t, s, store)
		})
	}
	for _, path := range []string{"/v2/devices/20005601/pattern-jobs?", "/v2/devices/20005601/pattern-jobs?x=1", "/v2/devices/bad.serial/pattern-jobs"} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", path, []byte(good))
		patternV2Error(t, w, 400, "invalid_pattern_request")
		publicNoCalls(t, s, store)
	}
	for _, n := range []int{4096, 4097} {
		s := &publicPatternService{}
		store := &publicPatternStore{record: publicRecord(t, "program")}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/devices/20005601/pattern-jobs", []byte(good+strings.Repeat(" ", n-len(good))))
		if n == 4096 {
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
		} else {
			patternV2Error(t, w, 413, "body_too_large")
			if w.Header().Get("Connection") != "close" {
				t.Fatal(w.Header())
			}
			publicNoCalls(t, s, store)
		}
	}
}

func TestPatternExecutionV2StrictQueryCancelAndMedia(t *testing.T) {
	ref := publicRef(publicRecord(t, "program"))
	location := publicLocation(ref)
	cancelPath := strings.Split(location, "?")[0] + "/cancel"
	for _, query := range []string{"", "?", strings.Split(location, "?")[1] + "&extra=1", strings.Split(location, "?")[1] + "&revision=" + url.QueryEscape(ref.Revision), strings.Split(location, "?")[1] + "&%72evision=x", strings.Split(location, "?")[1] + "&", "&" + strings.Split(location, "?")[1], strings.Replace(strings.Split(location, "?")[1], "&", "&&", 1), strings.Replace(strings.Split(location, "?")[1], "pattern_id=", "pattern_id=%zz", 1), strings.Replace(strings.Split(location, "?")[1], url.QueryEscape(ref.RecordContract), "", 1)} {
		if query != "" && !strings.HasPrefix(query, "?") {
			query = "?" + query
		}
		s := &publicPatternService{}
		store := &publicPatternStore{}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "GET", strings.Split(location, "?")[0]+query, nil)
		patternV2Error(t, w, 400, "invalid_pattern_request")
		publicNoCalls(t, s, store)
	}
	good := string(publicBytes(t, map[string]interface{}{"pattern_ref": ref, "expected_daemon_instance_id": frameHTTPInstance}))
	for _, extra := range []string{`,"confirmed_job_id":""`, `,"confirmed_job_id":null`, `,"confirmed_job_id":"job_bad"`, `,"confirmed_job_id":"job_` + strings.Repeat("A", 32) + `"`, `,"confirmed_job_id":"\ud800"`, `,"request_id":"` + frameHTTPRequest + `"`, `,"expected_daemon_instance_id":"` + frameHTTPInstance + `"`} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", cancelPath, []byte(strings.TrimSuffix(good, "}")+extra+"}"))
		patternV2Error(t, w, 400, "invalid_pattern_request")
		publicNoCalls(t, s, store)
	}
	for _, headers := range [][]string{{}, {"text/plain"}, {"application/json", "application/json"}, {"application/json, application/json"}} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		r := httptest.NewRequest("POST", "http://127.0.0.1"+cancelPath, strings.NewReader(good))
		for _, v := range headers {
			r.Header.Add("Content-Type", v)
		}
		w := httptest.NewRecorder()
		httpapi.New(s, httpapi.WithPatternStoreV2(store)).ServeHTTP(w, r)
		patternV2Error(t, w, 415, "json_required")
		publicNoCalls(t, s, store)
	}
	for _, body := range []string{"body", "chunked"} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		r := httptest.NewRequest("GET", "http://127.0.0.1"+location, strings.NewReader("x"))
		if body == "chunked" {
			r.ContentLength = -1
			r.TransferEncoding = []string{"chunked"}
		}
		w := httptest.NewRecorder()
		httpapi.New(s, httpapi.WithPatternStoreV2(store)).ServeHTTP(w, r)
		patternV2Error(t, w, 400, "invalid_pattern_request")
		publicNoCalls(t, s, store)
	}
}

func TestPatternExecutionV2ResolverErrorsAreClassifiedBeforeCoreInvalid(t *testing.T) {
	ref := publicRef(publicRecord(t, "program"))
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"notfound", patternstore.ErrNotFound, 404, "pattern_not_found"}, {"revision", patternstore.ErrRevisionConflict, 409, "pattern_revision_conflict"}, {"busy", patternstore.ErrBusy, 409, "pattern_library_busy"},
		{"invalid-frozen", patternsv2.ErrInvalid, 500, "pattern_integrity_error"}, {"core-invalid-from-store", control.ErrInvalid, 500, "pattern_integrity_error"}, {"wrong-ref", patternresolver.ErrReference, 500, "pattern_integrity_error"},
		{"corrupt", patternstore.ErrCorrupt, 503, "pattern_library_unavailable"}, {"unsafe", patternstore.ErrUnsafe, 503, "pattern_library_unavailable"}, {"format", patternstore.ErrFormatMismatch, 503, "pattern_library_format_mismatch"},
		{"source", patternsv2.ErrUnsupported, 501, "pattern_source_unsupported"}, {"store-unsupported", patternstore.ErrUnsupported, 501, "pattern_library_unsupported"}, {"readonly", patternstore.ErrReadOnly, 501, "pattern_library_unsupported"},
		{"permission", os.ErrPermission, 503, "pattern_library_unavailable"}, {"unknown", errors.New("private-path-secret"), 503, "pattern_library_unavailable"}, {"bare-io", syscall.EIO, 503, "pattern_library_unavailable"}, {"read-timeout", &os.PathError{Op: "read", Path: "private-path-secret", Err: syscall.ETIMEDOUT}, 503, "pattern_library_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &publicPatternService{}
			store := &publicPatternStore{fail: tc.err}
			w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, publicSubmit(ref)))
			patternV2Error(t, w, tc.status, tc.code)
			if store.calls != 1 || strings.Contains(w.Body.String(), "private-path-secret") {
				t.Fatal(store.calls, w.Body.String())
			}
		})
	}
	for _, options := range [][]httpapi.Option{nil, {httpapi.WithPatternDirectoryV2("relative-private")}} {
		s := &publicPatternService{}
		w := patternHTTP(httpapi.New(s, options...), "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, publicSubmit(ref)))
		status, code := 501, "pattern_library_unsupported"
		if len(options) > 0 {
			status, code = 503, "pattern_library_unavailable"
		}
		patternV2Error(t, w, status, code)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{control.ErrRequestConflict, 409, "idempotency_conflict"}, {control.ErrInstanceChanged, 409, "daemon_instance_changed"}, {control.ErrRequestExpired, 410, "request_expired"}, {control.ErrReceiptCapacity, 429, "idempotency_capacity"}, {control.ErrRequestNotFound, 404, "request_not_found"}, {control.ErrBusy, 409, "busy"}, {control.ErrQueueFull, 409, "queue_full"}, {control.ErrOffline, 503, "unavailable"}, {control.ErrClosed, 503, "unavailable"}, {control.ErrInvalid, 400, "invalid_request"}, {context.Canceled, 503, "unavailable"}, {context.DeadlineExceeded, 503, "unavailable"}} {
		s := &publicPatternService{fail: tc.err}
		store := &publicPatternStore{}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, publicSubmit(ref)))
		patternV2Error(t, w, tc.status, tc.code)
		if store.calls != 0 {
			t.Fatal(store.calls)
		}
	}
}

func TestPatternExecutionV2PreviewRefHasNoAdmissionAndPreservesFrozenExecution(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			r := publicRecord(t, kind)
			ref := publicRef(r)
			store := &publicPatternStore{record: r}
			s := &publicPatternService{state: control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: "20005601", Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold"}}, Connection: "online"}}
			for _, target := range []bool{false, true} {
				input := map[string]interface{}{"pattern_ref": ref}
				if target {
					input["serial"] = "20005601"
				}
				w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/patterns/preview-ref", publicBytes(t, input))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var out struct {
					SchemaVersion int                 `json:"schema_version"`
					Contract      string              `json:"contract"`
					Ref           patternidentity.Ref `json:"pattern_ref"`
					Digest        string              `json:"execution_digest"`
					Execution     json.RawMessage     `json:"execution"`
					Preview       json.RawMessage     `json:"preview"`
					Availability  struct {
						Available bool   `json:"available"`
						Reason    string `json:"reason"`
					} `json:"pattern_execution"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				digest, err := patternsv2.ExecutionDigest(r)
				if err != nil {
					t.Fatal(err)
				}
				if out.SchemaVersion != 1 || out.Contract != httpapi.PatternRefPreviewContract || out.Ref != ref || out.Digest != digest || !out.Availability.Available || !reflect.DeepEqual(patternV2JSON(t, out.Execution), patternV2JSON(t, publicBytes(t, r.Compiled.Execution))) {
					t.Fatal(w.Body.String())
				}
				if kind == "program" {
					var p control.PreviewResult
					if json.Unmarshal(out.Preview, &p) != nil || p.CompileContract != control.CompileContract || (target && p.Validation.Device != "supported") {
						t.Fatal(string(out.Preview))
					}
				} else {
					var p control.FramePreview
					if json.Unmarshal(out.Preview, &p) != nil || p.Contract != control.FrameContract || (target && !p.Execution.Available) {
						t.Fatal(string(out.Preview))
					}
				}
				if w.Header().Get("Location") != "" || w.Header().Get("X-NB1-Replayed") != "" {
					t.Fatal(w.Header())
				}
			}
			if store.calls != 2 || s.devices != 1 || s.submits+s.queries+s.cancels != 0 {
				t.Fatal(store.calls, s)
			}
		})
	}
	// A successful resolver or cached read cannot hide caller cancellation.
	r := publicRecord(t, "program")
	for _, phase := range []string{"resolve", "device"} {
		ctx, cancel := context.WithCancel(context.Background())
		s := &publicPatternService{}
		store := &publicPatternStore{record: r}
		input := map[string]interface{}{"pattern_ref": publicRef(r)}
		if phase == "resolve" {
			store.hook = cancel
		} else {
			s.onDevice = cancel
			input["serial"] = "20005601"
		}
		req := httptest.NewRequest("POST", "http://127.0.0.1/v2/patterns/preview-ref", bytes.NewReader(publicBytes(t, input))).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		httpapi.New(s, httpapi.WithPatternStoreV2(store)).ServeHTTP(w, req)
		cancel()
		patternV2Error(t, w, 503, "unavailable")
	}
}

func TestPatternExecutionV2RoutingAndHostOrigin(t *testing.T) {
	ref := publicRef(publicRecord(t, "program"))
	submit := publicBytes(t, publicSubmit(ref))
	for _, tc := range []struct {
		method, path, allow string
		status              int
	}{{"PUT", "/v2/patterns/preview-ref", "GET, POST", 405}, {"GET", "/v2/devices/20005601/pattern-jobs", "POST", 405}, {"POST", strings.Split(publicLocation(ref), "?")[0], "GET", 405}, {"GET", strings.Split(publicLocation(ref), "?")[0] + "/cancel", "POST", 405}, {"POST", "/v2/devices/20005601/pattern-jobs/", "", 404}, {"POST", "/v2//devices/20005601/pattern-jobs", "", 404}} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), tc.method, tc.path, submit)
		if w.Code != tc.status || w.Header().Get("Allow") != tc.allow {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		publicNoCalls(t, s, store)
	}
	for _, tc := range []struct{ host, origin string }{{"evil.example", ""}, {"127.0.0.1", "https://evil.example"}, {"127.0.0.1", "null"}} {
		s := &publicPatternService{}
		store := &publicPatternStore{}
		req := httptest.NewRequest("POST", "http://127.0.0.1/v2/devices/20005601/pattern-jobs", bytes.NewReader(submit))
		req.Host = tc.host
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		httpapi.New(s, httpapi.WithPatternStoreV2(store)).ServeHTTP(w, req)
		patternV2Error(t, w, 403, "local_only")
		publicNoCalls(t, s, store)
	}
	// preview-ref remains a legal GET record ID; no resolver needed to read it.
	r := publicRecord(t, "program")
	r.ID = "preview-ref"
	r, err := patternsv2.NewRecord(r.ID, r.Title, r.Source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := patternsv2.EncodeSnapshot([]patternsv2.Record{r})
	if err != nil {
		t.Fatal(err)
	}
	store := patternV2Store(t)
	if _, err = store.Import(snapshot); err != nil {
		t.Fatal(err)
	}
	w := patternHTTP(httpapi.New(noPreviewIO{}, httpapi.WithPatternStoreV2(store)), "GET", "/v2/patterns/preview-ref", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = patternHTTP(httpapi.New(noPreviewIO{}), "POST", "/v2/devices/20005601/pattern-jobs", submit)
	patternV2Error(t, w, 501, "pattern_execution_unsupported")
}

var _ control.PatternService = (*publicPatternService)(nil)

func TestPatternExecutionV2PureAuthorPreviewReportsWiringWithoutStoreCalls(t *testing.T) {
	for _, tc := range []struct {
		name      string
		service   control.Service
		options   []httpapi.Option
		available bool
		reason    string
	}{
		{"no-extension", noPreviewIO{}, nil, false, "pattern_service_unsupported"},
		{"no-store", &publicPatternService{}, nil, false, "pattern_library_unconfigured"},
		{"configuration-error", &publicPatternService{}, []httpapi.Option{httpapi.WithPatternDirectoryV2("relative")}, false, "pattern_library_configuration_error"},
		{"wired", &publicPatternService{}, []httpapi.Option{httpapi.WithPatternStoreV2(&publicPatternStore{})}, true, "pattern_service_configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := patternHTTP(httpapi.New(tc.service, tc.options...), "POST", "/v2/patterns/preview", patternV2File(t, "requests/preview-pulse-lime.json"))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var out struct {
				Execution struct {
					Available bool   `json:"available"`
					Reason    string `json:"reason"`
				} `json:"pattern_execution"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Execution.Available != tc.available || out.Execution.Reason != tc.reason {
				t.Fatal(w.Body.String())
			}
			if s, ok := tc.service.(*publicPatternService); ok && s.submits+s.queries+s.cancels+s.devices != 0 {
				t.Fatal(s)
			}
		})
	}
}

func TestPatternExecutionV2FramePreviewHoldAndCachedProfileAreNotAdmission(t *testing.T) {
	record, err := patternsv2.NewRecord("hold-orbit", "Hold fixture", patternsv2.Source{Kind: "light_action", Contract: "nb1-light-action/experimental-v2", Action: json.RawMessage(`{"kind":"orbit","colors":["red","blue"],"period_ms":400,"segments":4,"cycles":1,"on_finish":"hold"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, serial, connection string
		caps                     []string
		want                     bool
		reason                   string
	}{
		{"no-hold", "20005601", "online", []string{"set", "raw-rgb", "mk2-basic"}, false, "missing_capabilities"},
		{"with-hold", "20005601", "online", []string{"set", "raw-rgb", "mk2-basic", "hold"}, true, "cached_profile_supported"},
		{"offline", "20005601", "offline", []string{"set", "raw-rgb", "mk2-basic", "hold"}, false, "device_not_online"},
		{"wrong-serial", "different", "online", []string{"set", "raw-rgb", "mk2-basic", "hold"}, false, "device_not_configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &publicPatternService{state: control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: tc.serial, Capabilities: tc.caps}, Connection: tc.connection}}
			store := &publicPatternStore{record: record}
			w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", "/v2/patterns/preview-ref", publicBytes(t, map[string]interface{}{"pattern_ref": publicRef(record), "serial": "20005601"}))
			var out struct {
				Preview control.FramePreview `json:"preview"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Preview.Execution.Available != tc.want || out.Preview.Execution.Reason != tc.reason {
				t.Fatal(w.Code, w.Body.String())
			}
			if store.calls != 1 || s.devices != 1 || s.submits+s.queries+s.cancels != 0 {
				t.Fatal(store.calls, s)
			}
		})
	}
}

func TestPatternExecutionV2RealRecordPoisonAndUnknownCannotPreviewOrSubmit(t *testing.T) {
	r := publicRecord(t, "frame")
	for _, tc := range []struct {
		name   string
		mutate func(*patternsv2.Record)
	}{
		{"wrong-reference", func(r *patternsv2.Record) { r.ID = "different" }},
		{"wrong-outer-execution-contract", func(r *patternsv2.Record) { r.Compiled.Execution.Contract = "future" }},
		{"changed-frozen-frame", func(r *patternsv2.Record) { r.Compiled.Execution.FrameProgram.Frames[0].Targets[0].Color = "#123456" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := publicRecord(t, "frame")
			tc.mutate(&record)
			for _, preview := range []bool{false, true} {
				s := &publicPatternService{}
				store := &publicPatternStore{record: record}
				path := "/v2/devices/20005601/pattern-jobs"
				body := publicBytes(t, publicSubmit(publicRef(r)))
				if preview {
					path = "/v2/patterns/preview-ref"
					body = publicBytes(t, map[string]interface{}{"pattern_ref": publicRef(r)})
				}
				w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", path, body)
				patternV2Error(t, w, 500, "pattern_integrity_error")
				if store.calls != 1 || s.devices != 0 {
					t.Fatal(store.calls, s)
				}
			}
		})
	}
	// Read-only inspection is valid, but its unknown recipe is not executable.
	snapshot, err := patternsv2.InspectSnapshot(patternV2File(t, "responses/export-single-unknown.snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	unknown := snapshot.Records()[0].Record()
	for _, path := range []string{"/v2/patterns/preview-ref", "/v2/devices/20005601/pattern-jobs"} {
		s := &publicPatternService{}
		store := &publicPatternStore{record: unknown}
		var body interface{} = publicSubmit(publicRef(unknown))
		if path == "/v2/patterns/preview-ref" {
			body = map[string]interface{}{"pattern_ref": publicRef(unknown)}
		}
		w := patternHTTP(httpapi.New(s, httpapi.WithPatternStoreV2(store)), "POST", path, publicBytes(t, body))
		patternV2Error(t, w, 501, "pattern_source_unsupported")
		if store.calls != 1 {
			t.Fatal(store.calls)
		}
	}
}
