package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A reference is a selected immutable revision, never an implicit latest.
type patternRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type patternPreview struct {
	PatternRef patternRef    `json:"pattern_ref"`
	Preview    previewResult `json:"preview"`
}

type patternSubmission struct {
	PatternRef               patternRef `json:"pattern_ref"`
	Policy                   string     `json:"policy"`
	RequestID                string     `json:"request_id"`
	ExpectedDaemonInstanceID string     `json:"expected_daemon_instance_id"`
}

type patternJobWire struct {
	ID             string         `json:"id"`
	Device         string         `json:"device"`
	Status         string         `json:"status"`
	Program        previewProgram `json:"program"`
	CompletedSteps int            `json:"completed_steps"`
	TotalSteps     int            `json:"total_steps"`
	CreatedAt      time.Time      `json:"created_at"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	Error          string         `json:"error,omitempty"`
}

func validPatternRef(ref patternRef) bool {
	return patternID.MatchString(ref.ID) && patternRevision.MatchString(ref.Revision)
}

func patternRefSchema() map[string]any {
	return objectSchema(map[string]any{
		"id":       map[string]any{"type": "string", "pattern": patternID.String()},
		"revision": map[string]any{"type": "string", "pattern": patternRevision.String(), "description": "Complete revision returned by list_patterns/get_pattern; never replace with latest"},
	}, "id", "revision")
}

func oneProgramInput(selected ...bool) bool {
	count := 0
	for _, present := range selected {
		if present {
			count++
		}
	}
	return count == 1
}

func programInputVariants(run bool) []any {
	names := []string{"program", "effect", "pattern_ref"}
	variants := make([]any, 0, len(names))
	for _, name := range names {
		var forbidden []any
		for _, other := range names {
			if name != other {
				forbidden = append(forbidden, map[string]any{"required": []string{other}})
			}
		}
		required := []string{name}
		variant := map[string]any{"required": required, "not": map[string]any{"anyOf": forbidden}}
		if name == "pattern_ref" && run {
			variant["required"] = append(required, "policy", "request_id", "expected_daemon_instance_id")
			variant["properties"] = map[string]any{"policy": map[string]any{"const": "reject"}}
			forbidden = append(forbidden, map[string]any{"required": []string{"dry_run"}})
			variant["not"] = map[string]any{"anyOf": forbidden}
		}
		variants = append(variants, variant)
	}
	return variants
}

func validatePatternPreview(data []byte, input any) (any, error) {
	args, ok := input.(previewArgs)
	if !ok || args.PatternRef == nil || args.Program != nil || args.Effect != nil || !validPatternRef(*args.PatternRef) {
		return nil, errors.New("missing original fixed pattern reference")
	}
	var value patternPreview
	if err := decodeSoftwareWire(data, &value); err != nil {
		return nil, err
	}
	if value.PatternRef != *args.PatternRef {
		return nil, errors.New("pattern preview changed the selected revision")
	}
	p := program{Steps: value.Preview.Program.Steps, Repeat: value.Preview.Program.Repeat, OnFinish: value.Preview.Program.OnFinish}
	if err := validatePreviewResult(value.Preview, previewArgs{Program: &p, Serial: args.Serial}); err != nil {
		return nil, err
	}
	return value, nil
}

func runPattern(ctx context.Context, d *daemon, args runArgs) (*mcp.CallToolResult, output, error) {
	if !validToken(args.Serial) || args.PatternRef == nil || !validPatternRef(*args.PatternRef) || args.Policy != "reject" || args.DryRun {
		return invalid("pattern_ref requires valid serial, fixed revision and explicit reject; use preview_program instead of dry_run")
	}
	ticket, err := optionalTicket(args.RequestID, args.ExpectedDaemonInstanceID)
	if err != nil || ticket == nil {
		return invalid("pattern_ref requires a complete original request ticket")
	}
	ref := *args.PatternRef
	preview := d.requestSoftware(ctx, http.MethodPost, "/v1/programs/preview", "pattern_preview", previewArgs{PatternRef: &ref, Serial: &args.Serial})
	if preview.Error != nil {
		preview = preserveTicket(preview, ticket)
		preview.Error.OutcomeUnknown = true
		preview.Error.Message += "; no execution POST made; query the original ticket for any earlier attempt"
		return result(preview)
	}
	prepared, ok := preview.Data.(patternPreview)
	if !ok || prepared.Preview.Validation.Device != "supported" {
		return result(preserveTicket(errorOutput("unsupported", "pattern target is not confirmed supported; no execution POST made; query the original ticket", 0, true), ticket))
	}
	request := patternSubmission{PatternRef: ref, Policy: "reject", RequestID: ticket.RequestID, ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID}
	out := d.requestBound(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(args.Serial)+"/jobs", "pattern_job", request, &receiptBinding{serial: args.Serial, ticket: *ticket})
	out = preserveTicket(out, ticket)
	if out.Error != nil {
		if strings.HasPrefix(out.Error.Code, "pattern_") {
			out.Error.OutcomeUnknown = true
		}
		return result(out)
	}
	if err := validatePatternJob(out.Data, prepared.Preview); err != nil {
		return result(preserveTicket(errorOutput("invalid_daemon_response", err.Error(), http.StatusAccepted, true), ticket))
	}
	return result(out)
}

func validatePatternJob(value any, prepared previewResult) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var job patternJobWire
	if err := decodeSoftwareWire(data, &job); err != nil {
		return err
	}
	if !validToken(job.ID) || !validToken(job.Device) || job.CreatedAt.IsZero() {
		return errors.New("Pattern Job identity or creation timestamp is invalid")
	}
	if !reflect.DeepEqual(job.Program, prepared.Program) {
		return errors.New("accepted Pattern Job changed its frozen Program")
	}
	if job.TotalSteps != prepared.TotalSteps {
		return errors.New("Pattern Job total_steps differs from preview")
	}
	if job.CompletedSteps < 0 || job.CompletedSteps > job.TotalSteps {
		return errors.New("invalid Pattern Job completed_steps")
	}
	switch job.Status {
	case "succeeded":
		if job.CompletedSteps != job.TotalSteps {
			return errors.New("successful Pattern Job has incomplete progress")
		}
	case "queued", "running", "cancelling", "failed", "cancelled":
	default:
		return errors.New("unrecognized Pattern Job status")
	}
	return nil
}
