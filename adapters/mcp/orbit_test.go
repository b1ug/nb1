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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Fixed pure compiler/preview snapshots for the physically recorded targets.
// This independent module does not import the compiler or repeat its curve.
//
//go:embed testdata/orbit-preview-v2.json
var orbitFixtureJSON []byte

const orbitJobID = "framejob_12345678901234567890123456789012"

func orbitFixture(t *testing.T, segments int, serial *string) orbitPreviewWire {
	t.Helper()
	var all map[string]orbitPreviewWire
	if err := json.Unmarshal(orbitFixtureJSON, &all); err != nil {
		t.Fatal(err)
	}
	p := all[fmt.Sprint(segments)]
	if serial != nil {
		p.Preview.Device = &frameDeviceWire{Serial: *serial, Connection: "online"}
		p.Preview.Execution = frameExecutionWire{Available: true, Reason: "cached_profile_supported"}
	}
	return p
}

func orbitAction(t *testing.T, segments int) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(orbitFixture(t, segments, nil).Result.Action)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func orbitJob(p orbitPreviewWire, status string) frameJobWire {
	created := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	started := created.Add(time.Millisecond)
	finished := created.Add(time.Second)
	j := frameJobWire{ID: orbitJobID, Device: "20005601", Status: status, Program: p.Result.Program, TotalFrames: p.Result.TotalFrames, PlannedTargetWrites: p.Result.PlannedTargetWrites, CreatedAt: created}
	if status != "queued" {
		j.StartedAt = &started
	}
	if status == "succeeded" || status == "failed" || status == "cancelled" {
		j.FinishedAt = &finished
	}
	if status == "succeeded" {
		j.CompletedFrames, j.CompletedTargetWrites = j.TotalFrames, j.PlannedTargetWrites
	}
	return j
}

func orbitHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
	w.Header().Set("X-NB1-Replayed", "false")
	w.Header().Set("Location", "/v1/devices/20005601/frame-requests/"+testRequestID+"?expected_daemon_instance_id="+testInstanceID)
}

