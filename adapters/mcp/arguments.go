package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxArgumentBytes = 1 << 20
const maxArgumentDepth = 64

// rawArgumentsGuard runs before AddTool's typed/schema wrapper. That wrapper
// decodes arguments into a map before validation, which would erase duplicate
// keys and allow a later value to replace an earlier device/LED selector.
// The SDK still owns protocol decoding, schema validation and tool dispatch.
func rawArgumentsGuard(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, request)
		}
		params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
		if !ok || params == nil {
			return next(ctx, method, request)
		}
		switch params.Name {
		case "list_devices", "get_device_state", "set_device_state", "run_program", "preview_program", "resolve_color", "get_job", "cancel_job":
		default:
			return next(ctx, method, request)
		}
		err := validateRawArguments(params.Arguments)
		if params.Name == "resolve_color" && len(params.Arguments) > maxColorRequestBytes {
			err = fmt.Errorf("color arguments exceed 4096 bytes")
		}
		if err != nil {
			out := errorOutput("invalid_request", "tool arguments: "+err.Error(), 0, false)
			encoded, _ := json.Marshal(out)
			return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
		}
		return next(ctx, method, request)
	}
}

// A missing arguments member is allowed by MCP and behaves as an empty object.
// An explicit null is different and is rejected, including nested null values.
func validateRawArguments(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > maxArgumentBytes {
		return fmt.Errorf("arguments exceed 1 MiB")
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("explicit null is not accepted; omit optional fields")
	}
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("one JSON object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := argumentValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("one JSON object is required; trailing data is not allowed")
	}
	return nil
}

func argumentValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("explicit null is not accepted; omit optional fields")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if depth >= maxArgumentDepth {
		return fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := argumentValue(decoder, depth+1); err != nil {
				return fmt.Errorf("field %s: %w", key, err)
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := argumentValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}
