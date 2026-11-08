package main

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This is an intent wire type, not an effect compiler. Optional pointers keep
// omitted defaults distinct from explicit zero or fields for another kind.
// The daemon owns CompileEffect, timing expansion and total program limits.
type effect struct {
	Kind     string  `json:"kind"`
	Color    string  `json:"color"`
	LED      int     `json:"led"`
	FadeMS   *int    `json:"fade_ms,omitempty"`
	Cycles   *int    `json:"cycles,omitempty"`
	PeriodMS *int    `json:"period_ms,omitempty"`
	OnFinish *string `json:"on_finish,omitempty"`
}

type effectSubmission struct {
	Effect                   effect `json:"effect"`
	Policy                   string `json:"policy"`
	RequestID                string `json:"request_id,omitempty"`
	ExpectedDaemonInstanceID string `json:"expected_daemon_instance_id,omitempty"`
}

// Only structural bounds are checked here. In particular, accepting this
// structure does not establish that the daemon can compile it within 24h.
// Omitted optional fields stay omitted for the daemon to apply its defaults.
func normalizeEffectStructure(e effect) (effect, error) {
	if e.LED < 0 || e.LED > 2 {
		return effect{}, errors.New("effect led must be 0, 1 or 2")
	}
	if len(e.Color) != 7 || e.Color[0] != '#' {
		return effect{}, errors.New("effect color must be #RRGGBB")
	}
	if _, err := hex.DecodeString(e.Color[1:]); err != nil {
		return effect{}, errors.New("effect color must be #RRGGBB")
	}
	e.Color = strings.ToLower(e.Color)
	switch e.Kind {
	case "steady":
		if e.Cycles != nil || e.PeriodMS != nil || e.OnFinish != nil {
			return effect{}, errors.New("steady effect accepts fade_ms, not cycles, period_ms or on_finish")
		}
		if e.FadeMS != nil && (*e.FadeMS < 0 || *e.FadeMS > 600000 || *e.FadeMS%10 != 0) {
			return effect{}, errors.New("steady fade_ms must be 0–600000 in 10ms units")
		}
	case "pulse":
		if e.FadeMS != nil {
			return effect{}, errors.New("pulse effect does not accept fade_ms, including zero")
		}
		if e.Cycles != nil && (*e.Cycles < 1 || *e.Cycles > 10000) {
			return effect{}, errors.New("pulse cycles must be 1–10000")
		}
		if e.PeriodMS != nil && (*e.PeriodMS < 60 || *e.PeriodMS > 1200000 || *e.PeriodMS%10 != 0) {
			return effect{}, errors.New("pulse period_ms must be 60–1200000 in 10ms units")
		}
		if e.OnFinish != nil && *e.OnFinish != "off" && *e.OnFinish != "hold" {
			return effect{}, errors.New("pulse on_finish must be off or hold")
		}
	default:
		return effect{}, errors.New("effect kind must be steady or pulse")
	}
	return e, nil
}

func run(ctx context.Context, d *daemon, args runArgs) (*mcp.CallToolResult, output, error) {
	if (args.Program == nil) == (args.Effect == nil) {
		return invalid("provide exactly one of program or effect")
	}
	if args.Program != nil {
		return submit(ctx, d, args.Serial, *args.Program, args.Policy, args.DryRun, args.RequestID, args.ExpectedDaemonInstanceID)
	}
	if !validToken(args.Serial) {
		return invalid("invalid serial")
	}
	intent, err := normalizeEffectStructure(*args.Effect)
	if err != nil {
		return invalid(err.Error())
	}
	policy, err := normalizePolicy(args.Policy)
	if err != nil {
		return invalid(err.Error())
	}
	ticket, err := optionalTicket(args.RequestID, args.ExpectedDaemonInstanceID)
	if err != nil {
		return invalid(err.Error())
	}
	request := effectSubmission{Effect: intent, Policy: policy, RequestID: args.RequestID, ExpectedDaemonInstanceID: args.ExpectedDaemonInstanceID}
	if args.DryRun {
		data := map[string]any{"serial": args.Serial, "request": request, "compiled": false, "validation_level": "structure_only"}
		out := output{Kind: "preview", DryRun: true, Data: data, RequestTicket: ticket}
		if ticket != nil {
			verified := false
			data["instance_binding_verified"] = false
			out.InstanceBindingVerified = &verified
		}
		return result(out)
	}
	return result(d.requestJob(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(args.Serial)+"/jobs", request, args.Serial, ticket))
}

func effectSchema() map[string]any {
	led := map[string]any{"type": "integer", "enum": []int{0, 1, 2}}
	cycles := map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}
	period := map[string]any{"type": "integer", "minimum": 60, "maximum": 1200000, "multipleOf": 10}
	finish := map[string]any{"type": "string", "enum": []string{"off", "hold"}}
	steady := objectSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"steady"}}, "color": colorSchema(), "led": led, "fade_ms": fadeSchema()}, "kind", "color")
	pulse := objectSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"pulse"}}, "color": colorSchema(), "led": led, "cycles": cycles, "period_ms": period, "on_finish": finish}, "kind", "color")
	schema := objectSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"steady", "pulse"}}, "color": colorSchema(), "led": led, "fade_ms": fadeSchema(), "cycles": cycles, "period_ms": period, "on_finish": finish}, "kind", "color")
	schema["oneOf"] = []any{steady, pulse}
	return schema
}

func runSchema() map[string]any {
	schema := submissionSchema(map[string]any{"serial": tokenSchema(), "program": programSchema(), "effect": effectSchema(), "policy": policySchema(), "dry_run": map[string]any{"type": "boolean"}}, "serial")
	schema["oneOf"] = []any{
		map[string]any{"required": []string{"program"}, "not": map[string]any{"required": []string{"effect"}}},
		map[string]any{"required": []string{"effect"}, "not": map[string]any{"required": []string{"program"}}},
	}
	return schema
}
