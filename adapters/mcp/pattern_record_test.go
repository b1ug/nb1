package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func recordTestRevision(s string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(patternRecordContract+"\n"+s)))
}
func recordFixture(t *testing.T) (patternRecord, string) {
	t.Helper()
	// Independent literal for fixed ordering, long lower-case C0 escapes and
	// direct HTML/U+2028/U+2029. Do not derive this with the product writer.
	raw := `{"id":"A-1","title":"name\u0000\u0008\u0009\u000a\u000d\"\\<>&` + "\u2028\u2029💡" + `","source":{"program":{"steps":[{"led":0,"color":"#ff0000","fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"off"}},"compiled_program":{"steps":[{"led":0,"color":"#ff0000","fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"off"},"compile_contract":"nb1-program-effect/v1"}`
	var r patternRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	r.Revision = recordTestRevision(raw)
	return r, raw
}
func legacyRecordFixture(t *testing.T) (patternRecord, string) {
	t.Helper()
	raw := `{"id":"legacy","title":"Old pulse","source":{"effect":{"kind":"pulse","color":"#ff0000","led":0,"cycles":1,"period_ms":1000,"on_finish":"off"}},"compiled_program":{"steps":[{"led":0,"color":"#000000","fade_ms":0,"hold_ms":0},{"led":0,"color":"#ff0000","fade_ms":480,"hold_ms":0},{"led":0,"color":"#000000","fade_ms":490,"hold_ms":0}],"repeat":1,"on_finish":"off"},"compile_contract":"nb1-program-effect/v1"}`
	var r patternRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	r.Revision = recordTestRevision(raw)
	return r, raw
}

func steadyRecordFixture(t *testing.T) (patternRecord, string) {
	t.Helper()
	raw := `{"id":"steady","title":"Hold","source":{"effect":{"kind":"steady","color":"#00ff00","led":1,"fade_ms":0}},"compiled_program":{"steps":[{"led":1,"color":"#00ff00","fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"hold"},"compile_contract":"nb1-program-effect/v1"}`
	var r patternRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	r.Revision = recordTestRevision(raw)
	return r, raw
}