func TestOrbitOfficialSDKMemoryAndStdio(t *testing.T) {
	for _, transport := range []string{"memory", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var previews, writes, byID, byReceipt, cancels, unexpected atomic.Int32
			serial := "20005601"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/v2/light/preview":
					previews.Add(1)
					var req lightPreviewArgs
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil || req.Contract != orbitLightContract {
						t.Error("invalid Orbit preview request")
					}
					var action orbitActionWire
					if err := json.Unmarshal(req.Action, &action); err != nil {
						t.Error(err)
					}
					json.NewEncoder(w).Encode(orbitFixture(t, *action.Segments, req.Serial))
				case r.URL.Path == "/v2/devices/20005601/light":
					writes.Add(1)
					var req lightRunArgs
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil || req.Contract != orbitLightContract || req.RequestID != testRequestID || req.ExpectedDaemonInstanceID != testInstanceID || req.Policy != "reject" {
						t.Error("Orbit ticket, contract, policy or route changed")
					}
					var action orbitActionWire
					json.Unmarshal(req.Action, &action)
					orbitHeaders(w)
					w.WriteHeader(http.StatusAccepted)
					json.NewEncoder(w).Encode(orbitJob(orbitFixture(t, *action.Segments, nil), "running"))
				case r.URL.Path == "/v1/frame-jobs/"+orbitJobID:
					byID.Add(1)
					json.NewEncoder(w).Encode(orbitJob(orbitFixture(t, 4, nil), "succeeded"))
				case r.URL.Path == "/v1/frame-jobs/"+orbitJobID+"/cancel":
					cancels.Add(1)
					w.WriteHeader(http.StatusAccepted)
					json.NewEncoder(w).Encode(orbitJob(orbitFixture(t, 4, nil), "cancelled"))
				case strings.HasPrefix(r.URL.Path, "/v1/devices/20005601/frame-requests/"):
					byReceipt.Add(1)
					if r.URL.Query().Get("expected_daemon_instance_id") != testInstanceID || !strings.HasSuffix(r.URL.Path, testRequestID) {
						t.Error("receipt lookup changed the original ticket")
					}
					w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
					json.NewEncoder(w).Encode(orbitJob(orbitFixture(t, 4, nil), "succeeded"))
				default:
					unexpected.Add(1)
					w.WriteHeader(500)
					fmt.Fprint(w, `{"error":{"code":"unexpected","message":"no fallback allowed"}}`)
				}
			}))
			defer srv.Close()
			var session *mcp.ClientSession
			if transport == "stdio" {
				session = connectStdio(t, srv.URL)
			} else {
				session = connect(t, srv.URL)
			}
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil || len(tools.Tools) != 16 {
				t.Fatalf("tool set changed: %v %v", tools, err)
			}
			for _, segments := range []int{4, 8} {
				res, out := call(t, session, "preview_light", map[string]any{"contract": orbitLightContract, "action": orbitAction(t, segments)})
				if res.IsError || out.Error != nil || out.Kind != "light_preview" || out.Accepted || out.RequestTicket != nil {
					t.Fatalf("Orbit preview failed: %+v", out)
				}
				b, _ := json.Marshal(out.Data)
				var p orbitPreviewWire
				if json.Unmarshal(b, &p) != nil || !reflect.DeepEqual(p, orbitFixture(t, segments, nil)) {
					t.Fatal("Orbit preview data changed")
				}
				res, out = call(t, session, "run_light", withTicket(map[string]any{"serial": serial, "contract": orbitLightContract, "action": orbitAction(t, segments)}))
				if res.IsError || out.Error != nil || out.Kind != "job" || !out.Accepted || out.PollAfterMS != 200 {
					t.Fatalf("Orbit run failed: %+v", out)
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			}
			for _, args := range []map[string]any{{"id": orbitJobID}, withTicket(map[string]any{"serial": serial, "job_kind": "frame"})} {
				res, out := call(t, session, "get_job", args)
				if res.IsError || out.Error != nil || out.Accepted || out.Data.(map[string]any)["id"] != orbitJobID {
					t.Fatalf("frame lookup failed: %+v", out)
				}
			}
			res, out := call(t, session, "cancel_job", map[string]any{"id": orbitJobID})
			if res.IsError || out.Error != nil || !out.Accepted || out.Data.(map[string]any)["status"] != "cancelled" {
				t.Fatalf("frame cancellation failed: %+v", out)
			}
			if previews.Load() != 4 || writes.Load() != 2 || byID.Load() != 1 || byReceipt.Load() != 1 || cancels.Load() != 1 || unexpected.Load() != 0 {
				t.Fatalf("unexpected counts %d/%d/%d/%d/%d/%d", previews.Load(), writes.Load(), byID.Load(), byReceipt.Load(), cancels.Load(), unexpected.Load())
			}
		})
	}
}

func TestOrbitInputRejectedBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	session := connect(t, srv.URL)
	for i, action := range []string{
		`{"kind":"set","color":"red"}`, `{"kind":"orbit","segments":32}`, `{"kind":"orbit","segments":4.0}`,
		`{"kind":"orbit","cycles":257}`, `{"kind":"orbit","segments":8,"cycles":129}`, `{"kind":"orbit","period_ms":199}`,
		`{"kind":"orbit","period_ms":2400001}`, `{"kind":"orbit","period_ms":864000,"cycles":100}`,
		`{"kind":"orbit","leds":[1,1]}`, `{"kind":"orbit","colors":["red"]}`,
		`{"kind":"orbit","colors":["\ud800","blue"]}`, `{"kind":"orbit","kind":"orbit"}`,
		`{"kind":"orbit","extra":0}`, `{"kind":"orbit","on_finish":null}`,
		`{"kind":"orbit","colors":[{"space":"rgb8","r":1e0,"g":0,"b":0},"blue"]}`,
		`{"kind":"orbit","colors":[{"space":"hsv","h_deg":240,"s_pct":100.0000001,"v_pct":100},"blue"]}`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			res, _ := call(t, session, "preview_light", json.RawMessage(`{"contract":"`+orbitLightContract+`","action":`+action+`}`))
			if !res.IsError || calls.Load() != 0 {
				t.Fatalf("bad Orbit input reached HTTP: %s", action)
			}
		})
	}
	for _, args := range []map[string]any{
		{"id": orbitJobID, "job_kind": "frame"}, {"id": "framejob_bad"},
		withTicket(map[string]any{"serial": "20005601", "job_kind": "orbit"}),
	} {
		res, _ := call(t, session, "get_job", args)
		if !res.IsError || calls.Load() != 0 {
			t.Fatal("invalid job selector reached HTTP")
		}
	}
}

