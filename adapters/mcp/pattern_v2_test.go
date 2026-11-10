package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func patternV2TestRef() patternV2Ref {
	return patternV2Ref{patternV2RecordContract, "lamp-study", "sha256:" + strings.Repeat("a", 64)}
}
func patternV2TestArgs() map[string]any {
	return map[string]any{"pattern_ref": patternV2TestRef(), "serial": "20005601", "request_id": testRequestID, "expected_daemon_instance_id": testInstanceID}
}
func patternV2TestEnvelope(t *testing.T, kind, status string) patternV2Envelope {
	t.Helper()
	e := patternV2Envelope{PatternRef: patternV2TestRef(), JobKind: kind}
	created := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	started := created.Add(time.Millisecond)
	finished := created.Add(time.Second)
	if kind == "frame" {
		j := orbitJob(orbitFixture(t, 4, nil), status)
		e.Job = independentJSON(j)
		e.ExecutionDigest, _ = patternV2FrameDigest(j.Program)
	} else {
		p := independentPreview(nil).Preview.Program
		j := patternJobWire{ID: "job_" + strings.Repeat("1", 32), Device: "20005601", Status: status, Program: p, TotalSteps: len(p.Steps) * p.Repeat, CreatedAt: created}
		if status != "queued" {
			j.StartedAt = &started
		}
		if patternV2Terminal(status) {
			j.FinishedAt = &finished
		}
		if status == "succeeded" {
			j.CompletedSteps = j.TotalSteps
		}
		e.Job = independentJSON(j)
		e.ExecutionDigest, _ = patternV2ProgramDigest(p)
	}
	return e
}
func patternV2TestPreview(t *testing.T, kind string, serial *string) patternV2Preview {
	t.Helper()
	e := patternV2TestEnvelope(t, kind, "queued")
	v := patternV2Preview{SchemaVersion: 1, Contract: patternV2PreviewContract, PatternRef: e.PatternRef, ExecutionDigest: e.ExecutionDigest, PatternExecution: frameExecutionWire{Available: true, Reason: "pattern_service_configured"}}
	if kind == "program" {
		p := independentPreview(serial).Preview
		v.Execution = patternV2Execution{"program", compileContract, independentJSON(p.Program)}
		v.Preview = independentJSON(p)
	} else {
		p := orbitFixture(t, 4, nil).Preview
		p.Execution = frameExecutionWire{Reason: "not_selected"}
		if serial != nil {
			p.Execution = frameExecutionWire{Available: true, Reason: "cached_profile_supported"}
			p.Device = &frameDeviceWire{Serial: *serial, Connection: "online"}
		}
		v.Execution = patternV2Execution{"frame_program", frameCompileContract, independentJSON(p.Program)}
		v.Preview = independentJSON(p)
	}
	return v
}
func patternV2TestHeaders(w http.ResponseWriter, operation string) {
	w.Header().Set("Content-Type", "application/json")
	if operation != "preview" {
		w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
	}
	if operation == "submit" {
		a := patternV2RequestArgs{PatternRef: patternV2TestRef(), Serial: "20005601", RequestID: testRequestID, ExpectedDaemonInstanceID: testInstanceID}
		w.Header().Set("Location", patternV2ReceiptPath(a))
		w.Header().Set("X-NB1-Replayed", "false")
	}
}
func TestPatternV2LegacySchemaFreeze(t *testing.T) {
	b, err := os.ReadFile("testdata/pattern-v2-legacy-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	if json.Unmarshal(b, &before) != nil {
		t.Fatal("bad legacy schema fixture")
	}
	s := connect(t, "http://127.0.0.1:1")
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 22 || len(before) != 12 {
		t.Fatal(len(tools.Tools), len(before))
	}
	seen := 0
	for _, tool := range tools.Tools {
		want, old := before[tool.Name]
		if !old {
			continue
		}
		seen++
		b, _ := json.Marshal(map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema})
		var got any
		_ = json.Unmarshal(b, &got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("old schema changed: %s", tool.Name)
		}
	}
	if seen != 12 {
		t.Fatal(seen)
	}
}
func TestPatternV2ExecutionGoldenDigests(t *testing.T) {
	b, err := os.ReadFile("testdata/pattern-v2-execution-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string             `json:"name"`
		Execution patternV2Execution `json:"execution"`
		Canonical string             `json:"canonical"`
		Digest    string             `json:"digest"`
	}
	if err = json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			got, err := validatePatternV2Execution(v.Execution)
			if err != nil || got != v.Digest {
				t.Fatal(got, err, v.Digest)
			}
		})
	}
}
func TestPatternV2OfficialSDKMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		for _, kind := range []string{"program", "frame"} {
			t.Run(transport+"/"+kind, func(t *testing.T) {
				var mu sync.Mutex
				var paths []string
				status := "running"
				serial := "20005601"
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					paths = append(paths, r.Method+" "+r.URL.RequestURI())
					switch {
					case r.URL.Path == "/v2/patterns/preview-ref":
						var a patternV2PreviewArgs
						if json.NewDecoder(r.Body).Decode(&a) != nil || a.PatternRef != patternV2TestRef() {
							t.Error("wrong preview Ref")
						}
						patternV2TestHeaders(w, "preview")
						_ = json.NewEncoder(w).Encode(patternV2TestPreview(t, kind, a.Serial))
					case strings.HasSuffix(r.URL.Path, "/pattern-jobs"):
						var a patternV2RunArgs
						if json.NewDecoder(r.Body).Decode(&a) != nil || a.PatternRef != patternV2TestRef() || a.Policy != "queue" || a.RequestID != testRequestID || a.ExpectedDaemonInstanceID != testInstanceID {
							t.Error("changed high-level run intent")
						}
						patternV2TestHeaders(w, "submit")
						w.WriteHeader(202)
						_ = json.NewEncoder(w).Encode(patternV2TestEnvelope(t, kind, status))
					case strings.HasSuffix(r.URL.Path, "/cancel"):
						var a struct {
							PatternRef patternV2Ref `json:"pattern_ref"`
							Instance   string       `json:"expected_daemon_instance_id"`
							JobID      string       `json:"confirmed_job_id"`
						}
						if json.NewDecoder(r.Body).Decode(&a) != nil || a.PatternRef != patternV2TestRef() || a.Instance != testInstanceID || a.JobID == "" {
							t.Error("cancel lost original binding")
						}
						status = "cancelling"
						patternV2TestHeaders(w, "cancel")
						w.WriteHeader(202)
						_ = json.NewEncoder(w).Encode(patternV2TestEnvelope(t, kind, status))
					case r.Method == "GET" && strings.Contains(r.URL.Path, "/pattern-requests/"):
						if r.URL.Query().Get("record_contract") != patternV2RecordContract || r.URL.Query().Get("revision") != patternV2TestRef().Revision || r.URL.Query().Get("pattern_id") != "lamp-study" || r.URL.Query().Get("expected_daemon_instance_id") != testInstanceID {
							t.Error("query lost exact Ref/ticket")
						}
						patternV2TestHeaders(w, "query")
						_ = json.NewEncoder(w).Encode(patternV2TestEnvelope(t, kind, status))
					default:
						t.Errorf("unexpected HTTP request %s %s", r.Method, r.URL)
						http.NotFound(w, r)
					}
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				if transport == "stdio" {
					_ = session.Close()
					session = connectStdio(t, srv.URL)
				}
				res, out := call(t, session, "preview_pattern", map[string]any{"pattern_ref": patternV2TestRef(), "serial": serial})
				if res.IsError || out.Kind != "pattern_ref_preview" || out.Accepted || out.RequestTicket != nil {
					t.Fatalf("preview: %+v", out)
				}
				args := patternV2TestArgs()
				args["policy"] = "queue"
				res, out = call(t, session, "run_pattern", args)
				if res.IsError || out.Kind != "pattern_job" || !out.Accepted || out.PollAfterMS != 200 {
					t.Fatalf("run: %+v", out)
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
				res, out = call(t, session, "get_pattern_request", patternV2TestArgs())
				if res.IsError || out.Kind != "pattern_request" || out.Accepted {
					t.Fatalf("query: %+v", out)
				}
				res, out = call(t, session, "cancel_pattern_request", patternV2TestArgs())
				if res.IsError || out.Kind != "pattern_cancel" || !out.Accepted || out.PollAfterMS != 200 {
					t.Fatalf("cancel: %+v", out)
				}
				res, out = call(t, session, "cancel_pattern_request", patternV2TestArgs())
				if res.IsError || out.Accepted {
					t.Fatalf("already cancelling must not repeat POST: %+v", out)
				}
				mu.Lock()
				status = "cancelled"
				mu.Unlock()
				res, out = call(t, session, "get_pattern_request", patternV2TestArgs())
				if res.IsError || out.PollAfterMS != 0 {
					t.Fatalf("completion: %+v", out)
				}
				mu.Lock()
				defer mu.Unlock()
				writes := 0
				previews := 0
				for _, path := range paths {
					if strings.Contains(path, "POST ") && !strings.Contains(path, "preview-ref") {
						writes++
					}
					if strings.Contains(path, "preview-ref") {
						previews++
					}
				}
				if writes != 2 || previews != 1 {
					t.Fatal(paths)
				}
			})
		}
	}
}
func TestPatternV2StrictInputBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) }))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, name := range []string{"run_pattern", "get_pattern_request", "cancel_pattern_request"} {
		for _, tc := range []struct {
			name   string
			change func(map[string]any)
		}{
			{"missing_ref", func(a map[string]any) { delete(a, "pattern_ref") }}, {"v1_ref", func(a map[string]any) { a["pattern_ref"] = independentRef() }}, {"latest", func(a map[string]any) { r := patternV2TestRef(); r.Revision = "latest"; a["pattern_ref"] = r }}, {"no_ticket", func(a map[string]any) { delete(a, "request_id") }}, {"path_serial", func(a map[string]any) { a["serial"] = "../device" }}, {"null", func(a map[string]any) { a["pattern_ref"] = nil }}, {"raw_frames", func(a map[string]any) { a["program"] = map[string]any{"frames": []any{}} }}, {"dry_run_false", func(a map[string]any) { a["dry_run"] = false }}, {"job_empty", func(a map[string]any) { a["job_id"] = "" }}, {"digest_bad", func(a map[string]any) { a["execution_digest"] = "SHA256:bad" }},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				a := patternV2TestArgs()
				if name == "run_pattern" {
					a["policy"] = "reject"
				}
				tc.change(a)
				res, _ := call(t, session, name, a)
				if !res.IsError {
					t.Fatal("invalid accepted")
				}
			})
		}
	}
	for _, name := range []string{"preview_pattern", "run_pattern", "get_pattern_request", "cancel_pattern_request"} {
		for _, raw := range []string{`{"pattern_ref":{},"pattern_ref":{}}`, `{"pattern_ref":null}`, `{"pattern_ref":{"id":"\ud800"}}`, `{"serial":"20005601","Serial":"20005601"}`} {
			t.Run(name+"/raw/"+raw, func(t *testing.T) {
				res, _ := call(t, session, name, json.RawMessage(raw))
				if !res.IsError {
					t.Fatal("invalid raw accepted")
				}
			})
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments reached HTTP", calls.Load())
	}
}

