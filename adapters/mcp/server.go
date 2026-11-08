package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type deviceArgs struct {
	Serial string `json:"serial"`
}
type jobArgs struct {
	ID string `json:"id"`
}
type setArgs struct {
	Serial string `json:"serial"`
	Color  string `json:"color"`
	LED    int    `json:"led,omitempty"`
	FadeMS int    `json:"fade_ms,omitempty"`
	HoldMS int    `json:"hold_ms,omitempty"`
	Policy string `json:"policy,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type runArgs struct {
	Serial  string  `json:"serial"`
	Program program `json:"program"`
	Policy  string  `json:"policy,omitempty"`
	DryRun  bool    `json:"dry_run,omitempty"`
}

func newServer(d *daemon) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "nb1-local-device-adapter", Version: "0.1.0-dev"}, &mcp.ServerOptions{Instructions: "Use only devices selected by the local daemon. RGB values are exact raw #RRGGBB, without implicit gamma. Job acceptance is not completion: inspect get_job and its actual status. cancel_job may return cancelling until hardware cleanup succeeds. HTTP timeout or closing this MCP session does not cancel accepted daemon jobs. This adapter never opens USB or silently falls back to direct control. Dry-run validates a program locally and sends no HTTP request."})
	s.AddReceivingMiddleware(rawArgumentsGuard)
	mcp.AddTool(s, tool("list_devices", "List configured daemon devices with connection, capabilities and timestamped observations.", objectSchema(map[string]any{}), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, output, error) {
		return result(d.request(ctx, http.MethodGet, "/v1/devices", "devices", nil))
	})
	mcp.AddTool(s, tool("get_device_state", "Get the daemon's latest observed raw RGB and timestamp; this is a snapshot, not a camera measurement.", objectSchema(map[string]any{"serial": tokenSchema()}, "serial"), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args deviceArgs) (*mcp.CallToolResult, output, error) {
		if !validToken(args.Serial) {
			return invalid("invalid serial")
		}
		return result(d.request(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(args.Serial), "device", nil))
	})
	setProperties := map[string]any{"serial": tokenSchema(), "color": colorSchema(), "led": map[string]any{"type": "integer", "enum": []int{0, 1, 2}, "description": "0=both, 1=top, 2=bottom"}, "fade_ms": fadeSchema(), "hold_ms": holdSchema(), "policy": policySchema(), "dry_run": map[string]any{"type": "boolean", "description": "Validate and return the program without contacting the daemon"}}
	mcp.AddTool(s, tool("set_device_state", "Submit one exact RGB state; successful completion retains the color. Returns a daemon job, not a completion guarantee. dry_run previews locally.", objectSchema(setProperties, "serial", "color"), false, false), func(ctx context.Context, _ *mcp.CallToolRequest, args setArgs) (*mcp.CallToolResult, output, error) {
		return submit(ctx, d, args.Serial, program{Steps: []step{{LED: args.LED, Color: args.Color, FadeMS: args.FadeMS, HoldMS: args.HoldMS}}, Repeat: 1, OnFinish: "hold"}, args.Policy, args.DryRun)
	})
	mcp.AddTool(s, tool("run_program", "Submit a bounded structured program to one device worker. replace cancels old work, queue is bounded, reject reports busy. Cancel and daemon shutdown clean up with off.", objectSchema(map[string]any{"serial": tokenSchema(), "program": programSchema(), "policy": policySchema(), "dry_run": map[string]any{"type": "boolean"}}, "serial", "program"), false, false), func(ctx context.Context, _ *mcp.CallToolRequest, args runArgs) (*mcp.CallToolResult, output, error) {
		return submit(ctx, d, args.Serial, args.Program, args.Policy, args.DryRun)
	})
	mcp.AddTool(s, tool("get_job", "Inspect actual job status: queued, running, cancelling, succeeded, cancelled or failed. Terminal cancellation confirms cleanup; history is bounded and not persistent.", objectSchema(map[string]any{"id": tokenSchema()}, "id"), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args jobArgs) (*mcp.CallToolResult, output, error) {
		if !validToken(args.ID) {
			return invalid("invalid job id")
		}
		return result(d.request(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(args.ID), "job", nil))
	})
	mcp.AddTool(s, tool("cancel_job", "Request cancellation of a daemon job. A returned cancelling status is still in progress; poll get_job. Repeated cancellation is safe.", objectSchema(map[string]any{"id": tokenSchema()}, "id"), false, true), func(ctx context.Context, _ *mcp.CallToolRequest, args jobArgs) (*mcp.CallToolResult, output, error) {
		if !validToken(args.ID) {
			return invalid("invalid job id")
		}
		return result(d.request(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(args.ID)+"/cancel", "job", nil))
	})
	return s
}

func submit(ctx context.Context, d *daemon, serial string, p program, policy string, dry bool) (*mcp.CallToolResult, output, error) {
	if !validToken(serial) {
		return invalid("invalid serial")
	}
	p, err := normalizeProgram(p)
	if err != nil {
		return invalid(err.Error())
	}
	policy, err = normalizePolicy(policy)
	if err != nil {
		return invalid(err.Error())
	}
	request := submission{Program: p, Policy: policy}
	if dry {
		return result(output{Kind: "preview", DryRun: true, Data: map[string]any{"serial": serial, "request": request}})
	}
	return result(d.request(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", "job", request))
}

func result(out output) (*mcp.CallToolResult, output, error) {
	return &mcp.CallToolResult{IsError: out.Error != nil}, out, nil
}
func invalid(message string) (*mcp.CallToolResult, output, error) {
	return result(errorOutput("invalid_request", message, 0, false))
}

func tool(name, description string, schema any, readOnly, idempotent bool) *mcp.Tool {
	closed, destructive := false, !readOnly
	return &mcp.Tool{Name: name, Description: description, InputSchema: schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: idempotent, OpenWorldHint: &closed, DestructiveHint: &destructive}}
}
func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func tokenSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"}
}
func colorSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^#[0-9a-fA-F]{6}$", "description": "Exact raw device RGB, without implicit gamma"}
}
func fadeSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 600000, "multipleOf": 10}
}
func holdSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": 3600000}
}
func policySchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"replace", "queue", "reject"}}
}
func programSchema() map[string]any {
	step := objectSchema(map[string]any{"led": map[string]any{"type": "integer", "enum": []int{0, 1, 2}}, "color": colorSchema(), "fade_ms": fadeSchema(), "hold_ms": holdSchema()}, "color")
	return objectSchema(map[string]any{"steps": map[string]any{"type": "array", "minItems": 1, "maxItems": 1024, "items": step}, "repeat": map[string]any{"type": "integer", "minimum": 0, "maximum": 10000, "description": "Omit or use zero to run once; zero does not mean infinite"}, "on_finish": map[string]any{"type": "string", "enum": []string{"off", "hold"}}}, "steps")
}
