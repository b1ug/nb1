package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authorAdvRequests struct {
	mu      sync.Mutex
	paths   []string
	bodies  [][]byte
	arrival [][]byte
}

func (r *authorAdvRequests) recordRequest(request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, request.Method+" "+request.URL.RequestURI())
	r.bodies = append(r.bodies, append([]byte(nil), body...))
}
func (r *authorAdvRequests) recordArrival(raw []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arrival = append(r.arrival, append([]byte(nil), raw...))
}
func (r *authorAdvRequests) snapshot() ([]string, [][]byte, [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := append([]string(nil), r.paths...)
	bodies := make([][]byte, len(r.bodies))
	arrival := make([][]byte, len(r.arrival))
	for i := range bodies {
		bodies[i] = append([]byte(nil), r.bodies[i]...)
	}
	for i := range arrival {
		arrival[i] = append([]byte(nil), r.arrival[i]...)
	}
	return paths, bodies, arrival
}

// Record bytes at the actual SDK receiving boundary. CallTool's client may
// compact a RawMessage before transport; its pre-send file whitespace is not
// the oracle for what the server must retain and forward unchanged.
func authorAdvConnect(t *testing.T, endpoint string, requests *authorAdvRequests) *mcp.ClientSession {
	t.Helper()
	d, err := newDaemon(endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	server := newServer(d)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if params, ok := request.GetParams().(*mcp.CallToolParamsRaw); ok && params != nil && isPatternAuthorTool(params.Name) {
					requests.recordArrival(params.Arguments)
				}
			}
			return next(ctx, method, request)
		}
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "author-adversarial-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func authorAdvMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	value, err := parsePatternAuthorValue(raw, maxPatternAuthorResponse, 64)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatal("fixture must be an object")
	}
	return result
}
func authorAdvJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
func authorAdvRehash(t *testing.T, record map[string]any) string {
	t.Helper()
	delete(record, "revision")
	payload, err := canonicalPatternAuthorValue(record, maxPatternAuthorRecord)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte(patternV2RecordContract+"\n"), payload...))
	revision := fmt.Sprintf("sha256:%x", digest)
	record["revision"] = revision
	return revision
}