func TestPatternV2RunResponseBindingAndNoRetry(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, mode := range []string{"valid", "missing_instance", "wrong_instance", "duplicate_instance", "missing_replay", "duplicate_replay", "wrong_location", "duplicate_location", "wrong_ref", "wrong_digest", "changed_body_rehashed", "wrong_device", "bad_id", "bad_count", "bad_created", "status200", "unknown_status", "duplicate_json", "null", "alias", "tail", "wrong_media", "duplicate_media", "invalid_unicode", "oversize", "truncated", "503", "409", "redirect"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/pattern-jobs") {
						t.Error("run must not preview or recover automatically", r.URL)
					}
					body, _ := io.ReadAll(r.Body)
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(body, &fields)
					if len(fields) != 4 || fields["pattern_ref"] == nil || fields["program"] != nil {
						t.Error("run changed high-level Ref submission", string(body))
					}
					patternV2TestHeaders(w, "submit")
					e := patternV2TestEnvelope(t, kind, "running")
					status := 202
					switch mode {
					case "missing_instance":
						w.Header().Del("X-NB1-Daemon-Instance-ID")
					case "wrong_instance":
						w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_"+strings.Repeat("f", 32))
					case "duplicate_instance":
						w.Header().Add("X-NB1-Daemon-Instance-ID", testInstanceID)
					case "missing_replay":
						w.Header().Del("X-NB1-Replayed")
					case "duplicate_replay":
						w.Header().Add("X-NB1-Replayed", "false")
					case "wrong_location":
						w.Header().Set("Location", "/v1/jobs/other")
					case "duplicate_location":
						w.Header().Add("Location", w.Header().Get("Location"))
					case "wrong_ref":
						e.PatternRef.ID = "other"
					case "wrong_digest":
						e.ExecutionDigest = "sha256:" + strings.Repeat("f", 64)
					case "changed_body_rehashed":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["device"] = "20005602"
						e.Job = independentJSON(j)
					case "wrong_device":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["device"] = "20005602"
						e.Job = independentJSON(j)
					case "bad_id":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["id"] = "job_example"
						e.Job = independentJSON(j)
					case "bad_count":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["completed_steps"] = 999999999
						e.Job = independentJSON(j)
					case "bad_created":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["created_at"] = "0001-01-01T00:00:00Z"
						e.Job = independentJSON(j)
					case "status200":
						status = 200
					case "unknown_status":
						var j map[string]any
						_ = json.Unmarshal(e.Job, &j)
						j["status"] = "magic"
						e.Job = independentJSON(j)
					case "wrong_media":
						w.Header().Set("Content-Type", "text/plain")
					case "duplicate_media":
						w.Header().Add("Content-Type", "application/json")
					case "503":
						status = 503
					case "409":
						status = 409
					case "redirect":
						status = 307
						w.Header().Set("Location", "/redirected")
					}
					data := independentJSON(e)
					switch mode {
					case "duplicate_json":
						data = append([]byte(`{"job_kind":"frame",`), data[1:]...)
					case "null":
						data = []byte(`{"job":null}`)
					case "alias":
						data = []byte(strings.Replace(string(data), `"job_kind"`, `"Job_kind"`, 1))
					case "tail":
						data = append(data, []byte(` {}`)...)
					case "invalid_unicode":
						data = []byte(`{"error":{"code":"bad","message":"\ud800"}}`)
						status = 503
					case "oversize":
						data = append(data, []byte(strings.Repeat(" ", maxPatternResponseBytes))...)
					case "truncated":
						data = data[:len(data)/2]
					case "503", "409":
						data = []byte(`{"error":{"code":"pattern_library_unavailable","message":"not available"}}`)
					case "redirect":
						data = []byte(`{"error":{"code":"redirect","message":"not followed"}}`)
					}
					w.WriteHeader(status)
					_, _ = w.Write(data)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				args := patternV2TestArgs()
				args["policy"] = "reject"
				res, out := call(t, session, "run_pattern", args)
				if mode == "valid" {
					if res.IsError || !out.Accepted {
						t.Fatal(out)
					}
				} else {
					if !res.IsError || out.Error == nil {
						t.Fatal("damaged response accepted", out)
					}
					if mode != "409" && mode != "redirect" && !out.Error.OutcomeUnknown {
						t.Fatal("ambiguous acceptance lost", out)
					}
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
				if calls.Load() != 1 {
					t.Fatal("write retried or implicit preflight", calls.Load())
				}
			})
		}
	}
}
func TestPatternV2CancelPreservesBindingAndLastTrustworthyJob(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, mode := range []string{"cancelling", "cancelled", "natural_succeeded_hold", "terminal_succeeded", "already_cancelling", "missing_header", "duplicate_header", "wrong_ref", "wrong_digest", "different_job", "different_created", "progress_regression", "running", "503", "lost_body", "preflight_bad", "known_job_bad", "known_digest_bad"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var gets, posts atomic.Int32
				before := patternV2TestEnvelope(t, kind, "running")
				if mode == "terminal_succeeded" {
					before = patternV2TestEnvelope(t, kind, "succeeded")
				}
				if mode == "already_cancelling" {
					before = patternV2TestEnvelope(t, kind, "cancelling")
				}
				if mode == "natural_succeeded_hold" {
					var j map[string]any
					_ = json.Unmarshal(before.Job, &j)
					p := j["program"].(map[string]any)
					p["on_finish"] = "hold"
					before.Job = independentJSON(j)
					if kind == "program" {
						var job patternJobWire
						_ = json.Unmarshal(before.Job, &job)
						before.ExecutionDigest, _ = patternV2ProgramDigest(job.Program)
					} else {
						var job frameJobWire
						_ = json.Unmarshal(before.Job, &job)
						before.ExecutionDigest, _ = patternV2FrameDigest(job.Program)
					}
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						gets.Add(1)
						patternV2TestHeaders(w, "query")
						e := before
						if mode == "preflight_bad" {
							e.ExecutionDigest = "sha256:" + strings.Repeat("f", 64)
						}
						_ = json.NewEncoder(w).Encode(e)
						return
					}
					posts.Add(1)
					if !strings.HasSuffix(r.URL.Path, "/cancel") {
						t.Error("wrong cancel path", r.URL)
					}
					var in map[string]any
					_ = json.NewDecoder(r.Body).Decode(&in)
					state, _ := validatePatternV2Envelope(before, before.PatternRef, "20005601", nil, nil)
					if len(in) != 3 || in["confirmed_job_id"] != state.id || in["expected_daemon_instance_id"] != testInstanceID {
						t.Error("cancel not atomically bound", in)
					}
					patternV2TestHeaders(w, "cancel")
					after := patternV2TestEnvelope(t, kind, "cancelling")
					switch mode {
					case "cancelled":
						after = patternV2TestEnvelope(t, kind, "cancelled")
					case "natural_succeeded_hold":
						after = before
						var j map[string]any
						_ = json.Unmarshal(after.Job, &j)
						j["status"] = "succeeded"
						j["finished_at"] = "2026-11-10T00:00:01Z"
						if kind == "program" {
							j["completed_steps"] = j["total_steps"]
						} else {
							j["completed_frames"] = j["total_frames"]
							j["completed_target_writes"] = j["planned_target_writes"]
						}
						after.Job = independentJSON(j)
					case "missing_header":
						w.Header().Del("X-NB1-Daemon-Instance-ID")
					case "duplicate_header":
						w.Header().Add("X-NB1-Daemon-Instance-ID", testInstanceID)
					case "wrong_ref":
						after.PatternRef.ID = "wrong"
					case "wrong_digest":
						after.ExecutionDigest = "sha256:" + strings.Repeat("f", 64)
					case "different_job":
						var j map[string]any
						_ = json.Unmarshal(after.Job, &j)
						prefix := "job_"
						if kind == "frame" {
							prefix = "framejob_"
						}
						j["id"] = prefix + strings.Repeat("2", 32)
						after.Job = independentJSON(j)
					case "different_created":
						var j map[string]any
						_ = json.Unmarshal(after.Job, &j)
						j["created_at"] = "2026-11-09T23:59:59Z"
						after.Job = independentJSON(j)
					case "progress_regression":
						var j map[string]any
						_ = json.Unmarshal(after.Job, &j)
						j["started_at"] = "2026-11-10T00:00:00.0001Z"
						after.Job = independentJSON(j)
					case "running":
						after = before
					case "503":
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":{"code":"unavailable","message":"cancel may be in flight"}}`)
						return
					}
					w.WriteHeader(202)
					data := independentJSON(after)
					if mode == "lost_body" {
						data = data[:len(data)/2]
					}
					_, _ = w.Write(data)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				args := patternV2TestArgs()
				if mode == "known_job_bad" {
					args["job_id"] = "job_" + strings.Repeat("f", 32)
				}
				if mode == "known_digest_bad" {
					args["execution_digest"] = "sha256:" + strings.Repeat("f", 64)
				}
				res, out := call(t, session, "cancel_pattern_request", args)
				valid := mode == "cancelling" || mode == "cancelled" || mode == "natural_succeeded_hold" || mode == "terminal_succeeded" || mode == "already_cancelling"
				if valid == res.IsError {
					t.Fatalf("cancel validity mismatch: %+v", out)
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
				expectedPosts := int32(1)
				if mode == "terminal_succeeded" || mode == "already_cancelling" || mode == "preflight_bad" || mode == "known_job_bad" || mode == "known_digest_bad" {
					expectedPosts = 0
				}
				if gets.Load() != 1 || posts.Load() != expectedPosts {
					t.Fatal("cancel performed extra I/O", gets.Load(), posts.Load())
				}
				if !valid && expectedPosts == 1 {
					if !out.Error.OutcomeUnknown {
						t.Fatal("cancel error lost unknown state")
					}
					b, _ := json.Marshal(out.Data)
					var data patternV2CancelData
					_ = json.Unmarshal(b, &data)
					if !data.CancelAttempted || data.PatternJob == nil || !reflect.DeepEqual(patternV2TestJSONNode(*data.PatternJob), patternV2TestJSONNode(before)) {
						t.Fatal("cancel lost last trusted Job", string(b))
					}
				}
			})
		}
	}
}

