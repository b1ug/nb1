package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/playback"
)

func TestPlaybackHTTPPolicyOverrideAndSuppressedAdmission(t *testing.T) {
	backend := &sequenceRouteBackend{}
	engine, err := control.New(routedSequenceDriver{"20000001": backend}, control.Options{
		Serials: []string{"20000001"}, PlaybackPolicy: playback.Policy{Mode: playback.ModeNever, HistoryLimit: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	legacy := httptest.NewRecorder()
	httpapi.New(engine).ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/playback", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("Playback routes escaped the sequence-v1 profile: HTTP %d", legacy.Code)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := engine.Device("20000001")
		if state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("device did not connect: %+v", state)
		}
		time.Sleep(time.Millisecond)
	}
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithSequenceAPI()))
	t.Cleanup(server.Close)
	request := func(method, path, body string, wantStatus int, out any) {
		t.Helper()
		var payload *strings.Reader
		if body == "" {
			payload = strings.NewReader("")
		} else {
			payload = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, server.URL+path, payload)
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != wantStatus {
			t.Fatalf("%s %s: HTTP %d, want %d", method, path, response.StatusCode, wantStatus)
		}
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var status control.PlaybackState
	request(http.MethodGet, "/v1/playback", "", http.StatusOK, &status)
	if status.Mode != "never" || status.Status != "suppressed" {
		t.Fatalf("initial status=%+v", status)
	}
	var history struct {
		Requests []control.PlaybackRequest `json:"requests"`
		Limit    int                       `json:"limit"`
		Count    int                       `json:"count"`
	}
	request(http.MethodGet, "/v1/playback/requests", "", http.StatusOK, &history)
	if history.Requests == nil || len(history.Requests) != 0 || history.Count != 0 {
		t.Fatalf("empty history must be an empty array: %+v", history)
	}
	request(http.MethodPost, "/v1/jobs", `{"id":"blocked-job","serial":"20000001","action":{"kind":"off"}}`, http.StatusLocked, nil)
	request(http.MethodGet, "/v1/jobs/blocked-job", "", http.StatusNotFound, nil)
	if len(backend.steps) != 0 {
		t.Fatalf("suppressed request reached hardware: %+v", backend.steps)
	}
	request(http.MethodGet, "/v1/playback/requests", "", http.StatusOK, &history)
	if len(history.Requests) != 1 || history.Count != 1 || history.Limit != 10 || history.Requests[0].Decision != "suppressed" || history.Requests[0].JobID != "blocked-job" {
		t.Fatalf("history=%+v", history)
	}
	request(http.MethodPost, "/v1/playback/override", `{"mode":"suppress","duration_ms":60000}`, http.StatusOK, &status)
	if status.Status != "suppressed" || status.Override == nil || status.Override.Mode != "suppress" {
		t.Fatalf("suppress status=%+v", status)
	}
	request(http.MethodPost, "/v1/jobs", `{"id":"blocked-by-override","serial":"20000001","action":{"kind":"off"}}`, http.StatusLocked, nil)
	request(http.MethodPost, "/v1/playback/override", `{"mode":"permit","duration_ms":60000}`, http.StatusOK, &status)
	if status.Status != "allowed" || status.Override == nil {
		t.Fatalf("permit status=%+v", status)
	}
	status = control.PlaybackState{}
	request(http.MethodDelete, "/v1/playback/override", "", http.StatusOK, &status)
	if status.Status != "suppressed" || status.Override != nil {
		t.Fatalf("clear status=%+v", status)
	}
}