func TestGetPatternCanonicalGoldens(t *testing.T) {
	for _, makeFixture := range []func(*testing.T) (patternRecord, string){recordFixture, legacyRecordFixture, steadyRecordFixture} {
		r, want := makeFixture(t)
		got, err := canonicalPatternRecord(r)
		if err != nil || string(got) != want {
			t.Fatalf("canonical mismatch %v\n%s\nwant %s", err, got, want)
		}
		if err := validatePatternRecord(r, r.ID); err != nil {
			t.Fatal(err)
		}
		// Standard Marshal escapes HTML/line separators. They remain equivalent
		// wire spellings but the canonical digest must not use those escapes.
		var decoded patternRecord
		if err := decodeSoftwareWire([]byte(patternJSON(r)), &decoded); err != nil {
			t.Fatal(err)
		}
		if err := validatePatternRecord(decoded, r.ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestGetPatternOfficialSDKTransports(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			want, _ := recordFixture(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "GET" || r.URL.Path != "/v1/patterns/A-1" || r.URL.RawQuery != "" || len(body) != 0 || r.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected request %s %s %s", r.Method, r.URL, body)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				fmt.Fprint(w, patternJSON(want))
			}))
			defer server.Close()
			session := connect(t, server.URL)
			if transport == "stdio" {
				_ = session.Close()
				session = connectStdio(t, server.URL)
			}
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, tool := range tools.Tools {
				if tool.Name == "get_pattern" {
					found++
					if tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
						t.Fatal("missing read-only schema")
					}
				}
			}
			if found != 1 {
				t.Fatal("get_pattern missing/duplicate")
			}
			result, out := call(t, session, "get_pattern", map[string]any{"id": want.ID})
			if result.IsError || out.Error != nil || out.Kind != "pattern_record" || out.Accepted || out.RequestTicket != nil || out.DryRun || out.PollAfterMS != 0 || calls.Load() != 1 {
				t.Fatalf("unexpected result/calls %+v %d", out, calls.Load())
			}
			var got patternRecord
			if err := json.Unmarshal([]byte(patternJSON(out.Data)), &got); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(err, got)
			}
		})
	}
}
func TestGetPatternArgumentsNeverReachHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	d, err := newDaemon(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	for i, raw := range []string{"", `{}`, `null`, `[]`, `{"id":null}`, `{"id":""}`, `{"id":"_first"}`, `{"id":"../bad"}`, `{"id":"a/b"}`, `{"id":"中文"}`, `{"id":"` + strings.Repeat("x", 65) + `"}`, `{"id":"A-1","path":"/tmp"}`, `{"id":"A-1","revision":"x"}`, `{"id":"a","id":"b"}`, `{"id":"a","i\u0064":"b"}`, `{"id":"\ud800"}`, `{"id":"a"} {}`, `{"id":"a"}` + strings.Repeat(" ", 4096), string([]byte{'{', '"', 'i', 'd', '"', ':', '"', 0xff, '"', '}'})} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			result, out, err := getPattern(context.Background(), d, json.RawMessage(raw))
			if err != nil || !result.IsError || out.Error == nil || out.Accepted || out.Error.OutcomeUnknown || calls.Load() != 0 {
				t.Fatalf("invalid argument escaped %+v %v", out, err)
			}
		})
	}
}
func assertGetRecordResponse(t *testing.T, status int, body string, media []string, headers map[string]string, good bool) output {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/patterns/A-1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		w.Header()["Content-Type"] = media
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	d, err := newDaemon(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := d.readPatternRecord(context.Background(), "A-1")
	if (out.Error == nil) != good || out.Accepted || out.RequestTicket != nil || calls.Load() != 1 || out.Error != nil && out.Error.OutcomeUnknown {
		t.Fatalf("response result %+v calls=%d", out, calls.Load())
	}
	return out
}
func TestGetPatternStrictWireBoundary(t *testing.T) {
	r, _ := recordFixture(t)
	valid := patternJSON(r)
	cases := []struct{ name, body string }{
		{"duplicate", strings.Replace(valid, `"id":"A-1"`, `"id":"wrong","id":"A-1"`, 1)},
		{"escaped-duplicate", strings.Replace(valid, `"id":"A-1"`, `"id":"wrong","i\u0064":"A-1"`, 1)},
		{"unknown", strings.TrimSuffix(valid, "}") + `,"extra":true}`},
		{"nested-unknown", strings.Replace(valid, `"hold_ms":0`, `"hold_ms":0,"extra":true`, 1)},
		{"missing-id", strings.Replace(valid, `"id":"A-1",`, "", 1)},
		{"null-source", strings.Replace(valid, `"source":{`, `"source":null,"extra":{`, 1)},
		{"null-step", strings.Replace(valid, `"led":0`, `"led":null`, 1)},
		{"decimal", strings.Replace(valid, `"repeat":1`, `"repeat":1.0`, 1)},
		{"exponent", strings.Replace(valid, `"repeat":1`, `"repeat":1e0`, 1)},
		{"negative-zero", strings.Replace(valid, `"led":0`, `"led":-0`, 1)},
		{"utf8", strings.Replace(valid, "name", string([]byte{0xff}), 1)},
		{"high-surrogate", strings.Replace(valid, "name", `\ud800`, 1)},
		{"low-surrogate", strings.Replace(valid, "name", `\udc00`, 1)},
		{"trailing", valid + ` {}`}, {"too-large", valid + strings.Repeat(" ", maxPatternResponseBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertGetRecordResponse(t, 200, tc.body, []string{"application/json"}, nil, false) })
	}
	for _, tc := range []struct {
		name  string
		media []string
	}{{"missing", nil}, {"wrong", []string{"text/plain"}}, {"duplicate", []string{"application/json", "text/plain"}}, {"bad-parameter", []string{"application/json; charset="}}} {
		t.Run("media-"+tc.name, func(t *testing.T) { assertGetRecordResponse(t, 200, valid, tc.media, nil, false) })
	}
	for _, status := range []int{201, 202, 206} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			assertGetRecordResponse(t, status, valid, []string{"application/json"}, nil, false)
		})
	}
	for _, header := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
		t.Run(header, func(t *testing.T) {
			assertGetRecordResponse(t, 200, valid, []string{"application/json"}, map[string]string{header: "unexpected"}, false)
		})
	}
	assertGetRecordResponse(t, 200, valid+strings.Repeat(" ", maxPatternResponseBytes-len(valid)), []string{"application/json"}, nil, true)
}
func TestGetPatternNormalizedRecordAndBinding(t *testing.T) {
	cases := []struct {
		name   string
		change func(*patternRecord)
	}{
		{"id", func(r *patternRecord) { r.ID = "B" }}, {"bad-id", func(r *patternRecord) { r.ID = "_bad" }},
		{"empty-title", func(r *patternRecord) { r.Title = "" }}, {"long-title", func(r *patternRecord) { r.Title = strings.Repeat("💡", 257) }},
		{"contract", func(r *patternRecord) { r.CompileContract = "v2" }}, {"revision", func(r *patternRecord) { r.Revision = "sha256:" + strings.Repeat("0", 64) }},
		{"missing-source", func(r *patternRecord) { r.Source = patternRecordSource{} }}, {"both-sources", func(r *patternRecord) { r.Source.Effect = &effect{Kind: "steady", Color: "#ff0000"} }},
		{"source-differs", func(r *patternRecord) { r.Source.Program.Steps[0].Color = "#000000" }},
		{"repeat-default", func(r *patternRecord) { r.CompiledProgram.Repeat = 0 }}, {"finish-default", func(r *patternRecord) { r.CompiledProgram.OnFinish = "" }},
		{"uppercase", func(r *patternRecord) { r.CompiledProgram.Steps[0].Color = "#FF0000" }},
		{"led", func(r *patternRecord) { r.CompiledProgram.Steps[0].LED = 3 }}, {"fade", func(r *patternRecord) { r.CompiledProgram.Steps[0].FadeMS = 1 }},
		{"time", func(r *patternRecord) { r.CompiledProgram.Steps[0].HoldMS = 3600000; r.CompiledProgram.Repeat = 10000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := recordFixture(t)
			tc.change(&r)
			assertGetRecordResponse(t, 200, patternJSON(r), []string{"application/json"}, nil, false)
		})
	}
	for _, name := range []string{"period-default", "cycles-default", "finish-default", "inapplicable", "uppercase", "time"} {
		t.Run("legacy-"+name, func(t *testing.T) {
			r, _ := legacyRecordFixture(t)
			switch name {
			case "period-default":
				r.Source.Effect.PeriodMS = nil
			case "cycles-default":
				r.Source.Effect.Cycles = nil
			case "finish-default":
				r.Source.Effect.OnFinish = nil
			case "inapplicable":
				n := 0
				r.Source.Effect.FadeMS = &n
			case "uppercase":
				r.Source.Effect.Color = "#FF0000"
			case "time":
				n := 10000
				r.Source.Effect.Cycles = &n
			}
			if err := validatePatternRecord(r, r.ID); err == nil {
				t.Fatal("invalid legacy accepted")
			}
		})
	}
	// A valid saved Program may exceed the new Action's 1024 total-step cap.
	r, _ := recordFixture(t)
	r.Source.Program.Repeat = 2000
	r.CompiledProgram.Repeat = 2000
	raw, err := canonicalPatternRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Revision = recordTestRevision(string(raw))
	if err := validatePatternRecord(r, r.ID); err != nil {
		t.Fatal(err)
	}
}
func TestGetPatternErrorsStayReadOnly(t *testing.T) {
	for _, status := range []int{400, 404, 409, 501, 503} {
		out := assertGetRecordResponse(t, status, `{"error":{"code":"pattern_not_found","message":"missing"}}`, []string{"application/json"}, nil, false)
		if out.Error.Code != "pattern_not_found" || out.Error.HTTPStatus != status {
			t.Fatal(out)
		}
	}
	for _, body := range []string{`not JSON`, `{"error":{"code":"a","code":"b","message":"bad"}}`, `{"error":{"code":"a","message":null}}`, `{"error":{"code":"a","message":"bad","extra":true}}`, `{"error":{"code":"a","message":"\ud800"}}`, `{"error":{"code":"","message":"bad"}}`} {
		out := assertGetRecordResponse(t, 503, body, []string{"application/json"}, nil, false)
		if out.Error.Code != "invalid_daemon_response" {
			t.Fatal(out)
		}
	}
	assertGetRecordResponse(t, 404, "not found", []string{"text/plain"}, nil, false)
	out := assertGetRecordResponse(t, 503, `{"error":{"code":"unavailable","message":"bad"}}`, []string{"application/json"}, map[string]string{"Location": "/v1/jobs/x"}, false)
	if out.Error.Code != "invalid_daemon_response" {
		t.Fatal(out)
	}
}

