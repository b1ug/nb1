package main

import (
	"context"
	_ "embed"
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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed testdata/light-preview-v1.json
var lightFixturesJSON []byte

func lightFixture(t *testing.T, kind string, serial *string) lightPreviewEnvelope {
	t.Helper()
	var fixtures map[string]json.RawMessage
	if err := json.Unmarshal(lightFixturesJSON, &fixtures); err != nil {
		t.Fatal(err)
	}
	var result lightPreviewEnvelope
	if err := json.Unmarshal(fixtures[kind], &result); err != nil {
		t.Fatal(err)
	}
	if serial != nil {
		result.Preview.Device = &previewDevice{Serial: *serial, Connection: "online", Capabilities: append([]string{}, result.Result.RequiredCapabilities...)}
		result.Preview.Validation = previewValidation{Program: "valid", Device: "supported"}
	}
	return result
}

func lightFixtureAction(t *testing.T, kind string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(lightFixture(t, kind, nil).Result.Action)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func lightJobFixture(p lightPreviewEnvelope) map[string]any {
	return map[string]any{"id": "job_light", "device": "20005601", "status": "running", "program": p.Result.Program, "total_steps": p.Result.TotalSteps, "completed_steps": 0}
}

func TestLightOfficialSDKMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var previews, writes, other atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost {
					other.Add(1)
					http.Error(w, "unexpected", 500)
					return
				}
				var request lightRunArgs
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				var action lightActionWire
				if err := json.Unmarshal(request.Action, &action); err != nil {
					t.Error(err)
					return
				}
				if request.Contract != lightContract {
					t.Error("missing contract")
				}
				if r.URL.Path == "/v1/light/preview" {
					previews.Add(1)
					var serial *string
					if request.Serial != "" {
						serial = &request.Serial
					}
					json.NewEncoder(w).Encode(lightFixture(t, action.Kind, serial))
					return
				}
				if r.URL.Path != "/v1/devices/20005601/light" {
					other.Add(1)
					http.Error(w, "unexpected", 500)
					return
				}
				writes.Add(1)
				if request.Policy != "reject" || request.RequestID != testRequestID || request.ExpectedDaemonInstanceID != testInstanceID {
					t.Error("ticket or policy rewritten")
				}
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(lightJobFixture(lightFixture(t, action.Kind, nil)))
			}))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			list, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, tool := range list.Tools {
				names = append(names, tool.Name)
				if tool.Name == "preview_light" && (!tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.OutputSchema == nil) {
					t.Fatal("preview annotations/schema")
				}
				if tool.Name == "run_light" && (tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint) {
					t.Fatal("run annotations")
				}
			}
			sort.Strings(names)
			if strings.Join(names, ",") != "cancel_job,cancel_pattern_request,get_device_state,get_job,get_pattern,get_pattern_request,get_pattern_v2,list_devices,list_patterns,list_patterns_v2,preview_light,preview_pattern,preview_pattern_draft_v2,preview_program,resolve_color,run_light,run_pattern,run_program,save_pattern_v2,set_device_state" {
				t.Fatal(names)
			}
			for _, kind := range []string{"set", "off", "fade", "blink", "breathe", "pulse", "alternate", "rainbow"} {
				action := lightFixtureAction(t, kind)
				beforeP, beforeW := previews.Load(), writes.Load()
				res, out := call(t, session, "preview_light", map[string]any{"contract": lightContract, "action": action})
				if res.IsError || out.Error != nil || out.Kind != "light_preview" || out.Accepted || out.DryRun || out.RequestTicket != nil || previews.Load() != beforeP+1 || writes.Load() != beforeW {
					t.Fatalf("preview %s: %+v", kind, out)
				}
				data, _ := json.Marshal(out.Data)
				var value lightPreviewEnvelope
				if err := json.Unmarshal(data, &value); err != nil || !reflect.DeepEqual(value, lightFixture(t, kind, nil)) {
					t.Fatalf("preview payload %s: %v", kind, err)
				}
				res, out = call(t, session, "run_light", map[string]any{"serial": "20005601", "contract": lightContract, "action": action, "request_id": testRequestID, "expected_daemon_instance_id": testInstanceID})
				if res.IsError || out.Error != nil || out.Kind != "job" || !out.Accepted || out.PollAfterMS != 200 || previews.Load() != beforeP+2 || writes.Load() != beforeW+1 {
					t.Fatalf("run %s: %+v", kind, out)
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			}
			if other.Load() != 0 {
				t.Fatalf("unexpected requests: %d", other.Load())
			}
		})
	}
}

func TestLightInvalidArgumentsDoNotReachHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, raw := range []string{
		`{}`, `{"contract":"v2","action":{"kind":"off"}}`, `{"contract":"nb1-light-action/v1","action":null}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off"},"serial":null}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":1.0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":1e0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":-0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"pulse","color":"red","rest_ms":-0}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off","kind":"set"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off","color":"red"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"off"},"dry_run":true}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"spin"}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"alternate","leds":[1,1]}}`,
		`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":{"space":"rgb8","r":1,"g":2,"b":3,"extra":0}}}`,
	} {
		res, out := call(t, session, "preview_light", json.RawMessage(raw))
		if !res.IsError && out.Error == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, suffix := range []string{``, `,"request_id":"` + testRequestID + `"`, `,"expected_daemon_instance_id":"` + testInstanceID + `"`, `,"policy":"replace","request_id":"` + testRequestID + `","expected_daemon_instance_id":"` + testInstanceID + `"`, `,"dry_run":true,"request_id":"` + testRequestID + `","expected_daemon_instance_id":"` + testInstanceID + `"`} {
		raw := `{"serial":"20005601","contract":"nb1-light-action/v1","action":{"kind":"off"}` + suffix + `}`
		res, out := call(t, session, "run_light", json.RawMessage(raw))
		if !res.IsError && out.Error == nil {
			t.Fatal(raw)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid input made %d requests", requests.Load())
	}
}

func TestLightOriginalColorNumbersSurviveSDK(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var received atomic.Int32
			for _, color := range []string{`{"space":"rgb8","r":1.0,"g":0,"b":0}`, `{"space":"rgb8","r":1e0,"g":0,"b":0}`, `{"space":"rgb8","r":-0,"g":0,"b":0}`, `{"space":"hsv","h_deg":1e2,"s_pct":100,"v_pct":50}`, `{"space":"hsv","h_deg":120.000000,"s_pct":100.000000,"v_pct":50.000000}`} {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received.Add(1)
					data, _ := io.ReadAll(r.Body)
					var request lightPreviewArgs
					if err := json.Unmarshal(data, &request); err != nil {
						t.Error(err)
					}
					var action lightActionWire
					if err := json.Unmarshal(request.Action, &action); err != nil {
						t.Error(err)
					}
					if string(action.Color) != color {
						t.Errorf("original color changed: got %s want %s", action.Color, color)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"code":"invalid_light_action","message":"fixture rejects or records original lexeme"}}`)
				}))
				var session *mcp.ClientSession
				if transport == "stdio" {
					session = connectStdio(t, srv.URL)
				} else {
					session = connect(t, srv.URL)
				}
				res, out := call(t, session, "preview_light", json.RawMessage(`{"contract":"nb1-light-action/v1","action":{"kind":"set","color":`+color+`}}`))
				if !res.IsError || out.Error == nil || out.Error.Code != "invalid_light_action" || out.Error.OutcomeUnknown {
					t.Fatalf("color lexical flow: %+v", out)
				}
				session.Close()
				srv.Close()
			}
			if received.Load() != 5 {
				t.Fatalf("count %d", received.Load())
			}
		})
	}
}

func TestLightPreviewRejectsChangedDeclarations(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*lightPreviewEnvelope)
	}{
		{"contract", func(p *lightPreviewEnvelope) { p.Result.Contract = "v2" }},
		{"kind", func(p *lightPreviewEnvelope) { p.Result.Action.Kind = "blink" }},
		{"led", func(p *lightPreviewEnvelope) { n := 2; p.Result.Action.LED = &n }},
		{"missing default", func(p *lightPreviewEnvelope) { p.Result.Action.AttackMS = nil }},
		{"timing", func(p *lightPreviewEnvelope) { n := 70; p.Result.Action.AttackMS = &n }},
		{"hex color", func(p *lightPreviewEnvelope) { p.Result.Action.Color = json.RawMessage(`"#ff0000"`) }},
		{"program mismatch", func(p *lightPreviewEnvelope) { p.Result.Program.Steps[1].Color = "#ff0000" }},
		{"both programs wrong color", func(p *lightPreviewEnvelope) {
			p.Result.Program.Steps[1].Color = "#ff0000"
			p.Preview.Program.Steps[1].Color = "#ff0000"
		}},
		{"both programs wrong LED", func(p *lightPreviewEnvelope) {
			for i := range p.Result.Program.Steps {
				p.Result.Program.Steps[i].LED = 2
				p.Preview.Program.Steps[i].LED = 2
			}
		}},
		{"both programs wrong cycles", func(p *lightPreviewEnvelope) {
			p.Result.Program.Repeat = 2
			p.Preview.Program.Repeat = 2
			p.Result.TotalSteps = 6
			p.Preview.TotalSteps = 6
			p.Result.NominalDurationMS = 2060
			p.Preview.NominalDurationMS = 2060
		}},
		{"same total wrong phase times", func(p *lightPreviewEnvelope) {
			p.Result.Program.Steps[1].FadeMS += 10
			p.Result.Program.Steps[2].FadeMS -= 10
			p.Preview.Program.Steps[1].FadeMS += 10
			p.Preview.Program.Steps[2].FadeMS -= 10
		}},
		{"total", func(p *lightPreviewEnvelope) { p.Result.TotalSteps++ }},
		{"target", func(p *lightPreviewEnvelope) { p.Preview.Device.Serial = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			serial := "20005601"
			fixture := lightFixture(t, "pulse", &serial)
			test.change(&fixture)
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(fixture)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "preview_light", map[string]any{"contract": lightContract, "action": lightFixtureAction(t, "pulse"), "serial": serial})
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || out.Error.OutcomeUnknown || requests.Load() != 1 {
				t.Fatalf("changed preview accepted: %+v", out)
			}
		})
	}
}

func TestLightRunBindsJobAndNeverRetriesWrite(t *testing.T) {
	for _, mode := range []string{"program", "total", "completed", "instance", "replayed", "transport", "busy", "preflight-offline", "preflight-unsupported"} {
		t.Run(mode, func(t *testing.T) {
			var previews, writes atomic.Int32
			serial := "20005601"
			prepared := lightFixture(t, "pulse", &serial)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/light/preview" {
					previews.Add(1)
					if mode == "preflight-unsupported" {
						http.Error(w, "unsupported", 501)
						return
					}
					if mode == "preflight-offline" {
						prepared.Preview.Device.Connection = "offline"
						prepared.Preview.Validation = previewValidation{Program: "valid", Device: "unknown", Reason: "not_online"}
					}
					json.NewEncoder(w).Encode(prepared)
					return
				}
				writes.Add(1)
				if mode == "transport" {
					h := w.(http.Hijacker)
					c, _, _ := h.Hijack()
					c.Close()
					return
				}
				if mode == "busy" {
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":{"code":"busy","message":"occupied"}}`)
					return
				}
				job := lightJobFixture(prepared)
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				switch mode {
				case "program":
					p := prepared.Result.Program
					p.Steps = append([]step{}, p.Steps...)
					p.Steps[1].Color = "#ff0000"
					job["program"] = p
				case "total":
					job["total_steps"] = 999
				case "completed":
					job["completed_steps"] = 999
				case "instance":
					w.Header().Set("X-NB1-Daemon-Instance-ID", "wrong")
				case "replayed":
					w.Header().Set("X-NB1-Replayed", "wrong")
				}
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(job)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "run_light", map[string]any{"serial": serial, "contract": lightContract, "action": lightFixtureAction(t, "pulse"), "request_id": testRequestID, "expected_daemon_instance_id": testInstanceID})
			if !res.IsError || out.Error == nil || out.Accepted {
				t.Fatalf("failure accepted: %+v", out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			wantWrites := int32(1)
			if strings.HasPrefix(mode, "preflight-") {
				wantWrites = 0
			}
			if previews.Load() != 1 || writes.Load() != wantWrites {
				t.Fatalf("repeated/fallback request %d/%d", previews.Load(), writes.Load())
			}
			if mode == "busy" || mode == "preflight-offline" {
				if out.Error.OutcomeUnknown {
					t.Fatal("definite rejection classified unknown")
				}
			} else if !out.Error.OutcomeUnknown {
				t.Fatalf("uncertain prior ticket outcome lost: %+v", out)
			}
		})
	}
}

