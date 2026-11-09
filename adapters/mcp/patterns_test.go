package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func patternFixture() patternList {
	return patternList{SchemaVersion: 1, Contract: patternLibraryContract,
		Library:  patternLibraryInfo{Path: "/nonexistent/daemon-selected-patterns", State: "ready", CreateOnly: true, MaxRecords: 128, MaxPackageBytes: 1 << 20, RecordContract: patternRecordContract, CompileContract: compileContract, SnapshotKind: "nb1-pattern-snapshot"},
		Patterns: []patternSummary{{ID: "A-1", Title: "绿色提示\x00 💡", Revision: "sha256:" + strings.Repeat("a", 64), CompileContract: compileContract, TotalSteps: 2, NominalDurationMS: 100, OnFinish: "off", RequiredCapabilities: []string{"set", "program", "raw-rgb"}}, {ID: "b_2", Title: "保持", Revision: "sha256:" + strings.Repeat("b", 64), CompileContract: compileContract, TotalSteps: 2000, NominalDurationMS: 20000, OnFinish: "hold", RequiredCapabilities: []string{"set", "program", "raw-rgb", "hold"}}}}
}

func patternJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestPatternListOfficialSDKMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var requests atomic.Int32
			want := patternFixture()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "GET" || r.URL.Path != "/v1/patterns" || r.URL.RawQuery != "" || len(body) != 0 {
					t.Errorf("unexpected library request: %s %s %s", r.Method, r.URL, body)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				fmt.Fprint(w, patternJSON(want))
			}))
			defer server.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, server.URL)
			} else {
				session = connect(t, server.URL)
			}
			listed, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, tool := range listed.Tools {
				names = append(names, tool.Name)
				if tool.Name == "list_patterns" && (!tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.OutputSchema == nil) {
					t.Fatal("missing readonly schema/annotations")
				}
			}
			sort.Strings(names)
			if strings.Join(names, ",") != "cancel_job,get_device_state,get_job,list_devices,list_patterns,preview_light,preview_program,resolve_color,run_light,run_program,set_device_state" {
				t.Fatal(names)
			}
			result, out := call(t, session, "list_patterns", map[string]any{})
			if result.IsError || out.Kind != "pattern_list" || out.Accepted || out.Error != nil || out.RequestTicket != nil || out.DryRun || out.PollAfterMS != 0 || requests.Load() != 1 {
				t.Fatalf("bad directory result: %+v", out)
			}
			var got patternList
			if err := json.Unmarshal([]byte(patternJSON(out.Data)), &got); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(err, got)
			}
			// A valid repeated legacy Program can exceed the new light Action's
			// 1024 executed-step limit. This summary represents 1 step x2000.
			if got.Patterns[1].TotalSteps != 2000 {
				t.Fatal("legacy repeated Program restricted by new Action limit")
			}
		})
	}
}

