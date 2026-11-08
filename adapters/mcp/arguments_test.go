package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRawMCPArgumentsRejectedBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"id":"job_example","status":"queued"}`)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	deep := `{"unknown":` + strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65) + `}`
	cases := []struct{ name, tool, arguments, message string }{
		{"duplicate LED", "set_device_state", `{"serial":"20005601","color":"#ff0000","led":1,"led":0}`, "duplicate"},
		{"escaped duplicate LED", "set_device_state", `{"serial":"20005601","color":"#ff0000","led":1,"l\u0065d":0}`, "duplicate"},
		{"nested duplicate step", "run_program", `{"serial":"20005601","program":{"steps":[{"led":1,"led":0,"color":"#ff0000"}]}}`, "duplicate"},
		{"nested duplicate program", "run_program", `{"serial":"20005601","program":{"steps":[{"color":"#ff0000"}],"repeat":1,"repeat":2}}`, "duplicate"},
		{"duplicate serial", "get_device_state", `{"serial":"20005601","serial":"other"}`, "duplicate"},
		{"duplicate get id", "get_job", `{"id":"job_a","id":"job_b"}`, "duplicate"},
		{"duplicate cancel id", "cancel_job", `{"id":"job_a","id":"job_b"}`, "duplicate"},
		{"duplicate list field", "list_devices", `{"x":1,"x":2}`, "duplicate"},
		{"null arguments", "list_devices", `null`, "null"},
		{"explicit optional null", "set_device_state", `{"serial":"20005601","color":"#ff0000","dry_run":null}`, "null"},
		{"nested null", "run_program", `{"serial":"20005601","program":{"steps":[null]}}`, "null"},
		{"noncanonical LED", "set_device_state", `{"serial":"20005601","color":"#ff0000","LED":1}`, ""},
		{"uppercase alias alongside canonical", "set_device_state", `{"serial":"20005601","color":"#ff0000","led":1,"LED":0}`, ""},
		{"depth", "list_devices", deep, "64"},
		{"size", "list_devices", `{"unknown":"` + strings.Repeat("x", 1<<20) + `"}`, "1 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			res, out := call(t, session, tc.tool, json.RawMessage(tc.arguments))
			if !res.IsError {
				t.Fatalf("ambiguous arguments accepted: %+v", out)
			}
			if requests.Load() != before {
				t.Fatal("invalid raw arguments reached HTTP")
			}
			if tc.message != "" {
				if out.Error == nil || out.Error.Code != "invalid_request" || !strings.Contains(out.Error.Message, tc.message) {
					t.Fatalf("raw guard was bypassed: %+v", out)
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid calls contacted daemon %d times", requests.Load())
	}
}

func TestMissingListArgumentsAndCanonicalStateStillWork(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `[]`)
			return
		}
		var request submission
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Program.Steps[0].LED != 1 {
			t.Errorf("canonical LED changed: %+v", request)
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"id":"job_example","status":"queued"}`)
	}))
	defer srv.Close()
	session := connect(t, srv.URL)
	res, out := call(t, session, "list_devices", nil)
	if res.IsError || out.Kind != "devices" {
		t.Fatalf("missing args must mean empty object: %+v", out)
	}
	res, out = call(t, session, "set_device_state", json.RawMessage(`{"serial":"20005601","color":"#ff0000","led":1}`))
	if res.IsError || !out.Accepted {
		t.Fatalf("canonical state rejected: %+v", out)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected one request per valid call, got %d", requests.Load())
	}
}

func TestRawArgumentBoundaryLimits(t *testing.T) {
	// Root object plus 63 nested arrays is 64 container levels.
	accepted := `{"x":` + strings.Repeat(`[`, 63) + `0` + strings.Repeat(`]`, 63) + `}`
	if err := validateRawArguments(json.RawMessage(accepted)); err != nil {
		t.Fatalf("64 container levels rejected: %v", err)
	}
	tooDeep := `{"x":` + strings.Repeat(`[`, 64) + `0` + strings.Repeat(`]`, 64) + `}`
	if err := validateRawArguments(json.RawMessage(tooDeep)); err == nil {
		t.Fatal("65 container levels accepted")
	}
	boundary := `{"x":"` + strings.Repeat("x", maxArgumentBytes-8) + `"}`
	if len(boundary) != maxArgumentBytes {
		t.Fatal("incorrect byte-boundary fixture")
	}
	if err := validateRawArguments(json.RawMessage(boundary)); err != nil {
		t.Fatalf("1 MiB object rejected: %v", err)
	}
	if err := validateRawArguments(json.RawMessage(boundary + " ")); err == nil {
		t.Fatal("byte limit ignored trailing whitespace")
	}
}