func TestLightJobRawResponseFailsClosed(t *testing.T) {
	for _, mode := range []string{"duplicate-device", "duplicate-step", "invalid-utf8", "unpaired-surrogate", "wrong-media", "missing-media"} {
		t.Run(mode, func(t *testing.T) {
			var writes atomic.Int32
			serial := "20005601"
			prepared := lightFixture(t, "pulse", &serial)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/light/preview" {
					json.NewEncoder(w).Encode(prepared)
					return
				}
				writes.Add(1)
				data, _ := json.Marshal(lightJobFixture(prepared))
				switch mode {
				case "duplicate-device":
					data = []byte(`{"device":"other",` + string(data[1:]))
				case "duplicate-step":
					data = []byte(strings.Replace(string(data), `"color":"#000000"`, `"color":"#ff0000","color":"#000000"`, 1))
				case "invalid-utf8":
					data = append(append([]byte(`{"extra":"`), 0xff), append([]byte(`",`), data[1:]...)...)
				case "unpaired-surrogate":
					data = []byte(`{"extra":"\ud800",` + string(data[1:]))
				case "wrong-media":
					w.Header().Set("Content-Type", "text/plain")
				case "missing-media":
					w.Header().Del("Content-Type")
				}
				w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				w.WriteHeader(202)
				w.Write(data)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "run_light", map[string]any{"serial": serial, "contract": lightContract, "action": lightFixtureAction(t, "pulse"), "request_id": testRequestID, "expected_daemon_instance_id": testInstanceID})
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || !out.Error.OutcomeUnknown || out.Accepted || writes.Load() != 1 {
				t.Fatalf("invalid raw Job accepted: %+v", out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		})
	}
}

