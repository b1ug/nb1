// Command contract-fixtures generates documentation examples from pure codecs
// and no-service HTTP handlers, including an exact in-memory record resolver.
// It never creates an Engine, filesystem Store, listener, device, admission
// receipt or actual Job. P1 records come from the pure source verifier; all
// synthetic Job/Envelope examples are explicitly wire-shape-only.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

const sourceBase = "f8bb267b5cf8b952ce6a8cdbd86e8b18971028ae"

type source struct {
	Kind          string `json:"kind"`
	BaseCommit    string `json:"base_commit"`
	Function      string `json:"function,omitempty"`
	Method        string `json:"method,omitempty"`
	Path          string `json:"path,omitempty"`
	RequestSample string `json:"request_sample,omitempty"`
}

type sample struct {
	Name   string      `json:"name"`
	Schema string      `json:"schema"`
	Source source      `json:"source"`
	Value  interface{} `json:"value"`
}

type actionCase struct {
	name, schema, normalizedSchema, action string
}

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	var samples []sample
	pure := func(name, schema, function, request string, value interface{}) {
		samples = append(samples, sample{name, schema, source{Kind: "pure-codec", BaseCommit: sourceBase, Function: function, RequestSample: request}, value})
	}
	handler := httpapi.New(nil)
	httpKind := "httptest-no-service"
	httpFunction := "httpapi.New(nil).ServeHTTP + httptest.NewRecorder"
	addHTTP := func(name, schema, method, path, request string, body []byte) ([]byte, error) {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			return nil, fmt.Errorf("%s: HTTP status %d: %s", name, w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			return nil, fmt.Errorf("%s: unexpected common response headers: %v", name, w.Header())
		}
		if w.Header().Get("Location") != "" || w.Header().Get("X-NB1-Daemon-Instance-ID") != "" || w.Header().Get("X-NB1-Replayed") != "" {
			return nil, fmt.Errorf("%s: pure preview unexpectedly returned admission headers", name)
		}
		responseBody := append([]byte(nil), w.Body.Bytes()...)
		if !json.Valid(responseBody) {
			return nil, fmt.Errorf("%s: handler returned invalid JSON", name)
		}
		samples = append(samples, sample{name, schema,
			source{Kind: httpKind, BaseCommit: sourceBase, Function: httpFunction, Method: method, Path: path, RequestSample: request},
			json.RawMessage(responseBody)})
		return responseBody, nil
	}

	actions := []actionCase{
		{"set", "LightActionSet", "NormalizedLightActionSet", `{"kind":"set","color":"red"}`},
		{"off", "LightActionOff", "NormalizedLightActionOff", `{"kind":"off"}`},
		{"fade", "LightActionFade", "NormalizedLightActionFade", `{"kind":"fade","color":"blue","duration_ms":10}`},
		{"blink", "LightActionBlink", "NormalizedLightActionBlink", `{"kind":"blink","color":"lime","on_ms":100,"off_ms":150,"cycles":2}`},
		{"breathe", "LightActionBreathe", "NormalizedLightActionBreathe", `{"kind":"breathe","color":"#f80","period_ms":2000,"cycles":1}`},
		{"pulse", "LightActionPulse", "NormalizedLightActionPulse", `{"kind":"pulse","color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50.5},"attack_ms":60,"decay_ms":240,"rest_ms":700,"cycles":2}`},
		{"alternate", "LightActionAlternate", "NormalizedLightActionAlternate", `{"kind":"alternate","colors":["red",{"space":"rgb8","r":0,"g":0,"b":255}],"leds":[2,1],"period_ms":2000,"cycles":2,"transition":"orbit"}`},
		{"rainbow", "LightActionRainbow", "NormalizedLightActionRainbow", `{"kind":"rainbow","led":1,"period_ms":3000,"cycles":1,"on_finish":"hold"}`},
	}
	for _, c := range actions {
		input := json.RawMessage(c.action)
		result, err := lightaction.CompileJSON(input)
		if err != nil {
			return fmt.Errorf("%s compile: %w", c.name, err)
		}
		preview, err := control.Preview(&result.Program, nil)
		if err != nil {
			return fmt.Errorf("%s pure preview: %w", c.name, err)
		}
		if result.Contract != lightaction.Contract || result.ProgramCompileContract != control.CompileContract ||
			result.SchemaVersion != 1 || preview.TotalSteps != result.TotalSteps || preview.NominalDurationMS != result.NominalDurationMS {
			return fmt.Errorf("%s: compilation and pure preview disagree", c.name)
		}
		requestName := "light-action-" + c.name
		pure(requestName, c.schema, "lightaction.CompileJSON input", "", input)
		pure("light-normalized-"+c.name, c.normalizedSchema, "lightaction.CompileJSON.Action", requestName, result.Action)
		pure("light-compilation-"+c.name, "LightCompilation", "lightaction.CompileJSON + control.Preview", requestName, result)
		if c.name == "breathe" {
			body, err := json.Marshal(struct {
				Contract string          `json:"contract"`
				Action   json.RawMessage `json:"action"`
			}{lightaction.Contract, input})
			if err != nil {
				return err
			}
			if _, err := addHTTP("light-preview-breathe", "LightPreviewResponse", http.MethodPost, "/v1/light/preview", requestName, body); err != nil {
				return err
			}
		}
	}

	for _, segments := range []int{4, 8} {
		input := json.RawMessage(fmt.Sprintf(`{"kind":"orbit","colors":["red",{"space":"hsv","h_deg":240,"s_pct":100,"v_pct":100}],"leds":[2,1],"period_ms":3001,"cycles":2,"segments":%d,"on_finish":"hold"}`, segments))
		result, err := lightactionv2.CompileJSON(input)
		if err != nil {
			return fmt.Errorf("orbit %d compile: %w", segments, err)
		}
		preview, err := control.PreviewFrameProgram(result.Program)
		if err != nil {
			return fmt.Errorf("orbit %d pure preview: %w", segments, err)
		}
		if result.SchemaVersion != 1 || result.Contract != lightactionv2.Contract || result.ProgramCompileContract != control.FrameContract ||
			preview.TotalFrames != result.TotalFrames || preview.PlannedTargetWrites != result.PlannedTargetWrites || preview.NominalDurationMS != result.NominalDurationMS {
			return fmt.Errorf("orbit %d: compilation and pure preview disagree", segments)
		}
		name := fmt.Sprintf("orbit-segments%d", segments)
		pure(name+"-action", "OrbitAction", "lightactionv2.CompileJSON input", "", input)
		pure(name+"-normalized", "NormalizedOrbitAction", "lightactionv2.CompileJSON.Action", name+"-action", result.Action)
		pure(name+"-compilation", "OrbitCompilation", "lightactionv2.CompileJSON + control.PreviewFrameProgram", name+"-action", result)
		body, err := json.Marshal(struct {
			Contract string          `json:"contract"`
			Action   json.RawMessage `json:"action"`
		}{lightactionv2.Contract, input})
		if err != nil {
			return err
		}
		if _, err := addHTTP(name+"-preview", "OrbitPreviewResponse", http.MethodPost, "/v2/light/preview", name+"-action", body); err != nil {
			return err
		}
	}

	frameRequest := json.RawMessage(`{"contract":"nb1-frame-program/experimental-v1","initial":[{"led":2,"color":"#ABCDEF"}],"frames":[{"targets":[{"led":2,"color":"#010203"}],"fade_ms":0,"interval_ms":50}]}`)
	frameInput := control.FrameProgram{Contract: control.FrameContract,
		Initial: []control.FrameTarget{{LED: 2, Color: "#ABCDEF"}},
		Frames:  []control.Frame{{Targets: []control.FrameTarget{{LED: 2, Color: "#010203"}}, FadeMS: 0, IntervalMS: 50}},
	}
	framePreview, err := control.PreviewFrameProgram(frameInput)
	if err != nil {
		return fmt.Errorf("standalone frame pure preview: %w", err)
	}
	pure("frame-program-request", "FrameProgramRequest", "control.PreviewFrameProgram input; omitted repeat/on_finish", "", frameRequest)
	pure("frame-program-normalized", "FrameProgram", "control.PreviewFrameProgram.Program", "frame-program-request", framePreview.Program)
	body, err := json.Marshal(struct {
		Program json.RawMessage `json:"program"`
	}{frameRequest})
	if err != nil {
		return err
	}
	response, err := addHTTP("frame-program-preview", "FramePreview", http.MethodPost, "/v1/frame-programs/preview", "frame-program-request", body)
	if err != nil {
		return err
	}
	var actualFrame struct {
		Program control.FrameProgram `json:"program"`
	}
	if err := json.Unmarshal(response, &actualFrame); err != nil {
		return err
	}
	wantProgram, err := json.Marshal(framePreview.Program)
	if err != nil {
		return err
	}
	actualProgram, err := json.Marshal(actualFrame.Program)
	if err != nil {
		return err
	}
	if !bytes.Equal(wantProgram, actualProgram) {
		return fmt.Errorf("Frame HTTP normalization disagrees with pure PreviewFrameProgram")
	}
	if _, err := addHTTP("frame-capabilities-no-service", "FrameCapabilities", http.MethodGet, "/v1/frame-capabilities", "", nil); err != nil {
		return err
	}
	created := time.Date(2026, time.November, 9, 19, 0, 0, 0, time.UTC)
	started, finished := created.Add(time.Second), created.Add(2*time.Second)
	samples = append(samples, sample{
		Name: "frame-job-synthetic-failed-wire-only", Schema: "FrameJob",
		Source: source{Kind: "synthetic-Go-wire-struct", BaseCommit: sourceBase,
			Function:      "control.FrameJob struct literal; synthetic wire shape only, no Engine or submission",
			RequestSample: "frame-program-normalized"},
		Value: control.FrameJob{
			ID: "framejob_00000000000000000000000000000001", Device: "fixture-device", Status: "failed",
			Program: framePreview.Program, CompletedFrames: 0, TotalFrames: framePreview.TotalFrames,
			CompletedTargetWrites: len(framePreview.Program.Initial), PlannedTargetWrites: framePreview.PlannedTargetWrites,
			CreatedAt: created, StartedAt: &started, FinishedAt: &finished,
			Error: "Synthetic wire-shape example only; no Engine, submission or execution occurred",
		},
	})

	// New P6 samples use source-verified records in memory, not a filesystem
	// library. A nil service proves preview never needs an Engine or ticket.
	for _, kind := range []string{"program", "frame"} {
		contract, action := lightaction.Contract, `{"kind":"set","color":"lime","on_finish":"hold"}`
		if kind == "frame" {
			contract, action = lightactionv2.Contract, `{"kind":"orbit","colors":["red","blue"],"period_ms":3000,"segments":4,"cycles":1,"on_finish":"hold"}`
		}
		record, err := patternsv2.NewRecord("doc-"+kind, "文档纯来源夹具", patternsv2.Source{Kind: "light_action", Contract: contract, Action: json.RawMessage(action)})
		if err != nil {
			return fmt.Errorf("P6 %s source: %w", kind, err)
		}
		ref := patternidentity.Ref{RecordContract: record.Contract, ID: record.ID, Revision: record.Revision}
		digest, err := patternsv2.ExecutionDigest(record)
		if err != nil {
			return err
		}
		pure("pattern-ref-"+kind, "PatternRefV2", "patternsv2.NewRecord exact identity", "", ref)
		previewBody := map[string]interface{}{"pattern_ref": ref}
		pure("pattern-ref-preview-request-"+kind, "PatternRefPreviewRequestV2", "P6 typed request from source-verified Ref", "pattern-ref-"+kind, previewBody)
		pure("pattern-submit-request-"+kind, "PatternSubmitRequestV2", "P6 request wire only; syntactic ticket not prepared/admitted", "pattern-ref-"+kind, map[string]interface{}{"pattern_ref": ref, "policy": "queue", "request_id": "req_60000_00000000000000000000000000000001", "expected_daemon_instance_id": "daemon_00000000000000000000000000000001"})
		pure("pattern-cancel-request-"+kind, "PatternCancelRequestV2", "P6 request wire only; no actual receipt/cancellation", "pattern-ref-"+kind, map[string]interface{}{"pattern_ref": ref, "expected_daemon_instance_id": "daemon_00000000000000000000000000000001", "confirmed_job_id": map[string]string{"program": "job_00000000000000000000000000000001", "frame": "framejob_00000000000000000000000000000001"}[kind]})
		fixture := &recordFixture{record: record}
		handler = httpapi.New(nil, httpapi.WithPatternStoreV2(fixture))
		httpKind = "httptest-in-memory-verified-record-no-service"
		httpFunction = "patternsv2.NewRecord + in-memory exact Resolve + httpapi.New(nil).ServeHTTP + Recorder; no FS/Engine"
		for _, target := range []bool{false, true} {
			name := "pattern-ref-preview-" + kind
			body := map[string]interface{}{"pattern_ref": ref}
			if target {
				body["serial"] = "fixture-device"
				name += "-selected"
			}
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			if _, err = addHTTP(name, "PatternRefPreviewV2", http.MethodPost, "/v2/patterns/preview-ref", "pattern-ref-preview-request-"+kind, b); err != nil {
				return err
			}
		}
		if fixture.calls != 2 {
			return fmt.Errorf("P6 preview resolved %d times", fixture.calls)
		}
		envelope := control.PatternJobEnvelope{PatternRef: ref, ExecutionDigest: digest, JobKind: kind}
		if kind == "program" {
			preview, err := control.Preview(record.Compiled.Execution.Program, nil)
			if err != nil {
				return err
			}
			envelope.ProgramJob = &control.Job{ID: "job_00000000000000000000000000000001", Device: "fixture-device", Status: "failed", Program: preview.Program, TotalSteps: preview.TotalSteps, CreatedAt: created, StartedAt: &started, FinishedAt: &finished, Error: "Synthetic Pattern wire envelope only; no actual receipt/admission/execution"}
		} else {
			preview, err := control.PreviewFrameProgram(*record.Compiled.Execution.FrameProgram)
			if err != nil {
				return err
			}
			envelope.FrameJob = &control.FrameJob{ID: "framejob_00000000000000000000000000000001", Device: "fixture-device", Status: "failed", Program: preview.Program, TotalFrames: preview.TotalFrames, PlannedTargetWrites: preview.PlannedTargetWrites, CreatedAt: created, StartedAt: &started, FinishedAt: &finished, Error: "Synthetic Pattern wire envelope only; no actual receipt/admission/execution"}
		}
		samples = append(samples, sample{Name: "pattern-envelope-synthetic-" + kind, Schema: "PatternJobEnvelopeV2", Source: source{Kind: "synthetic-Go-wire-envelope", BaseCommit: sourceBase, Function: "control.PatternJobEnvelope struct literal from pure verified P1 record; no admission", RequestSample: "pattern-ref-" + kind}, Value: envelope})
	}

	if len(samples) != 52 {
		return fmt.Errorf("generated %d samples, expected 52", len(samples))
	}
	seen := make(map[string]bool)
	for _, s := range samples {
		if seen[s.Name] || s.Source.BaseCommit != sourceBase || s.Schema == "" {
			return fmt.Errorf("invalid/duplicate sample metadata: %s", s.Name)
		}
		seen[s.Name] = true
	}
	output, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		return err
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("cannot locate fixture generator")
	}
	path := filepath.Join(filepath.Dir(file), "actual-samples.json")
	if err := os.WriteFile(path, append(output, '\n'), 0644); err != nil {
		return err
	}
	// Probe conditional static resources independently of JSON model samples.
	// This works with either complete embedded profile: old bundle =>404,
	// extended bundle =>200 bytes. Neither probe creates a job or listener.
	resourceHandler := httpapi.New(nil)
	var observations []map[string]interface{}
	for _, asset := range []struct{ Path, File, Media string }{{"/demo/patterns.html", "patterns.html", "text/html; charset=utf-8"}, {"/demo/patterns.js", "patterns.js", "text/javascript; charset=utf-8"}, {"/demo/patterns.css", "patterns.css", "text/css; charset=utf-8"}} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+asset.Path, nil)
		w := httptest.NewRecorder()
		resourceHandler.ServeHTTP(w, request)
		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			return fmt.Errorf("conditional Web probe %s returned%d: %s", asset.Path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			return fmt.Errorf("conditional Web probe lost common headers")
		}
		for _, key := range []string{"Location", "X-NB1-Replayed", "X-NB1-Daemon-Instance-ID"} {
			if len(w.Header().Values(key)) != 0 {
				return fmt.Errorf("conditional Web probe has receipt metadata")
			}
		}
		row := map[string]interface{}{"method": "GET", "path": asset.Path, "asset": asset.File, "status": w.Code, "content_type": w.Header().Get("Content-Type"), "bytes": w.Body.Len(), "body_sha256": fmt.Sprintf("%x", sha256.Sum256(w.Body.Bytes())), "headers": w.Header()}
		if w.Code == http.StatusNotFound {
			var problem httpapi.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || problem.Error.Code != "not_found" {
				return fmt.Errorf("conditional Web404 is not standard Error")
			}
			row["error"] = problem
		} else if w.Header().Get("Content-Type") != asset.Media || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			return fmt.Errorf("conditional Web200 has invalid security/media headers")
		}
		observations = append(observations, row)
	}
	probeOutput, err := json.MarshalIndent(map[string]interface{}{"host_source": "56c4aef153c560c68845e89ebdaa15bc1323eeb2", "scope": "direct Recorder, no service/Store/listener/device; status conditional on this binary embedded bundle", "observations": observations}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(file), "resource-observations.json"), append(probeOutput, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("generated %d samples and3 conditional static observations at %s\n", len(samples), path)
	return nil
}

// recordFixture exposes only exact in-memory resolution. Any unexpected Path,
// list/get/export/import call dispatches to a nil embedded interface and fails.
type recordFixture struct {
	httpapi.PatternStoreV2
	record patternsv2.Record
	calls  int
}

func (f *recordFixture) Resolve(id, revision string) (patternsv2.Record, error) {
	f.calls++
	if id != f.record.ID || revision != f.record.Revision {
		return patternsv2.Record{}, patternstore.ErrNotFound
	}
	return f.record, nil
}