func TestOrbitPreservesRawColorInputAndOnlyUsesHighLevelRoute(t *testing.T) {
	action := json.RawMessage(`{"kind":"orbit","colors":["RED",{"space":"hsv","h_deg":240.000000,"s_pct":100,"v_pct":100}],"segments":4,"cycles":3}`)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		var req lightRunArgs
		json.NewDecoder(r.Body).Decode(&req)
		if !bytes.Contains(req.Action, []byte("240.000000")) {
			t.Error("color numeric lexeme was rewritten")
		}
		if r.URL.Path == "/v2/light/preview" {
			s := "20005601"
			json.NewEncoder(w).Encode(orbitFixture(t, 4, &s))
			return
		}
		if r.URL.Path != "/v2/devices/20005601/light" {
			t.Error("high-level action fell back to another route")
		}
		orbitHeaders(w)
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(orbitJob(orbitFixture(t, 4, nil), "queued"))
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	res, out := call(t, session, "run_light", withTicket(map[string]any{"contract": orbitLightContract, "action": action, "serial": "20005601"}))
	if res.IsError || out.Error != nil || !out.Accepted || requests.Load() != 2 {
		t.Fatalf("raw input run failed: %+v", out)
	}
}

func TestOrbitWireRejectsChangedPreviewAndJobs(t *testing.T) {
	serial := "20005601"
	for _, tc := range []struct {
		name   string
		change func(*orbitPreviewWire)
	}{
		{"recipe", func(p *orbitPreviewWire) { p.Result.RecipeID = "orbit-paired-linear-8/experimental-v1" }},
		{"input_color", func(p *orbitPreviewWire) { p.Result.Action.Colors[0] = json.RawMessage(`"#00ff00"`) }},
		{"missing_action_default", func(p *orbitPreviewWire) { p.Result.Action.Cycles = nil }},
		{"repeated_initial_in_cycle", func(p *orbitPreviewWire) {
			p.Result.Program.Frames = append(p.Result.Program.Frames, p.Result.Program.Frames[0])
		}},
		{"wrong_timing", func(p *orbitPreviewWire) { p.Result.Program.Frames[0].FadeMS = 730 }},
		{"wrong_order", func(p *orbitPreviewWire) { p.Result.Program.Frames[0].Targets[0].LED = 2 }},
		{"wrong_duration", func(p *orbitPreviewWire) { p.Preview.NominalDurationMS++ }},
		{"device_changed", func(p *orbitPreviewWire) { p.Preview.Device.Serial = "20005602" }},
		{"availability_without_online", func(p *orbitPreviewWire) { p.Preview.Device.Connection = "offline" }},
		{"initial_end_mismatch", func(p *orbitPreviewWire) {
			p.Result.Program.Initial[0].Color = "#123456"
			p.Preview.Program = p.Result.Program
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := orbitFixture(t, 4, &serial)
			tc.change(&p)
			b, _ := json.Marshal(p)
			if _, err := validateOrbitPreview(b, lightPreviewArgs{Contract: orbitLightContract, Action: orbitAction(t, 4), Serial: &serial}); err == nil {
				t.Fatal("changed preview accepted")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*frameJobWire)
	}{
		{"id", func(j *frameJobWire) { j.ID = "job_legacy" }}, {"counter", func(j *frameJobWire) { j.CompletedFrames = j.TotalFrames + 1 }},
		{"frames_without_writes", func(j *frameJobWire) { j.CompletedFrames = 1 }},
		{"writes_ahead_of_frames", func(j *frameJobWire) { j.CompletedTargetWrites = j.PlannedTargetWrites }},
		{"missing_time", func(j *frameJobWire) { j.CreatedAt = time.Time{} }}, {"early_success", func(j *frameJobWire) { j.Status = "succeeded" }},
		{"finished_running", func(j *frameJobWire) { now := j.CreatedAt; j.FinishedAt = &now }},
		{"missing_start", func(j *frameJobWire) { j.StartedAt = nil }}, {"before_created", func(j *frameJobWire) { now := j.CreatedAt.Add(-time.Second); j.StartedAt = &now }},
		{"status", func(j *frameJobWire) { j.Status = "ready" }},
	} {
		t.Run("job_"+tc.name, func(t *testing.T) {
			j := orbitJob(orbitFixture(t, 4, nil), "running")
			tc.change(&j)
			if validateFrameJob(j) == nil {
				t.Fatal("bad FrameJob accepted")
			}
		})
	}
}

func TestOrbitSubmissionFailureNeverRetriesAndKeepsTicket(t *testing.T) {
	for _, mode := range []string{"wrong_program", "wrong_location", "wrong_device", "missing_instance", "duplicate_instance", "bad_status", "bad_count", "bad_created", "duplicate_json", "error_surrogate", "busy", "lost_body"} {
		t.Run(mode, func(t *testing.T) {
			var previews, writes, other atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v2/light/preview" {
					previews.Add(1)
					s := "20005601"
					json.NewEncoder(w).Encode(orbitFixture(t, 4, &s))
					return
				}
				if r.URL.Path != "/v2/devices/20005601/light" {
					other.Add(1)
					w.WriteHeader(500)
					return
				}
				writes.Add(1)
				orbitHeaders(w)
				j := orbitJob(orbitFixture(t, 4, nil), "running")
				switch mode {
				case "wrong_program":
					j.Program.Initial[0].Color = "#123456"
				case "wrong_location":
					w.Header().Set("Location", "/v1/devices/20005601/requests/"+testRequestID)
				case "wrong_device":
					j.Device = "20005602"
				case "missing_instance":
					w.Header().Del("X-NB1-Daemon-Instance-ID")
				case "duplicate_instance":
					w.Header().Add("X-NB1-Daemon-Instance-ID", testInstanceID)
				case "bad_status":
					w.WriteHeader(200)
					json.NewEncoder(w).Encode(j)
					return
				case "bad_count":
					j.CompletedTargetWrites = j.PlannedTargetWrites + 1
				case "bad_created":
					j.CreatedAt = time.Time{}
				case "duplicate_json":
					w.WriteHeader(202)
					b, _ := json.Marshal(j)
					w.Write(append([]byte(`{"id":"x",`), b[1:]...))
					return
				case "error_surrogate":
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"code":"invalid_request","message":"\ud800"}}`)
					return
				case "busy":
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":{"code":"busy","message":"active"}}`)
					return
				case "lost_body":
					w.Header().Set("Content-Length", "4096")
					w.WriteHeader(202)
					fmt.Fprint(w, `{"id":`)
					return
				}
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(j)
			}))
			defer srv.Close()
			session := connect(t, srv.URL)
			res, out := call(t, session, "run_light", withTicket(map[string]any{"contract": orbitLightContract, "action": orbitAction(t, 4), "serial": "20005601"}))
			if !res.IsError || out.Error == nil || out.Accepted || previews.Load() != 1 || writes.Load() != 1 || other.Load() != 0 {
				t.Fatalf("failure retried or accepted %+v", out)
			}
			if out.Error.OutcomeUnknown != (mode != "busy") {
				t.Fatalf("wrong uncertainty %+v", out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
		})
	}
}