type patternV2RoundTripFunc func(*http.Request) (*http.Response, error)

func (f patternV2RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPatternV2LostTransportDoesNotReplayOrPrepare(t *testing.T) {
	for _, name := range []string{"run_pattern", "cancel_pattern_request"} {
		t.Run(name, func(t *testing.T) {
			var calls int
			d, _ := newDaemon("http://127.0.0.1:1", time.Second)
			defer d.close()
			d.client.Transport = patternV2RoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method == "POST" {
					if r.GetBody != nil || r.Body == nil {
						t.Error("replayable or missing POST body")
					}
					return nil, errors.New("response lost after delivery")
				}
				e := patternV2TestEnvelope(t, "frame", "running")
				h := http.Header{"Content-Type": {"application/json"}, "X-Nb1-Daemon-Instance-Id": {testInstanceID}}
				return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(string(independentJSON(e))))}, nil
			})
			raw := patternV2TestArgs()
			if name == "run_pattern" {
				raw["policy"] = "replace"
			}
			out := invokePatternV2Direct(t, d, name, independentJSON(raw))
			if out.Error == nil || !out.Error.OutcomeUnknown {
				t.Fatal(out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			want := 1
			if name == "cancel_pattern_request" {
				want = 2
			}
			if calls != want {
				t.Fatal("write was retried", calls)
			}
		})
	}
}
func invokePatternV2Direct(t *testing.T, d *daemon, name string, raw json.RawMessage) output {
	t.Helper()
	server, client := newPatternV2Session(t, d)
	defer server()
	res, out := call(t, client, name, raw)
	if res == nil {
		t.Fatal("missing result")
	}
	return out
}

