package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type nativeReadArgs struct {
	Serial    string
	StartLine int
	LineCount int
}
type nativeToolError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}
type nativeInspectionOutput struct {
	Kind     string           `json:"kind"`
	Accepted bool             `json:"accepted"`
	Data     json.RawMessage  `json:"data,omitempty"`
	Error    *nativeToolError `json:"error,omitempty"`
}

func isNativeInspectionTool(name string) bool {
	return name == "read_device_sequence_ram" || name == "read_device_playback_state"
}
func nativeToolKind(name string) string {
	if name == "read_device_sequence_ram" {
		return "ram"
	}
	return "playback"
}
func nativeSerial(value string) bool {
	if len(value) != 8 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func readNativeInspectionArgs(raw json.RawMessage, kind string) (nativeReadArgs, error) {
	var out nativeReadArgs
	if len(raw) == 0 || len(raw) > maxNativeInspectionArguments || !utf8.Valid(raw) {
		return out, fmt.Errorf("one UTF-8 argument object of at most4096 bytes is required")
	}
	if err := validateRawArguments(raw); err != nil {
		return out, err
	}
	if err := patternAuthorCheckStrings(raw); err != nil {
		return out, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, err
	}
	want := 1
	if kind == "ram" {
		want = 3
	} else if kind != "playback" {
		return out, fmt.Errorf("unknown native inspection kind")
	}
	if len(fields) != want || fields["serial"] == nil {
		return out, fmt.Errorf("exact explicit serial and required range fields are required")
	}
	if err := json.Unmarshal(fields["serial"], &out.Serial); err != nil || !nativeSerial(out.Serial) {
		return out, fmt.Errorf("serial must be exactly8 hexadecimal digits")
	}
	if kind == "playback" {
		return out, nil
	}
	parse := func(name string) (int, error) {
		text := string(bytes.TrimSpace(fields[name]))
		if !nativeIntegerLexeme(text) {
			return 0, fmt.Errorf("%s must be an ordinary integer", name)
		}
		n, err := strconv.ParseInt(text, 10, 32)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s is out of range", name)
		}
		return int(n), nil
	}
	var err error
	if out.StartLine, err = parse("start_line"); err != nil {
		return out, err
	}
	if out.LineCount, err = parse("line_count"); err != nil {
		return out, err
	}
	if out.StartLine > 31 || out.LineCount < 1 || out.LineCount > 32-out.StartLine {
		return out, fmt.Errorf("RAM range must select1..32 lines entirely within0..31")
	}
	return out, nil
}

