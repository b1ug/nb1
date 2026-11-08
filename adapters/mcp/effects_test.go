package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStdioEffectIntentForwardingAndReceiptBinding(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/devices/20005601/jobs" {
			t.Errorf("unexpected effect request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		if _, ok := body["program"]; ok {
			t.Error("MCP expanded an effect into a program")
		}
		w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
		w.Header().Set("X-NB1-Replayed", "false")
		w.WriteHeader(http.StatusAccepted)
		// Compiled program data is supplied solely by the fake daemon. MCP
		// must preserve it without treating its local intent as compilation.
		fmt.Fprint(w, `{"id":"job_effect","device":"20005601","status":"queued","program":{"steps":[{"color":"#000000"},{"color":"#123abc"}],"repeat":2,"on_finish":"off"}}`)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	for _, intent := range []map[string]any{
		{"kind": "steady", "color": "#123ABC", "fade_ms": 0},
		{"kind": "pulse", "color": "#123ABC", "led": 2, "cycles": 2, "period_ms": 800, "on_finish": "off"},
		{"kind": "pulse", "color": "#123ABC"},
	} {
		args := withTicket(map[string]any{"serial": "20005601", "effect": intent, "policy": "queue"})
		res, out := call(t, session, "run_program", args)
		if res.IsError || !out.Accepted || out.PollAfterMS != 200 || out.Data.(map[string]any)["program"] == nil {
			t.Fatalf("daemon effect response not preserved: %+v %+v", res, out)
		}
		assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("effect submission retried or prepared a new key: %d", len(requests))
	}
	for i, body := range requests {
		var forwarded effect
		if err := json.Unmarshal(body["effect"], &forwarded); err != nil {
			t.Fatal(err)
		}
		if forwarded.Color != "#123abc" || string(body["policy"]) != `"queue"` || string(body["request_id"]) != `"`+testRequestID+`"` || string(body["expected_daemon_instance_id"]) != `"`+testInstanceID+`"` || len(body) != 4 {
			t.Fatalf("pure effect intent changed: %+v", body)
		}
		if i == 2 && (forwarded.Cycles != nil || forwarded.PeriodMS != nil || forwarded.OnFinish != nil) {
			t.Fatal("MCP filled daemon-owned effect defaults")
		}
	}
}

func TestStdioEffectDryRunIsStructureOnlyAndProgramPreviewUnchanged(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	// This intent is structurally valid but its compiled program exceeds the
	// daemon's total duration budget. Offline MCP preview must not compile it
	// or claim authoritative acceptance, and must not contact the daemon.
	args := withTicket(map[string]any{"serial": "20005601", "dry_run": true, "effect": map[string]any{"kind": "pulse", "color": "#ABCDEF", "cycles": 10000, "period_ms": 1200000}})
	res, out := call(t, session, "run_program", args)
	if res.IsError || !out.DryRun || out.Accepted || out.InstanceBindingVerified == nil || *out.InstanceBindingVerified {
		t.Fatalf("effect preview claimed authority: %+v %+v", res, out)
	}
	assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
	data := out.Data.(map[string]any)
	if data["compiled"] != false || data["validation_level"] != "structure_only" || data["instance_binding_verified"] != false {
		t.Fatalf("missing effect preview limits: %+v", data)
	}
	request := data["request"].(map[string]any)
	if request["program"] != nil || request["effect"].(map[string]any)["color"] != "#abcdef" || request["request_id"] != testRequestID {
		t.Fatalf("effect preview compiled or lost the ticket: %+v", data)
	}
	res, out = call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "effect": map[string]any{"kind": "steady", "color": "#010203"}})
	if res.IsError || out.RequestTicket != nil || out.InstanceBindingVerified != nil || out.Data.(map[string]any)["compiled"] != false {
		t.Fatalf("ordinary effect preview changed: %+v", out)
	}
	if _, present := out.Data.(map[string]any)["instance_binding_verified"]; present {
		t.Fatal("unkeyed effect preview invented an instance binding")
	}
	res, out = call(t, session, "run_program", map[string]any{"serial": "20005601", "dry_run": true, "program": map[string]any{"steps": []any{map[string]any{"color": "#123ABC"}}}})
	if res.IsError || !out.DryRun || out.Accepted {
		t.Fatalf("raw program preview regressed: %+v", out)
	}
	data = out.Data.(map[string]any)
	if len(data) != 2 || data["compiled"] != nil || data["validation_level"] != nil || data["request"].(map[string]any)["program"] == nil {
		t.Fatalf("raw program preview shape changed: %+v", data)
	}
	if requests.Load() != 0 {
		t.Fatalf("preview made %d HTTP requests", requests.Load())
	}
}

