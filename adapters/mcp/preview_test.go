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
)

// Fixed cross-entry golden fixture, not an alternative effect compiler.
func pulsePreviewFixture() previewResult {
	return previewResult{SchemaVersion: 1, CompileContract: compileContract, InputKind: "effect",
		Program:           previewProgram{Steps: []step{{Color: "#000000"}, {Color: "#00a000", FadeMS: 380}, {Color: "#000000", FadeMS: 390}}, Repeat: 2, OnFinish: "off"},
		NominalDurationMS: 1600, TotalSteps: 6, RequiredCapabilities: []string{"set", "program", "raw-rgb"},
		Validation: previewValidation{Program: "valid", Device: "not_requested"}}
}

func TestSDKAuthoritativePreviewIsReadOnlySinglePost(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bodies []previewArgs
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Method != http.MethodPost || r.URL.Path != "/v1/programs/preview" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL.Path)
		}
		var args previewArgs
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, args)
		p := pulsePreviewFixture()
		if args.Program != nil {
			p.InputKind, p.Program = "program", previewProgram{args.Program.Steps, args.Program.Repeat, args.Program.OnFinish}
			p.NominalDurationMS, p.TotalSteps = 110, 1
			p.RequiredCapabilities = append(p.RequiredCapabilities, "hold")
		}
		if args.Serial != nil {
			p.Device = &previewDevice{Serial: *args.Serial, Connection: "online", Capabilities: []string{"set", "program", "raw-rgb"}}
			p.Validation.Device = "supported"
		}
		_ = json.NewEncoder(w).Encode(p)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("expected seven tools: %+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "preview_program" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint) {
			t.Fatalf("preview annotations: %+v", tool.Annotations)
		}
	}
	for _, args := range []map[string]any{
		{"effect": map[string]any{"kind": "pulse", "color": "#00A000", "cycles": 2, "period_ms": 800}},
		{"serial": "20005601", "effect": map[string]any{"kind": "pulse", "color": "#00a000", "cycles": 2, "period_ms": 800}},
		{"program": map[string]any{"steps": []any{map[string]any{"color": "#00A000", "fade_ms": 100}}, "on_finish": "hold"}},
	} {
		res, out := call(t, session, "preview_program", args)
		if res.IsError || out.Error != nil || out.Kind != "program_preview" || out.Accepted || out.DryRun || out.RequestTicket != nil || out.PollAfterMS != 0 {
			t.Fatalf("preview lost read-only semantics: %+v", out)
		}
		data := out.Data.(map[string]any)
		if data["compile_contract"] != compileContract || data["program"] == nil {
			t.Fatalf("missing authoritative result: %+v", out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 || bodies[0].Effect.OnFinish != nil || bodies[0].Effect.Color != "#00a000" || bodies[0].Program != nil {
		t.Fatalf("intent compiled locally, retried or extra operation: %v %+v", paths, bodies)
	}
}

func TestSDKListCapabilitiesIsOptionalAndPreparationIndependent(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v1/devices":
			fmt.Fprint(w, `[{"serial":"20005601","connection":"online"}]`)
		case "/v1/capabilities":
			c := expectedCatalog()
			c.Effects = append(c.Effects, "future-effect")
			c.PatternLibrary, c.HardwarePatternUpload, c.MaxRequestBytes = true, true, 4096
			_ = json.NewEncoder(w).Encode(c)
		case "/v1/instance":
			fmt.Fprintf(w, `{"daemon_instance_id":%q,"idempotency":{"supported":true,"request_id_prefix":"req_60000_","accept_until_uptime_ms":60000}}`, testInstanceID)
		default:
			t.Errorf("extra request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, args := range []map[string]any{{}, {"prepare_request": true}, {"include_capabilities": true}, {"prepare_request": true, "include_capabilities": true}} {
		res, out := call(t, session, "list_devices", args)
		if res.IsError || out.Error != nil || out.Accepted {
			t.Fatal(out)
		}
		if args["include_capabilities"] == true {
			data, ok := out.Data.(map[string]any)
			if !ok || data["devices"] == nil || data["capabilities"] == nil {
				t.Fatalf("missing requested catalog: %+v", out)
			}
		} else if _, ok := out.Data.([]any); !ok {
			t.Fatalf("legacy data array changed: %+v", out)
		}
		if (out.RequestTicket != nil) != (args["prepare_request"] == true) {
			t.Fatalf("capability request changed preparation: %+v", out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /v1/devices", "GET /v1/devices", "GET /v1/instance", "GET /v1/devices", "GET /v1/capabilities", "GET /v1/devices", "GET /v1/instance", "GET /v1/capabilities"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("unexpected requests: %v", paths)
	}
}

func TestSDKPreviewStrictArgumentsNeverReachHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		requests.Add(1)
		t.Error("invalid arguments reached HTTP")
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, tc := range []struct{ tool, raw string }{
		{"preview_program", `{}`},
		{"preview_program", `{"effect":{"kind":"steady","color":"#000000"},"program":{"steps":[{"color":"#000000"}]}}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000"},"dry_run":true}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000"},"policy":"reject"}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000"},"serial":""}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000"},"serial":null}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000","color":"#ffffff"}}`},
		{"preview_program", `{"effect":{"kind":"pulse","color":"#000000","c\u006flor":"#ffffff"}}`},
		{"preview_program", `{"Effect":{"kind":"pulse","color":"#000000"}}`},
		{"preview_program", `{"effect":{"kind":"steady","color":"#000000","on_finish":"hold"}}`},
		{"list_devices", `{"include_capabilities":null}`},
		{"list_devices", `{"include_capabilities":"true"}`},
		{"list_devices", `{"include_capabilities":true,"include_c\u0061pabilities":false}`},
		{"list_devices", `{"Include_capabilities":true}`},
	} {
		res, _ := call(t, session, tc.tool, json.RawMessage(tc.raw))
		if !res.IsError {
			t.Fatalf("accepted %s %s", tc.tool, tc.raw)
		}
	}
	if requests.Load() != 0 {
		t.Fatal(requests.Load())
	}
}

func TestSoftwareResponsesRejectMalformedOrMisboundSuccess(t *testing.T) {
	base, _ := json.Marshal(pulsePreviewFixture())
	catalog, _ := json.Marshal(expectedCatalog())
	mutation := func(body []byte, from, to string) string { return strings.Replace(string(body), from, to, 1) }
	for _, tc := range []struct {
		name, kind, body string
		status           int
	}{
		{"accepted status", "program_preview", string(base), 202},
		{"unknown contract", "program_preview", mutation(base, compileContract, "future/v2"), 200},
		{"wrong input kind", "program_preview", mutation(base, `"input_kind":"effect"`, `"input_kind":"program"`), 200},
		{"duration", "program_preview", mutation(base, `"nominal_duration_ms":1600`, `"nominal_duration_ms":1599`), 200},
		{"steps", "program_preview", mutation(base, `"total_steps":6`, `"total_steps":3`), 200},
		{"noncanonical color", "program_preview", mutation(base, "#00a000", "#00A000"), 200},
		{"missing zero", "program_preview", mutation(base, `"led":0,`, ""), 200},
		{"missing required", "program_preview", mutation(base, `"required_capabilities":["set","program","raw-rgb"],`, ""), 200},
		{"target validation", "program_preview", mutation(base, "not_requested", "supported"), 200},
		{"wrong case", "program_preview", mutation(base, "schema_version", "Schema_version"), 200},
		{"escaped duplicate", "program_preview", mutation(base, `"schema_version":1`, `"schema_version":1,"schema_vers\u0069on":1`), 200},
		{"null", "program_preview", mutation(base, `"required_capabilities":["set","program","raw-rgb"]`, `"required_capabilities":null`), 200},
		{"unknown property", "program_preview", mutation(base, `"schema_version":1`, `"schema_version":1,"job":{}`), 200},
		{"trailing JSON", "program_preview", string(base) + `{}`, 200},
		{"missing false", "capabilities", mutation(catalog, `"pattern_library":false,`, ""), 200},
		{"missing integer", "capabilities", mutation(catalog, `"max_steps":1024,`, ""), 200},
		{"wrong limit", "capabilities", mutation(catalog, `"max_steps":1024`, `"max_steps":2048`), 200},
		{"duplicate name", "capabilities", mutation(catalog, `"steady","pulse"`, `"steady","pulse","pulse"`), 200},
		{"unknown catalog contract", "capabilities", mutation(catalog, compileContract, "future/v2"), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				requests.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			d, err := newDaemon(srv.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			var input any
			method, path := http.MethodGet, "/v1/capabilities"
			if tc.kind == "program_preview" {
				input = previewArgs{Effect: &effect{Kind: "pulse", Color: "#00a000"}}
				method, path = http.MethodPost, "/v1/programs/preview"
			}
			out := d.requestSoftware(context.Background(), method, path, tc.kind, input)
			if out.Error == nil || out.Error.Code != "invalid_daemon_response" || out.Error.OutcomeUnknown || out.Accepted || requests.Load() != 1 {
				t.Fatalf("malformed read-only result accepted or treated as job: %+v requests=%d", out, requests.Load())
			}
		})
	}
}

func TestPreviewTargetSnapshotConsistency(t *testing.T) {
	serial := "20005601"
	for _, tc := range []struct {
		connection     string
		caps           []string
		status, reason string
		missing        []string
	}{
		{"", []string{}, "unknown", "not_configured", nil},
		{"offline", []string{"set", "program", "raw-rgb"}, "unknown", "not_online", nil},
		{"online", []string{}, "unknown", "capabilities_unavailable", nil},
		{"online", []string{"set"}, "unsupported", "capabilities_missing", []string{"program", "raw-rgb"}},
		{"online", []string{"set", "program", "raw-rgb"}, "supported", "", nil},
	} {
		p := pulsePreviewFixture()
		p.Device = &previewDevice{serial, tc.connection, tc.caps}
		p.Validation = previewValidation{"valid", tc.status, tc.reason, tc.missing}
		args := previewArgs{Serial: &serial, Effect: &effect{Kind: "pulse", Color: "#00a000"}}
		if err := validatePreviewResult(p, args); err != nil {
			t.Fatalf("valid snapshot rejected: %+v %v", tc, err)
		}
		p.Validation.Device = "not_requested"
		if err := validatePreviewResult(p, args); err == nil {
			t.Fatal("inconsistent target validation accepted")
		}
	}
	p := pulsePreviewFixture()
	p.Device = &previewDevice{Serial: "other", Capabilities: []string{}}
	if err := validatePreviewResult(p, previewArgs{Serial: &serial, Effect: &effect{}}); err == nil {
		t.Fatal("wrong target accepted")
	}
}

func TestPreviewErrorsNeverBorrowJobOutcomeOrFallback(t *testing.T) {
	for _, status := range []int{400, 404, 501, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				requests.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"invalid_request","message":"preview unavailable"}}`)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "preview_program", map[string]any{"effect": map[string]any{"kind": "pulse", "color": "#00a000", "cycles": 10000, "period_ms": 1200000}})
			if !res.IsError || out.Error == nil || out.Error.OutcomeUnknown || out.Accepted || requests.Load() != 1 {
				t.Fatalf("read-only failure semantics %+v", out)
			}
			if (status == 404 || status == 501) && out.Error.Code != "unsupported" {
				t.Fatalf("old daemon not explicit: %+v", out)
			}
		})
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		requests.Add(1)
		time.Sleep(70 * time.Millisecond)
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	_, out, _ := preview(context.Background(), d, previewArgs{Effect: &effect{Kind: "pulse", Color: "#00a000"}})
	if out.Error == nil || out.Error.OutcomeUnknown || strings.Contains(out.Error.Message, "outcome unknown") || requests.Load() != 1 {
		t.Fatalf("timeout became job uncertainty: %+v", out)
	}
}

func TestSDKUnsupportedCatalogDoesNotDropPreparationOrFallback(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(fmt.Sprint(prepare), func(t *testing.T) {
			var paths []string
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				mu.Lock()
				defer mu.Unlock()
				paths = append(paths, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case "/v1/devices":
					fmt.Fprint(w, `[]`)
				case "/v1/instance":
					fmt.Fprintf(w, `{"daemon_instance_id":%q,"idempotency":{"supported":true,"request_id_prefix":"req_60000_","accept_until_uptime_ms":60000}}`, testInstanceID)
				case "/v1/capabilities":
					w.WriteHeader(404)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "list_devices", map[string]any{"include_capabilities": true, "prepare_request": prepare})
			if !res.IsError || out.Error == nil || out.Error.Code != "unsupported" || out.Error.OutcomeUnknown || (out.RequestTicket != nil) != prepare || out.Data != nil {
				t.Fatalf("catalog fabricated or ticket semantics changed: %+v", out)
			}
			mu.Lock()
			defer mu.Unlock()
			want := []string{"GET /v1/devices", "GET /v1/capabilities"}
			if prepare {
				want = []string{"GET /v1/devices", "GET /v1/instance", "GET /v1/capabilities"}
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatal(paths)
			}
		})
	}
}

func TestSoftwareResponseMediaAndSizeAreBounded(t *testing.T) {
	valid, _ := json.Marshal(pulsePreviewFixture())
	for _, tc := range []struct{ name, media, body string }{
		{"missing JSON media", "", string(valid)},
		{"wrong media", "text/plain", string(valid)},
		{"oversize", "application/json", strings.Repeat(" ", maxResponseBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.media != "" {
					w.Header().Set("Content-Type", tc.media)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			d, err := newDaemon(srv.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			_, out, _ := preview(context.Background(), d, previewArgs{Effect: &effect{Kind: "pulse", Color: "#00a000"}})
			if out.Error == nil || out.Error.Code != "invalid_daemon_response" || out.Error.OutcomeUnknown || out.Accepted {
				t.Fatalf("invalid software response accepted or treated as job: %+v", out)
			}
		})
	}
}
