package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewKeyedSuccessMustConfirmBinding(t *testing.T) {
	for _, lookup := range []bool{false, true} {
		t.Run(fmt.Sprint("lookup=", lookup), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A valid ordinary job response, but no receipt capability confirmation.
				if !lookup {
					w.WriteHeader(202)
				}
				fmt.Fprint(w, `{"id":"job_review","device":"20005601","status":"succeeded"}`)
			}))
			defer srv.Close()
			d, err := newDaemon(srv.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			var out output
			if lookup {
				out = getJob(context.Background(), d, getJobArgs{Serial: "20005601", RequestID: testRequestID, ExpectedDaemonInstanceID: testInstanceID})
			} else {
				_, out, _ = submit(context.Background(), d, "20005601", program{Steps: []step{{Color: "#102030"}}}, "replace", false, testRequestID, testInstanceID)
			}
			if out.Error == nil || !out.Error.OutcomeUnknown {
				t.Fatalf("unconfirmed binding reported as successful: %+v", out)
			}
		})
	}
}
func TestReviewReceiptLookupFailureKeepsOriginalOutcomeUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"code":"internal_error","message":"cannot load receipt"}}`)
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := getJob(context.Background(), d, getJobArgs{Serial: "20005601", RequestID: testRequestID, ExpectedDaemonInstanceID: testInstanceID})
	if out.Error == nil || !out.Error.OutcomeUnknown {
		t.Fatalf("failed lookup falsely clears uncertainty of prior intent: %+v", out)
	}
}

func TestStdioReceiptBindingConfirmation(t *testing.T) {
	var mode atomic.Value
	mode.Store("valid")
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		value := mode.Load().(string)
		w.Header().Set("X-NB1-Daemon-Instance-ID", testInstanceID)
		if r.Method == http.MethodPost {
			w.Header().Set("X-NB1-Replayed", "false")
		}
		job := map[string]any{"id": "job_bound", "device": "20005601", "status": "succeeded"}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusAccepted
		}
		switch value {
		case "replayed":
			w.Header().Set("X-NB1-Replayed", "true")
		case "missing instance":
			w.Header().Del("X-NB1-Daemon-Instance-ID")
		case "wrong instance":
			w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_ffffffffffffffffffffffffffffffff")
		case "duplicate instance":
			w.Header().Add("X-NB1-Daemon-Instance-ID", testInstanceID)
		case "missing replayed":
			w.Header().Del("X-NB1-Replayed")
		case "invalid replayed":
			w.Header().Set("X-NB1-Replayed", "TRUE")
		case "duplicate replayed":
			w.Header().Add("X-NB1-Replayed", "false")
		case "missing device":
			delete(job, "device")
		case "wrong device":
			job["device"] = "another_serial"
		case "nonstring device":
			job["device"] = []string{"20005601"}
		case "wrong success status":
			if status == http.StatusOK {
				status = http.StatusAccepted
			} else {
				status = http.StatusOK
			}
		case "missing job id":
			delete(job, "id")
		case "invalid job status":
			job["status"] = "not_a_job_status"
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(job)
	}))
	defer srv.Close()
	session := connectStdio(t, srv.URL)
	for _, name := range []string{"set_device_state", "get_job"} {
		for _, value := range []string{"valid", "replayed", "missing instance", "wrong instance", "duplicate instance", "missing replayed", "invalid replayed", "duplicate replayed", "missing device", "wrong device", "nonstring device", "wrong success status", "missing job id", "invalid job status"} {
			if name == "get_job" && (value == "replayed" || value == "missing replayed" || value == "invalid replayed" || value == "duplicate replayed") {
				continue // lookup confirms the instance/device but has no replay header
			}
			mode.Store(value)
			args := withTicket(map[string]any{"serial": "20005601"})
			if name == "set_device_state" {
				args["color"] = "#102030"
			}
			before := requests.Load()
			res, out := call(t, session, name, args)
			if value == "valid" || value == "replayed" {
				if res.IsError || out.Error != nil || out.Accepted != (name == "set_device_state") {
					t.Fatalf("%s/%s binding was rejected: %+v", name, value, out)
				}
			} else if !res.IsError || out.Error == nil || out.Error.Code != "invalid_daemon_response" || !out.Error.OutcomeUnknown || out.Accepted {
				t.Fatalf("%s/%s unconfirmed binding looked successful: %+v", name, value, out)
			}
			assertTicket(t, out, requestTicket{testRequestID, testInstanceID})
			if requests.Load() != before+1 {
				t.Fatalf("%s/%s retried or replaced the ticket", name, value)
			}
		}
	}
}