func TestStdioEffectAmbiguousAndInvalidInputsStopBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	invalid := []string{
		`{"serial":"20005601"}`,
		`{"serial":"20005601","program":{"steps":[{"color":"#010203"}]},"effect":{"kind":"steady","color":"#010203"}}`,
		`{"serial":"20005601","effect":null}`,
		`{"serial":"20005601","program":null,"effect":{"kind":"steady","color":"#010203"}}`,
		`{"serial":"20005601","effect":{"kind":"steady","color":"#010203"},"effect":{"kind":"pulse","color":"#010203"}}`,
		`{"serial":"20005601","effect":{"kind":"steady","k\u0069nd":"pulse","color":"#010203"}}`,
		`{"serial":"20005601","Effect":{"kind":"steady","color":"#010203"}}`,
		`{"serial":"20005601","effect":{"kind":"steady","color":"#010203","LED":1}}`,
		`{"serial":"20005601","effect":{"kind":"steady","color":"#010203","fade_ms":null}}`,
		`{"serial":"20005601","effect":{"kind":"pulse","color":"#010203","on_finish":null}}`,
	}
	for _, intent := range []string{
		`{}`, `{"kind":"steady"}`, `{"kind":"flash","color":"#010203"}`, `{"kind":"STEADY","color":"#010203"}`,
		`{"kind":"steady","color":"#xyzxyz"}`, `{"kind":"steady","color":"#010203","led":3}`,
		`{"kind":"steady","color":"#010203","fade_ms":1}`, `{"kind":"steady","color":"#010203","fade_ms":600010}`,
		`{"kind":"steady","color":"#010203","cycles":0}`, `{"kind":"steady","color":"#010203","period_ms":0}`, `{"kind":"steady","color":"#010203","on_finish":"hold"}`,
		`{"kind":"pulse","color":"#010203","fade_ms":0}`, `{"kind":"pulse","color":"#010203","cycles":0}`, `{"kind":"pulse","color":"#010203","cycles":10001}`,
		`{"kind":"pulse","color":"#010203","period_ms":50}`, `{"kind":"pulse","color":"#010203","period_ms":61}`, `{"kind":"pulse","color":"#010203","period_ms":1200010}`,
		`{"kind":"pulse","color":"#010203","on_finish":""}`, `{"kind":"pulse","color":"#010203","hold_ms":1000}`,
	} {
		invalid = append(invalid, `{"serial":"20005601","dry_run":true,"effect":`+intent+`}`)
	}
	for i, raw := range invalid {
		res, out := call(t, session, "run_program", json.RawMessage(raw))
		if !res.IsError {
			t.Fatalf("invalid effect case %d accepted: %s %+v", i, raw, out)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid effect made %d HTTP requests", requests.Load())
	}
	// Runtime XOR remains a guard even without the SDK/schema wrapper.
	for _, args := range []runArgs{{}, {Program: &program{}, Effect: &effect{}}} {
		res, out, err := run(context.Background(), nil, args)
		if err != nil || !res.IsError || out.Error == nil || !strings.Contains(out.Error.Message, "exactly one") {
			t.Fatalf("runtime XOR guard missed: %+v %v", out, err)
		}
	}
}

func TestStdioEffectReceiptUnknownAndBindingChecksRemain(t *testing.T) {
	var mode atomic.Value
	mode.Store("expired")
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if mode.Load().(string) == "expired" {
			w.WriteHeader(http.StatusGone)
			fmt.Fprint(w, `{"error":{"code":"request_expired","message":"old intent may have run"}}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"id":"job_unbound_effect","device":"20005601","status":"succeeded"}`)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	for _, value := range []string{"expired", "unconfirmed binding"} {
		mode.Store(value)
		res, out := call(t, session, "run_program", withTicket(map[string]any{"serial": "20005601", "effect": map[string]any{"kind": "pulse", "color": "#010203"}}))
		if !res.IsError || out.Error == nil || !out.Error.OutcomeUnknown || out.Accepted {
			t.Fatalf("effect %s lost unknown outcome: %+v", value, out)
		}
		assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
	}
	if requests.Load() != 2 {
		t.Fatalf("effect failure retried or replaced key: %d", requests.Load())
	}
}
