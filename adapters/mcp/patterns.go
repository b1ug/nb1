package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const patternLibraryContract = "nb1-pattern-library/v1"
const patternRecordContract = "nb1-pattern-record/v1"
const maxPatternResponseBytes = 1 << 20

// Independent wire types. A directory entry is neither a compiled Program nor
// permission to execute it; the daemon owns validation, storage and admission.
type patternLibraryInfo struct {
	Path            string `json:"path"`
	State           string `json:"state"`
	CreateOnly      bool   `json:"create_only"`
	MaxRecords      int    `json:"max_records"`
	MaxPackageBytes int    `json:"max_package_bytes"`
	RecordContract  string `json:"record_contract"`
	CompileContract string `json:"compile_contract"`
	SnapshotKind    string `json:"snapshot_kind"`
}

type patternSummary struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Revision             string   `json:"revision"`
	CompileContract      string   `json:"compile_contract"`
	TotalSteps           int      `json:"total_steps"`
	NominalDurationMS    int64    `json:"nominal_duration_ms"`
	OnFinish             string   `json:"on_finish"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

type patternList struct {
	SchemaVersion int                `json:"schema_version"`
	Contract      string             `json:"contract"`
	Library       patternLibraryInfo `json:"library"`
	Patterns      []patternSummary   `json:"patterns"`
}

func registerPatternTools(s *mcp.Server, d *daemon) {
	t := tool("list_patterns", "Read the daemon's bounded Pattern library directory through GET /v1/patterns. Takes only an empty object; no id, path, limit, device, ticket or write. Returns pattern_list with accepted:false. Summaries describe server-validated frozen records; they do not prove device support, grant execution, compile an effect, or run a pattern. The displayed absolute library path is never opened by this adapter. Missing/unsupported/unavailable libraries remain errors; no fallback, auto-import or replacement library.", objectSchema(map[string]any{}), true, true)
	t.OutputSchema = patternListOutputSchema()
	mcp.AddTool(s, t, func(ctx context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, output, error) {
		return listPatterns(ctx, d, request.Params.Arguments)
	})
	registerGetPatternTool(s, d)
}

func listPatterns(ctx context.Context, d *daemon, raw json.RawMessage) (*mcp.CallToolResult, output, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var args struct{}
	if !utf8.Valid(raw) {
		return invalid("list_patterns requires an empty UTF-8 object")
	}
	if err := decodeSoftwareWire(raw, &args); err != nil {
		return invalid("list_patterns accepts only an empty object: " + err.Error())
	}
	return result(d.readPatternList(ctx))
}

func (d *daemon) readPatternList(ctx context.Context) output {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+"/v1/patterns", nil)
	if err != nil {
		return errorOutput("invalid_request", err.Error(), 0, false)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return errorOutput("daemon_unreachable", fmt.Sprintf("read Pattern directory: %v", err), 0, false)
	}
	defer response.Body.Close()
	bad := func(err error) output {
		return errorOutput("invalid_daemon_response", err.Error(), response.StatusCode, false)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxPatternResponseBytes+1))
	if err != nil {
		return bad(err)
	}
	if len(data) > maxPatternResponseBytes {
		return bad(errors.New("Pattern directory response exceeds 1 MiB"))
	}
	media := response.Header.Values("Content-Type")
	if len(media) != 1 {
		return bad(errors.New("Pattern directory requires exactly one Content-Type"))
	}
	kind, _, err := mime.ParseMediaType(media[0])
	if err != nil || kind != "application/json" || !utf8.Valid(data) {
		return bad(errors.New("Pattern directory requires UTF-8 application/json"))
	}
	if err := validateRawArguments(data); err != nil {
		return bad(err)
	}
	if err := validateLightUnicodeEscapes(data); err != nil {
		return bad(err)
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
		return bad(errors.New("Pattern directory requires HTTP200"))
	}
	if len(response.Header.Values("Location")) != 0 || len(response.Header.Values("X-NB1-Replayed")) != 0 || len(response.Header.Values("X-NB1-Daemon-Instance-ID")) != 0 {
		return bad(errors.New("Pattern directory unexpectedly contains submission metadata"))
	}
	var value patternList
	if err := decodeSoftwareWire(data, &value); err != nil {
		return bad(err)
	}
	if err := validatePatternList(value); err != nil {
		return bad(err)
	}
	return output{Kind: "pattern_list", Data: value, Accepted: false}
}

var patternID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var patternRevision = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var windowsAbsolutePatternPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
var windowsUNCPatternPath = regexp.MustCompile(`^\\\\[^\\]+\\[^\\]+(?:\\|$)`)

func validatePatternList(value patternList) error {
	library := value.Library
	if value.SchemaVersion != 1 || value.Contract != patternLibraryContract || library.State != "ready" || !library.CreateOnly || library.MaxRecords != 128 || library.MaxPackageBytes != 1<<20 || library.RecordContract != patternRecordContract || library.CompileContract != compileContract || library.SnapshotKind != "nb1-pattern-snapshot" {
		return errors.New("unsupported Pattern directory contract, state or limits")
	}
	// This is syntax only. Never stat/open the daemon's path. The daemon may
	// run on another supported OS, so recognize drive-absolute Windows paths.
	if !filepath.IsAbs(library.Path) && !windowsAbsolutePatternPath.MatchString(library.Path) && !windowsUNCPatternPath.MatchString(library.Path) {
		return errors.New("Pattern library path must be absolute")
	}
	if value.Patterns == nil || len(value.Patterns) > 128 {
		return errors.New("Pattern directory requires at most 128 entries")
	}
	previous := ""
	for _, entry := range value.Patterns {
		if !patternID.MatchString(entry.ID) || entry.ID <= previous || !patternRevision.MatchString(entry.Revision) || entry.CompileContract != compileContract {
			return errors.New("Pattern directory IDs/revisions must be supported, unique and ASCII-sorted")
		}
		previous = entry.ID
		if !utf8.ValidString(entry.Title) || utf8.RuneCountInString(entry.Title) < 1 || utf8.RuneCountInString(entry.Title) > 256 {
			return errors.New("Pattern title requires 1-256 Unicode scalar values")
		}
		if entry.TotalSteps < 1 || entry.TotalSteps > 1024*10000 || entry.NominalDurationMS < int64(entry.TotalSteps)*10 || entry.NominalDurationMS > 86400000 {
			return errors.New("Pattern summary exceeds finite Program limits")
		}
		required := []string{"set", "program", "raw-rgb"}
		switch entry.OnFinish {
		case "hold":
			required = append(required, "hold")
		case "off":
		default:
			return errors.New("unknown Pattern finish behavior")
		}
		if !reflect.DeepEqual(entry.RequiredCapabilities, required) {
			return errors.New("Pattern summary capabilities disagree with its finish behavior")
		}
	}
	return nil
}

func patternListOutputSchema() map[string]any {
	constant := func(value any) map[string]any { return map[string]any{"const": value} }
	text := map[string]any{"type": "string"}
	fields := map[string]any{"path": map[string]any{"type": "string", "minLength": 1, "description": "Daemon-selected absolute display path; adapter never opens it"}, "state": constant("ready"), "create_only": constant(true), "max_records": constant(128), "max_package_bytes": constant(1 << 20), "record_contract": constant(patternRecordContract), "compile_contract": constant(compileContract), "snapshot_kind": constant("nb1-pattern-snapshot")}
	library := objectSchema(fields, "path", "state", "create_only", "max_records", "max_package_bytes", "record_contract", "compile_contract", "snapshot_kind")
	entry := objectSchema(map[string]any{"id": map[string]any{"type": "string", "pattern": patternID.String()}, "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "revision": map[string]any{"type": "string", "pattern": patternRevision.String()}, "compile_contract": constant(compileContract), "total_steps": map[string]any{"type": "integer", "minimum": 1, "maximum": 1024 * 10000}, "nominal_duration_ms": map[string]any{"type": "integer", "minimum": 10, "maximum": 86400000}, "on_finish": map[string]any{"enum": []string{"off", "hold"}}, "required_capabilities": map[string]any{"type": "array", "minItems": 3, "maxItems": 4, "items": text}}, "id", "title", "revision", "compile_contract", "total_steps", "nominal_duration_ms", "on_finish", "required_capabilities")
	data := objectSchema(map[string]any{"schema_version": constant(1), "contract": constant(patternLibraryContract), "library": library, "patterns": map[string]any{"type": "array", "minItems": 0, "maxItems": 128, "items": entry}}, "schema_version", "contract", "library", "patterns")
	problem := objectSchema(map[string]any{"code": text, "message": text, "http_status": map[string]any{"type": "integer"}, "outcome_unknown": constant(false)}, "code", "message")
	out := objectSchema(map[string]any{"kind": map[string]any{"enum": []string{"pattern_list", "error"}}, "accepted": constant(false), "data": data, "error": problem}, "kind", "accepted")
	out["oneOf"] = []any{map[string]any{"properties": map[string]any{"kind": constant("pattern_list")}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}}, map[string]any{"properties": map[string]any{"kind": constant("error")}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}}}
	return out
}
