package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func TestListPatternsV2ActualStoreDoesNotCreateOrExecute(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	handler := httpapi.New(nil, httpapi.WithPatternDirectoryV2(dir))
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	empty, err := client.ListPatternsV2(context.Background())
	if err != nil || empty.Patterns == nil || len(empty.Patterns) != 0 || empty.Library.Path != dir || empty.Library.State != "ready" {
		t.Fatal(empty, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("listing an absent library created a directory", err)
	}
	for _, name := range []string{"pulse-lime", "orbit-red-blue"} {
		if _, err := client.SavePatternV2(context.Background(), clientPatternV2Fixture(t, "requests/save-"+name+".json")); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := client.ListPatternsV2(context.Background())
	if err != nil || len(listed.Patterns) != 2 || listed.Patterns[0].PatternRef.ID != "orbit-red-blue" || listed.Patterns[1].PatternRef.ID != "pulse-lime" {
		t.Fatal(listed, err)
	}
	want := []string{"GET /v2/patterns", "POST /v2/patterns", "POST /v2/patterns", "GET /v2/patterns"}
	if !reflect.DeepEqual(requests, want) || listed.PatternExecution.Available || listed.PatternExecution.Reason != "pattern_service_unsupported" {
		t.Fatal("listing fetched records, prepared or played", requests, listed)
	}
}

func TestListPatternsV2PreservesReadOnlySummariesAndForeignPaths(t *testing.T) {
	for _, fixture := range []struct {
		name, file string
		future     bool
	}{
		{"ready", "list-ready.json", false},
		{"unknown-recipe", "list-read-only.json", false},
		{"unknown-source", "list-read-only.json", true},
		{"empty", "list-empty.json", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var want PatternListV2
			body := clientPatternV2Fixture(t, "responses/"+fixture.file)
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatal(err)
			}
			// Display-only Windows path on this host: do not create or inspect it.
			want.Library.Path = `C:\unavailable\daemon-owned\patterns-v2`
			if fixture.future {
				want.Patterns[0].SourceContract = "future/light-v99"
				want.Patterns[0].Verification = patternsv2.Support{Reason: "unsupported_source"}
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.RequestURI() != "/v2/patterns" || r.ContentLength != 0 {
					t.Error("non-listing request", r.Method, r.URL, r.ContentLength)
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(want)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			got, err := client.ListPatternsV2(context.Background())
			if err != nil || !reflect.DeepEqual(got, want) || calls != 1 {
				t.Fatal("lost unknown summary or performed extra I/O", got, err, calls)
			}
		})
	}
}

func TestListPatternsV2RejectsUntrustedListWithoutPartialResults(t *testing.T) {
	base := clientPatternV2Fixture(t, "responses/list-ready.json")
	mutate := func(fn func(map[string]interface{})) []byte {
		var value map[string]interface{}
		if err := json.Unmarshal(base, &value); err != nil {
			t.Fatal(err)
		}
		fn(value)
		out, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	row := func(v map[string]interface{}) map[string]interface{} {
		return v["patterns"].([]interface{})[0].(map[string]interface{})
	}
	tests := map[string][]byte{
		"contract":       mutate(func(v map[string]interface{}) { v["contract"] = "future/v99" }),
		"null":           mutate(func(v map[string]interface{}) { v["patterns"] = nil }),
		"missing":        mutate(func(v map[string]interface{}) { delete(v, "pattern_execution") }),
		"unknown":        mutate(func(v map[string]interface{}) { v["request_id"] = "not-a-task" }),
		"unsorted":       mutate(func(v map[string]interface{}) { a := v["patterns"].([]interface{}); a[0], a[1] = a[1], a[0] }),
		"duplicate-id":   mutate(func(v map[string]interface{}) { v["patterns"] = []interface{}{row(v), row(v)} }),
		"limit":          mutate(func(v map[string]interface{}) { v["library"].(map[string]interface{})["max_records"] = 129 }),
		"readonly-state": mutate(func(v map[string]interface{}) { v["library"].(map[string]interface{})["state"] = "read_only" }),
		"relative-path":  mutate(func(v map[string]interface{}) { v["library"].(map[string]interface{})["path"] = "relative" }),
		"source-token":   mutate(func(v map[string]interface{}) { row(v)["source_contract"] = "future/\nmalformed" }),
		"long-source":    mutate(func(v map[string]interface{}) { row(v)["source_contract"] = strings.Repeat("f", 129) }),
		"digest":         mutate(func(v map[string]interface{}) { row(v)["execution_digest"] = "sha256:wrong" }),
		"ref-contract": mutate(func(v map[string]interface{}) {
			row(v)["pattern_ref"].(map[string]interface{})["record_contract"] = "nb1-pattern-record/v1"
		}),
		"ref-id":                mutate(func(v map[string]interface{}) { row(v)["pattern_ref"].(map[string]interface{})["id"] = "../path" }),
		"title":                 mutate(func(v map[string]interface{}) { row(v)["title"] = strings.Repeat("灯", 257) }),
		"kind-contract":         mutate(func(v map[string]interface{}) { row(v)["execution_kind"] = "program" }),
		"unknown-kind":          mutate(func(v map[string]interface{}) { row(v)["execution_kind"] = "future" }),
		"verified-future":       mutate(func(v map[string]interface{}) { row(v)["source_contract"] = "future/v99" }),
		"false-verified-reason": mutate(func(v map[string]interface{}) { row(v)["verification"].(map[string]interface{})["verified"] = false }),
		"wrong-availability":    mutate(func(v map[string]interface{}) { v["pattern_execution"].(map[string]interface{})["available"] = true }),
		"duplicate-json-key":    append(append([]byte(nil), base[:len(base)-2]...), []byte(`,"schema_version":2}`)...),
		"surrogate":             []byte(strings.Replace(string(base), "红蓝环绕", `\ud800`, 1)),
		"trailing":              append(append([]byte(nil), base...), []byte(" {}")...),
		"over-response-budget":  []byte(strings.Repeat(" ", (2<<20)+1)),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.Write(body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			got, err := client.ListPatternsV2(context.Background())
			var malformed *DecodeError
			if !errors.As(err, &malformed) || !reflect.DeepEqual(got, PatternListV2{}) || calls != 1 {
				t.Fatal("untrusted summary accepted or followed up", got, err, calls)
			}
		})
	}
}

func TestListPatternsV2RejectsExecutionMetadataEvenEmpty(t *testing.T) {
	body := clientPatternV2Fixture(t, "responses/list-empty.json")
	for _, header := range []string{"Location", "X-NB1-Daemon-Instance-ID", "X-NB1-Replayed"} {
		for _, value := range []string{"", "false", "metadata"} {
			t.Run(header+"/"+value, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set(header, value)
					w.Write(body)
				}))
				defer server.Close()
				client, _ := New(server.URL)
				defer client.Close()
				got, err := client.ListPatternsV2(context.Background())
				if err == nil || !reflect.DeepEqual(got, PatternListV2{}) {
					t.Fatal("listing accepted execution metadata", got, err)
				}
			})
		}
	}
}

func TestListPatternsV2PathMetadataAcceptsBothDaemonOperatingSystems(t *testing.T) {
	var value PatternListV2
	if err := json.Unmarshal(clientPatternV2Fixture(t, "responses/list-empty.json"), &value); err != nil {
		t.Fatal(err)
	}
	// These are daemon paths, not paths on the client OS. In particular Windows
	// filepath.IsAbs rejects the valid POSIX example; it still must be readable.
	for _, path := range []string{"/var/lib/nb1/patterns-v2", `C:\daemon\patterns-v2`, "C:/daemon/patterns-v2", `\\server\share\patterns-v2`} {
		value.Library.Path = path
		if err := validatePatternListV2(value); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{"", "relative/path", "C:relative", `\root-relative`, `\\server`} {
		value.Library.Path = path
		if err := validatePatternListV2(value); err == nil {
			t.Fatal("relative or incomplete path accepted", path)
		}
	}
}