// Intentionally use ordinary generic decoding for both client result channels.
// Exact arbitrary-precision source values must survive as record_json strings,
// without a UseNumber client masking a server-side float64 conversion.
func authorAdvGenericResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured map[string]any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 {
		t.Fatal("expected one text mirror", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("result has no text mirror")
	}
	var mirrored map[string]any
	if err := json.Unmarshal([]byte(text.Text), &mirrored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(structured, mirrored) {
		t.Fatal("generic structured/text client results diverge", structured, mirrored)
	}
	return structured
}
func authorAdvSaveFromResult(t *testing.T, result *mcp.CallToolResult) patternAuthorSaveOutput {
	t.Helper()
	generic := authorAdvGenericResult(t, result)
	if _, exists := generic["request_ticket"]; exists {
		t.Fatal("publication acquired an execution ticket")
	}
	if problem, ok := generic["error"].(map[string]any); ok {
		if _, exists := problem["outcome_unknown"]; exists {
			t.Fatal("publication reused execution uncertainty")
		}
	}
	var value patternAuthorSaveOutput
	if err := json.Unmarshal(authorAdvJSON(t, generic), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPatternAuthorAdversarialUnknownRecordsSurviveGenericSDK(t *testing.T) {
	for _, kind := range []string{"unsupported_source_exact_values", "unsupported_recipe"} {
		t.Run(kind, func(t *testing.T) {
			get := authorAdvMap(t, authorFixture(t, "responses/get-unknown-recipe.json"))
			record := get["record"].(map[string]any)
			listing := authorAdvMap(t, authorFixture(t, "responses/list-read-only.json"))
			row := listing["patterns"].([]any)[0].(map[string]any)
			if kind == "unsupported_source_exact_values" {
				source := record["source"].(map[string]any)
				source["contract"] = "future/light-v99"
				source["action"] = map[string]any{"counter": json.Number("9007199254740993"), "maximum": json.Number("9223372036854775807"), "fraction": json.Number("12.340000"), "name": "灯💡<&>é\n"}
				record["compiled"].(map[string]any)["normalized_action"] = map[string]any{"opaque": json.Number("9007199254740993"), "name": "灯💡<&>é\n"}
				revision := authorAdvRehash(t, record)
				get["verification"] = map[string]any{"verified": false, "reason": "unsupported_source"}
				row["source_contract"] = "future/light-v99"
				row["verification"] = get["verification"]
				row["pattern_ref"].(map[string]any)["revision"] = revision
			}
			canonical, err := canonicalPatternAuthorValue(record, maxPatternAuthorRecord)
			if err != nil {
				t.Fatal(err)
			}
			body, listBody := authorAdvJSON(t, get), authorAdvJSON(t, listing)
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					t.Error("read-only operation wrote", r.Method)
				}
				if r.URL.Path == "/v2/patterns" {
					w.Write(listBody)
				} else {
					w.Write(body)
				}
			}))
			defer server.Close()
			session := authorAdvConnect(t, server.URL, requests)
			args := authorAdvJSON(t, map[string]any{"id": record["id"], "expected_revision": record["revision"]})
			result := authorCall(t, session, "get_pattern_v2", args)
			generic := authorAdvGenericResult(t, result)
			if result.IsError || generic["accepted"] != false || generic["kind"] != "pattern_record_v2" {
				t.Fatal(generic)
			}
			data := generic["data"].(map[string]any)
			if data["record_json"] != string(canonical) || data["verification"].(map[string]any)["verified"] != false {
				t.Fatal("read-only Record was rounded or promoted", data)
			}
			if kind == "unsupported_source_exact_values" {
				text := data["record_json"].(string)
				for _, exact := range []string{"9007199254740993", "9223372036854775807", "12.34", "灯💡<&>é", `\u000a`} {
					if !strings.Contains(text, exact) {
						t.Fatal("lossless Record string omitted exact source value", exact, text)
					}
				}
			}
			result = authorCall(t, session, "list_patterns_v2", []byte(`{}`))
			generic = authorAdvGenericResult(t, result)
			if result.IsError || generic["accepted"] != false {
				t.Fatal(generic)
			}
			data = generic["data"].(map[string]any)
			if data["library"].(map[string]any)["state"] != "read_only" || len(data["patterns"].([]any)) != 2 {
				t.Fatal("read-only advertisement was lost", data)
			}
			paths, _, _ := requests.snapshot()
			if !reflect.DeepEqual(paths, []string{"GET /v2/patterns/orbit-red-blue", "GET /v2/patterns"}) {
				t.Fatal("get/list performed fanout or execution", paths)
			}
		})
	}
}

func TestPatternAuthorAdversarialGetRejectsInvalidIntegrityAndUnicodeKeys(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"typed-execution-digest": func(value map[string]any) { value["execution_digest"] = "sha256:" + strings.Repeat("0", 64) },
		"record-revision": func(value map[string]any) {
			value["record"].(map[string]any)["revision"] = "sha256:" + strings.Repeat("f", 64)
		},
		"unknown-source-unicode-key": func(value map[string]any) {
			record := value["record"].(map[string]any)
			source := record["source"].(map[string]any)
			source["contract"] = "future/light-v99"
			source["action"] = map[string]any{"灯": json.Number("1")}
			value["verification"] = map[string]any{"verified": false, "reason": "unsupported_source"}
		},
		"unknown-execution-contract": func(value map[string]any) {
			value["record"].(map[string]any)["compiled"].(map[string]any)["execution"].(map[string]any)["contract"] = "future/program-v99"
		},
		"false-verification": func(value map[string]any) {
			value["verification"] = map[string]any{"verified": false, "reason": "verified"}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			body := authorAdvMap(t, authorFixture(t, "responses/get-orbit-red-blue.json"))
			mutate(body)
			encoded := authorAdvJSON(t, body)
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", "application/json")
				w.Write(encoded)
			}))
			defer server.Close()
			result := authorCall(t, authorAdvConnect(t, server.URL, requests), "get_pattern_v2", []byte(`{"id":"orbit-red-blue"}`))
			generic := authorAdvGenericResult(t, result)
			if !result.IsError || generic["kind"] != "error" || generic["accepted"] != false || generic["data"] != nil {
				t.Fatal("untrusted Record yielded partial success", generic)
			}
			paths, _, _ := requests.snapshot()
			if !reflect.DeepEqual(paths, []string{"GET /v2/patterns/orbit-red-blue"}) {
				t.Fatal("invalid Get retried or fanned out", paths)
			}
		})
	}
}

