package daemonclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testClientToken = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func TestEndpointBoundary(t *testing.T) {
	if DefaultEndpoint != "http://127.0.0.1:5885" {
		t.Fatal("unexpected default daemon endpoint:", DefaultEndpoint)
	}
	for _, endpoint := range []string{"http://127.0.0.1:8931", "http://localhost:8931/", "http://[::1]:8931"} {
		client, err := New(endpoint)
		if err != nil {
			t.Errorf("%s: %v", endpoint, err)
		} else {
			client.Close()
		}
	}
	for _, endpoint := range []string{"https://127.0.0.1:8931", "http://example.com", "http://192.168.1.1", "http://user:pass@localhost", "http://localhost/v1", "http://localhost?token=x", "http://localhost#x", ":bad"} {
		if client, err := New(endpoint); err == nil {
			client.Close()
			t.Errorf("accepted %s", endpoint)
		}
	}
}

func TestLoopbackAPIClientSendsOnlyItsConfiguredCredential(t *testing.T) {
	for _, mode := range []struct {
		name  string
		token string
		want  string
	}{
		{"default", "", ""},
		{"authenticated", testClientToken, "Bearer " + testClientToken},
	} {
		t.Run(mode.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				values := r.Header.Values("Authorization")
				if mode.token == "" && len(values) != 0 || mode.token != "" && (len(values) != 1 || values[0] != mode.want) {
					t.Error("unexpected Authorization header")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/healthz":
					fmt.Fprint(w, `{"status":"ok","api_version":"v1"}`)
				case "/v1/patterns":
					fmt.Fprint(w, `[]`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewWithToken(server.URL, mode.token)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Health(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := client.SequencePatterns(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthenticatedDialRejectsNonLoopbackResolution(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8931", "[::1]:8931"} {
		if err := loopbackOnly(context.Background(), "tcp", address, nil); err != nil {
			t.Errorf("loopback address rejected: %v", err)
		}
	}
	for _, address := range []string{"192.0.2.1:8931", "localhost:8931", "[2001:db8::1]:8931"} {
		if err := loopbackOnly(context.Background(), "tcp", address, nil); err == nil {
			t.Errorf("unverified socket address accepted: %s", address)
		}
	}
}
