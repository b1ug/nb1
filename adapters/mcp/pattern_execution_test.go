package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func independentRef() patternRef {
	return patternRef{ID: "frozen-2000", Revision: "sha256:" + strings.Repeat("a", 64)}
}
func independentPreview(serial *string) patternPreview {
	p := previewProgram{Steps: []step{{LED: 0, Color: "#123456", FadeMS: 0, HoldMS: 0}}, Repeat: 2000, OnFinish: "off"}
	v := previewResult{SchemaVersion: 1, CompileContract: compileContract, InputKind: "program", Program: p, NominalDurationMS: 20000, TotalSteps: 2000, RequiredCapabilities: []string{"set", "program", "raw-rgb"}, Validation: previewValidation{Program: "valid", Device: "not_requested"}}
	if serial != nil {
		v.Device = &previewDevice{Serial: *serial, Connection: "online", Capabilities: []string{"set", "program", "raw-rgb"}}
		v.Validation.Device = "supported"
	}
	return patternPreview{PatternRef: independentRef(), Preview: v}
}
func independentJob() map[string]any {
	p := independentPreview(nil).Preview
	return map[string]any{"id": "job_independent", "device": "20005601", "status": "succeeded", "program": p.Program, "total_steps": p.TotalSteps, "completed_steps": p.TotalSteps, "created_at": "2026-11-09T15:00:00Z", "started_at": "2026-11-09T15:00:00Z", "finished_at": "2026-11-09T15:00:20Z"}
}
func independentRunArgs() map[string]any {
	return map[string]any{"serial": "20005601", "pattern_ref": independentRef(), "policy": "reject", "request_id": testRequestID, "expected_daemon_instance_id": testInstanceID}
}
func independentJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func independentReceiptHeaders(w http.ResponseWriter) {
	w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
	w.Header().Set("X-NB1-Replayed", "false")
	w.Header().Set("Location", "/v1/devices/20005601/requests/"+testRequestID+"?expected_daemon_instance_id="+testInstanceID)
}

func TestIndependentPatternInputRejection(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	session := connect(t, srv.URL)
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing_policy", func(a map[string]any) { delete(a, "policy") }},
		{"replace", func(a map[string]any) { a["policy"] = "replace" }},
		{"queue", func(a map[string]any) { a["policy"] = "queue" }},
		{"missing_request", func(a map[string]any) { delete(a, "request_id") }},
		{"missing_instance", func(a map[string]any) { delete(a, "expected_daemon_instance_id") }},
		{"dry_false_presence", func(a map[string]any) { a["dry_run"] = false }},
		{"dry_true", func(a map[string]any) { a["dry_run"] = true }},
		{"program_and_ref", func(a map[string]any) { a["program"] = independentPreview(nil).Preview.Program }},
		{"effect_and_ref", func(a map[string]any) { a["effect"] = map[string]any{"kind": "steady", "color": "#123456"} }},
		{"bad_revision", func(a map[string]any) { a["pattern_ref"] = map[string]any{"id": "frozen-2000", "revision": "latest"} }},
		{"path_id", func(a map[string]any) {
			a["pattern_ref"] = map[string]any{"id": "../x", "revision": independentRef().Revision}
		}},
		{"extra_ref", func(a map[string]any) {
			a["pattern_ref"] = map[string]any{"id": independentRef().ID, "revision": independentRef().Revision, "path": "/tmp/private"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := independentRunArgs()
			tc.change(a)
			res, _ := call(t, session, "run_program", a)
			if !res.IsError {
				t.Fatal("accepted invalid arguments")
			}
			if requests.Load() != 0 {
				t.Fatal("invalid argument contacted HTTP")
			}
		})
	}
}