func TestPatternAuthorAdversarialArgumentsRejectBeforeHTTP(t *testing.T) {
	for _, tool := range []string{"preview_pattern_draft_v2", "save_pattern_v2"} {
		t.Run(tool, func(t *testing.T) {
			fixture := "requests/preview-orbit-red-blue.json"
			if tool == "save_pattern_v2" {
				fixture = "requests/save-orbit-red-blue.json"
			}
			base := authorFixture(t, fixture)
			mutate := func(change func(map[string]any)) []byte {
				value := authorAdvMap(t, base)
				change(value)
				return authorAdvJSON(t, value)
			}
			cases := map[string][]byte{
				"integer-lexeme": []byte(strings.Replace(string(base), `"cycles": 3`, `"cycles": 3.0`, 1)),
				"null-source":    mutate(func(value map[string]any) { value["source"] = nil }),
				"unicode-key": mutate(func(value map[string]any) {
					value["source"].(map[string]any)["action"].(map[string]any)["灯"] = "future"
				}),
				"unknown-source":  mutate(func(value map[string]any) { value["source"].(map[string]any)["contract"] = "future/light-v99" }),
				"execution-field": mutate(func(value map[string]any) { value["frames"] = []any{} }),
				"device-field":    mutate(func(value map[string]any) { value["serial"] = "20005601" }),
				"ticket-field":    mutate(func(value map[string]any) { value["request_id"] = "unrelated" }),
				"duplicate-title": []byte(strings.Replace(string(base), "{", `{"title":"duplicate",`, 1)),
				"over-budget":     mutate(func(value map[string]any) { value["title"] = strings.Repeat("x", maxPatternAuthorArguments+1) }),
			}
			if tool == "save_pattern_v2" {
				cases["missing-revision"] = mutate(func(value map[string]any) { delete(value, "expected_revision") })
			} else {
				cases["forbidden-revision"] = mutate(func(value map[string]any) { value["expected_revision"] = "sha256:" + strings.Repeat("f", 64) })
			}
			for name, raw := range cases {
				t.Run(name, func(t *testing.T) {
					requests := &authorAdvRequests{}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.recordRequest(r)
						http.Error(w, "unexpected request", 500)
					}))
					defer server.Close()
					result := authorCall(t, authorAdvConnect(t, server.URL, requests), tool, raw)
					generic := authorAdvGenericResult(t, result)
					if !result.IsError || generic["accepted"] != false || generic["error"] == nil {
						t.Fatal("invalid authoring arguments accepted", generic)
					}
					if tool == "save_pattern_v2" {
						value := authorAdvSaveFromResult(t, result)
						if value.PublicationAttempted || value.PublicationUnknown || value.PublicationConfirmed {
							t.Fatal("invalid local arguments created publication uncertainty", value)
						}
					}
					paths, _, _ := requests.snapshot()
					if len(paths) != 0 {
						t.Fatal("invalid authoring reached HTTP", paths)
					}
				})
			}
		})
	}
}
