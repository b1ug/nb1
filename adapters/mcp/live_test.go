package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This test never runs by default. Endpoint and serial opt in to read-only
// daemon observation; COLOR separately opts in to an exact RGB mutation.
func TestLiveDaemonOptIn(t *testing.T) {
	endpoint, serial := os.Getenv("NB1_MCP_LIVE_ENDPOINT"), os.Getenv("NB1_MCP_LIVE_SERIAL")
	if endpoint == "" {
		t.Skip("set NB1_MCP_LIVE_ENDPOINT and NB1_MCP_LIVE_SERIAL for an explicit live check")
	}
	if !validToken(serial) {
		t.Fatal("explicit NB1_MCP_LIVE_SERIAL required")
	}
	binary := os.Getenv("NB1_MCP_LIVE_BINARY")
	if binary == "" {
		binary = filepath.Join(".build", "nb1-mcp")
	}
	command := exec.Command(binary, "--endpoint", endpoint)
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "nb1-live-acceptance", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 10 {
		t.Fatalf("tool discovery: %v %v", listed, err)
	}
	res, state := call(t, session, "get_device_state", map[string]any{"serial": serial})
	if res.IsError {
		t.Fatalf("read state: %+v", state)
	}
	if state.Data.(map[string]any)["serial"] != serial {
		t.Fatalf("wrong device: %+v", state)
	}
	t.Logf("read-only MCP state: %+v", state)
	color := os.Getenv("NB1_MCP_LIVE_COLOR")
	if color == "" {
		return
	}
	res, job := call(t, session, "set_device_state", map[string]any{"serial": serial, "color": color})
	if res.IsError || !job.Accepted {
		t.Fatalf("set: %+v", job)
	}
	id := job.Data.(map[string]any)["id"].(string)
	for {
		res, job = call(t, session, "get_job", map[string]any{"id": id})
		if res.IsError {
			t.Fatalf("job: %+v", job)
		}
		status := job.Data.(map[string]any)["status"]
		if status == "failed" || status == "cancelled" {
			t.Fatalf("job did not succeed: %+v", job)
		}
		if status == "succeeded" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	res, state = call(t, session, "get_device_state", map[string]any{"serial": serial})
	if res.IsError {
		t.Fatalf("final state: %+v", state)
	}
	observed, ok := state.Data.(map[string]any)["observed"].(map[string]any)
	if !ok || observed["led1"] != strings.ToLower(color) || observed["led2"] != strings.ToLower(color) {
		t.Fatalf("raw RGB mismatch: %+v", state)
	}
	t.Logf("MCP color job succeeded and observed: %+v", state)
}
