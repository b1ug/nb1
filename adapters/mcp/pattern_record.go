package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These are independent wire shapes, not imports of the CLI, Store or USB
// modules. A record contains a frozen Program; fetching it never executes it.
type patternRecordSource struct {
	Program *previewProgram `json:"program,omitempty"`
	Effect  *effect         `json:"effect,omitempty"`
}

type patternRecord struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Source          patternRecordSource `json:"source"`
	CompiledProgram previewProgram      `json:"compiled_program"`
	CompileContract string              `json:"compile_contract"`
	Revision        string              `json:"revision"`
}

type getPatternArgs struct {
	ID string `json:"id"`
}

func registerGetPatternTool(s *mcp.Server, d *daemon) {
	t := tool("get_pattern", "Read one full frozen Pattern record by its exact ID through one GET /v1/patterns/{id}. Returns pattern_record with accepted:false after canonical revision and normalized Program validation. No files, USB, ticket preparation, compilation or execution. Legacy Effect expansion is validated by the authoritative Store, not recompiled here. To play a record, explicitly select its exact ID and revision through a supported fixed-reference execution tool.", objectSchema(map[string]any{"id": map[string]any{"type": "string", "pattern": patternID.String(), "minLength": 1, "maxLength": 64}}, "id"), true, true)
	t.OutputSchema = patternRecordOutputSchema()
	mcp.AddTool(s, t, func(ctx context.Context, request *mcp.CallToolRequest, _ getPatternArgs) (*mcp.CallToolResult, output, error) {
		return getPattern(ctx, d, request.Params.Arguments)
	})
}

func getPattern(ctx context.Context, d *daemon, raw json.RawMessage) (*mcp.CallToolResult, output, error) {
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return invalid("get_pattern requires one bounded UTF-8 object with an id")
	}
	var args getPatternArgs
	if err := validateLightUnicodeEscapes(raw); err != nil {
		return invalid(err.Error())
	}
	if err := decodeSoftwareWire(raw, &args); err != nil {
		return invalid("get_pattern accepts only id: " + err.Error())
	}
	if !patternID.MatchString(args.ID) {
		return invalid("Pattern id must be 1-64 ASCII letters/digits, with hyphen/underscore allowed after the first character")
	}
	return result(d.readPatternRecord(ctx, args.ID))
}

func (d *daemon) readPatternRecord(ctx context.Context, id string) output {
	if !patternID.MatchString(id) {
		return errorOutput("invalid_request", "invalid Pattern ID", 0, false)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+"/v1/patterns/"+url.PathEscape(id), nil)
	if err != nil {
		return errorOutput("invalid_request", err.Error(), 0, false)
	}
	request.Header.Set("Accept", "application/json")
	response, err := d.client.Do(request)
	if err != nil {
		return errorOutput("daemon_unreachable", fmt.Sprintf("read Pattern record: %v", err), 0, false)
	}
	bad := func(err error) output {
		return errorOutput("invalid_daemon_response", err.Error(), response.StatusCode, false)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxPatternResponseBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return bad(readErr)
	}
	if closeErr != nil {
		return bad(closeErr)
	}
	if len(data) > maxPatternResponseBytes {
		return bad(errors.New("Pattern record response exceeds 1 MiB"))
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return bad(errors.New("Pattern record requires exactly one Content-Type"))
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" || !utf8.Valid(data) {
		return bad(errors.New("Pattern record requires UTF-8 application/json"))
	}
	if err := validateRawArguments(data); err != nil {
		return bad(err)
	}
	if err := validateLightUnicodeEscapes(data); err != nil {
		return bad(err)
	}
	for _, name := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
		if len(response.Header.Values(name)) != 0 {
			return bad(errors.New("Pattern record unexpectedly contains submission metadata"))
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := decodeSoftwareWire(data, &problem); err != nil {
			return bad(err)
		}
		if problem.Error.Code == "" || problem.Error.Message == "" {
			return bad(errors.New("Pattern error requires nonempty code and message"))
		}
		return errorOutput(problem.Error.Code, problem.Error.Message, response.StatusCode, false)
	}
	if response.StatusCode != http.StatusOK {
		return bad(errors.New("Pattern record requires HTTP200"))
	}
	var value patternRecord
	if err := decodeSoftwareWire(data, &value); err != nil {
		return bad(err)
	}
	if err := validatePatternRecord(value, id); err != nil {
		return bad(err)
	}
	return output{Kind: "pattern_record", Data: value, Accepted: false}
}