func TestOrbitUnavailablePreviewAndReceiptErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/light/preview" {
			s := "20005601"
			p := orbitFixture(t, 4, &s)
			p.Preview.Execution = frameExecutionWire{Reason: "missing_capabilities"}
			json.NewEncoder(w).Encode(p)
			return
		}
		if strings.Contains(r.URL.Path, "/frame-requests/") {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"request_not_found","message":"not retained"}}`)
			return
		}
		t.Error("unexpected fallback")
		w.WriteHeader(500)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	res, out := call(t, session, "run_light", withTicket(map[string]any{"contract": orbitLightContract, "action": orbitAction(t, 4), "serial": "20005601"}))
	if !res.IsError || out.Error == nil || out.Accepted || calls.Load() != 1 {
		t.Fatal("unsupported preflight admitted work")
	}
	assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
	res, out = call(t, session, "get_job", withTicket(map[string]any{"serial": "20005601", "job_kind": "frame"}))
	if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown || calls.Load() != 2 {
		t.Fatal("missing receipt retried or cleared old uncertainty")
	}
	assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
}

func TestFrameReceiptSelectorNeverProbesOtherRoute(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"code":"request_not_found","message":"not retained"}}`)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	for _, kind := range []string{"", "program", "frame"} {
		args := withTicket(map[string]any{"serial": "20005601"})
		if kind != "" {
			args["job_kind"] = kind
		}
		mu.Lock()
		before := len(paths)
		mu.Unlock()
		res, out := call(t, session, "get_job", args)
		mu.Lock()
		current := append([]string(nil), paths...)
		mu.Unlock()
		if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown || len(current) != before+1 {
			t.Fatalf("receipt lookup probed/retried: %+v %v", out, current)
		}
		part := "/requests/"
		if kind == "frame" {
			part = "/frame-requests/"
		}
		if current[len(current)-1] != "/v1/devices/20005601"+part+testRequestID {
			t.Fatal("wrong explicit receipt family", current)
		}
	}
}

