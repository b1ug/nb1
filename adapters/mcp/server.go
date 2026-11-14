package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolError struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

type output struct {
	Endpoint string     `json:"endpoint"`
	ID       string     `json:"id,omitempty"`
	Name     string     `json:"name,omitempty"`
	Data     any        `json:"data,omitempty"`
	Error    *toolError `json:"error,omitempty"`
}

type toolArgs struct {
	Serial    string          `json:"serial,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Title     string          `json:"title,omitempty"`
	Color     json.RawMessage `json:"color,omitempty"`
	Action    json.RawMessage `json:"action,omitempty"`
	Sequence  json.RawMessage `json:"sequence,omitempty"`
	PatternID *string         `json:"pattern_id,omitempty"`
	input     daemonclient.SequenceInput
}

func newServer(d *daemon) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "nb1", Version: "1.0.0-dev"}, &mcp.ServerOptions{Instructions: "Control the devices selected by the local nb1 daemon. Preview an Action, Sequence or saved pattern_id before running it. run_light submits once and returns a Job, whose status is read with get_job. A cancelling Job is still cleaning up. Keep its ID and endpoint when a response is lost and query that ID without submitting it again. HTTP timeout or MCP session closure leaves accepted daemon Jobs running. Active Job records remain retained; terminal records default to a thirty-minute TTL and 16,384-entry LRU limit, configurable through jobs.terminal_ttl and jobs.terminal_limit. Capacity eviction or daemon restart may remove them earlier. Colors and Patterns are saved in the daemon's computer-side library. This adapter connects over HTTP, selects its endpoint with --endpoint (default http://127.0.0.1:8931), does not read nb1 YAML, and never opens USB or executes the CLI. Foreground CLI direct Jobs are separate process-local work and cannot be queried through these HTTP tools."})
	for _, spec := range toolSpecifications() {
		name := spec.Name
		server.AddTool(spec, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := decodeArguments(name, request.Params.Arguments)
			if err != nil {
				return toolResult(output{Endpoint: d.endpoint, Error: &toolError{Code: "invalid_request", Message: err.Error()}})
			}
			ctx, cancel := context.WithTimeout(ctx, d.timeout)
			defer cancel()
			return toolResult(invoke(ctx, d, name, args))
		})
	}
	return server
}

func decodeArguments(name string, raw json.RawMessage) (toolArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > sequence.MaxInputBytes || !jsoninput.ValidUnicode(raw) {
		return toolArgs{}, fmt.Errorf("arguments must contain valid Unicode and be at most 1 MiB")
	}
	var fields map[string]json.RawMessage
	if err := jsoninput.DecodeObject(raw, &fields); err != nil {
		return toolArgs{}, err
	}
	allowed, required := argumentFields(name)
	for field := range fields {
		if !contains(allowed, field) {
			return toolArgs{}, fmt.Errorf("unknown field %q", field)
		}
	}
	for _, field := range required {
		if fields[field] == nil {
			return toolArgs{}, fmt.Errorf("%s is required", field)
		}
	}
	var args toolArgs
	if err := jsoninput.DecodeObject(raw, &args); err != nil {
		return toolArgs{}, err
	}
	if contains(required, "serial") || fields["serial"] != nil {
		if !control.ValidDeviceSerial(args.Serial) {
			return toolArgs{}, fmt.Errorf("invalid device serial")
		}
	}
	if contains(required, "id") || args.ID != "" {
		if !control.ValidSequenceJobID(args.ID) {
			return toolArgs{}, fmt.Errorf("id must contain 1–64 ASCII letters, digits, hyphens or underscores")
		}
	}
	if name == "save_color" && !daemonclient.ValidSequenceColorName(args.Name) {
		return toolArgs{}, fmt.Errorf("invalid color name")
	}
	if name == "preview_light" || name == "run_light" || name == "save_pattern" {
		input, err := inputFor(args)
		if err != nil {
			return toolArgs{}, err
		}
		if name == "save_pattern" && input.PatternID != nil {
			return toolArgs{}, fmt.Errorf("saving a Pattern requires an Action or Sequence")
		}
		args.input = input
	}
	return args, nil
}

func inputFor(args toolArgs) (daemonclient.SequenceInput, error) {
	raw, err := json.Marshal(daemonclient.SequenceInput{Action: args.Action, Sequence: args.Sequence, PatternID: args.PatternID})
	if err != nil {
		return daemonclient.SequenceInput{}, err
	}
	return daemonclient.DecodeSequenceInput(raw)
}

func invoke(ctx context.Context, d *daemon, name string, args toolArgs) output {
	out := output{Endpoint: d.endpoint, ID: args.ID, Name: args.Name}
	var err error
	switch name {
	case "list_devices":
		out.Data, err = d.core.Devices(ctx)
	case "get_device_state":
		out.Data, err = d.core.Device(ctx, args.Serial)
	case "resolve_color":
		out.Data, err = d.resolveColor(ctx, args.Color)
	case "preview_light":
		out.Data, err = d.core.PreviewSequence(ctx, args.input, args.Serial)
	case "run_light":
		if out.ID == "" {
			out.ID, err = control.NewSequenceJobID()
			if err != nil {
				break
			}
		}
		out.Data, err = d.core.SubmitSequence(ctx, args.input, args.Serial, out.ID)
	case "get_job":
		out.Data, err = d.core.SequenceJob(ctx, args.ID)
	case "cancel_job":
		out.Data, err = d.core.CancelSequenceJob(ctx, args.ID)
	case "list_colors":
		out.Data, err = d.core.SequenceColors(ctx)
	case "save_color":
		out.Data, err = d.core.SaveSequenceColor(ctx, args.Name, args.Color)
	case "list_patterns":
		out.Data, err = d.core.SequencePatterns(ctx)
	case "get_pattern":
		out.Data, err = d.core.SequencePattern(ctx, args.ID)
	case "save_pattern":
		out.Data, err = d.core.SaveSequencePattern(ctx, args.ID, args.input, args.Title)
	}
	if err == nil {
		return out
	}
	out.Data = nil
	out.Error = &toolError{Code: "invalid_request", Message: err.Error()}
	var rejected *daemonclient.Error
	if errors.As(err, &rejected) {
		out.Error.Code, out.Error.HTTPStatus = rejected.Code, rejected.Status
	}
	var malformed *daemonclient.DecodeError
	if errors.As(err, &malformed) {
		out.Error.Code = "invalid_daemon_response"
		out.Error.HTTPStatus = malformed.Status
	}
	var transport *url.Error
	if errors.As(err, &transport) {
		out.Error.Code = "daemon_unreachable"
	}
	if name == "run_light" || name == "cancel_job" || name == "save_color" || name == "save_pattern" {
		out.Error.OutcomeUnknown = uncertainWrite(err)
	}
	if out.Error.OutcomeUnknown && (name == "run_light" || name == "cancel_job") {
		out.Error.Message += fmt.Sprintf("; query get_job with id %q on endpoint %s", out.ID, out.Endpoint)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		out.Error.Code = "request_timeout"
	}
	return out
}

func toolResult(out output) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{IsError: out.Error != nil, StructuredContent: json.RawMessage(raw), Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
