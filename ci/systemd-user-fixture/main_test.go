package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, args := range [][]string{
		{"server", "--devices", "00000000", "--listen", "127.0.0.1:8931"},
		{"server", "--devices", "0123ABcd,20005601", "--listen", "[::1]:8931", "--lock-dir", filepath.Join(t.TempDir(), "fixture-locks")},
	} {
		if _, err := parse(args); err != nil {
			t.Fatalf("valid argv: %v", err)
		}
	}
	for _, args := range [][]string{
		nil, {"doctor"}, {"server"},
		{"server", "--devices", "00000000", "--listen", "0.0.0.0:8931"},
		{"server", "--devices", "00000000", "--listen", "localhost:8931"},
		{"server", "--devices", "00000000", "--listen", "127.0.0.1:0"},
		{"server", "--devices", "bad", "--listen", "127.0.0.1:8931"},
		{"server", "--devices", "2000abcd,2000ABCD", "--listen", "127.0.0.1:8931"},
		{"server", "--devices", "00000000", "--listen", "127.0.0.1:8931", "--unknown"},
		{"server", "--devices", "00000000", "--listen", "127.0.0.1:8931", "--lock-dir", "relative"},
		{"server", "--devices", "00000000", "--listen", "127.0.0.1:8931", "extra"},
	} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted invalid argv %q", args)
		}
	}
}

func TestHealth(t *testing.T) {
	c := config{devices: []string{"00000000"}, listen: "127.0.0.1:8931"}
	r := httptest.NewRecorder()
	handler(c).ServeHTTP(r, httptest.NewRequest("GET", "/health", nil))
	var payload struct {
		Fixture bool     `json:"fixture"`
		PID     int      `json:"pid"`
		Devices []string `json:"devices"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &payload) != nil || !payload.Fixture || payload.PID != os.Getpid() || len(payload.Devices) != 1 || payload.Devices[0] != "00000000" {
		t.Fatalf("unexpected health: %s", r.Body.String())
	}
	r = httptest.NewRecorder()
	handler(c).ServeHTTP(r, httptest.NewRequest("POST", "/health", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatal("POST was accepted")
	}
}

func TestCancelledServeClosesListener(t *testing.T) {
	listener := &fakeListener{closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, config{}, listener) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("listener survived shutdown")
	}
}

type fakeListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *fakeListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *fakeListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (l *fakeListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8931} }