func newPatternV2Session(t *testing.T, d *daemon) (func(), *mcp.ClientSession) {
	t.Helper()
	a, b := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ss, err := newServer(d).Connect(ctx, a, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "pattern-v2-fake-transport", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		ss.Close()
		cancel()
		t.Fatal(err)
	}
	return func() { cs.Close(); ss.Close(); cancel() }, cs
}

func patternV2TestJSONNode(v any) any {
	b, _ := json.Marshal(v)
	var n any
	_ = json.Unmarshal(b, &n)
	return n
}
func TestPatternV2DirectDecoderRejectsTailAndOversize(t *testing.T) {
	for _, raw := range []string{`{} {}`, `{} true`, strings.Repeat(" ", 4096) + `{}`} {
		var a patternV2PreviewArgs
		if decodePatternV2Args([]byte(raw), &a) == nil {
			t.Fatal("accepted invalid direct JSON")
		}
	}
}

func TestPatternV2UnknownRecoveryOnlyUsesOriginalGET(t *testing.T) {
	for _, mode := range []string{"run_lost_header", "cancel_lost_header", "old_service", "receipt_expired", "different_instance"} {
		t.Run(mode, func(t *testing.T) {
			var posts, gets atomic.Int32
			job := patternV2TestEnvelope(t, "frame", "running")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					gets.Add(1)
					patternV2TestHeaders(w, "query")
					if mode == "receipt_expired" {
						w.WriteHeader(410)
						fmt.Fprint(w, `{"error":{"code":"request_expired","message":"old receipt expired"}}`)
						return
					}
					if mode == "different_instance" {
						w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_"+strings.Repeat("e", 32))
					}
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				posts.Add(1)
				if mode == "old_service" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(501)
					fmt.Fprint(w, `{"error":{"code":"pattern_execution_unsupported","message":"old service"}}`)
					return
				}
				operation := "submit"
				if strings.HasSuffix(r.URL.Path, "/cancel") {
					operation = "cancel"
				}
				patternV2TestHeaders(w, operation)
				w.Header().Del("X-NB1-Daemon-Instance-ID")
				w.WriteHeader(202)
				_ = json.NewEncoder(w).Encode(job)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			args := patternV2TestArgs()
			name := "run_pattern"
			if mode == "cancel_lost_header" {
				name = "cancel_pattern_request"
			} else {
				args["policy"] = "reject"
			}
			res, out := call(t, session, name, args)
			if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown {
				t.Fatal(out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			res, out = call(t, session, "get_pattern_request", patternV2TestArgs())
			if (mode == "receipt_expired" || mode == "different_instance") == !res.IsError {
				t.Fatal(out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			if posts.Load() != 1 {
				t.Fatal("recovery replayed original POST", posts.Load())
			}
		})
	}
}
func TestPatternV2PreviewBindingAndNoAdmission(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, mode := range []string{"no_target", "target", "wrong_ref", "wrong_digest", "wrong_execution_contract", "missing_default", "wrong_preview_program", "wrong_count", "wrong_target", "wrong_availability", "unexpected_receipt"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				serial := "20005601"
				target := &serial
				if mode == "no_target" {
					target = nil
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/v2/patterns/preview-ref" {
						t.Error("preview accessed execution", r.URL)
					}
					patternV2TestHeaders(w, "preview")
					v := patternV2TestPreview(t, kind, target)
					switch mode {
					case "wrong_ref":
						v.PatternRef.ID = "wrong"
					case "wrong_digest":
						v.ExecutionDigest = "sha256:" + strings.Repeat("f", 64)
					case "wrong_execution_contract":
						v.Execution.Contract = "future"
					case "missing_default":
						var p map[string]any
						_ = json.Unmarshal(v.Execution.Program, &p)
						delete(p, "repeat")
						v.Execution.Program = independentJSON(p)
					case "wrong_preview_program":
						var p map[string]any
						_ = json.Unmarshal(v.Preview, &p)
						body := p["program"].(map[string]any)
						body["on_finish"] = "hold"
						v.Preview = independentJSON(p)
					case "wrong_count":
						var p map[string]any
						_ = json.Unmarshal(v.Preview, &p)
						p["nominal_duration_ms"] = 1
						v.Preview = independentJSON(p)
					case "wrong_target":
						var p map[string]any
						_ = json.Unmarshal(v.Preview, &p)
						p["device"].(map[string]any)["serial"] = "20005602"
						v.Preview = independentJSON(p)
					case "wrong_availability":
						v.PatternExecution = frameExecutionWire{Available: true, Reason: "pattern_service_unsupported"}
					case "unexpected_receipt":
						w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
					}
					_ = json.NewEncoder(w).Encode(v)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				a := map[string]any{"pattern_ref": patternV2TestRef()}
				if target != nil {
					a["serial"] = serial
				}
				res, out := call(t, session, "preview_pattern", a)
				good := mode == "target" || mode == "no_target"
				if good == res.IsError || out.Accepted || out.RequestTicket != nil || out.Error != nil && out.Error.OutcomeUnknown || calls.Load() != 1 {
					t.Fatal(out, calls.Load())
				}
			})
		}
	}
}
func TestPatternV2ProgressValidation(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, mode := range []string{"normal", "different_id", "different_program_rehashed", "different_created", "started_changed", "progress_backwards", "cancelling_to_succeeded", "terminal_changed", "terminal_reordered_json"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				before := patternV2TestEnvelope(t, kind, "running")
				after := patternV2TestEnvelope(t, kind, "cancelling")
				switch mode {
				case "cancelling_to_succeeded":
					before = patternV2TestEnvelope(t, kind, "cancelling")
					after = patternV2TestEnvelope(t, kind, "succeeded")
				case "terminal_changed":
					before = patternV2TestEnvelope(t, kind, "cancelled")
					after = patternV2TestEnvelope(t, kind, "failed")
				case "terminal_reordered_json":
					before = patternV2TestEnvelope(t, kind, "succeeded")
					after = before
					var value map[string]any
					_ = json.Unmarshal(after.Job, &value)
					after.Job = independentJSON(value)
				default:
					var j map[string]any
					_ = json.Unmarshal(after.Job, &j)
					switch mode {
					case "different_id":
						prefix := "job_"
						if kind == "frame" {
							prefix = "framejob_"
						}
						j["id"] = prefix + strings.Repeat("b", 32)
					case "different_created":
						j["created_at"] = "2026-11-09T23:59:59Z"
					case "started_changed":
						j["started_at"] = "2026-11-10T00:00:00.0005Z"
					case "different_program_rehashed":
						j["program"].(map[string]any)["on_finish"] = "hold"
					case "progress_backwards":
						var b map[string]any
						_ = json.Unmarshal(before.Job, &b)
						if kind == "program" {
							b["completed_steps"] = 1
						} else {
							b["completed_frames"] = 1
							b["completed_target_writes"] = 4
						}
						before.Job = independentJSON(b)
					}
					after.Job = independentJSON(j)
					if mode == "different_program_rehashed" {
						if kind == "program" {
							var job patternJobWire
							_ = json.Unmarshal(after.Job, &job)
							after.ExecutionDigest, _ = patternV2ProgramDigest(job.Program)
						} else {
							var job frameJobWire
							_ = json.Unmarshal(after.Job, &job)
							after.ExecutionDigest, _ = patternV2FrameDigest(job.Program)
						}
					}
				}
				err := validatePatternV2Progress(before, after, "20005601")
				if (mode == "normal" || mode == "terminal_reordered_json") != (err == nil) {
					t.Fatal(mode, err)
				}
			})
		}
	}
}
func TestPatternV2TransportCancellationIsUnknownAndSingleAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	d, _ := newDaemon("http://127.0.0.1:1", time.Second)
	defer d.close()
	d.client.Transport = patternV2RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Error("POST body can be replayed")
		}
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	_, _, _, out := d.patternV2HTTP(ctx, "POST", "/v2/devices/20005601/pattern-jobs", map[string]any{"pattern_ref": patternV2TestRef()}, true)
	if out.Error == nil || !out.Error.OutcomeUnknown || calls != 1 {
		t.Fatal(out, calls)
	}
}

