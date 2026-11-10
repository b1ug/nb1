package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type patternAuthorError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}
type patternAuthorReadOutput struct {
	Kind     string              `json:"kind"`
	Accepted bool                `json:"accepted"`
	Data     any                 `json:"data,omitempty"`
	Error    *patternAuthorError `json:"error,omitempty"`
}
type patternAuthorWriteResult struct {
	SchemaVersion   int                  `json:"schema_version"`
	Contract        string               `json:"contract"`
	RecordJSON      string               `json:"record_json"`
	Verification    patternAuthorSupport `json:"verification"`
	ExecutionDigest string               `json:"execution_digest"`
	Created         []string             `json:"created"`
	Existing        []string             `json:"existing"`
	Total           int                  `json:"total"`
}

// Publication is deliberately a new output type. Existing sixteen schemas and
// their execution OutcomeUnknown/Accepted fields are not extended or reused.
type patternAuthorSaveOutput struct {
	Kind                 string                    `json:"kind"`
	Accepted             bool                      `json:"accepted"`
	PublicationAttempted bool                      `json:"publication_attempted"`
	PublicationConfirmed bool                      `json:"publication_confirmed"`
	PublicationUnknown   bool                      `json:"publication_unknown"`
	PatternRef           *patternV2Ref             `json:"pattern_ref,omitempty"`
	OriginalAuthorJSON   string                    `json:"original_author_json,omitempty"`
	Result               *patternAuthorWriteResult `json:"result,omitempty"`
	Error                *patternAuthorError       `json:"error,omitempty"`
}
type patternAuthorRawKey struct{}

const patternAuthorInstructions = " Experimental high-level library authoring uses list_patterns_v2/get_pattern_v2/preview_pattern_draft_v2/save_pattern_v2, separate from legacy v1 list_patterns/get_pattern. Preview original source, then explicitly save that original source with expected_revision. Saving never accepts or runs a device Job: inspect independent publication_confirmed/publication_unknown and preserve original_author_json plus the original Ref on uncertainty. Use an explicit same-ID/revision GET to recover, never automatically resave. Complete records use canonical record_json strings so generic SDK/JavaScript maps cannot round large source numbers. Source-to-Compiled semantic verification is the daemon's responsibility; the adapter independently checks record/execution integrity without an action compiler. Software v2 is unrelated to hardware mk2/mk3."

func isPatternAuthorTool(name string) bool {
	switch name {
	case "list_patterns_v2", "get_pattern_v2", "preview_pattern_draft_v2", "save_pattern_v2":
		return true
	}
	return false
}
func patternAuthorProblem(code, message string, status int) *patternAuthorError {
	if len(message) > 4096 {
		message = message[:4096]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += " [truncated]"
	}
	return &patternAuthorError{code, message, status}
}
func patternAuthorResult(value any, isError bool) *mcp.CallToolResult {
	encoded, _ := json.Marshal(value)
	return &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: json.RawMessage(encoded)}
}
func patternAuthorFits(value any, isError bool) bool {
	encoded, err := json.Marshal(patternAuthorResult(value, isError))
	return err == nil && len(encoded) <= maxPatternAuthorOutput
}
func patternAuthorReadResult(out patternAuthorReadOutput) (*mcp.CallToolResult, patternAuthorReadOutput, error) {
	if !patternAuthorFits(out, out.Error != nil) {
		out = patternAuthorReadOutput{Kind: "error", Error: patternAuthorProblem("adapter_output_limit", "bounded Pattern author result exceeds 4MiB", 0)}
	}
	return patternAuthorResult(out, out.Error != nil), out, nil
}
func patternAuthorSaveResult(out patternAuthorSaveOutput) (*mcp.CallToolResult, patternAuthorSaveOutput, error) {
	if !patternAuthorFits(out, out.Error != nil) {
		out.Result = nil
		out.PublicationConfirmed = false
		out.PublicationUnknown = out.PublicationAttempted
		out.Error = patternAuthorProblem("adapter_output_limit", "bounded publication result exceeds 4MiB; keep original intent and use an explicit GET", 0)
	}
	return patternAuthorResult(out, out.Error != nil), out, nil
}
func patternAuthorReadError(code, message string, status int) patternAuthorReadOutput {
	return patternAuthorReadOutput{Kind: "error", Error: patternAuthorProblem(code, message, status)}
}

