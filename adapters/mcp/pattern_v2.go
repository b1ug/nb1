package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type patternV2PreviewArgs struct {
	PatternRef patternV2Ref `json:"pattern_ref"`
	Serial     *string      `json:"serial,omitempty"`
}
type patternV2RunArgs struct {
	PatternRef               patternV2Ref `json:"pattern_ref"`
	Serial                   string       `json:"serial"`
	Policy                   string       `json:"policy"`
	RequestID                string       `json:"request_id"`
	ExpectedDaemonInstanceID string       `json:"expected_daemon_instance_id"`
}
type patternV2RequestArgs struct {
	PatternRef               patternV2Ref `json:"pattern_ref"`
	Serial                   string       `json:"serial"`
	RequestID                string       `json:"request_id"`
	ExpectedDaemonInstanceID string       `json:"expected_daemon_instance_id"`
	JobID                    *string      `json:"job_id,omitempty"`
	ExecutionDigest          *string      `json:"execution_digest,omitempty"`
}
type patternV2CancelData struct {
	PatternJob      *patternV2Envelope `json:"pattern_job,omitempty"`
	CancelAttempted bool               `json:"cancel_attempted"`
}

func registerPatternV2Tools(s *mcp.Server, d *daemon) {
	add := func(name, description string, schema map[string]any, readOnly bool, handler func(context.Context, json.RawMessage) output) {
		t := tool(name, description, schema, readOnly, readOnly)
		t.OutputSchema = patternV2OutputSchema(name)
		mcp.AddTool(s, t, func(ctx context.Context, r *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, output, error) {
			return result(handler(ctx, r.Params.Arguments))
		})
	}
	add("preview_pattern", "Preview an exact experimental-v2 stored Pattern Ref through one read-only daemon POST. The daemon verifies its original high-level source, recipe and frozen execution; this adapter verifies the typed execution digest, not the source compiler. Optional serial checks cached capabilities only. No Job, ticket, admission or USB; accepted:false. Unknown sources remain unsupported, never converted to legacy steps.", patternV2PreviewSchema(), true, func(ctx context.Context, raw json.RawMessage) output {
		var a patternV2PreviewArgs
		if err := decodePatternV2Args(raw, &a); err != nil {
			return errorOutput("invalid_request", err.Error(), 0, false)
		}
		if !validPatternV2Ref(a.PatternRef) || a.Serial != nil && !validFrameSerial(*a.Serial) {
			return errorOutput("invalid_request", "preview_pattern requires an exact v2 Pattern Ref and optional explicit hardware serial", 0, false)
		}
		data, _, status, out := d.patternV2HTTP(ctx, http.MethodPost, "/v2/patterns/preview-ref", a, false)
		if out.Error != nil {
			return out
		}
		bad := func(err error) output { return errorOutput("invalid_daemon_response", err.Error(), status, false) }
		var p patternV2Preview
		if err := decodeSoftwareWire(data, &p); err != nil {
			return bad(err)
		}
		if err := validatePatternV2Preview(p, a); err != nil {
			return bad(err)
		}
		return output{Kind: "pattern_ref_preview", Data: p}
	})
	add("run_pattern", "Submit one exact experimental-v2 Pattern Ref with explicit replace/queue/reject and the complete original request ticket. Makes at most one execution POST, no implicit preview, ticket preparation, retry or CLI/USB fallback. The daemon retains source identity and owns compilation/verification; the adapter recomputes the returned frozen execution digest. Acceptance is not completion. Keep the Ref and ticket for get_pattern_request/cancel_pattern_request even if the library disappears or the response is lost.", patternV2RunSchema(), false, func(ctx context.Context, raw json.RawMessage) output {
		var a patternV2RunArgs
		if err := decodePatternV2Args(raw, &a); err != nil {
			return errorOutput("invalid_request", err.Error(), 0, false)
		}
		ticket := requestTicket{a.RequestID, a.ExpectedDaemonInstanceID}
		if !validPatternV2Ref(a.PatternRef) || !validFrameSerial(a.Serial) || validateTicket(ticket) != nil || (a.Policy != "replace" && a.Policy != "queue" && a.Policy != "reject") {
			return errorOutput("invalid_request", "run_pattern requires exact v2 Ref, hardware serial, explicit policy and complete original ticket", 0, false)
		}
		request := struct {
			PatternRef               patternV2Ref `json:"pattern_ref"`
			Policy                   string       `json:"policy"`
			RequestID                string       `json:"request_id"`
			ExpectedDaemonInstanceID string       `json:"expected_daemon_instance_id"`
		}{a.PatternRef, a.Policy, a.RequestID, a.ExpectedDaemonInstanceID}
		data, h, status, out := d.patternV2HTTP(ctx, http.MethodPost, "/v2/devices/"+url.PathEscape(a.Serial)+"/pattern-jobs", request, true)
		out = preserveTicket(out, &ticket)
		if out.Error != nil {
			return out
		}
		binding := patternV2RequestArgs{PatternRef: a.PatternRef, Serial: a.Serial, RequestID: a.RequestID, ExpectedDaemonInstanceID: a.ExpectedDaemonInstanceID}
		e, err := parsePatternV2Envelope(data, h, status, binding, "submit")
		if err != nil {
			return preserveTicket(errorOutput("invalid_daemon_response", err.Error(), status, true), &ticket)
		}
		return patternV2JobOutput("pattern_job", e, a.Serial, true, &ticket)
	})
	add("get_pattern_request", "Read an experimental-v2 Pattern Job only through its original serial, exact Ref and complete original request ticket. No Store lookup, source recompilation, ticket replacement or execution retry. Optional known job_id and execution_digest must match. Expired/missing receipts or changed instances leave the original outcome unknown. The independent envelope retains provenance and job_kind; do not use legacy get_job for these Jobs.", patternV2RequestSchema(), true, func(ctx context.Context, raw json.RawMessage) output {
		a, err := decodePatternV2Request(raw)
		if err != nil {
			return errorOutput("invalid_request", err.Error(), 0, false)
		}
		return d.getPatternV2Request(ctx, a)
	})
	add("cancel_pattern_request", "Cancel an experimental-v2 Pattern through its original exact Ref and complete ticket. First GETs and validates that receipt, then makes at most one bound cancel POST with confirmed_job_id. Optional known job_id/execution_digest further bind it. Does not access the Store, prepare tickets, retry writes, or force off a naturally completed hold. cancelling is in progress; inspect get_pattern_request. Errors retain the original ticket and last trustworthy Job, with cancel_attempted indicating whether the sole POST was attempted.", patternV2RequestSchema(), false, func(ctx context.Context, raw json.RawMessage) output {
		a, err := decodePatternV2Request(raw)
		if err != nil {
			return errorOutput("invalid_request", err.Error(), 0, false)
		}
		ticket := requestTicket{a.RequestID, a.ExpectedDaemonInstanceID}
		before := d.getPatternV2Request(ctx, a)
		if before.Error != nil {
			return before
		}
		e := before.Data.(patternV2Envelope)
		s, _ := validatePatternV2Envelope(e, a.PatternRef, a.Serial, a.JobID, a.ExecutionDigest)
		if patternV2Terminal(s.status) || s.status == "cancelling" {
			before.Kind = "pattern_cancel"
			before.Data = patternV2CancelData{&e, false}
			return before
		}
		failure := func(out output, attempted bool) output {
			out = preserveTicket(out, &ticket)
			out.Data = patternV2CancelData{&e, attempted}
			if out.Error != nil {
				out.Error.OutcomeUnknown = true
			}
			return out
		}
		if err := ctx.Err(); err != nil {
			return failure(errorOutput("daemon_unreachable", err.Error(), 0, true), false)
		}
		request := struct {
			PatternRef               patternV2Ref `json:"pattern_ref"`
			ExpectedDaemonInstanceID string       `json:"expected_daemon_instance_id"`
			ConfirmedJobID           string       `json:"confirmed_job_id"`
		}{a.PatternRef, a.ExpectedDaemonInstanceID, s.id}
		data, h, status, out := d.patternV2HTTP(ctx, http.MethodPost, patternV2RequestBase(a)+"/cancel", request, true)
		if out.Error != nil {
			return failure(out, true)
		}
		a.JobID = &s.id
		a.ExecutionDigest = &e.ExecutionDigest
		after, err := parsePatternV2Envelope(data, h, status, a, "cancel")
		if err == nil {
			err = validatePatternV2Progress(e, after, a.Serial)
		}
		if err == nil {
			afterState, _ := validatePatternV2Envelope(after, a.PatternRef, a.Serial, a.JobID, a.ExecutionDigest)
			if afterState.status == "queued" || afterState.status == "running" {
				err = errors.New("cancel response did not acknowledge cancellation or terminal completion")
			}
		}
		if err != nil {
			return failure(errorOutput("invalid_daemon_response", err.Error(), status, true), true)
		}
		out = patternV2JobOutput("pattern_cancel", after, a.Serial, true, &ticket)
		out.Data = patternV2CancelData{&after, true}
		return out
	})
}
func decodePatternV2Args(raw json.RawMessage, out any) error {
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return errors.New("Pattern arguments require one UTF-8 object within 4096 bytes")
	}
	if err := validateLightUnicodeEscapes(raw); err != nil {
		return err
	}
	return decodeSoftwareWire(raw, out)
}
func decodePatternV2Request(raw json.RawMessage) (patternV2RequestArgs, error) {
	var a patternV2RequestArgs
	if err := decodePatternV2Args(raw, &a); err != nil {
		return a, err
	}
	if !validPatternV2Ref(a.PatternRef) || !validFrameSerial(a.Serial) || validateTicket(requestTicket{a.RequestID, a.ExpectedDaemonInstanceID}) != nil || a.JobID != nil && !(validLowerHexID(*a.JobID, "job_") || validLowerHexID(*a.JobID, "framejob_")) || a.ExecutionDigest != nil && !patternRevision.MatchString(*a.ExecutionDigest) {
		return a, errors.New("Pattern request requires exact v2 Ref, serial, complete original ticket and valid optional Job/digest")
	}
	return a, nil
}
func patternV2RequestBase(a patternV2RequestArgs) string {
	return "/v2/devices/" + url.PathEscape(a.Serial) + "/pattern-requests/" + url.PathEscape(a.RequestID)
}
func patternV2ReceiptPath(a patternV2RequestArgs) string {
	return patternV2RequestBase(a) + "?" + url.Values{"expected_daemon_instance_id": {a.ExpectedDaemonInstanceID}, "record_contract": {a.PatternRef.RecordContract}, "pattern_id": {a.PatternRef.ID}, "revision": {a.PatternRef.Revision}}.Encode()
}
func (d *daemon) getPatternV2Request(ctx context.Context, a patternV2RequestArgs) output {
	ticket := requestTicket{a.RequestID, a.ExpectedDaemonInstanceID}
	data, h, status, out := d.patternV2HTTP(ctx, http.MethodGet, patternV2ReceiptPath(a), nil, false)
	if out.Error != nil {
		out = preserveTicket(out, &ticket)
		out.Error.OutcomeUnknown = true
		return out
	}
	e, err := parsePatternV2Envelope(data, h, status, a, "query")
	if err != nil {
		return preserveTicket(errorOutput("invalid_daemon_response", err.Error(), status, true), &ticket)
	}
	return patternV2JobOutput("pattern_request", e, a.Serial, false, &ticket)
}
func patternV2JobOutput(kind string, e patternV2Envelope, serial string, accepted bool, ticket *requestTicket) output {
	out := output{Kind: kind, Data: e, Accepted: accepted, RequestTicket: ticket}
	state, err := validatePatternV2Envelope(e, e.PatternRef, serial, nil, nil)
	if err != nil {
		return preserveTicket(errorOutput("invalid_daemon_response", err.Error(), 0, true), ticket)
	}
	if !patternV2Terminal(state.status) {
		out.PollAfterMS = 200
	}
	return out
}
func parsePatternV2Envelope(data []byte, h http.Header, status int, a patternV2RequestArgs, operation string) (patternV2Envelope, error) {
	var e patternV2Envelope
	want := http.StatusAccepted
	if operation == "query" {
		want = http.StatusOK
	}
	if status != want {
		return e, errors.New("Pattern response has unexpected HTTP status")
	}
	ids := h.Values("X-NB1-Daemon-Instance-ID")
	if len(ids) != 1 || ids[0] != a.ExpectedDaemonInstanceID {
		return e, errors.New("Pattern response does not bind the original daemon instance")
	}
	if operation == "submit" {
		replay, location := h.Values("X-NB1-Replayed"), h.Values("Location")
		if len(replay) != 1 || (replay[0] != "true" && replay[0] != "false") || len(location) != 1 || location[0] != patternV2ReceiptPath(a) {
			return e, errors.New("Pattern submission receipt metadata changed")
		}
	} else if len(h.Values("X-NB1-Replayed")) != 0 || len(h.Values("Location")) != 0 {
		return e, errors.New("Pattern query/cancel contains unexpected submission metadata")
	}
	if err := decodeSoftwareWire(data, &e); err != nil {
		return e, err
	}
	_, err := validatePatternV2Envelope(e, a.PatternRef, a.Serial, a.JobID, a.ExecutionDigest)
	return e, err
}