func TestLightErrorResponseFailsClosed(t *testing.T) {
	type malformed struct {
		name, body, media string
		status            int
	}
	cases := []malformed{
		{"duplicate-code", `{"error":{"code":"daemon_instance_changed","code":"invalid_request","message":"rejected"}}`, "application/json", 400},
		{"duplicate-escaped-code", `{"error":{"code":"daemon_instance_changed","c\u006fde":"invalid_request","message":"rejected"}}`, "application/json", 400},
		{"duplicate-message", `{"error":{"code":"invalid_request","message":"one","message":"two"}}`, "application/json", 400},
		{"null-message", `{"error":{"code":"invalid_request","message":null}}`, "application/json", 400},
		{"missing-message", `{"error":{"code":"invalid_request"}}`, "application/json", 400},
		{"unknown-error-field", `{"error":{"code":"invalid_request","message":"rejected","extra":true}}`, "application/json", 400},
		{"unknown-envelope-field", `{"error":{"code":"invalid_request","message":"rejected"},"extra":true}`, "application/json", 400},
		{"wrong-field-case", `{"error":{"Code":"invalid_request","message":"rejected"}}`, "application/json", 400},
		{"wrong-media", `{"error":{"code":"invalid_request","message":"rejected"}}`, "text/plain", 400},
		{"trailing", `{"error":{"code":"invalid_request","message":"rejected"}} {}`, "application/json", 400},
		{"null-envelope", `null`, "application/json", 400},
		{"invalid-utf8", "{\"error\":{\"code\":\"invalid_request\",\"message\":\"\xff\"}}", "application/json", 400},
		{"unpaired-code", `{"error":{"code":"invalid_\ud800request","message":"rejected"}}`, "application/json", 400},
		{"unpaired-message", `{"error":{"code":"invalid_request","message":"\udc00"}}`, "application/json", 400},
		{"wrong-surrogate-pair", `{"error":{"code":"invalid_request","message":"\ud800\u0041"}}`, "application/json", 400},
		{"invalid-unsupported", `{"error":{"code":"not_found","message":null}}`, "application/json", 404},
	}
	for _, stage := range []string{"preview", "run-preflight", "run-write"} {
		for _, tc := range cases {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				var previews, writes atomic.Int32
				serial := "20005601"
				prepared := lightFixture(t, "pulse", &serial)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/light/preview" {
						previews.Add(1)
						if stage == "run-write" {
							w.Header().Set("Content-Type", "application/json")
							json.NewEncoder(w).Encode(prepared)
							return
						}
					} else {
						writes.Add(1)
					}
					w.Header().Set("Content-Type", tc.media)
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				args := map[string]any{"serial": serial, "contract": lightContract, "action": lightFixtureAction(t, "pulse")}
				tool := "preview_light"
				if stage != "preview" {
					tool = "run_light"
					args["request_id"], args["expected_daemon_instance_id"] = testRequestID, testInstanceID
				}
				res, out := call(t, session, tool, args)
				if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || out.Accepted || previews.Load() != 1 {
					t.Fatalf("malformed light error accepted: %+v; reads=%d writes=%d", out, previews.Load(), writes.Load())
				}
				if stage == "preview" {
					if out.Error.OutcomeUnknown || out.RequestTicket != nil || writes.Load() != 0 {
						t.Fatalf("read-only preview invented a submission: %+v", out)
					}
				} else {
					assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
					if !out.Error.OutcomeUnknown {
						t.Fatalf("original ticket uncertainty lost: %+v", out)
					}
					wantWrites := int32(0)
					if stage == "run-write" {
						wantWrites = 1
					}
					if writes.Load() != wantWrites {
						t.Fatalf("write retried or preflight bypassed: %d", writes.Load())
					}
				}
			})
		}
	}
}