// Separate middleware for the four new tools. Never feed their raw numbers
// through a generic float64 map, and do not relax any legacy tool validator.
func patternAuthorArgumentsGuard(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, request)
		}
		params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
		if !ok || params == nil || !isPatternAuthorTool(params.Name) {
			return next(ctx, method, request)
		}
		raw := params.Arguments
		if len(raw) == 0 && params.Name == "list_patterns_v2" {
			raw = json.RawMessage(`{}`)
		}
		limit := maxPatternAuthorArguments
		if params.Name == "list_patterns_v2" || params.Name == "get_pattern_v2" {
			limit = 4096
		}
		value, err := parsePatternAuthorValue(raw, limit, 32)
		if err == nil {
			if _, ok := value.(map[string]any); !ok {
				err = errors.New("one Pattern argument object required")
			}
		}
		if err == nil {
			err = patternAuthorValidateArguments(params.Name, raw)
		}
		if err != nil {
			problem := patternAuthorProblem("invalid_request", err.Error(), 0)
			if params.Name == "save_pattern_v2" {
				return patternAuthorResult(patternAuthorSaveOutput{Kind: "pattern_publication_v2", Error: problem}, true), nil
			}
			return patternAuthorResult(patternAuthorReadOutput{Kind: "error", Error: problem}, true), nil
		}
		ctx = context.WithValue(ctx, patternAuthorRawKey{}, append(json.RawMessage(nil), raw...))
		return next(ctx, method, request)
	}
}
func patternAuthorValidateArguments(name string, raw json.RawMessage) error {
	switch name {
	case "preview_pattern_draft_v2", "save_pattern_v2":
		_, err := patternAuthorReadDraft(raw, name == "save_pattern_v2")
		return err
	case "list_patterns_v2":
		var empty struct{}
		return patternAuthorDecode(raw, &empty, 4096, 32)
	case "get_pattern_v2":
		var value struct {
			ID               string  `json:"id"`
			ExpectedRevision *string `json:"expected_revision,omitempty"`
		}
		if err := patternAuthorDecode(raw, &value, 4096, 32); err != nil {
			return err
		}
		if !patternID.MatchString(value.ID) || value.ExpectedRevision != nil && !patternRevision.MatchString(*value.ExpectedRevision) {
			return errors.New("exact bounded ID and optional revision required")
		}
	}
	return nil
}
func patternAuthorArguments(ctx context.Context, r *mcp.CallToolRequest) json.RawMessage {
	if raw, ok := ctx.Value(patternAuthorRawKey{}).(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...)
	}
	return append(json.RawMessage(nil), r.Params.Arguments...)
}
func registerPatternAuthorTools(s *mcp.Server, d *daemon) {
	addRead := func(name, description string, handler func(context.Context, json.RawMessage) patternAuthorReadOutput) {
		t := tool(name, description, patternAuthorInputSchema(name), true, true)
		t.OutputSchema = patternAuthorReadSchema(name)
		mcp.AddTool(s, t, func(ctx context.Context, r *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, patternAuthorReadOutput, error) {
			return patternAuthorReadResult(handler(ctx, patternAuthorArguments(ctx, r)))
		})
	}
	addRead("list_patterns_v2", "List experimental software-v2 high-level Pattern metadata through one GET. Unknown source/recipe rows stay read-only. No Record fanout, local path access, preview, ticket preparation, playback or USB. Server verification is advertised metadata, not independent source compilation; accepted:false.", d.patternAuthorList)
	addRead("get_pattern_v2", "Read one complete experimental-v2 Pattern by ID, optionally bound to expected_revision. Return canonical record_json as a lossless string, including Unicode values and exact int64 numbers; do not round-trip it through float64. Adapter checks record revision and typed execution digest, while source-to-Compiled semantics remain daemon-owned. Valid unknown sources/recipes remain read-only. No device work or write; accepted:false.", d.patternAuthorGet)
	addRead("preview_pattern_draft_v2", "Preview original {id,title,source} high-level author JSON through one daemon pure-compilation POST. Input <=128KiB/action<=64KiB; raw numeric lexemes and source notation are forwarded unchanged. Returns canonical record_json and candidate revision for an explicit separate save. No Store write, ticket, Job, USB or local action compiler; accepted:false.", d.patternAuthorPreview)
	t := tool("save_pattern_v2", "Conditionally create one experimental-v2 high-level Pattern using original {id,title,source,expected_revision}. At most one POST, no implicit preview, prepare, automatic retry or playback. Independent publication_confirmed/publication_unknown flags describe storage, never execution acceptance; accepted is always false. Keep the original Ref and author JSON before calling, even if this MCP session disappears; preserve returned original_author_json on uncertainty, then explicitly get the same ID/revision. A matching GET proves desired content exists now, not which historical POST published it. This software v2 is unrelated to hardware mk2/mk3.", patternAuthorInputSchema("save_pattern_v2"), false, false)
	destructive := false
	t.Annotations.DestructiveHint = &destructive
	t.OutputSchema = patternAuthorSaveSchema()
	mcp.AddTool(s, t, func(ctx context.Context, r *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, patternAuthorSaveOutput, error) {
		return patternAuthorSaveResult(d.patternAuthorSave(ctx, patternAuthorArguments(ctx, r)))
	})
}