func TestIndependentPatternResponseFailures(t *testing.T) {
	mutations := []struct {
		name    string
		status  int
		mutate  func([]byte) []byte
		headers func(http.Header)
	}{
		{"duplicate_field", 200, func(b []byte) []byte { return append([]byte(`{"extra":0,"extra":1,`), b[1:]...) }, nil},
		{"unknown_field", 200, func(b []byte) []byte { return append([]byte(`{"extra":0,`), b[1:]...) }, nil},
		{"null", 200, func([]byte) []byte { return []byte(`null`) }, nil},
		{"trailing", 200, func(b []byte) []byte { return append(b, []byte(` {}`)...) }, nil},
		{"invalid_utf8", 200, func(b []byte) []byte {
			return append(append([]byte(`{"bad":"`), 0xff), append([]byte(`",`), b[1:]...)...)
		}, nil},
		{"unpaired_surrogate", 200, func(b []byte) []byte { return append([]byte(`{"bad":"\ud800",`), b[1:]...) }, nil},
		{"wrong_mime", 200, nil, func(h http.Header) { h.Set("Content-Type", "text/plain") }},
		{"duplicate_mime", 200, nil, func(h http.Header) { h.Add("Content-Type", "application/json") }},
		{"error_duplicate_code", 400, func([]byte) []byte {
			return []byte(`{"error":{"code":"daemon_instance_changed","code":"invalid_request","message":"x"}}`)
		}, nil},
		{"error_unknown", 400, func([]byte) []byte { return []byte(`{"error":{"code":"invalid_request","message":"x","extra":true}}`) }, nil},
		{"error_surrogate", 400, func([]byte) []byte { return []byte(`{"error":{"code":"invalid_request","message":"\ud800"}}`) }, nil},
	}
	for _, phase := range []string{"preview", "preflight", "write"} {
		for _, tc := range mutations {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				var previews, writes atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					isWrite := r.URL.Path == "/v1/devices/20005601/jobs"
					var body []byte
					if isWrite {
						writes.Add(1)
						independentReceiptHeaders(w)
						body = independentJSON(independentJob())
					} else if r.URL.Path == "/v1/programs/preview" {
						previews.Add(1)
						var req previewArgs
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						body = independentJSON(independentPreview(req.Serial))
					} else {
						t.Errorf("unexpected HTTP path %s", r.URL.Path)
						w.WriteHeader(500)
						return
					}
					target := (phase == "write") == isWrite
					status := 200
					if isWrite {
						status = 202
					}
					if target {
						if tc.mutate != nil {
							body = tc.mutate(body)
						}
						if tc.headers != nil {
							tc.headers(w.Header())
						}
						if tc.status != 200 {
							status = tc.status
						}
					}
					w.WriteHeader(status)
					w.Write(body)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				tool, args := "run_program", independentRunArgs()
				if phase == "preview" {
					tool = "preview_program"
					args = map[string]any{"pattern_ref": independentRef()}
				}
				res, out := call(t, session, tool, args)
				if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" {
					t.Fatalf("invalid response accepted/misclassified: %+v", out)
				}
				wantWrites := int32(0)
				if phase == "write" {
					wantWrites = 1
				}
				if previews.Load() != 1 || writes.Load() != wantWrites {
					t.Fatalf("retry/unexpected calls p=%d w=%d", previews.Load(), writes.Load())
				}
				if phase == "preview" {
					if out.Error.OutcomeUnknown || out.RequestTicket != nil {
						t.Fatalf("readonly preview attached ticket/unknown: %+v", out)
					}
				} else {
					if !out.Error.OutcomeUnknown || out.RequestTicket == nil || out.RequestTicket.RequestID != testRequestID || out.RequestTicket.ExpectedDaemonInstanceID != testInstanceID {
						t.Fatalf("original ticket lost: %+v", out)
					}
				}
			})
		}
	}
}

func TestIndependentPatternValidMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var previews, writes, other atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if r.Method != "POST" {
					other.Add(1)
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/v1/programs/preview" {
					previews.Add(1)
					var req previewArgs
					json.NewDecoder(r.Body).Decode(&req)
					json.NewEncoder(w).Encode(independentPreview(req.Serial))
					return
				}
				if r.URL.Path != "/v1/devices/20005601/jobs" {
					other.Add(1)
					w.WriteHeader(500)
					return
				}
				writes.Add(1)
				var fields map[string]json.RawMessage
				json.NewDecoder(r.Body).Decode(&fields)
				if len(fields) != 4 || string(fields["policy"]) != `"reject"` || string(fields["request_id"]) != fmt.Sprintf("%q", testRequestID) || string(fields["expected_daemon_instance_id"]) != fmt.Sprintf("%q", testInstanceID) {
					t.Errorf("rewritten/unexpected request %s", independentJSON(fields))
				}
				var ref patternRef
				json.Unmarshal(fields["pattern_ref"], &ref)
				if ref != independentRef() {
					t.Error("ref changed")
				}
				independentReceiptHeaders(w)
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(independentJob())
			}))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			listed, err := session.ListTools(context.Background(), nil)
			if err != nil || len(listed.Tools) != 22 {
				t.Fatalf("tools %v %v", listed, err)
			}
			res, out := call(t, session, "preview_program", json.RawMessage(`{"patt\u0065rn_ref":{"id":"frozen-2000","revision":"`+independentRef().Revision+`"}}`))
			if res.IsError || out.Error != nil || out.Kind != "pattern_preview" || out.Accepted {
				t.Fatalf("escaped-key preview %+v", out)
			}
			res, out = call(t, session, "run_program", independentRunArgs())
			if res.IsError || out.Error != nil || out.Kind != "job" || !out.Accepted || out.RequestTicket == nil || out.RequestTicket.RequestID != testRequestID {
				t.Fatalf("run %+v", out)
			}
			if previews.Load() != 2 || writes.Load() != 1 || other.Load() != 0 {
				t.Fatalf("unexpected calls %d/%d/%d", previews.Load(), writes.Load(), other.Load())
			}
		})
	}
}

func TestIndependentPatternRawArgumentsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	session := connect(t, srv.URL)
	valid := string(independentJSON(independentRunArgs()))
	cases := map[string]string{
		"dry_false_presence":         strings.Replace(valid, `"policy":"reject"`, `"policy":"reject","dry_run":false`, 1),
		"escaped_dry_false_presence": strings.Replace(valid, `"policy":"reject"`, `"policy":"reject","dry\u005frun":false`, 1),
		"duplicate_ref":              strings.Replace(valid, `"pattern_ref":`, `"pattern_ref":{},"pattern_ref":`, 1),
		"unpaired_surrogate":         strings.Replace(valid, `frozen-2000`, `frozen-\ud800`, 1),
		"invalid_utf8":               strings.Replace(valid, `frozen-2000`, "frozen-"+string([]byte{0xff}), 1),
		"explicit_null":              strings.Replace(valid, `"policy":"reject"`, `"policy":null`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			res, _ := call(t, session, "run_program", json.RawMessage(raw))
			if !res.IsError {
				t.Fatal("invalid raw Pattern argument accepted")
			}
			if requests.Load() != 0 {
				t.Fatal("invalid raw argument contacted HTTP")
			}
		})
	}
}