func validateFrozenPatternProgram(p previewProgram) error {
	normal, err := normalizeProgram(program{Steps: p.Steps, Repeat: p.Repeat, OnFinish: p.OnFinish})
	if err != nil {
		return err
	}
	if p.Repeat < 1 || !reflect.DeepEqual(normal, program{Steps: p.Steps, Repeat: p.Repeat, OnFinish: p.OnFinish}) {
		return errors.New("Pattern Program must already contain canonical defaults and lower-case colors")
	}
	return nil
}

func validatePatternRecord(value patternRecord, requestedID string) error {
	if !patternID.MatchString(value.ID) || value.ID != requestedID || !patternRevision.MatchString(value.Revision) || value.CompileContract != compileContract {
		return errors.New("Pattern record does not match the requested ID, revision or compiler contract")
	}
	if !utf8.ValidString(value.Title) || utf8.RuneCountInString(value.Title) < 1 || utf8.RuneCountInString(value.Title) > 256 {
		return errors.New("Pattern title requires 1-256 Unicode scalar values")
	}
	if err := validateFrozenPatternProgram(value.CompiledProgram); err != nil {
		return fmt.Errorf("frozen Program: %w", err)
	}
	if (value.Source.Program == nil) == (value.Source.Effect == nil) {
		return errors.New("Pattern source requires exactly one program or effect")
	}
	if value.Source.Program != nil {
		if err := validateFrozenPatternProgram(*value.Source.Program); err != nil {
			return fmt.Errorf("source Program: %w", err)
		}
		if !reflect.DeepEqual(*value.Source.Program, value.CompiledProgram) {
			return errors.New("Pattern source Program differs from frozen Program")
		}
	} else {
		// The authoritative Store checks legacy Effect -> Program expansion.
		// Here only canonical structure, limits and the full-record hash are
		// validated. No second effect compiler or device execution is added.
		e := *value.Source.Effect
		normal, err := normalizeEffectStructure(e)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(normal, e) || e.Kind == "steady" && e.FadeMS == nil || e.Kind == "pulse" && (e.Cycles == nil || e.PeriodMS == nil || e.OnFinish == nil) {
			return errors.New("Pattern Effect must contain canonical explicit fields")
		}
		if e.Kind == "pulse" && int64(*e.Cycles)*int64(*e.PeriodMS) > 86400000 {
			return errors.New("Pattern Effect exceeds the finite time budget")
		}
	}
	canonical, err := canonicalPatternRecord(value)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(patternRecordContract + "\n"))
	_, _ = digest.Write(canonical)
	if value.Revision != fmt.Sprintf("sha256:%x", digest.Sum(nil)) {
		return errors.New("Pattern revision does not match its canonical complete record")
	}
	return nil
}

// The Pattern hash domain is distinct from encoding/json: fixed field order,
// all C0 bytes as lower-case \u00xx, and direct HTML/U+2028/U+2029. Appends are
// bounded even when a short wire escape expands to a longer canonical form.
type patternRecordWriter struct {
	data []byte
	err  error
}

