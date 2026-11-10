package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pure core CompileJSON/control.Preview snapshots, not a second compiler.
//
//go:embed testdata/light-flow-v1.json
var lightFlowFixturesJSON []byte

func lightFlowFixture(t *testing.T, name string, serial *string) lightPreviewEnvelope {
	t.Helper()
	var all map[string]lightPreviewEnvelope
	if err := json.Unmarshal(lightFlowFixturesJSON, &all); err != nil {
		t.Fatal(err)
	}
	p, ok := all[name]
	if !ok {
		t.Fatal("unknown flow fixture", name)
	}
	if serial != nil {
		p.Preview.Device = &previewDevice{Serial: *serial, Connection: "online", Capabilities: append([]string{}, p.Result.RequiredCapabilities...)}
		p.Preview.Validation = previewValidation{Program: "valid", Device: "supported"}
	}
	return p
}

func lightFlowAction(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(lightFlowFixture(t, name, nil).Result.Action)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLightFlowOfficialSDKMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var previews, writes, other atomic.Int32
			serial := "20005601"
			names := []string{"omitted", "cut", "linear", "sine", "orbit", "linear_min", "orbit_limit", "linear_max"}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var request lightRunArgs
				if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil {
					other.Add(1)
					w.WriteHeader(500)
					return
				}
				name := ""
				for _, candidate := range names {
					if bytes.Equal(request.Action, lightFlowAction(t, candidate)) {
						name = candidate
						break
					}
				}
				if name == "" || request.Contract != lightContract {
					t.Error("original action or contract changed", string(request.Action))
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/v1/light/preview" {
					previews.Add(1)
					var target *string
					if request.Serial != "" {
						target = &request.Serial
					}
					json.NewEncoder(w).Encode(lightFlowFixture(t, name, target))
					return
				}
				if r.URL.Path != "/v1/devices/20005601/light" {
					other.Add(1)
					w.WriteHeader(500)
					return
				}
				writes.Add(1)
				if request.RequestID != testRequestID || request.ExpectedDaemonInstanceID != testInstanceID || request.Policy != "reject" {
					t.Error("original ticket or reject policy changed")
				}
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(lightJobFixture(lightFlowFixture(t, name, nil)))
			}))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil || len(tools.Tools) != 22 {
				t.Fatalf("expected twelve tools: %v %v", tools, err)
			}
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					bp, bw := previews.Load(), writes.Load()
					action := lightFlowAction(t, name)
					res, out := call(t, session, "preview_light", map[string]any{"contract": lightContract, "action": action})
					if res.IsError || out.Error != nil || out.Kind != "light_preview" || out.Accepted || out.RequestTicket != nil {
						t.Fatalf("preview rejected: %+v", out)
					}
					data, _ := json.Marshal(out.Data)
					var got lightPreviewEnvelope
					if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got, lightFlowFixture(t, name, nil)) {
						t.Fatal("authoritative preview changed")
					}
					if previews.Load() != bp+1 || writes.Load() != bw {
						t.Fatal("preview was not exactly one read-only POST")
					}
					res, out = call(t, session, "run_light", withTicket(map[string]any{"contract": lightContract, "action": action, "serial": serial}))
					if res.IsError || out.Error != nil || out.Kind != "job" || !out.Accepted || previews.Load() != bp+2 || writes.Load() != bw+1 {
						t.Fatalf("run rejected or retried: %+v", out)
					}
					assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
				})
			}
			if other.Load() != 0 {
				t.Fatal("unexpected discovery, fallback or retry", other.Load())
			}
		})
	}
}

func TestLightFlowInvalidInputBeforeHTTP(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			actions := []string{
				`{"kind":"alternate","transition":""}`, `{"kind":"alternate","transition":"spin"}`,
				`{"kind":"alternate","transition":"SINE"}`, `{"kind":"alternate","transition":null}`,
				`{"kind":"alternate","transition":1}`, `{"kind":"alternate","transition":"sine","transition":"cut"}`,
				`{"kind":"alternate","transition":"sine","\u0074ransition":"cut"}`,
				`{"kind":"alternate","transition":"linear","period_ms":1299}`,
				`{"kind":"alternate","transition":"sine","period_ms":1200001}`,
				`{"kind":"alternate","transition":"orbit","cycles":16}`,
				`{"kind":"alternate","transition":"linear","cycles":0}`,
				`{"kind":"alternate","transition":"sine","period_ms":1300.0}`,
				`{"kind":"alternate","transition":"orbit","period_ms":13e2}`,
				`{"kind":"alternate","transition":"cut","period_ms":39}`,
			}
			for _, kind := range []string{"set", "off", "fade", "blink", "breathe", "pulse", "rainbow"} {
				valid := lightFixtureAction(t, kind)
				actions = append(actions, `{"transition":"linear",`+string(valid[1:]))
			}
			for _, action := range actions {
				if _, err := normalizedLightStructure(json.RawMessage(action)); err == nil {
					t.Fatalf("wire validator accepted invalid action %s", action)
				}
				for _, tool := range []string{"preview_light", "run_light"} {
					args := map[string]any{"contract": lightContract, "action": json.RawMessage(action)}
					if tool == "run_light" {
						args["serial"] = "20005601"
						withTicket(args)
					}
					res, out := call(t, session, tool, args)
					if !res.IsError && out.Error == nil {
						t.Fatalf("accepted %s %s", tool, action)
					}
				}
			}
			if calls.Load() != 0 {
				t.Fatal("invalid arguments reached HTTP", calls.Load())
			}
		})
	}
}

func TestLightFlowRejectsChangedPreviewGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*lightPreviewEnvelope)
	}{
		{"result-version", func(p *lightPreviewEnvelope) { p.Result.SchemaVersion = 2 }},
		{"compile-version", func(p *lightPreviewEnvelope) { p.Result.ProgramCompileContract = "v2" }},
		{"preview-version", func(p *lightPreviewEnvelope) { p.Preview.SchemaVersion = 2 }},
		{"missing-transition", func(p *lightPreviewEnvelope) { p.Result.Action.Transition = "" }},
		{"other-transition", func(p *lightPreviewEnvelope) { p.Result.Action.Transition = "sine" }},
		{"color-input", func(p *lightPreviewEnvelope) { p.Result.Action.Colors[0] = json.RawMessage(`"#ffffff"`) }},
		{"period", func(p *lightPreviewEnvelope) { v := 2004; p.Result.Action.PeriodMS = &v }},
		{"cycles", func(p *lightPreviewEnvelope) { v := 1; p.Result.Action.Cycles = &v }},
		{"phase-count", func(p *lightPreviewEnvelope) { p.Result.Program.Steps = p.Result.Program.Steps[:64] }},
		{"selector-order", func(p *lightPreviewEnvelope) { p.Result.Program.Steps[20].LED = 1 }},
		{"initial-overhead", func(p *lightPreviewEnvelope) {
			p.Result.Program.Steps[0].HoldMS = 1
			p.Result.Program.Steps[2].HoldMS--
		}},
		{"slot-redistribution", func(p *lightPreviewEnvelope) {
			p.Result.Program.Steps[2].FadeMS += 10
			p.Result.Program.Steps[3].FadeMS -= 10
		}},
		{"closure", func(p *lightPreviewEnvelope) { p.Result.Program.Steps[64].Color = "#000000" }},
		{"initial-color-anchor", func(p *lightPreviewEnvelope) {
			p.Result.Program.Steps[0].Color = "#ffffff"
			p.Result.Program.Steps[64].Color = "#ffffff"
		}},
		{"half-color-anchor", func(p *lightPreviewEnvelope) { p.Result.Program.Steps[32].Color = "#ffffff" }},
		{"total-count", func(p *lightPreviewEnvelope) { p.Result.TotalSteps++ }},
		{"nominal-duration", func(p *lightPreviewEnvelope) { p.Result.NominalDurationMS++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serial := "20005601"
			p := lightFlowFixture(t, "linear", &serial)
			tc.edit(&p)
			// Keep both copies internally consistent so geometric checks, rather
			// than only result/preview equality, must reject the altered result.
			p.Preview.Program = p.Result.Program
			p.Preview.TotalSteps = p.Result.TotalSteps
			p.Preview.NominalDurationMS = p.Result.NominalDurationMS
			var previews, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/light/preview" {
					previews.Add(1)
					json.NewEncoder(w).Encode(p)
				} else {
					writes.Add(1)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "run_light", withTicket(map[string]any{"serial": serial, "contract": lightContract, "action": lightFlowAction(t, "linear")}))
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || !out.Error.OutcomeUnknown || previews.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("bad geometry accepted or retried: %+v; %d/%d", out, previews.Load(), writes.Load())
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		})
	}
}