func TestPatternListEmptyAndAbsoluteDisplayPaths(t *testing.T) {
	for _, path := range []string{"/nonexistent/never-read", `C:\Users\example\patterns`, `\\server\share\patterns`} {
		t.Run(path, func(t *testing.T) {
			fixture := patternFixture()
			fixture.Library.Path = path
			fixture.Patterns = []patternSummary{}
			if err := validatePatternList(fixture); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPatternListRejectsArgumentsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	session := connect(t, server.URL)
	for _, input := range []string{`null`, `[]`, `{"id":"A-1"}`, `{"path":"/tmp"}`, `{"limit":1}`, `{"serial":"20005601"}`, `{"request_id":"x"}`, `{"x":1,"x":2}`, `{"id":null}`} {
		t.Run(input, func(t *testing.T) {
			res, out := call(t, session, "list_patterns", json.RawMessage(input))
			if !res.IsError || out.Accepted || calls.Load() != 0 {
				t.Fatalf("arguments reached HTTP: %+v", out)
			}
		})
	}
	// Invalid JSON cannot be wrapped by the official SDK's JSON encoder.
	// Exercise the handler's raw guard directly without bypassing HTTP counts.
	d, err := newDaemon(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	res, out, _ := listPatterns(context.Background(), d, json.RawMessage(`{} {}`))
	if !res.IsError || out.Error == nil || calls.Load() != 0 {
		t.Fatal("raw trailing arguments reached HTTP", out)
	}
}

func TestPatternListStrictDirectoryContract(t *testing.T) {
	tests := []struct {
		name   string
		change func(*patternList)
	}{
		{"version", func(p *patternList) { p.SchemaVersion = 2 }}, {"contract", func(p *patternList) { p.Contract = "v2" }}, {"not-ready", func(p *patternList) { p.Library.State = "unavailable" }},
		{"not-create-only", func(p *patternList) { p.Library.CreateOnly = false }}, {"record-limit", func(p *patternList) { p.Library.MaxRecords = 129 }}, {"byte-limit", func(p *patternList) { p.Library.MaxPackageBytes = 2 << 20 }},
		{"record-contract", func(p *patternList) { p.Library.RecordContract = "v2" }}, {"compile-contract", func(p *patternList) { p.Library.CompileContract = "v2" }}, {"snapshot-kind", func(p *patternList) { p.Library.SnapshotKind = "other" }},
		{"relative-path", func(p *patternList) { p.Library.Path = "patterns" }}, {"drive-relative-path", func(p *patternList) { p.Library.Path = `C:patterns` }}, {"incomplete-unc", func(p *patternList) { p.Library.Path = `\\server` }},
		{"duplicate-id", func(p *patternList) { p.Patterns[1].ID = p.Patterns[0].ID }}, {"unsorted", func(p *patternList) { p.Patterns[0], p.Patterns[1] = p.Patterns[1], p.Patterns[0] }},
		{"path-id", func(p *patternList) { p.Patterns[0].ID = "../escape" }}, {"unicode-id", func(p *patternList) { p.Patterns[0].ID = "图案" }}, {"long-id", func(p *patternList) { p.Patterns[0].ID = strings.Repeat("a", 65) }},
		{"empty-title", func(p *patternList) { p.Patterns[0].Title = "" }}, {"long-title", func(p *patternList) { p.Patterns[0].Title = strings.Repeat("界", 257) }},
		{"revision", func(p *patternList) { p.Patterns[0].Revision = "sha256:" + strings.Repeat("A", 64) }}, {"entry-contract", func(p *patternList) { p.Patterns[0].CompileContract = "v2" }},
		{"zero-steps", func(p *patternList) { p.Patterns[0].TotalSteps = 0 }}, {"step-limit", func(p *patternList) { p.Patterns[0].TotalSteps = 1024*10000 + 1 }},
		{"zero-time", func(p *patternList) { p.Patterns[0].NominalDurationMS = 0 }}, {"time-limit", func(p *patternList) { p.Patterns[0].NominalDurationMS = 86400001 }}, {"too-short", func(p *patternList) { p.Patterns[0].NominalDurationMS = 19 }},
		{"finish", func(p *patternList) { p.Patterns[0].OnFinish = "resume" }}, {"missing-capability", func(p *patternList) { p.Patterns[0].RequiredCapabilities = []string{"set"} }},
		{"duplicate-capability", func(p *patternList) {
			p.Patterns[0].RequiredCapabilities = []string{"set", "program", "raw-rgb", "set"}
		}}, {"hold-capability", func(p *patternList) { p.Patterns[1].RequiredCapabilities = []string{"set", "program", "raw-rgb"} }},
		{"nil-patterns", func(p *patternList) { p.Patterns = nil }}, {"too-many-patterns", func(p *patternList) { p.Patterns = make([]patternSummary, 129) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := patternFixture()
			tc.change(&fixture)
			assertPatternWireError(t, 200, patternJSON(fixture), []string{"application/json"}, nil)
		})
	}
}

func assertPatternWireError(t *testing.T, status int, body string, media []string, headers map[string]string) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header()["Content-Type"] = media
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	res, out := call(t, connect(t, server.URL), "list_patterns", map[string]any{})
	if !res.IsError || out.Error == nil || out.Accepted || out.Error.OutcomeUnknown || out.RequestTicket != nil || calls.Load() != 1 {
		t.Fatalf("bad response accepted or retried: %+v calls=%d", out, calls.Load())
	}
}

func TestPatternListRawResponseBoundary(t *testing.T) {
	valid := patternJSON(patternFixture())
	for _, tc := range []struct {
		name, body string
		media      []string
		status     int
	}{
		{"duplicate-field", strings.Replace(valid, `"schema_version":1`, `"schema_version":2,"schema_version":1`, 1), []string{"application/json"}, 200},
		{"escaped-duplicate", strings.Replace(valid, `"schema_version":1`, `"schema_version":2,"schema_\u0076ersion":1`, 1), []string{"application/json"}, 200},
		{"unknown-field", strings.TrimSuffix(valid, "}") + `,"extra":true}`, []string{"application/json"}, 200},
		{"unknown-nested", strings.Replace(valid, `"state":"ready"`, `"state":"ready","extra":true`, 1), []string{"application/json"}, 200},
		{"null-field", strings.Replace(valid, `"state":"ready"`, `"state":null`, 1), []string{"application/json"}, 200},
		{"missing-field", strings.Replace(valid, `"schema_version":1,`, "", 1), []string{"application/json"}, 200},
		{"number-decimal", strings.Replace(valid, `"total_steps":2,`, `"total_steps":2.0,`, 1), []string{"application/json"}, 200},
		{"number-exponent", strings.Replace(valid, `"total_steps":2,`, `"total_steps":2e0,`, 1), []string{"application/json"}, 200},
		{"negative-zero", strings.Replace(valid, `"total_steps":2,`, `"total_steps":-0,`, 1), []string{"application/json"}, 200},
		{"trailing", valid + ` {}`, []string{"application/json"}, 200}, {"oversized", valid + strings.Repeat(" ", 1<<20), []string{"application/json"}, 200},
		{"utf8", strings.Replace(valid, "保持", string([]byte{0xff}), 1), []string{"application/json"}, 200},
		{"surrogate", strings.Replace(valid, "保持", `\ud800`, 1), []string{"application/json"}, 200},
		{"wrong-media", valid, []string{"text/plain"}, 200}, {"missing-media", valid, nil, 200}, {"duplicate-media", valid, []string{"application/json", "application/json"}, 200},
		{"status", valid, []string{"application/json"}, 202},
		{"error-duplicate", `{"error":{"code":"x","code":"pattern_library_unavailable","message":"bad"}}`, []string{"application/json"}, 503},
		{"error-unknown", `{"error":{"code":"x","message":"bad","extra":true}}`, []string{"application/json"}, 503},
		{"error-null", `{"error":{"code":"x","message":null}}`, []string{"application/json"}, 503},
		{"error-surrogate", `{"error":{"code":"x","message":"\udc00"}}`, []string{"application/json"}, 503},
		{"missing-route-html", `not found`, []string{"text/plain"}, 404},
	} {
		t.Run(tc.name, func(t *testing.T) { assertPatternWireError(t, tc.status, tc.body, tc.media, nil) })
	}
	for _, header := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
		t.Run(header, func(t *testing.T) {
			assertPatternWireError(t, 200, valid, []string{"application/json"}, map[string]string{header: "unexpected"})
		})
	}
}

func TestPatternListErrorsStayErrorsAndNeverInventJob(t *testing.T) {
	for _, status := range []int{404, 409, 501, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"pattern_library_unavailable","message":"not available"}}`)
			}))
			defer server.Close()
			res, out := call(t, connect(t, server.URL), "list_patterns", map[string]any{})
			if !res.IsError || out.Error == nil || out.Error.Code != "pattern_library_unavailable" || out.Error.HTTPStatus != status || out.Error.OutcomeUnknown || out.Data != nil || out.RequestTicket != nil || calls.Load() != 1 {
				t.Fatal(out)
			}
		})
	}
}

func TestPatternListTimeoutIsReadOnly(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer server.Close()
	d, err := newDaemon(server.URL, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := d.readPatternList(context.Background())
	if out.Error == nil || out.Error.OutcomeUnknown || out.RequestTicket != nil || out.Accepted || calls.Load() != 1 {
		t.Fatal(out)
	}
}
