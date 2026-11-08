package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// No default endpoint/color or implicit hardware action: the extra effects
// flag and explicit endpoint, serial and exact RGB are all required.
func TestLiveEffectsOptIn(t *testing.T) {
	if os.Getenv("NB1_MCP_LIVE_EFFECTS") != "1" {
		t.Skip("set NB1_MCP_LIVE_EFFECTS=1 plus endpoint, serial and color for live finite-pulse verification")
	}
	endpoint, serial, color := os.Getenv("NB1_MCP_LIVE_ENDPOINT"), os.Getenv("NB1_MCP_LIVE_SERIAL"), os.Getenv("NB1_MCP_LIVE_COLOR")
	if endpoint == "" || !validToken(serial) || color == "" {
		t.Fatal("explicit NB1_MCP_LIVE_ENDPOINT, NB1_MCP_LIVE_SERIAL and NB1_MCP_LIVE_COLOR required")
	}
	cycles, period, finish := 2, 800, "off"
	intent, err := normalizeEffectStructure(effect{Kind: "pulse", Color: color, Cycles: &cycles, PeriodMS: &period, OnFinish: &finish})
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("NB1_MCP_LIVE_BINARY")
	if binary == "" {
		binary = filepath.Join(".build", "nb1-mcp")
	}
	command := exec.Command(binary, "--endpoint", endpoint)
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "nb1-live-effects", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// Final off is a separate cleanup intent even if the pulse response is
	// lost. It never resubmits the original intent under a replacement key.
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		off, cleanupErr := liveReceiptCall(cleanupCtx, session, "set_device_state", map[string]any{"serial": serial, "color": "#000000"})
		if cleanupErr == nil {
			_, cleanupErr = waitLiveReceiptJob(cleanupCtx, session, off)
		}
		if cleanupErr == nil {
			cleanupErr = assertLiveReceiptRGB(cleanupCtx, session, serial, "#000000")
		}
		if cleanupErr != nil {
			t.Errorf("final off was not confirmed: %v", cleanupErr)
		}
	}()
	job, err := liveReceiptCall(ctx, session, "run_program", map[string]any{"serial": serial, "effect": intent})
	if err != nil || !job.Accepted {
		t.Fatalf("effect submit: %+v %v", job, err)
	}
	id, err := waitLiveReceiptJob(ctx, session, job)
	if err != nil {
		t.Fatal(err)
	}
	if err = assertLiveReceiptRGB(ctx, session, serial, "#000000"); err != nil {
		t.Fatal(err)
	}
	t.Logf("daemon-compiled finite pulse succeeded: job=%s cycles=2 period_ms=800 color=%s; final raw RGB is off (not optical/camera proof)", id, intent.Color)
}