func TestIndependentPatternJobAndPreviewBinding(t *testing.T) {
	cases := []struct {
		name, phase   string
		changePreview func(*patternPreview)
		changeJob     func(map[string]any)
		changeHeaders func(http.Header)
	}{
		{name: "succeeded_incomplete", phase: "write", changeJob: func(j map[string]any) { j["completed_steps"] = 0 }},
		{name: "missing_created", phase: "write", changeJob: func(j map[string]any) { delete(j, "created_at") }},
		{name: "invalid_created", phase: "write", changeJob: func(j map[string]any) { j["created_at"] = "not-a-time" }},
		{name: "unknown_job_field", phase: "write", changeJob: func(j map[string]any) { j["extra"] = true }},
		{name: "changed_program", phase: "write", changeJob: func(j map[string]any) {
			p := independentPreview(nil).Preview.Program
			p.Steps = append([]step(nil), p.Steps...)
			p.Steps[0].Color = "#ffffff"
			j["program"] = p
		}},
		{name: "changed_steps", phase: "write", changeJob: func(j map[string]any) { j["total_steps"] = 1999 }},
		{name: "changed_device", phase: "write", changeJob: func(j map[string]any) { j["device"] = "other" }},
		{name: "changed_instance", phase: "write", changeHeaders: func(h http.Header) { h.Set("X-NB1-Daemon-Instance-ID", "daemon_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }},
		{name: "missing_location", phase: "write", changeHeaders: func(h http.Header) { h.Del("Location") }},
		{name: "wrong_location", phase: "write", changeHeaders: func(h http.Header) { h.Set("Location", "/v1/jobs/job_independent") }},
		{name: "duplicate_location", phase: "write", changeHeaders: func(h http.Header) { h.Add("Location", h.Get("Location")) }},
		{name: "changed_ref", phase: "preflight", changePreview: func(p *patternPreview) { p.PatternRef.ID = "other" }},
		{name: "changed_revision", phase: "preflight", changePreview: func(p *patternPreview) { p.PatternRef.Revision = "sha256:" + strings.Repeat("b", 64) }},
		{name: "changed_target", phase: "preflight", changePreview: func(p *patternPreview) { p.Preview.Device.Serial = "other" }},
		{name: "wrong_duration", phase: "preflight", changePreview: func(p *patternPreview) { p.Preview.NominalDurationMS++ }},
		{name: "wrong_steps", phase: "preflight", changePreview: func(p *patternPreview) { p.Preview.TotalSteps++ }},
		{name: "submission_header_on_preview", phase: "preflight", changeHeaders: func(h http.Header) { h.Set("Location", "/v1/jobs/job_independent") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var previews, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/programs/preview" {
					previews.Add(1)
					serial := "20005601"
					p := independentPreview(&serial)
					if tc.changePreview != nil {
						tc.changePreview(&p)
					}
					if tc.phase == "preflight" && tc.changeHeaders != nil {
						tc.changeHeaders(w.Header())
					}
					json.NewEncoder(w).Encode(p)
					return
				}
				if r.URL.Path != "/v1/devices/20005601/jobs" {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				writes.Add(1)
				j := independentJob()
				if tc.changeJob != nil {
					tc.changeJob(j)
				}
				independentReceiptHeaders(w)
				if tc.phase == "write" && tc.changeHeaders != nil {
					tc.changeHeaders(w.Header())
				}
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(j)
			}))
			defer srv.Close()
			res, out := call(t, connect(t, srv.URL), "run_program", independentRunArgs())
			if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown || out.RequestTicket == nil || out.RequestTicket.RequestID != testRequestID || out.RequestTicket.ExpectedDaemonInstanceID != testInstanceID {
				t.Fatalf("unbound result accepted or original ticket lost: %+v", out)
			}
			want := int32(1)
			if tc.phase == "preflight" {
				want = 0
			}
			if previews.Load() != 1 || writes.Load() != want {
				t.Fatalf("retry/unexpected call count p=%d w=%d", previews.Load(), writes.Load())
			}
		})
	}
}

func TestIndependentPatternMissingLibraryRetainsOriginalTicket(t *testing.T) {
	for _, tool := range []string{"preview_program", "run_program"} {
		t.Run(tool, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/programs/preview" {
					t.Error("unexpected submission")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(404)
				fmt.Fprint(w, `{"error":{"code":"pattern_not_found","message":"original id is absent"}}`)
			}))
			defer srv.Close()
			args := independentRunArgs()
			if tool == "preview_program" {
				args = map[string]any{"pattern_ref": independentRef()}
			}
			res, out := call(t, connect(t, srv.URL), tool, args)
			if !res.IsError || out.Error == nil || out.Error.Code != "pattern_not_found" || requests.Load() != 1 {
				t.Fatalf("missing library swallowed or retried: %+v", out)
			}
			if tool == "run_program" {
				if !out.Error.OutcomeUnknown || out.RequestTicket == nil || out.RequestTicket.RequestID != testRequestID {
					t.Fatalf("original outcome lost: %+v", out)
				}
			} else if out.Error.OutcomeUnknown || out.RequestTicket != nil {
				t.Fatalf("readonly result has ticket: %+v", out)
			}
		})
	}
}
