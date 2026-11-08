package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Explicitly opt in to submission in addition to naming the endpoint, serial
// and exact color. Nothing in the normal software suite touches a live daemon.
func TestLiveReceiptsOptIn(t *testing.T) {
	if os.Getenv("NB1_MCP_LIVE_RECEIPTS") != "1" {
		t.Skip("set NB1_MCP_LIVE_RECEIPTS=1 plus endpoint, serial and color for live receipt verification")
	}
	endpoint, serial, color := os.Getenv("NB1_MCP_LIVE_ENDPOINT"), os.Getenv("NB1_MCP_LIVE_SERIAL"), os.Getenv("NB1_MCP_LIVE_COLOR")
	if endpoint == "" || !validToken(serial) || color == "" {
		t.Fatal("explicit NB1_MCP_LIVE_ENDPOINT, NB1_MCP_LIVE_SERIAL and NB1_MCP_LIVE_COLOR required")
	}
	p, err := normalizeProgram(program{Steps: []step{{Color: color}}, OnFinish: "hold"})
	if err != nil {
		t.Fatal(err)
	}
	color = p.Steps[0].Color
	binary := os.Getenv("NB1_MCP_LIVE_BINARY")
	if binary == "" {
		binary = filepath.Join(".build", "nb1-mcp")
	}
	command := exec.Command(binary, "--endpoint", endpoint)
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "nb1-live-receipts", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	mutated := false
	// This is a separate, explicit cleanup intent. An ambiguous original
	// response is never automatically reissued with a new key.
	defer func() {
		if !mutated {
			return
		}
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
		} else {
			t.Log("final off job succeeded and both raw RGB observations were #000000")
		}
	}()
	prepared, err := liveReceiptCall(ctx, session, "list_devices", map[string]any{"prepare_request": true})
	if err != nil || prepared.RequestTicket == nil {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	ticket := *prepared.RequestTicket
	args := map[string]any{"serial": serial, "color": color, "request_id": ticket.RequestID, "expected_daemon_instance_id": ticket.ExpectedDaemonInstanceID}
	mutated = true // even a lost POST response requires the final cleanup intent
	first, err := liveReceiptCall(ctx, session, "set_device_state", args)
	if err != nil || !first.Accepted {
		t.Fatalf("first submit: %+v %v", first, err)
	}
	assertTicket(t, first, ticket)
	id, err := waitLiveReceiptJob(ctx, session, first)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := liveReceiptCall(ctx, session, "set_device_state", args)
	if err != nil || !replayed.Accepted || replayed.Data.(map[string]any)["id"] != id {
		t.Fatalf("same-key replay did not return the original job: %+v %v", replayed, err)
	}
	assertTicket(t, replayed, ticket)
	lookup, err := liveReceiptCall(ctx, session, "get_job", map[string]any{"serial": serial, "request_id": ticket.RequestID, "expected_daemon_instance_id": ticket.ExpectedDaemonInstanceID})
	if err != nil || lookup.Data.(map[string]any)["id"] != id || lookup.Data.(map[string]any)["status"] != "succeeded" {
		t.Fatalf("receipt lookup: %+v %v", lookup, err)
	}
	if err = assertLiveReceiptRGB(ctx, session, serial, color); err != nil {
		t.Fatal(err)
	}
	conflicting := "#010203"
	if color == conflicting {
		conflicting = "#030201"
	}
	args["color"] = conflicting
	conflict, err := liveReceiptCall(ctx, session, "set_device_state", args)
	if err == nil || conflict.Error == nil || conflict.Error.Code != "idempotency_conflict" || conflict.Error.HTTPStatus != 409 {
		t.Fatalf("different payload reused a key: %+v %v", conflict, err)
	}
	assertTicket(t, conflict, ticket)
	if err = assertLiveReceiptRGB(ctx, session, serial, color); err != nil {
		t.Fatalf("conflicting submission changed observed raw RGB: %v", err)
	}
	t.Logf("same-key submission and receipt lookup preserved job=%s ticket=%+v; payload conflict left raw RGB=%s", id, ticket, color)
}

func liveReceiptCall(ctx context.Context, session *mcp.ClientSession, name string, args any) (output, error) {
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return output{}, err
	}
	encoded, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return output{}, err
	}
	var out output
	if err = json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	if res.IsError || out.Error != nil {
		return out, fmt.Errorf("%s failed: %+v", name, out.Error)
	}
	return out, nil
}

func waitLiveReceiptJob(ctx context.Context, session *mcp.ClientSession, out output) (string, error) {
	job, ok := out.Data.(map[string]any)
	if !ok {
		return "", fmt.Errorf("missing job object: %+v", out)
	}
	id, _ := job["id"].(string)
	if !validToken(id) {
		return "", fmt.Errorf("missing job id: %+v", out)
	}
	for {
		if job["status"] == "succeeded" {
			return id, nil
		}
		if job["status"] == "failed" || job["status"] == "cancelled" {
			return id, fmt.Errorf("job did not succeed: %+v", job)
		}
		select {
		case <-ctx.Done():
			return id, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		var err error
		out, err = liveReceiptCall(ctx, session, "get_job", map[string]any{"id": id})
		if err != nil {
			return id, err
		}
		job = out.Data.(map[string]any)
	}
}

func assertLiveReceiptRGB(ctx context.Context, session *mcp.ClientSession, serial, color string) error {
	state, err := liveReceiptCall(ctx, session, "get_device_state", map[string]any{"serial": serial})
	if err != nil {
		return err
	}
	device, ok := state.Data.(map[string]any)
	if !ok || device["serial"] != serial {
		return fmt.Errorf("wrong device state: %+v", state)
	}
	observed, ok := device["observed"].(map[string]any)
	if !ok || observed["led1"] != color || observed["led2"] != color || observed["sampled_at"] == nil {
		return fmt.Errorf("expected raw RGB=%s with a timestamp: %+v", color, state)
	}
	return nil
}