// Dedicated transport keeps the old 12 response contracts unchanged. Every
// write has a non-replayable body, no GetBody, redirect or application retry.
func (d *daemon) patternV2HTTP(ctx context.Context, method, path string, input any, execution bool) ([]byte, http.Header, int, output) {
	bad := func(err error, status int) ([]byte, http.Header, int, output) {
		return nil, nil, status, errorOutput("invalid_daemon_response", err.Error(), status, execution)
	}
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return nil, nil, 0, errorOutput("invalid_request", err.Error(), 0, false)
		}
		if len(b) > 4096 {
			return nil, nil, 0, errorOutput("invalid_request", "Pattern request exceeds 4096 bytes", 0, false)
		}
		body = bytes.NewReader(b)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.endpoint+path, body)
	if err != nil {
		return nil, nil, 0, errorOutput("invalid_request", err.Error(), 0, false)
	}
	request.GetBody = nil
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, nil, 0, errorOutput("daemon_unreachable", fmt.Sprintf("contact Pattern daemon: %v", err), 0, execution)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxPatternResponseBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return bad(readErr, response.StatusCode)
	}
	if closeErr != nil {
		return bad(closeErr, response.StatusCode)
	}
	if len(data) > maxPatternResponseBytes {
		return bad(errors.New("Pattern response exceeds 1 MiB"), response.StatusCode)
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return bad(errors.New("Pattern response requires exactly one Content-Type"), response.StatusCode)
	}
	typ, _, err := mime.ParseMediaType(media[0])
	if err != nil || typ != "application/json" || !utf8.Valid(data) {
		return bad(errors.New("Pattern response requires UTF-8 application/json"), response.StatusCode)
	}
	if err := validateRawArguments(data); err != nil {
		return bad(err, response.StatusCode)
	}
	if err := validateLightUnicodeEscapes(data); err != nil {
		return bad(err, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if err := validateLightErrorResponse(data); err != nil {
			return bad(err, response.StatusCode)
		}
		var problem struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := decodeSoftwareWire(data, &problem); err != nil {
			return bad(err, response.StatusCode)
		}
		return nil, nil, response.StatusCode, errorOutput(problem.Error.Code, problem.Error.Message, response.StatusCode, execution && !patternV2KnownRejection(response.StatusCode, problem.Error.Code))
	}
	if path == "/v2/patterns/preview-ref" {
		if response.StatusCode != 200 {
			return bad(errors.New("Pattern preview requires HTTP200"), response.StatusCode)
		}
		for _, key := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
			if len(response.Header.Values(key)) != 0 {
				return bad(errors.New("Pattern preview unexpectedly contains receipt metadata"), response.StatusCode)
			}
		}
	}
	return data, response.Header, response.StatusCode, output{}
}

// Only status/code pairs emitted for a known rejected admission are definite.
// Unknown routes, redirects, changed instances and missing receipts cannot settle
// an earlier use of the original ticket. This never authorizes a new ticket.
func patternV2KnownRejection(status int, code string) bool {
	switch status {
	case http.StatusBadRequest:
		return code == "invalid_pattern_request" || code == "invalid_request"
	case http.StatusNotFound:
		return code == "pattern_not_found"
	case http.StatusConflict:
		return code == "busy" || code == "queue_full" || code == "idempotency_conflict" || code == "pattern_revision_conflict" || code == "pattern_library_busy"
	case http.StatusTooManyRequests:
		return code == "idempotency_capacity"
	}
	return false
}