type recordRoundTrip func(*http.Request) (*http.Response, error)

func (f recordRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type recordBody struct {
	source        io.Reader
	reads, closes int
	closeErr      error
}

func (b *recordBody) Read(p []byte) (int, error) { n, e := b.source.Read(p); b.reads += n; return n, e }
func (b *recordBody) Close() error               { b.closes++; return b.closeErr }
func TestGetPatternBoundedReadAndClose(t *testing.T) {
	r, _ := recordFixture(t)
	valid := patternJSON(r)
	for _, failClose := range []bool{false, true} {
		body := &recordBody{source: strings.NewReader(valid)}
		if failClose {
			body.closeErr = errors.New("fixture close failed")
		}
		d := &daemon{endpoint: "http://127.0.0.1:1", client: &http.Client{Transport: recordRoundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
		})}}
		out := d.readPatternRecord(context.Background(), "A-1")
		if body.closes != 1 || (out.Error != nil) != failClose {
			t.Fatalf("close: %+v %+v", out, body)
		}
	}
	body := &recordBody{source: strings.NewReader(valid + strings.Repeat(" ", 2*maxPatternResponseBytes))}
	d := &daemon{endpoint: "http://127.0.0.1:1", client: &http.Client{Transport: recordRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
	})}}
	out := d.readPatternRecord(context.Background(), "A-1")
	if out.Error == nil || body.reads != maxPatternResponseBytes+1 || body.closes != 1 {
		t.Fatalf("unbounded read: %+v %+v", out, body)
	}
}

func TestGetPatternCanonicalWriterIsBoundedAndTotal(t *testing.T) {
	var w patternRecordWriter
	all := make([]byte, 32)
	for i := range all {
		all[i] = byte(i)
	}
	w.text(string(all))
	want := `"`
	for i := 0; i < 32; i++ {
		want += fmt.Sprintf(`\u00%02x`, i)
	}
	want += `"`
	if w.err != nil || string(w.data) != want {
		t.Fatalf("C0 canonical spelling %q %v", w.data, w.err)
	}
	value, _ := recordFixture(t)
	value.Title = strings.Repeat("x", maxPatternResponseBytes)
	if data, err := canonicalPatternRecord(value); err == nil || data != nil {
		t.Fatal("oversized canonical writer returned bytes", len(data), err)
	}
	value, _ = recordFixture(t)
	value.Source.Program = nil
	if data, err := canonicalPatternRecord(value); err == nil || data != nil {
		t.Fatal("missing source canonicalized")
	}
	value, _ = steadyRecordFixture(t)
	value.Source.Effect.FadeMS = nil
	if data, err := canonicalPatternRecord(value); err == nil || data != nil {
		t.Fatal("missing effect field canonicalized")
	}
}