func TestPatternV2RunErrorClassificationKeepsOriginalOutcome(t *testing.T) {
	for _, tc := range []struct {
		status  int
		code    string
		unknown bool
	}{
		{400, "invalid_pattern_request", false}, {400, "invalid_request", false}, {404, "pattern_not_found", false}, {404, "not_found", true}, {409, "busy", false}, {409, "queue_full", false}, {409, "idempotency_conflict", false}, {409, "pattern_revision_conflict", false}, {409, "pattern_library_busy", false}, {429, "idempotency_capacity", false},
		{410, "request_expired", true}, {409, "daemon_instance_changed", true}, {404, "request_not_found", true}, {404, "http_error", true}, {404, "future_error", true}, {307, "redirect", true}, {400, "busy", true}, {500, "busy", true}, {501, "pattern_execution_unsupported", true},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.code), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": tc.code, "message": "diagnostic retained"}})
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			a := patternV2TestArgs()
			a["policy"] = "reject"
			res, out := call(t, session, "run_pattern", a)
			if !res.IsError || out.Error == nil || out.Error.Code != tc.code || out.Error.OutcomeUnknown != tc.unknown {
				t.Fatal(out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
		})
	}
}

func TestPatternV2ErrorRejectsContradictorySuccessMetadata(t *testing.T) {
	for _, key := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
		for _, value := range []string{"", "unexpected"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set(key, value)
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":{"code":"busy","message":"admission rejected"}}`)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				args := patternV2TestArgs()
				args["policy"] = "reject"
				res, out := call(t, session, "run_pattern", args)
				if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || !out.Error.OutcomeUnknown || out.Data != nil || calls.Load() != 1 {
					t.Fatal(out, calls.Load())
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			})
		}
	}
}
