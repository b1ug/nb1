package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func authorFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("testdata", "pattern-author-v2", name))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func authorCall(t *testing.T, s *mcp.ClientSession, name string, raw []byte) *mcp.CallToolResult {
	t.Helper()
	r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(raw)})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func authorReadFromResult(t *testing.T, r *mcp.CallToolResult) patternAuthorReadOutput {
	t.Helper()
	b, e := json.Marshal(r.StructuredContent)
	if e != nil {
		t.Fatal(e)
	}
	var v patternAuthorReadOutput
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestPatternAuthorCatalogFreezesCompleteLegacySixteen(t *testing.T) {
	data, e := os.ReadFile("testdata/pattern-author-legacy16-tools.json")
	if e != nil {
		t.Fatal(e)
	}
	var old []map[string]any
	if e = json.Unmarshal(data, &old); e != nil {
		t.Fatal(e)
	}
	session := connect(t, "http://127.0.0.1:1")
	list, e := session.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(list.Tools) != 22 || len(old) != 16 {
		t.Fatal(len(list.Tools), len(old))
	}
	byName := map[string]map[string]any{}
	for _, v := range old {
		byName[v["name"].(string)] = v
	}
	seen := 0
	for _, tool := range list.Tools {
		expected, ok := byName[tool.Name]
		if !ok {
			continue
		}
		b, e := json.Marshal(tool)
		if e != nil {
			t.Fatal(e)
		}
		var got map[string]any
		if e = json.Unmarshal(b, &got); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("legacy complete schema changed: %s\ngot=%s\nwant=%v", tool.Name, b, expected)
		}
		seen++
	}
	if seen != 16 {
		t.Fatal(seen)
	}
}
func TestPatternAuthorMemorySDKReadAndPreviewFixtures(t *testing.T) {
	for _, name := range []string{"pulse-lime", "orbit-red-blue"} {
		t.Run(name, func(t *testing.T) {
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.RequestURI())
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v2/patterns":
					w.Write(authorFixture(t, "responses/list-ready.json"))
				case "/v2/patterns/preview":
					w.Write(authorFixture(t, "responses/preview-"+name+".json"))
				default:
					w.Write(authorFixture(t, "responses/get-"+name+".json"))
				}
			}))
			defer server.Close()
			session := connect(t, server.URL)
			list := authorReadFromResult(t, authorCall(t, session, "list_patterns_v2", []byte(`{}`)))
			if list.Error != nil || list.Accepted || list.Kind != "pattern_library_v2" {
				t.Fatal(list)
			}
			get := authorReadFromResult(t, authorCall(t, session, "get_pattern_v2", []byte(`{"id":"`+name+`"}`)))
			if get.Error != nil || get.Kind != "pattern_record_v2" {
				t.Fatal(get)
			}
			preview := authorReadFromResult(t, authorCall(t, session, "preview_pattern_draft_v2", authorFixture(t, "requests/preview-"+name+".json")))
			if preview.Error != nil || preview.Kind != "pattern_draft_preview_v2" {
				t.Fatal(preview)
			}
			if !reflect.DeepEqual(calls, []string{"GET /v2/patterns", "GET /v2/patterns/" + name, "POST /v2/patterns/preview"}) {
				t.Fatal(calls)
			}
		})
	}
}

func TestPatternAuthorOutputBudgetBoundsBothSDKChannels(t *testing.T) {
	var value patternAuthorListData
	if err := json.Unmarshal(authorFixture(t, "responses/list-empty.json"), &value); err != nil {
		t.Fatal(err)
	}
	// The HTTP payload fits 2MiB, but HTML escaping and the text fallback could
	// otherwise amplify this untrusted display-only path far beyond 4MiB.
	value.Library.Path = "/" + strings.Repeat("<", 1<<20)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	result := authorCall(t, connect(t, server.URL), "list_patterns_v2", []byte(`{}`))
	got := authorReadFromResult(t, result)
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxPatternAuthorOutput || len(encoded) > 1024 ||
		!result.IsError || got.Error == nil || got.Error.Code != "adapter_output_limit" || got.Data != nil || calls != 1 {
		t.Fatal("large metadata escaped the combined output budget", len(encoded), got, calls, err)
	}
}