func (w *patternRecordWriter) raw(s string) {
	if w.err != nil {
		return
	}
	if len(s) > maxPatternResponseBytes-len(w.data) {
		w.err = errors.New("canonical Pattern record exceeds 1 MiB")
		return
	}
	w.data = append(w.data, s...)
}
func (w *patternRecordWriter) text(s string) {
	const hex = "0123456789abcdef"
	w.raw(`"`)
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '"' && c != '\\' && c >= 0x20 {
			continue
		}
		w.raw(s[start:i])
		switch c {
		case '"':
			w.raw(`\"`)
		case '\\':
			w.raw(`\\`)
		default:
			w.raw(string([]byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&15]}))
		}
		start = i + 1
	}
	w.raw(s[start:])
	w.raw(`"`)
}
func (w *patternRecordWriter) number(n int) { w.raw(strconv.Itoa(n)) }
func (w *patternRecordWriter) program(p previewProgram) {
	w.raw(`{"steps":[`)
	for i, s := range p.Steps {
		if i > 0 {
			w.raw(",")
		}
		w.raw(`{"led":`)
		w.number(s.LED)
		w.raw(`,"color":`)
		w.text(s.Color)
		w.raw(`,"fade_ms":`)
		w.number(s.FadeMS)
		w.raw(`,"hold_ms":`)
		w.number(s.HoldMS)
		w.raw("}")
	}
	w.raw(`],"repeat":`)
	w.number(p.Repeat)
	w.raw(`,"on_finish":`)
	w.text(p.OnFinish)
	w.raw("}")
}
func canonicalPatternRecord(value patternRecord) ([]byte, error) {
	if (value.Source.Program == nil) == (value.Source.Effect == nil) {
		return nil, errors.New("Pattern source requires exactly one branch")
	}
	if e := value.Source.Effect; e != nil {
		if e.Kind == "steady" && e.FadeMS == nil || e.Kind == "pulse" && (e.Cycles == nil || e.PeriodMS == nil || e.OnFinish == nil) || e.Kind != "steady" && e.Kind != "pulse" {
			return nil, errors.New("Pattern Effect canonical fields are missing")
		}
	}
	var w patternRecordWriter
	w.raw(`{"id":`)
	w.text(value.ID)
	w.raw(`,"title":`)
	w.text(value.Title)
	w.raw(`,"source":{`)
	if value.Source.Program != nil {
		w.raw(`"program":`)
		w.program(*value.Source.Program)
	} else if value.Source.Effect != nil {
		e := value.Source.Effect
		w.raw(`"effect":{"kind":`)
		w.text(e.Kind)
		w.raw(`,"color":`)
		w.text(e.Color)
		w.raw(`,"led":`)
		w.number(e.LED)
		if e.Kind == "steady" {
			w.raw(`,"fade_ms":`)
			w.number(*e.FadeMS)
		} else {
			w.raw(`,"cycles":`)
			w.number(*e.Cycles)
			w.raw(`,"period_ms":`)
			w.number(*e.PeriodMS)
			w.raw(`,"on_finish":`)
			w.text(*e.OnFinish)
		}
		w.raw("}")
	} else {
		return nil, errors.New("Pattern source is absent")
	}
	w.raw(`},"compiled_program":`)
	w.program(value.CompiledProgram)
	w.raw(`,"compile_contract":`)
	w.text(value.CompileContract)
	w.raw("}")
	if w.err != nil {
		return nil, w.err
	}
	return w.data, nil
}

func frozenPatternProgramSchema() map[string]any {
	p := programSchema()
	p["required"] = []string{"steps", "repeat", "on_finish"}
	properties := p["properties"].(map[string]any)
	properties["repeat"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}
	s := properties["steps"].(map[string]any)["items"].(map[string]any)
	s["required"] = []string{"led", "color", "fade_ms", "hold_ms"}
	s["properties"].(map[string]any)["color"] = map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"}
	return p
}
func patternRecordOutputSchema() map[string]any {
	constant := func(value any) map[string]any { return map[string]any{"const": value} }
	color := map[string]any{"type": "string", "pattern": "^#[0-9a-f]{6}$"}
	led := map[string]any{"type": "integer", "enum": []int{0, 1, 2}}
	steady := objectSchema(map[string]any{"kind": constant("steady"), "color": color, "led": led, "fade_ms": fadeSchema()}, "kind", "color", "led", "fade_ms")
	pulse := objectSchema(map[string]any{"kind": constant("pulse"), "color": color, "led": led, "cycles": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}, "period_ms": map[string]any{"type": "integer", "minimum": 60, "maximum": 1200000, "multipleOf": 10}, "on_finish": map[string]any{"type": "string", "enum": []string{"off", "hold"}}}, "kind", "color", "led", "cycles", "period_ms", "on_finish")
	source := objectSchema(map[string]any{"program": frozenPatternProgramSchema(), "effect": map[string]any{"oneOf": []any{steady, pulse}}})
	source["oneOf"] = []any{map[string]any{"required": []string{"program"}, "not": map[string]any{"required": []string{"effect"}}}, map[string]any{"required": []string{"effect"}, "not": map[string]any{"required": []string{"program"}}}}
	data := objectSchema(map[string]any{"id": map[string]any{"type": "string", "pattern": patternID.String()}, "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "source": source, "compiled_program": frozenPatternProgramSchema(), "compile_contract": constant(compileContract), "revision": map[string]any{"type": "string", "pattern": patternRevision.String()}}, "id", "title", "source", "compiled_program", "compile_contract", "revision")
	problem := objectSchema(map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "http_status": map[string]any{"type": "integer"}, "outcome_unknown": constant(false)}, "code", "message")
	out := objectSchema(map[string]any{"kind": map[string]any{"enum": []string{"pattern_record", "error"}}, "accepted": constant(false), "data": data, "error": problem}, "kind", "accepted")
	out["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": constant("pattern_record")}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}}, map[string]any{"properties": map[string]any{"kind": constant("error")}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}}}
	return out
}
