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
type listArgs struct {
	PrepareRequest      bool `json:"prepare_request,omitempty"`
	IncludeCapabilities bool `json:"include_capabilities,omitempty"`
}
type jobArgs struct {
	ID string `json:"id"`
}
type getJobArgs struct {
	ID                       string `json:"id,omitempty"`
	Serial                   string `json:"serial,omitempty"`
	RequestID                string `json:"request_id,omitempty"`
	ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id,omitempty"`
}
type setArgs struct {
	Serial                   string `json:"serial"`
	Color                    string `json:"color"`
	LED                      int    `json:"led,omitempty"`
	FadeMS                   int    `json:"fade_ms,omitempty"`
	HoldMS                   int    `json:"hold_ms,omitempty"`
	Policy                   string `json:"policy,omitempty"`
	DryRun                   bool   `json:"dry_run,omitempty"`
	RequestID                string `json:"request_id,omitempty"`
	ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id,omitempty"`
}
type runArgs struct {
	Serial                   string   `json:"serial"`
	Program                  *program `json:"program,omitempty"`
	Effect                   *effect  `json:"effect,omitempty"`
	Policy                   string   `json:"policy,omitempty"`
	DryRun                   bool     `json:"dry_run,omitempty"`
	RequestID                string   `json:"request_id,omitempty"`
	ExpectedDaemonInstanceID string   `json:"expected_daemon_instance_id,omitempty"`
}