type patternAuthorHTTPResult struct {
	body       []byte
	headers    http.Header
	status     int
	problem    *patternAuthorError
	trustedAPI bool
	attempted  bool
}

func (d *daemon) patternAuthorHTTP(ctx context.Context, method, path string, raw []byte, publication bool) patternAuthorHTTPResult {
	fail := func(code, message string, status int, attempted bool) patternAuthorHTTPResult {
		return patternAuthorHTTPResult{status: status, problem: patternAuthorProblem(code, message, status), attempted: attempted}
	}
	if err := ctx.Err(); err != nil {
		return fail("context_cancelled", err.Error(), 0, false)
	}
	var body io.Reader
	if raw != nil {
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.endpoint+path, body)
	if err != nil {
		return fail("invalid_request", err.Error(), 0, false)
	}
	req.Header.Set("Accept", "application/json")
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
		req.GetBody = nil
	}
	response, err := d.client.Do(req)
	if err != nil {
		return fail("daemon_unreachable", err.Error(), 0, true)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxPatternAuthorResponse+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > maxPatternAuthorResponse {
		return fail("invalid_daemon_response", "Pattern response unreadable, close failed, or exceeds 2MiB", response.StatusCode, true)
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return fail("invalid_daemon_response", "exactly one application/json Content-Type required", response.StatusCode, true)
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" {
		return fail("invalid_daemon_response", "application/json Content-Type required", response.StatusCode, true)
	}
	if _, err = parsePatternAuthorValue(data, maxPatternAuthorResponse, 64); err != nil {
		return fail("invalid_daemon_response", err.Error(), response.StatusCode, true)
	}
	forbidden := []string{"X-NB1-Daemon-Instance-ID", "X-NB1-Replayed"}
	if !publication || response.StatusCode < 200 || response.StatusCode >= 300 {
		forbidden = append(forbidden, "Location")
	}
	for _, name := range forbidden {
		if len(response.Header.Values(name)) != 0 {
			return fail("invalid_daemon_response", "author response contains contradictory execution/publication metadata", response.StatusCode, true)
		}
	}
	result := patternAuthorHTTPResult{body: data, headers: response.Header, status: response.StatusCode, attempted: true}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var value struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = patternAuthorDecode(data, &value, maxPatternAuthorResponse, 64); err != nil || value.Error.Code == "" || value.Error.Message == "" {
			return fail("invalid_daemon_response", "untrusted Pattern error response", response.StatusCode, true)
		}
		result.problem = patternAuthorProblem(value.Error.Code, value.Error.Message, response.StatusCode)
		result.trustedAPI = true
	}
	return result
}
func patternAuthorKnownRejection(value patternAuthorHTTPResult) bool {
	if !value.trustedAPI || value.problem == nil {
		return false
	}
	code := value.problem.Code
	switch value.status {
	case 400:
		return code == "invalid_pattern_request"
	case 409:
		return code == "pattern_revision_conflict" || code == "pattern_conflict" || code == "pattern_library_limit" || code == "pattern_library_busy" || code == "pattern_library_read_only"
	case 413:
		return code == "pattern_package_too_large"
	case 415:
		return code == "json_required"
	case 501:
		return code == "pattern_library_unsupported" || code == "pattern_source_unsupported"
	}
	return false
}
func patternAuthorResponseRecord(schema int, contract string, raw json.RawMessage, support patternAuthorSupport, digest string) (patternAuthorRecordInfo, error) {
	if schema != 2 || contract != patternAuthorLibraryContract {
		return patternAuthorRecordInfo{}, errors.New("unsupported Pattern library response")
	}
	info, err := patternAuthorInspectRecord(raw)
	if err != nil {
		return info, err
	}
	if info.data.Verification != support || info.data.ExecutionDigest != digest {
		return patternAuthorRecordInfo{}, errors.New("daemon record verification or execution digest disagrees")
	}
	return info, nil
}
func (d *daemon) patternAuthorGet(ctx context.Context, raw json.RawMessage) patternAuthorReadOutput {
	var args struct {
		ID               string  `json:"id"`
		ExpectedRevision *string `json:"expected_revision,omitempty"`
	}
	if err := patternAuthorDecode(raw, &args, 4096, 32); err != nil {
		return patternAuthorReadError("invalid_request", err.Error(), 0)
	}
	if !patternID.MatchString(args.ID) || args.ExpectedRevision != nil && !patternRevision.MatchString(*args.ExpectedRevision) {
		return patternAuthorReadError("invalid_request", "exact bounded ID and optional revision required", 0)
	}
	response := d.patternAuthorHTTP(ctx, "GET", "/v2/patterns/"+url.PathEscape(args.ID), nil, false)
	if response.problem != nil {
		return patternAuthorReadOutput{Kind: "error", Error: response.problem}
	}
	var value patternAuthorRecordResponse
	err := patternAuthorDecode(response.body, &value, maxPatternAuthorResponse, 64)
	if response.status != 200 {
		err = errors.New("get requires HTTP200")
	}
	var info patternAuthorRecordInfo
	if err == nil {
		info, err = patternAuthorResponseRecord(value.SchemaVersion, value.Contract, value.Record, value.Verification, value.ExecutionDigest)
	}
	if err == nil && (info.data.PatternRef.ID != args.ID || args.ExpectedRevision != nil && info.data.PatternRef.Revision != *args.ExpectedRevision) {
		err = errors.New("returned record changed requested ID/revision")
	}
	if err != nil {
		return patternAuthorReadError("invalid_daemon_response", err.Error(), response.status)
	}
	return patternAuthorReadOutput{Kind: "pattern_record_v2", Data: info.data}
}
func (d *daemon) patternAuthorPreview(ctx context.Context, raw json.RawMessage) patternAuthorReadOutput {
	draft, err := patternAuthorReadDraft(raw, false)
	if err != nil {
		return patternAuthorReadError("invalid_request", err.Error(), 0)
	}
	response := d.patternAuthorHTTP(ctx, "POST", "/v2/patterns/preview", raw, false)
	if response.problem != nil {
		return patternAuthorReadOutput{Kind: "error", Error: response.problem}
	}
	var value patternAuthorPreviewResponse
	err = patternAuthorDecode(response.body, &value, maxPatternAuthorResponse, 64)
	if response.status != 200 {
		err = errors.New("preview requires HTTP200")
	}
	var info patternAuthorRecordInfo
	if err == nil {
		info, err = patternAuthorResponseRecord(value.SchemaVersion, value.Contract, value.Record, value.Verification, value.ExecutionDigest)
	}
	if err == nil {
		err = patternAuthorBindRecord(info, draft)
	}
	if err == nil {
		err = patternAuthorAvailability(value.PatternExecution)
	}
	if err == nil {
		var facts patternAuthorFacts
		facts, err = patternAuthorPreviewFacts(info.wire.Compiled.Execution)
		if err == nil && !reflect.DeepEqual(facts, value.Preview) {
			err = errors.New("preview facts differ from frozen execution")
		}
	}
	if err != nil {
		return patternAuthorReadError("invalid_daemon_response", err.Error(), response.status)
	}
	data := patternAuthorPreviewData{2, patternAuthorLibraryContract, info.data.PatternRef, info.data.RecordJSON, info.data.Verification, info.data.ExecutionDigest, value.Preview, value.PatternExecution}
	return patternAuthorReadOutput{Kind: "pattern_draft_preview_v2", Data: data}
}
func (d *daemon) patternAuthorSave(ctx context.Context, raw json.RawMessage) patternAuthorSaveOutput {
	out := patternAuthorSaveOutput{Kind: "pattern_publication_v2"}
	draft, err := patternAuthorReadDraft(raw, true)
	if err != nil {
		out.Error = patternAuthorProblem("invalid_request", err.Error(), 0)
		return out
	}
	ref := patternV2Ref{patternV2RecordContract, draft.ID, *draft.ExpectedRevision}
	out.PatternRef = &ref
	out.OriginalAuthorJSON = string(raw)
	response := d.patternAuthorHTTP(ctx, "POST", "/v2/patterns", raw, true)
	out.PublicationAttempted = response.attempted
	if response.problem != nil {
		out.Error = response.problem
		out.PublicationUnknown = response.attempted && !patternAuthorKnownRejection(response)
		return out
	}
	var value struct {
		SchemaVersion int               `json:"schema_version"`
		Contract      string            `json:"contract"`
		Records       []json.RawMessage `json:"records"`
		Created       []string          `json:"created"`
		Existing      []string          `json:"existing"`
		Total         int               `json:"total"`
	}
	err = patternAuthorDecode(response.body, &value, maxPatternAuthorResponse, 64)
	if err == nil && (value.SchemaVersion != 2 || value.Contract != patternAuthorLibraryContract || len(value.Records) != 1 || value.Created == nil || value.Existing == nil || value.Total < 1 || value.Total > 128) {
		err = errors.New("invalid complete save result")
	}
	var info patternAuthorRecordInfo
	if err == nil {
		info, err = patternAuthorInspectRecord(value.Records[0])
	}
	if err == nil {
		err = patternAuthorBindRecord(info, draft)
	}
	if err == nil {
		created := response.status == 201 && len(value.Created) == 1 && value.Created[0] == draft.ID && len(value.Existing) == 0
		existing := response.status == 200 && len(value.Existing) == 1 && value.Existing[0] == draft.ID && len(value.Created) == 0
		location := response.headers.Values("Location")
		if !(created || existing) || len(location) != 1 || location[0] != "/v2/patterns/"+url.PathEscape(draft.ID) {
			err = errors.New("save status/classification/unique Location changed original candidate")
		}
	}
	if err != nil {
		out.PublicationUnknown = true
		out.Error = patternAuthorProblem("invalid_daemon_response", err.Error(), response.status)
		return out
	}
	out.PublicationConfirmed = true
	out.Result = &patternAuthorWriteResult{2, patternAuthorLibraryContract, info.data.RecordJSON, info.data.Verification, info.data.ExecutionDigest, value.Created, value.Existing, value.Total}
	return out
}