func TestOrbitHTTPMalformedResponsesStayBound(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte) []byte
		header func(http.Header)
	}{
		{"unknown", func(b []byte) []byte { return append([]byte(`{"extra":1,`), b[1:]...) }, nil},
		{"null", func([]byte) []byte { return []byte(`null`) }, nil},
		{"trailing", func(b []byte) []byte { return append(b, []byte(` {}`)...) }, nil},
		{"invalid_utf8", func(b []byte) []byte {
			return append(append([]byte(`{"extra":"`), 0xff), append([]byte(`",`), b[1:]...)...)
		}, nil},
		{"wrong_mime", nil, func(h http.Header) { h.Set("Content-Type", "text/plain") }},
		{"duplicate_mime", nil, func(h http.Header) { h.Add("Content-Type", "application/json") }},
	}
	for _, phase := range []string{"preflight", "write"} {
		for _, tc := range cases {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				var previews, writes atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					isWrite := r.URL.Path == "/v2/devices/20005601/light"
					var b []byte
					if isWrite {
						writes.Add(1)
						orbitHeaders(w)
						b, _ = json.Marshal(orbitJob(orbitFixture(t, 4, nil), "queued"))
					} else {
						previews.Add(1)
						s := "20005601"
						b, _ = json.Marshal(orbitFixture(t, 4, &s))
					}
					if isWrite == (phase == "write") {
						if tc.mutate != nil {
							b = tc.mutate(b)
						}
						if tc.header != nil {
							tc.header(w.Header())
						}
					}
					if isWrite {
						w.WriteHeader(202)
					}
					w.Write(b)
				}))
				defer srv.Close()
				session := connect(t, srv.URL)
				res, out := call(t, session, "run_light", withTicket(map[string]any{"contract": orbitLightContract, "action": orbitAction(t, 4), "serial": "20005601"}))
				wantWrites := int32(0)
				if phase == "write" {
					wantWrites = 1
				}
				if !res.IsError || out.Error == nil || out.Accepted || previews.Load() != 1 || writes.Load() != wantWrites {
					t.Fatalf("bad response was accepted/retried: %+v", out)
				}
				assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			})
		}
	}
}

func TestFrameIDResponseMustMatchRequestedID(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		j := orbitJob(orbitFixture(t, 4, nil), "succeeded")
		j.ID = "framejob_ffffffffffffffffffffffffffffffff"
		json.NewEncoder(w).Encode(j)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	res, out := call(t, session, "get_job", map[string]any{"id": orbitJobID})
	if !res.IsError || out.Error == nil || out.Accepted || calls.Load() != 1 {
		t.Fatalf("get_job accepted changed identity: %+v", out)
	}
}