func newServer(d *daemon) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "nb1-local-device-adapter", Version: "0.1.0-dev"}, &mcp.ServerOptions{Instructions: "Use only devices selected by the local daemon. RGB values are exact raw #RRGGBB, without implicit gamma. Job acceptance is not completion: inspect get_job and its actual status. cancel_job may return cancelling until hardware cleanup succeeds. HTTP timeout or closing this MCP session does not cancel accepted daemon jobs. This adapter never opens USB or silently falls back to direct control. Optional request tickets bind one intent to its original daemon instance; save the complete ticket before submission and keep it when an outcome is unknown. The adapter never automatically retries writes or replaces old tickets after expiry or restart. The existing dry_run option sends no HTTP request and cannot verify instance identity or deadlines: raw Programs are validated locally, while Effects receive only structure_only, compiled:false checks. Use preview_program for authoritative daemon compilation of either Program or Effect without creating a Job. Only the daemon compiles Effects and validates their total duration. Use resolve_color for read-only color notation conversion, then use its canonical_color in existing Program/Effect/set; those fields still require raw #RRGGBB. Color resolution does not execute a Job or authenticate the source. Use preview_light for versioned light actions and run_light only with a complete original ticket. run_light performs one read-only target preview then at most one execution POST; it never prepares or replaces tickets. New light pulse is attack/decay/rest while legacy effect pulse remains unchanged."})
	s.AddReceivingMiddleware(rawArgumentsGuard)
	mcp.AddTool(s, tool("list_devices", "List configured daemon devices. Optional include_capabilities returns data {devices,capabilities} with the daemon's software compiler catalog; default data remains a device array. Optional prepare_request independently returns a fresh request_ticket bound to this daemon instance, without submitting or reserving a job; save it before submission. Repeated preparation creates different tickets.", objectSchema(map[string]any{"prepare_request": map[string]any{"type": "boolean"}, "include_capabilities": map[string]any{"type": "boolean"}}), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, output, error) {
		return result(listDevicesWithCapabilities(ctx, d, args.PrepareRequest, args.IncludeCapabilities))
	})
	mcp.AddTool(s, tool("get_device_state", "Get the daemon's latest observed raw RGB and timestamp; this is a snapshot, not a camera measurement.", objectSchema(map[string]any{"serial": tokenSchema()}, "serial"), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args deviceArgs) (*mcp.CallToolResult, output, error) {
		if !validToken(args.Serial) {
			return invalid("invalid serial")
		}
		return result(d.request(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(args.Serial), "device", nil))
	})
	setProperties := map[string]any{"serial": tokenSchema(), "color": colorSchema(), "led": map[string]any{"type": "integer", "enum": []int{0, 1, 2}, "description": "0=both, 1=top, 2=bottom"}, "fade_ms": fadeSchema(), "hold_ms": holdSchema(), "policy": policySchema(), "dry_run": map[string]any{"type": "boolean", "description": "Validate and return the program without contacting the daemon"}}
	mcp.AddTool(s, tool("set_device_state", "Submit one exact RGB state; successful completion retains the color. Optional request_id and expected_daemon_instance_id must be supplied together and are never regenerated or retried automatically. Returns a job, not completion. dry_run only checks local syntax; it does not verify instance binding.", submissionSchema(setProperties, "serial", "color"), false, false), func(ctx context.Context, _ *mcp.CallToolRequest, args setArgs) (*mcp.CallToolResult, output, error) {
		return submit(ctx, d, args.Serial, program{Steps: []step{{LED: args.LED, Color: args.Color, FadeMS: args.FadeMS, HoldMS: args.HoldMS}}, Repeat: 1, OnFinish: "hold"}, args.Policy, args.DryRun, args.RequestID, args.ExpectedDaemonInstanceID)
	})
	mcp.AddTool(s, tool("run_program", "Submit exactly one raw program or high-level effect intent (steady/pulse). The daemon alone compiles effects and validates total duration. Effect dry_run is structure_only and compiled:false, with no HTTP; it cannot confirm compilation or instance binding. replace cancels old work, queue is bounded, reject reports busy. Optional request_id and expected_daemon_instance_id must be supplied together; no automatic submission retry or rebinding.", runSchema(), false, false), func(ctx context.Context, _ *mcp.CallToolRequest, args runArgs) (*mcp.CallToolResult, output, error) {
		return run(ctx, d, args)
	})
	mcp.AddTool(s, tool("preview_program", "Ask the daemon to compile exactly one program or steady/pulse effect without creating a job, receipt or USB access. Optional serial checks only cached device capabilities, not execution or admission. Returns authoritative program_preview with accepted:false. Unlike existing dry_run, this sends one read-only HTTP POST; no retry or direct fallback, and errors do not imply an unknown job outcome.", previewSchema(), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args previewArgs) (*mcp.CallToolResult, output, error) {
		return preview(ctx, d, args)
	})
	colorTool := tool("resolve_color", "Resolve one color notation through the daemon without a Job, USB access, Gamma or brightness correction. Accept a string (#RRGGBB/#RGB or fixed CSS name), closed rgb8 object, or closed hsv object. RGB8 channels are integers 0–255; HSV uses degrees 0–360 and percentages 0–100, plain decimal with at most six fractional digits, no exponent or negative zero. Returns color_resolution, accepted:false. The daemon validates numeric lexemes and owns CSS names/HSV conversion; use canonical_color in existing Program/Effect/set. Unknown route/errors never fall back or retry and do not imply an unknown Job outcome.", resolveColorSchema(), true, true)
	colorTool.OutputSchema = resolveColorOutputSchema()
	mcp.AddTool(s, colorTool, func(ctx context.Context, request *mcp.CallToolRequest, _ resolveColorArgs) (*mcp.CallToolResult, output, error) {
		// The SDK validates the union schema through a map and re-marshals it.
		// Decode its untouched original request, not typed normalized numbers.
		return resolveColor(ctx, d, request.Params.Arguments)
	})
	lightPreviewTool := tool("preview_light", "Compile one versioned light action through one read-only daemon POST, without a Job or ticket. set/off/fade and artistic blink/breathe/pulse/alternate/rainbow share contract nb1-light-action/v1. breathe is smooth symmetric cosine16; the new pulse uses attack/decay/rest and does not change legacy run_program.effect.pulse. Optional serial checks cached capabilities only. Colors accept the resolve_color grammar. Returns light_preview, accepted:false; no local effect compilation, retry or direct fallback.", lightPreviewSchema(), true, true)
	lightPreviewTool.OutputSchema = lightPreviewOutputSchema()
	mcp.AddTool(s, lightPreviewTool, func(ctx context.Context, request *mcp.CallToolRequest, _ lightPreviewArgs) (*mcp.CallToolResult, output, error) {
		return previewLight(ctx, d, request.Params.Arguments)
	})
	mcp.AddTool(s, tool("run_light", "Run one versioned light action with a complete original request ticket and reject policy only. First performs one read-only target preview, then at most one execution POST and checks the returned Job against that preview. Never prepares/replaces tickets, retries a write or falls back to legacy/direct control. Job acceptance is not completion; use get_job. No dry_run: use preview_light. New pulse is attack/decay/rest; legacy run_program.effect.pulse is unchanged.", lightRunSchema(), false, false), func(ctx context.Context, request *mcp.CallToolRequest, _ lightRunArgs) (*mcp.CallToolResult, output, error) {
		return runLight(ctx, d, request.Params.Arguments)
	})
	mcp.AddTool(s, tool("get_job", "Inspect actual job status using id, or the mutually exclusive serial plus complete original request ticket. Receipt lookup never submits work. Expired receipts or changed daemon instances leave the old outcome unknown; keep the original ticket. History is bounded and not persistent.", getJobSchema(), true, true), func(ctx context.Context, _ *mcp.CallToolRequest, args getJobArgs) (*mcp.CallToolResult, output, error) {
		return result(getJob(ctx, d, args))
	})
	mcp.AddTool(s, tool("cancel_job", "Request cancellation of a daemon job. A returned cancelling status is still in progress; poll get_job. Repeated cancellation is safe.", objectSchema(map[string]any{"id": tokenSchema()}, "id"), false, true), func(ctx context.Context, _ *mcp.CallToolRequest, args jobArgs) (*mcp.CallToolResult, output, error) {
		if !validToken(args.ID) {
			return invalid("invalid job id")
		}
		return result(d.request(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(args.ID)+"/cancel", "job", nil))
	})
	return s
}

func submit(ctx context.Context, d *daemon, serial string, p program, policy string, dry bool, requestID, instanceID string) (*mcp.CallToolResult, output, error) {
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
	ticket, err := optionalTicket(requestID, instanceID)
	if err != nil {
		return invalid(err.Error())
	}
	request := submission{Program: p, Policy: policy, RequestID: requestID, ExpectedDaemonInstanceID: instanceID}
	if dry {
		out := output{Kind: "preview", DryRun: true, Data: map[string]any{"serial": serial, "request": request}, RequestTicket: ticket}
		if ticket != nil {
			verified := false
			out.InstanceBindingVerified = &verified
		}
		return result(out)
	}
	return result(d.requestJob(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", request, serial, ticket))
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
