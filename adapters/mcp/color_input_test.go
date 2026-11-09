package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func colorResultFixture(kind, hex string, rgb colorRGB8) colorResolution {
	r := colorResolution{1, colorInputContract, kind, hex, rgb, ""}
	if kind == "name" {
		r.Palette = colorNamePalette
	}
	return r
}

func TestOfficialSDKColorMemoryAndStdioPreserveOriginalNumbers(t *testing.T) {
	// Contract goldens only: no name dictionary or HSV implementation in the adapter.
	fixtures := []struct {
		input string
		value colorResolution
	}{
		{`"green"`, colorResultFixture("name", "#008000", colorRGB8{0, 128, 0})},
		{`"LiMe"`, colorResultFixture("name", "#00ff00", colorRGB8{0, 255, 0})},
		{`" \tGrEeN\r\n"`, colorResultFixture("name", "#008000", colorRGB8{0, 128, 0})},
		{`"#12AbEF"`, colorResultFixture("hex6", "#12abef", colorRGB8{18, 171, 239})},
		{`"#0f8"`, colorResultFixture("hex3", "#00ff88", colorRGB8{0, 255, 136})},
		{`{"space":"rgb8","r":0,"g":255,"b":136}`, colorResultFixture("rgb8", "#00ff88", colorRGB8{0, 255, 136})},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`, colorResultFixture("hsv", "#008000", colorRGB8{0, 128, 0})},
		{`{"space":"hsv","h_deg":120.000000,"s_pct":100.000000,"v_pct":50.000000}`, colorResultFixture("hsv", "#008000", colorRGB8{0, 128, 0})},
		{`{"space":"hsv","h_deg":0.000001,"s_pct":0,"v_pct":50}`, colorResultFixture("hsv", "#808080", colorRGB8{128, 128, 128})},
	}
	// These are mathematically within the schema's number bounds but their exact
	// lexical forms are invalid. The daemon, not float64 re-marshalling, decides.
	invalidLexemes := []string{
		`{"space":"rgb8","r":1.0,"g":0,"b":0}`,
		`{"space":"rgb8","r":1e0,"g":0,"b":0}`,
		`{"space":"rgb8","r":-0,"g":0,"b":0}`,
		`{"space":"hsv","h_deg":1e2,"s_pct":100,"v_pct":50}`,
		`{"space":"hsv","h_deg":0.0000001,"s_pct":100,"v_pct":50}`,
		`{"space":"hsv","h_deg":-0,"s_pct":100,"v_pct":50}`,
	}
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var mu sync.Mutex
			var received []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/colors/resolve" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected side-effect/discovery route: %s %s", r.Method, r.URL.RequestURI())
				}
				var args resolveColorArgs
				if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
					t.Error(err)
				}
				raw := string(args.Color)
				received = append(received, raw)
				for _, f := range fixtures {
					if raw == f.input {
						_ = json.NewEncoder(w).Encode(f.value)
						return
					}
				}
				for _, rawInvalid := range invalidLexemes {
					if raw == rawInvalid {
						w.WriteHeader(400)
						fmt.Fprint(w, `{"error":{"code":"invalid_color","message":"invalid numeric lexeme"}}`)
						return
					}
				}
				t.Errorf("original JSON lexeme changed before HTTP: %s", raw)
				w.WriteHeader(500)
			}))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			listed, err := session.ListTools(context.Background(), nil)
			if err != nil || len(listed.Tools) != 8 {
				t.Fatalf("eight tools required: %+v %v", listed, err)
			}
			found := false
			for _, tool := range listed.Tools {
				if tool.Name == "resolve_color" {
					found = true
					if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
						t.Fatalf("missing closed-world read-only color schema/hints: %+v", tool)
					}
				}
			}
			if !found {
				t.Fatal("resolve_color missing")
			}
			for _, f := range fixtures {
				res, out := call(t, session, "resolve_color", json.RawMessage(`{"color":`+f.input+`}`))
				if res.IsError || out.Error != nil || out.Kind != "color_resolution" || out.Accepted || out.DryRun || out.PollAfterMS != 0 || out.RequestTicket != nil {
					t.Fatalf("color resolution became a job or failed: %s %+v", f.input, out)
				}
				encoded, _ := json.Marshal(out.Data)
				var actual colorResolution
				if err := json.Unmarshal(encoded, &actual); err != nil || actual != f.value {
					t.Fatalf("selected result changed: %s %+v %v", f.input, actual, err)
				}
			}
			for _, input := range invalidLexemes {
				res, out := call(t, session, "resolve_color", json.RawMessage(`{"color":`+input+`}`))
				if !res.IsError || out.Error == nil || out.Error.Code != "invalid_color" || out.Error.OutcomeUnknown || out.Accepted {
					t.Fatalf("invalid original lexeme silently normalized: %s %+v", input, out)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			var want []string
			for _, f := range fixtures {
				want = append(want, f.input)
			}
			want = append(want, invalidLexemes...)
			if !reflect.DeepEqual(received, want) {
				t.Fatalf("wrong POST count/order or rewritten numbers: got %v want %v", received, want)
			}
		})
	}
}

func TestSDKColorClosedSchemaAndRawGuardRejectBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); t.Error("invalid input reached HTTP") }))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, raw := range []string{
		`{}`, `null`, `{"color":null}`, `{"color":[]}`, `{"color":true}`, `{"color":12}`,
		`{"color":"green","serial":"20005601"}`, `{"color":"green","policy":"reject"}`, `{"color":"green","dry_run":true}`,
		`{"Color":"green"}`, `{"color":"green","c\u006flor":"lime"}`,
		`{"color":{"space":"rgb8","r":0,"g":0}}`,
		`{"color":{"space":"rgb8","r":256,"g":0,"b":0}}`,
		`{"color":{"space":"rgb8","r":-1,"g":0,"b":0}}`,
		`{"color":{"space":"rgb8","r":1.5,"g":0,"b":0}}`,
		`{"color":{"space":"rgb8","R":1,"g":0,"b":0}}`,
		`{"color":{"space":"rgb8","r":1,"r":2,"g":0,"b":0}}`,
		`{"color":{"space":"rgb8","r":1,"g":0,"b":0,"alpha":1}}`,
		`{"color":{"space":"hsv","h_deg":361,"s_pct":0,"v_pct":0}}`,
		`{"color":{"space":"hsv","h_deg":0,"s_pct":101,"v_pct":0}}`,
		`{"color":{"space":"hsv","h_deg":0,"s_pct":0,"v_pct":-1}}`,
		`{"color":{"space":"hsv","h_deg":"120","s_pct":0,"v_pct":0}}`,
		`{"color":{"space":"HSV","h_deg":120,"s_pct":0,"v_pct":0}}`,
		`{"color":{"space":"hsl","h_deg":120,"s_pct":0,"v_pct":0}}`,
		`{"color":{"space":"hsv","h_deg":0,"s_pct":null,"v_pct":0}}`,
		`{"color":"` + strings.Repeat("x", 65) + `"}`,
		`{"color":"` + strings.Repeat("绿", 33) + `"}`,
		`{"color":"` + strings.Repeat("x", maxColorRequestBytes) + `"}`,
	} {
		res, out := call(t, session, "resolve_color", json.RawMessage(raw))
		if !res.IsError {
			t.Fatalf("invalid input accepted: %s %+v", raw, out)
		}
	}
	// A trailing JSON value cannot be embedded as a valid SDK RawMessage; check
	// the same handler boundary directly without sending an invalid JSON-RPC frame.
	d, err := newDaemon(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	_, out, _ := resolveColor(context.Background(), d, json.RawMessage(`{"color":"green"} {}`))
	if out.Error == nil || requests.Load() != 0 {
		t.Fatalf("trailing JSON or prior invalid arguments reached HTTP: %+v %d", out, requests.Load())
	}
}

func TestColorResolutionRejectsBrokenResponsesWithoutJobUncertainty(t *testing.T) {
	base, _ := json.Marshal(colorResultFixture("name", "#008000", colorRGB8{0, 128, 0}))
	replace := func(from, to string) string { return strings.Replace(string(base), from, to, 1) }
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"accepted", string(base), 202},
		{"wrong schema", replace(`"schema_version":1`, `"schema_version":2`), 200},
		{"unknown contract", replace(colorInputContract, "future/v2"), 200},
		{"wrong kind", replace(`"input_kind":"name"`, `"input_kind":"hex6"`), 200},
		{"upper canonical", replace("#008000", "#00A000"), 200},
		{"RGB disagreement", replace(`"g":128`, `"g":127`), 200},
		{"out of range", replace(`"r":0`, `"r":256`), 200},
		{"missing zero", replace(`"r":0,`, ``), 200},
		{"missing palette", replace(`,"palette":"css-named-2026"`, ``), 200},
		{"wrong palette", replace(colorNamePalette, "old-sdk"), 200},
		{"wrong case", replace("schema_version", "Schema_version"), 200},
		{"duplicate escaped key", replace(`"schema_version":1`, `"schema_version":1,"schema_vers\u0069on":1`), 200},
		{"null", replace(`"rgb8":{"r":0,"g":128,"b":0}`, `"rgb8":null`), 200},
		{"unknown job property", replace(`"schema_version":1`, `"schema_version":1,"job":{}`), 200},
		{"trailing JSON", string(base) + `{}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "resolve_color", map[string]any{"color": "green"})
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || out.Error.OutcomeUnknown || out.Accepted || requests.Load() != 1 {
				t.Fatalf("broken color response accepted/ascribed to job: %+v requests=%d", out, requests.Load())
			}
		})
	}
	for _, tc := range []struct {
		input  string
		result colorResolution
	}{
		{`"#0f8"`, colorResultFixture("hex3", "#ff0088", colorRGB8{255, 0, 136})},
		{`"#12abef"`, colorResultFixture("hex6", "#000000", colorRGB8{})},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`, colorResultFixture("rgb8", "#008000", colorRGB8{0, 128, 0})},
		{`{"space":"rgb8","r":0,"g":128,"b":0}`, colorResultFixture("name", "#008000", colorRGB8{0, 128, 0})},
	} {
		data, _ := json.Marshal(tc.result)
		if _, err := validateColorResponse(data, resolveColorArgs{json.RawMessage(tc.input)}); err == nil {
			t.Fatalf("wrong original kind/hex binding accepted: %s", tc.input)
		}
	}
	nonName := colorResultFixture("hex3", "#00ff88", colorRGB8{0, 255, 136})
	nonName.Palette = colorNamePalette
	data, _ := json.Marshal(nonName)
	if _, err := validateColorResponse(data, resolveColorArgs{json.RawMessage(`"#0f8"`)}); err == nil {
		t.Fatal("palette accepted for non-name input")
	}
}

func TestColorResolutionHTTPFailuresAndTimeoutAreReadOnly(t *testing.T) {
	for _, status := range []int{400, 404, 500, 501} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"invalid_color","message":"resolution rejected"}}`)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "resolve_color", map[string]any{"color": "green"})
			if !res.IsError || out.Error == nil || out.Error.OutcomeUnknown || out.Accepted || requests.Load() != 1 {
				t.Fatalf("wrong read-only failure: %+v", out)
			}
			if (status == 404 || status == 501) && out.Error.Code != "unsupported" {
				t.Fatalf("old daemon not explicit: %+v", out)
			}
		})
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); time.Sleep(100 * time.Millisecond) }))
	defer srv.Close()
	d, err := newDaemon(srv.URL, 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	_, out, _ := resolveColor(context.Background(), d, json.RawMessage(`{"color":"green"}`))
	if out.Error == nil || out.Error.OutcomeUnknown || strings.Contains(out.Error.Message, "outcome unknown") || requests.Load() != 1 {
		t.Fatalf("read-only timeout treated as job/retried: %+v", out)
	}
}