func TestLightErrorAllowsLosslessUnicodeEscapes(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"invalid_request","message":"\ud83d\ude00"}}`,
		`{"error":{"code":"invalid_request","message":"\u4e2d\u6587\n\"quote\""}}`,
		`{"error":{"code":"invalid_request","message":"literal \\ud800"}}`,
		`{"e\u0072ror":{"c\u006fde":"invalid_request","message":"escaped keys"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "preview_light", map[string]any{"contract": lightContract, "action": lightFixtureAction(t, "pulse")})
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_request" || out.Error.OutcomeUnknown || out.Accepted || out.RequestTicket != nil {
				t.Fatalf("lossless Unicode rejected or submission invented: %+v", out)
			}
		})
	}
}

func TestLightRequiresUniqueContentType(t *testing.T) {
	for _, stage := range []string{"preview-success", "preview-error", "run-preflight-success", "run-preflight-error", "run-write-success", "run-write-error"} {
		for _, variant := range []string{"duplicate-same", "duplicate-conflicting", "single-charset"} {
			t.Run(stage+"/"+variant, func(t *testing.T) {
				var previews, writes atomic.Int32
				serial := "20005601"
				prepared := lightFixture(t, "pulse", &serial)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					preview := r.URL.Path == "/v1/light/preview"
					if preview {
						previews.Add(1)
					} else {
						writes.Add(1)
					}
					applyVariant := preview != strings.HasPrefix(stage, "run-write")
					w.Header().Set("Content-Type", "application/json")
					if applyVariant {
						switch variant {
						case "duplicate-same":
							w.Header().Add("Content-Type", "application/json")
						case "duplicate-conflicting":
							w.Header().Add("Content-Type", "text/plain")
						case "single-charset":
							w.Header().Set("Content-Type", "application/json; charset=utf-8")
						}
					}
					if applyVariant && strings.HasSuffix(stage, "-error") {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprint(w, `{"error":{"code":"invalid_request","message":"rejected"}}`)
					} else if preview {
						json.NewEncoder(w).Encode(prepared)
					} else {
						w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
						w.Header().Set("X-NB1-Replayed", "false")
						w.WriteHeader(http.StatusAccepted)
						json.NewEncoder(w).Encode(lightJobFixture(prepared))
					}
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				args := map[string]any{"serial": serial, "contract": lightContract, "action": lightFixtureAction(t, "pulse")}
				tool := "preview_light"
				run := strings.HasPrefix(stage, "run-")
				if run {
					tool = "run_light"
					args["request_id"], args["expected_daemon_instance_id"] = testRequestID, testInstanceID
				}
				res, out := call(t, session, tool, args)
				badHeader := variant != "single-charset"
				wantError := badHeader || strings.HasSuffix(stage, "-error")
				if previews.Load() != 1 || res.IsError != wantError || (out.Error != nil) != wantError {
					t.Fatalf("wrong Content-Type result: %+v; reads=%d writes=%d", out, previews.Load(), writes.Load())
				}
				if badHeader && (out.Error.Code != "invalid_daemon_response" || out.Accepted) {
					t.Fatalf("ambiguous Content-Type accepted: %+v", out)
				}
				if !badHeader && wantError && (out.Error.Code != "invalid_request" || out.Error.OutcomeUnknown) {
					t.Fatalf("valid charset error changed: %+v", out)
				}
				if !wantError && (run && (out.Kind != "job" || !out.Accepted) || !run && out.Kind != "light_preview") {
					t.Fatalf("valid charset success changed: %+v", out)
				}
				wantWrites := int32(0)
				if strings.HasPrefix(stage, "run-write") || stage == "run-preflight-success" && !badHeader {
					wantWrites = 1
				}
				if writes.Load() != wantWrites {
					t.Fatalf("unexpected write count %d, want %d", writes.Load(), wantWrites)
				}
				if run {
					assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
					if badHeader && !out.Error.OutcomeUnknown {
						t.Fatalf("original ticket uncertainty lost: %+v", out)
					}
				} else if out.Accepted || out.RequestTicket != nil || out.Error != nil && out.Error.OutcomeUnknown {
					t.Fatalf("preview invented a submission: %+v", out)
				}
			})
		}
	}
}