func TestLightFlowJobBindingAndNoRetry(t *testing.T) {
	for _, mode := range []string{"changed-interior-color", "changed-slot", "transport", "busy", "duplicate-json", "wrong-media"} {
		t.Run(mode, func(t *testing.T) {
			serial := "20005601"
			p := lightFlowFixture(t, "orbit", &serial)
			var previews, writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/light/preview" {
					previews.Add(1)
					json.NewEncoder(w).Encode(p)
					return
				}
				writes.Add(1)
				if mode == "transport" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				if mode == "busy" {
					w.WriteHeader(http.StatusConflict)
					fmt.Fprint(w, `{"error":{"code":"busy","message":"occupied"}}`)
					return
				}
				job := lightJobFixture(p)
				program := p.Result.Program
				program.Steps = append([]step{}, program.Steps...)
				if mode == "changed-interior-color" {
					program.Steps[9].Color = "#112233"
				}
				if mode == "changed-slot" {
					program.Steps[9].HoldMS++
				}
				job["program"] = program
				data, _ := json.Marshal(job)
				if mode == "duplicate-json" {
					data = []byte(`{"device":"other",` + string(data[1:]))
				}
				if mode == "wrong-media" {
					w.Header().Set("Content-Type", "text/plain")
				}
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				w.WriteHeader(http.StatusAccepted)
				w.Write(data)
			}))
			defer srv.Close()
			session := connectStdio(t, srv.URL)
			res, out := call(t, session, "run_light", withTicket(map[string]any{"serial": serial, "contract": lightContract, "action": lightFlowAction(t, "orbit")}))
			if !res.IsError || out.Error == nil || out.Accepted || previews.Load() != 1 || writes.Load() != 1 {
				t.Fatalf("invalid Job accepted or retry: %+v %d/%d", out, previews.Load(), writes.Load())
			}
			if out.Error.OutcomeUnknown != (mode != "busy") {
				t.Fatalf("unknown classification changed: %+v", out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		})
	}
}

func TestLightFlowSchemaKeepsOmittedTransitionOptional(t *testing.T) {
	for _, schema := range []map[string]any{lightActionSchema(), lightPreviewOutputSchema()["properties"].(map[string]any)["data"].(map[string]any)["properties"].(map[string]any)["result"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)} {
		for _, raw := range schema["oneOf"].([]any) {
			branch := raw.(map[string]any)
			properties := branch["properties"].(map[string]any)
			kind := properties["kind"].(map[string]any)["const"].(string)
			_, present := properties["transition"]
			if present != (kind == "alternate") {
				t.Fatal("transition schema leaked to another action", kind)
			}
			if kind == "alternate" {
				for _, field := range branch["required"].([]string) {
					if field == "transition" {
						t.Fatal("legacy omitted transition became required")
					}
				}
				encoded, _ := json.Marshal(branch["allOf"])
				for _, fragment := range []string{`"minimum":1300`, `"maximum":15`, `"linear"`, `"sine"`, `"orbit"`} {
					if !strings.Contains(string(encoded), fragment) {
						t.Fatal("missing conditional bounds", string(encoded))
					}
				}
			}
		}
	}
}

func TestLightFlowOmittedAndExplicitCutRemainDistinct(t *testing.T) {
	for _, name := range []string{"omitted", "cut"} {
		t.Run(name, func(t *testing.T) {
			p := lightFlowFixture(t, name, nil)
			if name == "omitted" {
				p.Result.Action.Transition = "cut"
			} else {
				p.Result.Action.Transition = ""
			}
			data, _ := json.Marshal(p)
			if _, err := validateLightPreview(data, lightPreviewArgs{Contract: lightContract, Action: lightFlowAction(t, name)}); err == nil {
				t.Fatal("daemon changed explicit/omitted transition shape")
			}
		})
	}
}

func TestLightFlowPreviewStrictWireGuard(t *testing.T) {
	for _, mode := range []string{"duplicate-transition", "unknown-field", "null", "unpaired-surrogate", "invalid-utf8", "trailing", "duplicate-media"} {
		t.Run(mode, func(t *testing.T) {
			serial := "20005601"
			p := lightFlowFixture(t, "orbit", &serial)
			data, _ := json.Marshal(p)
			switch mode {
			case "duplicate-transition":
				data = bytes.Replace(data, []byte(`"transition":"orbit"`), []byte(`"transition":"cut","transition":"orbit"`), 1)
			case "unknown-field":
				data = append([]byte(`{"future":1,`), data[1:]...)
			case "null":
				data = bytes.Replace(data, []byte(`"transition":"orbit"`), []byte(`"transition":null`), 1)
			case "unpaired-surrogate":
				data = bytes.Replace(data, []byte(`"transition":"orbit"`), []byte(`"transition":"\ud800"`), 1)
			case "invalid-utf8":
				data = append(append([]byte(`{"future":"`), 0xff), append([]byte(`",`), data[1:]...)...)
			case "trailing":
				data = append(data, []byte(` {}`)...)
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if mode == "duplicate-media" {
					w.Header().Add("Content-Type", "application/json")
				}
				w.Write(data)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "run_light", withTicket(map[string]any{"contract": lightContract, "serial": serial, "action": lightFlowAction(t, "orbit")}))
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || !out.Error.OutcomeUnknown || calls.Load() != 1 {
				t.Fatalf("invalid preflight wire accepted/retried: %+v, calls=%d", out, calls.Load())
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		})
	}
}
