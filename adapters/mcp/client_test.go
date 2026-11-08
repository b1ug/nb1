package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointMustStayLocal(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:8931", "http://example.com", "http://0.0.0.0:8931", "http://127.0.0.1:8931/path", "http://user:pass@127.0.0.1:8931", "http://127.0.0.1:8931?q=x"} {
		if d, err := newDaemon(endpoint, time.Second); err == nil {
			d.close()
			t.Errorf("accepted %s", endpoint)
		}
	}
}

func TestNoRedirectAndBoundedResponse(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed") }))
	defer destination.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1))
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if out := d.request(context.Background(), http.MethodGet, "/redirect", "devices", nil); out.Error == nil || out.Error.HTTPStatus != 302 {
		t.Fatalf("redirect error %+v", out)
	}
	if out := d.request(context.Background(), http.MethodGet, "/large", "devices", nil); out.Error == nil || out.Error.Code != "invalid_daemon_response" {
		t.Fatalf("large response %+v", out)
	}
}

func TestTimedOutPostDoesNotClaimRejectionOrCancel(t *testing.T) {
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := d.request(context.Background(), http.MethodPost, "/v1/devices/20005601/jobs", "job", map[string]any{})
	if out.Error == nil || !out.Error.OutcomeUnknown || out.Accepted {
		t.Fatalf("ambiguous outcome was hidden: %+v", out)
	}
	select {
	case <-entered:
	default:
		t.Fatal("request was not sent")
	}
}

func TestMalformedJobDoesNotBecomeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"id":"job_example","status":"accepted-as-done"}`)
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := d.request(context.Background(), http.MethodPost, "/jobs", "job", map[string]any{})
	if out.Error == nil || !out.Error.OutcomeUnknown {
		t.Fatalf("malformed acknowledged job accepted: %+v", out)
	}
}