func nativeInspectionResult(out nativeInspectionOutput) *mcp.CallToolResult {
	encoded, _ := json.Marshal(out)
	return &mcp.CallToolResult{IsError: out.Error != nil, StructuredContent: json.RawMessage(encoded),
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
}
func nativeInspectionProblem(code, message string, status int) *mcp.CallToolResult {
	message = strings.ToValidUTF8(message, "\ufffd")
	if len(message) > 4096 {
		message = message[:4096]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return nativeInspectionResult(nativeInspectionOutput{Kind: "error", Error: &nativeToolError{Code: code, Message: message, HTTPStatus: status}})
}

func registerNativeInspectionTools(s *mcp.Server, d *daemon) {
	for _, name := range []string{"read_device_sequence_ram", "read_device_playback_state"} {
		name, kind := name, nativeToolKind(name)
		description := "Read one experimental native playback-register observation through exactly one daemon GET. Preserve raw mode/count/next scheduling cursor and partial errors; next_position is not the currently lit row, count0 is not a forever promise, and scheduling stop is not optical completion. No Job, ticket, retry, USB fallback, stop, replay, save or Flash operation. Read-only refers to device state; the daemon may send an S query. Returns accepted:false and retains the complete raw envelope even on partial/failed observations. Requires a daemon supporting the experimental native inspection route."
		if kind == "ram" {
			description = "Read an explicit bounded RAM-row range through exactly one experimental daemon GET. Required start_line0..31 and line_count1..32 must fit within0..31. Returns raw RGB/ticks/LED codes, exact available hex prefixes, untrusted-length diagnostics, nullable fields and errors. R reads RAM, not Flash; the snapshot is sequential and not atomic. A derived sequence is optional and never reconstructed by this adapter. No Job, ticket, retry, direct USB fallback, stop, playback, import or save. Read-only refers to device state; the daemon may send R queries. accepted:false; partial/failed data remains available with IsError."
		}
		t := tool(name, description, nativeInspectionInputSchema(kind), true, true)
		t.OutputSchema = nativeInspectionOutputSchema(kind)
		// The official SDK's low-level handler preserves our validated RawMessage
		// output. Generic AddTool's schema/default round-trip can use float64 maps.
		s.AddTool(t, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := readNativeInspectionArgs(request.Params.Arguments, kind)
			if err != nil {
				return nativeInspectionProblem("invalid_request", err.Error(), 0), nil
			}
			return d.readNativeInspection(ctx, args, kind), nil
		})
	}
}

func (d *daemon) readNativeInspection(ctx context.Context, args nativeReadArgs, kind string) *mcp.CallToolResult {
	if err := ctx.Err(); err != nil {
		return nativeInspectionProblem("context_cancelled", err.Error(), 0)
	}
	path := "/v1/devices/" + url.PathEscape(args.Serial) + "/native-sequence/" + kind
	if kind == "ram" {
		q := url.Values{}
		q.Set("start_line", strconv.Itoa(args.StartLine))
		q.Set("line_count", strconv.Itoa(args.LineCount))
		path += "?" + q.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+path, nil)
	if err != nil {
		return nativeInspectionProblem("invalid_request", err.Error(), 0)
	}
	request.Header.Set("Accept", "application/json")
	request.Close = true
	if d.client == nil {
		return nativeInspectionProblem("adapter_transport", "a one-shot HTTP transport is required", 0)
	}
	base, ok := d.client.Transport.(*http.Transport)
	if !ok {
		return nativeInspectionProblem("adapter_transport", "native inspection requires a cloneable one-shot HTTP transport", 0)
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: d.client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nativeInspectionProblem("daemon_unreachable", "contact daemon: "+err.Error(), 0)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxNativeInspectionResponse+1))
	if err != nil || len(body) > maxNativeInspectionResponse {
		return nativeInspectionProblem("invalid_daemon_response", "native inspection body is incomplete or exceeds64KiB", response.StatusCode)
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return nativeInspectionProblem("invalid_daemon_response", "one application/json Content-Type is required", response.StatusCode)
	}
	contentType, _, mediaErr := mime.ParseMediaType(media[0])
	if mediaErr != nil || contentType != "application/json" {
		return nativeInspectionProblem("invalid_daemon_response", "native inspection requires application/json", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= 200 && response.StatusCode < 400 {
			return nativeInspectionProblem("invalid_daemon_response", "native inspection requires HTTP200; redirects and Job responses are not followed", response.StatusCode)
		}
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := decodeNativeWire(body, &failure); err != nil || !nativeASCII(failure.Error.Code, 64) || len(failure.Error.Message) > 4096 {
			return nativeInspectionProblem("invalid_daemon_response", "invalid native inspection API error", response.StatusCode)
		}
		return nativeInspectionProblem(failure.Error.Code, failure.Error.Message, response.StatusCode)
	}
	value, err := decodeNativeInspectionResponse(body, args.Serial, kind, args.StartLine, args.LineCount)
	if err != nil {
		return nativeInspectionProblem("invalid_daemon_response", err.Error(), response.StatusCode)
	}
	instance := response.Header.Values("X-NB1-Daemon-Instance-ID")
	if len(instance) != 1 || instance[0] != value.DaemonInstanceID || len(response.Header.Values("Location")) != 0 || len(response.Header.Values("X-NB1-Replayed")) != 0 {
		return nativeInspectionProblem("invalid_daemon_response", "native inspection did not confirm its instance or contains Job metadata", response.StatusCode)
	}
	outKind := "native_sequence_ram"
	if kind == "playback" {
		outKind = "native_playback_state"
	}
	out := nativeInspectionOutput{Kind: outKind, Data: append(json.RawMessage(nil), body...)}
	if !value.Complete {
		out.Error = &nativeToolError{Code: "native_inspection_incomplete", Message: "Native observation is " + value.Status + "; partial data and per-read errors are retained.", HTTPStatus: response.StatusCode}
	}
	return nativeInspectionResult(out)
}

func nativeInspectionInputSchema(kind string) map[string]any {
	properties := map[string]any{"serial": map[string]any{"type": "string", "minLength": 8, "maxLength": 8, "pattern": "^[0-9A-Fa-f]{8}$", "description": "Explicit device serial selected by the daemon; no discovery or first-device fallback."}}
	if kind == "playback" {
		return objectSchema(properties, "serial")
	}
	properties["start_line"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 31, "description": "Absolute RAM address; required ordinary integer without fractions or exponent."}
	properties["line_count"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 32, "description": "Explicit count; start_line+line_count must not exceed32."}
	return objectSchema(properties, "serial", "start_line", "line_count")
}

func nativeWireSchema(typ reflect.Type) map[string]any {
	if typ == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{"oneOf": []any{map[string]any{"type": "string", "const": "all"}, map[string]any{"type": "integer", "enum": []int{1, 2}}}}
	}
	if typ.Kind() == reflect.Ptr {
		return map[string]any{"anyOf": []any{map[string]any{"type": "null"}, nativeWireSchema(typ.Elem())}}
	}
	switch typ.Kind() {
	case reflect.Struct:
		properties, required := map[string]any{}, []string{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			properties[name] = nativeWireSchema(f.Type)
			switch name {
			case "native_code":
				properties[name] = map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]{0,19})$", "maxLength": 20}}, "description": "Exact unsigned64-bit decimal string, including18446744073709551615; never a floating-point number. Null means unavailable."}
			case "raw_hex", "unknown_tail_hex":
				properties[name] = map[string]any{"type": "string", "pattern": "^([0-9a-f]{2})*$", "maxLength": 512, "description": "Exact available lowercase hexadecimal bytes; no zero padding or native wire-capture claim."}
			case "layout":
				properties[name] = map[string]any{"enum": []string{nativeInspectionLayout, "unknown"}}
			case "prefix_trusted":
				properties[name] = map[string]any{"type": "boolean", "description": "False preserves original length diagnostics while exposing no trusted bytes or parsed fields."}
			case "next_position":
				properties[name].(map[string]any)["description"] = "Raw next scheduling cursor; never infer the currently illuminated row or completion."
			case "count":
				properties[name].(map[string]any)["description"] = "Raw firmware count; zero is not a universal forever promise."
			case "mode":
				properties[name].(map[string]any)["description"] = "Raw mode byte, including0..3 and unknown values; never reduced to a playing boolean."
			case "deployable":
				properties[name] = map[string]any{"const": false}
			case "operation_qualification":
				properties[name] = map[string]any{"const": "unverified"}
			case "playback_end_encoding":
				properties[name] = map[string]any{"enum": []string{"exclusive", "inclusive", "unknown"}, "description": "Explicit server reference convention; do not apply the p command's end0 sentinel to S readback."}
			case "ram_lines":
				properties[name] = map[string]any{"type": "integer", "minimum": 1, "maximum": 32}
			}
			required = append(required, name)
		}
		return objectSchema(properties, required...)
	case reflect.Array:
		return map[string]any{"type": "array", "minItems": typ.Len(), "maxItems": typ.Len(), "items": nativeWireSchema(typ.Elem())}
	case reflect.Slice:
		return map[string]any{"type": "array", "maxItems": 128, "items": nativeWireSchema(typ.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Uint8:
		return map[string]any{"type": "integer", "minimum": 0, "maximum": 255}
	case reflect.Uint16:
		return map[string]any{"type": "integer", "minimum": 0, "maximum": 65535}
	default:
		return map[string]any{"type": "integer"}
	}
}
func nativeInspectionOutputSchema(kind string) map[string]any {
	data := nativeWireSchema(reflect.TypeOf(nativeInspectionWire{}))
	properties := data["properties"].(map[string]any)
	properties["contract"] = map[string]any{"const": nativeInspectionContract}
	properties["kind"] = map[string]any{"const": kind}
	properties["source"] = map[string]any{"const": "live-native"}
	properties["consistency"] = map[string]any{"const": "sequential-best-effort-not-atomic"}
	properties["status"] = map[string]any{"enum": []string{"complete", "partial", "failed"}}
	properties["duration_ns"] = map[string]any{"type": "integer", "minimum": 0, "description": "Monotonic duration; keep exact integer. Wall timestamps need not be monotonic."}
	if kind == "ram" {
		properties["ram"] = nativeWireSchema(reflect.TypeOf(nativeRAMWire{}))
		properties["playback"] = map[string]any{"type": "null"}
	} else {
		properties["ram"] = map[string]any{"type": "null"}
		properties["playback"] = nativeWireSchema(reflect.TypeOf(nativePlaybackWire{}))
	}
	outKind := "native_sequence_ram"
	if kind == "playback" {
		outKind = "native_playback_state"
	}
	problem := objectSchema(map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "http_status": map[string]any{"type": "integer"}}, "code", "message")
	response := objectSchema(map[string]any{"kind": map[string]any{"enum": []string{outKind, "error"}}, "accepted": map[string]any{"const": false}, "data": data, "error": problem}, "kind", "accepted")
	response["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": map[string]any{"const": outKind}}, "required": []string{"data"}}, map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "error"}}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}}}
	return response
}
