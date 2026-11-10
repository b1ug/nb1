package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func authorAdvAssertPublication(t *testing.T, result *mcp.CallToolResult, requests *authorAdvRequests, confirmed, unknown bool) patternAuthorSaveOutput {
	t.Helper()
	value := authorAdvSaveFromResult(t, result)
	paths, bodies, arrival := requests.snapshot()
	if !reflect.DeepEqual(paths, []string{"POST /v2/patterns"}) || len(bodies) != 1 || len(arrival) != 1 || !bytes.Equal(bodies[0], arrival[0]) {
		t.Fatal("save did not forward one exact SDK-arrival body", paths, len(bodies), len(arrival))
	}
	var input struct {
		ID               string `json:"id"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := json.Unmarshal(arrival[0], &input); err != nil {
		t.Fatal(err)
	}
	ref := patternV2Ref{patternV2RecordContract, input.ID, input.ExpectedRevision}
	if value.Accepted || !value.PublicationAttempted || value.PublicationConfirmed != confirmed || value.PublicationUnknown != unknown || value.PatternRef == nil || *value.PatternRef != ref || value.OriginalAuthorJSON != string(arrival[0]) {
		t.Fatal("publication flags or original intent were lost", value)
	}
	if result.IsError == confirmed || (value.Error == nil) != confirmed || (value.Result != nil) != confirmed {
		t.Fatal("publication emitted partial confirmation or wrong tool error", value, result.IsError)
	}
	return value
}

func TestPatternAuthorAdversarialSaveCreatedExistingAndRawSDKBytes(t *testing.T) {
	for _, item := range []struct {
		name, fixture string
		status        int
	}{{"created", "responses/save-created.json", 201}, {"existing", "responses/save-existing.json", 200}} {
		t.Run(item.name, func(t *testing.T) {
			body := authorFixture(t, item.fixture)
			input := authorFixture(t, "requests/save-orbit-red-blue.json")
			// Explicit escape spelling survives the client compactor and must
			// remain the same between SDK arrival, HTTP and original_author_json.
			input = []byte(" \n\t" + strings.Replace(string(input), "红蓝环绕", `\u7ea2\u84dd\u73af\u7ed5`, 1) + "\n ")
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "/v2/patterns/orbit-red-blue")
				w.WriteHeader(item.status)
				w.Write(body)
			}))
			defer server.Close()
			result := authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", input)
			value := authorAdvAssertPublication(t, result, requests, true, false)
			if item.status == 201 && !reflect.DeepEqual(value.Result.Created, []string{"orbit-red-blue"}) || item.status == 200 && !reflect.DeepEqual(value.Result.Existing, []string{"orbit-red-blue"}) {
				t.Fatal("save classification changed", value.Result)
			}
			if !strings.Contains(value.OriginalAuthorJSON, `\u7ea2\u84dd\u73af\u7ed5`) {
				t.Fatal("original escaped author spelling was reserialized", value.OriginalAuthorJSON)
			}
		})
	}
	// The authoritative fixture's green Pulse remains the same; only its
	// retained source notation changes to the semantically equivalent HSV.
	get := authorAdvMap(t, authorFixture(t, "responses/get-pulse-lime.json"))
	record := get["record"].(map[string]any)
	source := record["source"].(map[string]any)
	action := source["action"].(map[string]any)
	action["color"] = map[string]any{"space": "hsv", "h_deg": json.Number("120"), "s_pct": json.Number("100"), "v_pct": json.Number("100")}
	revision := authorAdvRehash(t, record)
	body := authorAdvJSON(t, map[string]any{"schema_version": json.Number("2"), "contract": patternAuthorLibraryContract, "records": []any{record}, "created": []any{"pulse-lime"}, "existing": []any{}, "total": json.Number("1")})
	input := authorAdvJSON(t, map[string]any{"id": "pulse-lime", "title": record["title"], "source": source, "expected_revision": revision})
	input = []byte(strings.Replace(string(input), `"h_deg":120`, `"h_deg":120.000000`, 1))
	requests := &authorAdvRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.recordRequest(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v2/patterns/pulse-lime")
		w.WriteHeader(201)
		w.Write(body)
	}))
	defer server.Close()
	value := authorAdvAssertPublication(t, authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", input), requests, true, false)
	if !strings.Contains(value.OriginalAuthorJSON, `"h_deg":120.000000`) || !strings.Contains(value.Result.RecordJSON, `"h_deg":120`) {
		t.Fatal("HSV numeric spelling was lost before daemon or not canonicalized in returned Record", value)
	}
}

func TestPatternAuthorAdversarialSaveExactRejectionsAndUnknownClasses(t *testing.T) {
	cases := []struct {
		status int
		code   string
		known  bool
	}{
		{400, "invalid_pattern_request", true},
		{409, "pattern_revision_conflict", true}, {409, "pattern_conflict", true},
		{409, "pattern_library_limit", true}, {409, "pattern_library_busy", true}, {409, "pattern_library_read_only", true},
		{413, "pattern_package_too_large", true}, {415, "json_required", true},
		{501, "pattern_library_unsupported", true}, {501, "pattern_source_unsupported", true},
		{404, "not_found", false}, {400, "pattern_library_busy", false}, {409, "invalid_pattern_request", false},
		{500, "invalid_pattern_request", false}, {503, "pattern_commit_uncertain", false},
		{503, "pattern_library_unavailable", false}, {501, "future_error", false}, {307, "redirected", false},
	}
	for _, item := range cases {
		t.Run(http.StatusText(item.status)+"/"+item.code, func(t *testing.T) {
			body := authorAdvJSON(t, map[string]any{"error": map[string]any{"code": item.code, "message": "explicit rejection fixture"}})
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(item.status)
				w.Write(body)
			}))
			defer server.Close()
			value := authorAdvAssertPublication(t, authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", authorFixture(t, "requests/save-orbit-red-blue.json")), requests, false, !item.known)
			if value.Error.Code != item.code || value.Error.HTTPStatus != item.status {
				t.Fatal("original rejection diagnosis lost", value.Error)
			}
		})
	}
}

func TestPatternAuthorAdversarialSaveRejectsContradictoryHeaders(t *testing.T) {
	input := authorFixture(t, "requests/save-orbit-red-blue.json")
	created := authorFixture(t, "responses/save-created.json")
	busy := []byte(`{"error":{"code":"pattern_library_busy","message":"busy"}}`)
	for _, status := range []int{201, 409} {
		headers := []string{"X-NB1-Daemon-Instance-ID", "X-NB1-Replayed"}
		if status == 409 {
			headers = append(headers, "Location")
		}
		for _, header := range headers {
			for _, text := range []string{"", "false", "metadata"} {
				t.Run(http.StatusText(status)+"/"+header+"/"+text, func(t *testing.T) {
					requests := &authorAdvRequests{}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.recordRequest(r)
						w.Header().Set("Content-Type", "application/json")
						if status == 201 {
							w.Header().Set("Location", "/v2/patterns/orbit-red-blue")
						}
						w.Header().Set(header, text)
						w.WriteHeader(status)
						if status == 201 {
							w.Write(created)
						} else {
							w.Write(busy)
						}
					}))
					defer server.Close()
					value := authorAdvAssertPublication(t, authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", input), requests, false, true)
					if value.Error.Code != "invalid_daemon_response" {
						t.Fatal("contradictory headers became a trusted rejection", value.Error)
					}
				})
			}
		}
	}
	for _, location := range []string{"missing", "empty", "wrong", "duplicate"} {
		t.Run("Location/"+location, func(t *testing.T) {
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", "application/json")
				switch location {
				case "empty":
					w.Header().Set("Location", "")
				case "wrong":
					w.Header().Set("Location", "/v2/patterns/other")
				case "duplicate":
					w.Header().Add("Location", "/v2/patterns/orbit-red-blue")
					w.Header().Add("Location", "/v2/patterns/orbit-red-blue")
				}
				w.WriteHeader(201)
				w.Write(created)
			}))
			defer server.Close()
			authorAdvAssertPublication(t, authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", input), requests, false, true)
		})
	}
}

func TestPatternAuthorAdversarialSaveRejectsInvalidConfirmation(t *testing.T) {
	mutate := func(fn func(map[string]any)) []byte {
		value := authorAdvMap(t, authorFixture(t, "responses/save-created.json"))
		fn(value)
		return authorAdvJSON(t, value)
	}
	record := func(value map[string]any) map[string]any { return value["records"].([]any)[0].(map[string]any) }
	cases := []struct {
		name, media string
		status      int
		body        []byte
	}{
		{"wrong-status", "application/json", 202, authorFixture(t, "responses/save-created.json")},
		{"created-vs-existing", "application/json", 201, authorFixture(t, "responses/save-existing.json")},
		{"existing-vs-created", "application/json", 200, authorFixture(t, "responses/save-created.json")},
		{"empty-records", "application/json", 201, mutate(func(v map[string]any) { v["records"] = []any{} })},
		{"null-created", "application/json", 201, mutate(func(v map[string]any) { v["created"] = nil })},
		{"zero-total", "application/json", 201, mutate(func(v map[string]any) { v["total"] = json.Number("0") })},
		{"wrong-record-id", "application/json", 201, mutate(func(v map[string]any) { r := record(v); r["id"] = "other"; authorAdvRehash(t, r) })},
		{"wrong-revision", "application/json", 201, mutate(func(v map[string]any) { record(v)["revision"] = "sha256:" + strings.Repeat("0", 64) })},
		{"changed-source-rehashed", "application/json", 201, mutate(func(v map[string]any) {
			r := record(v)
			r["source"].(map[string]any)["action"].(map[string]any)["colors"].([]any)[0] = "orange"
			authorAdvRehash(t, r)
		})},
		{"frozen-body-corruption", "application/json", 201, mutate(func(v map[string]any) {
			p := record(v)["compiled"].(map[string]any)["execution"].(map[string]any)["program"].(map[string]any)
			p["frames"].([]any)[0].(map[string]any)["targets"].([]any)[0].(map[string]any)["color"] = "#00ff00"
		})},
		{"unsupported-execution-contract", "application/json", 201, mutate(func(v map[string]any) {
			record(v)["compiled"].(map[string]any)["execution"].(map[string]any)["contract"] = "future/program"
		})},
		{"unknown-response-field", "application/json", 201, mutate(func(v map[string]any) { v["job"] = "never" })},
		{"malformed-json", "application/json", 201, []byte(`{"records":`)},
		{"null-json", "application/json", 201, []byte(`null`)},
		{"duplicate-key", "application/json", 201, []byte(`{"schema_version":2,"schema_version":2}`)},
		{"surrogate", "application/json", 201, []byte(`{"record":"\ud800"}`)},
		{"trailing", "application/json", 201, append(authorFixture(t, "responses/save-created.json"), []byte(` {}`)...)},
		{"wrong-media", "text/plain", 201, authorFixture(t, "responses/save-created.json")},
		{"over-response-budget", "application/json", 201, []byte(strings.Repeat(" ", maxPatternAuthorResponse+1))},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				w.Header().Set("Content-Type", item.media)
				w.Header().Set("Location", "/v2/patterns/orbit-red-blue")
				w.WriteHeader(item.status)
				w.Write(item.body)
			}))
			defer server.Close()
			value := authorAdvAssertPublication(t, authorCall(t, authorAdvConnect(t, server.URL, requests), "save_pattern_v2", authorFixture(t, "requests/save-orbit-red-blue.json")), requests, false, true)
			if value.Error.Code != "invalid_daemon_response" {
				t.Fatal("malformed confirmation diagnosis lost", value.Error)
			}
		})
	}
}

func TestPatternAuthorAdversarialSaveLossAndExplicitGETNeverReplay(t *testing.T) {
	for _, mode := range []string{"commit-uncertain", "response-lost"} {
		t.Run(mode, func(t *testing.T) {
			getBody := authorFixture(t, "responses/get-orbit-red-blue.json")
			requests := &authorAdvRequests{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.recordRequest(r)
				if r.Method == "GET" {
					w.Header().Set("Content-Type", "application/json")
					w.Write(getBody)
					return
				}
				if mode == "response-lost" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					connection.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				w.Write([]byte(`{"error":{"code":"pattern_commit_uncertain","message":"publication may have succeeded"}}`))
			}))
			defer server.Close()
			session := authorAdvConnect(t, server.URL, requests)
			unknown := authorAdvAssertPublication(t, authorCall(t, session, "save_pattern_v2", authorFixture(t, "requests/save-orbit-red-blue.json")), requests, false, true)
			getArgs := authorAdvJSON(t, map[string]any{"id": unknown.PatternRef.ID, "expected_revision": unknown.PatternRef.Revision})
			result := authorCall(t, session, "get_pattern_v2", getArgs)
			generic := authorAdvGenericResult(t, result)
			if result.IsError || generic["kind"] != "pattern_record_v2" || generic["accepted"] != false || !unknown.PublicationUnknown || unknown.Error == nil {
				t.Fatal("explicit GET erased the old uncertainty or admitted execution", generic, unknown)
			}
			paths, _, _ := requests.snapshot()
			if !reflect.DeepEqual(paths, []string{"POST /v2/patterns", "GET /v2/patterns/orbit-red-blue"}) {
				t.Fatal("lookup replayed save or performed implicit prepare", paths)
			}
		})
	}
}

type authorAdvRoundTrip func(*http.Request) (*http.Response, error)

func (f authorAdvRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type authorAdvResponseBody struct {
	reader   io.Reader
	readErr  error
	closeErr error
	closes   int
}

func (b *authorAdvResponseBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.reader.Read(p)
}
func (b *authorAdvResponseBody) Close() error { b.closes++; return b.closeErr }

func TestPatternAuthorAdversarialSaveTransportAndPreAttemptCancellation(t *testing.T) {
	input := authorFixture(t, "requests/save-orbit-red-blue.json")
	created := authorFixture(t, "responses/save-created.json")
	for _, mode := range []string{"success-getbody-nil", "transport-timeout", "response-read-loss", "response-close-loss", "already-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			d, err := newDaemon("http://127.0.0.1:1", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			calls := 0
			body := &authorAdvResponseBody{reader: bytes.NewReader(created)}
			d.client.Transport = authorAdvRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v2/patterns" || r.GetBody != nil {
					t.Error("publication could replay or changed endpoint", r.Method, r.URL, r.GetBody != nil)
				}
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Equal(raw, input) {
					t.Error("direct publication body changed")
				}
				switch mode {
				case "transport-timeout":
					return nil, context.DeadlineExceeded
				case "response-read-loss":
					body.readErr = io.ErrUnexpectedEOF
				case "response-close-loss":
					body.closeErr = errors.New("close response failed")
				}
				return &http.Response{StatusCode: 201, Header: http.Header{"Content-Type": []string{"application/json"}, "Location": []string{"/v2/patterns/orbit-red-blue"}}, Body: body}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "already-cancelled" {
				cancel()
			}
			value := d.patternAuthorSave(ctx, input)
			confirmed := mode == "success-getbody-nil"
			attempted := mode != "already-cancelled"
			if value.Accepted || value.PublicationAttempted != attempted || value.PublicationConfirmed != confirmed || value.PublicationUnknown != (attempted && !confirmed) || value.PatternRef == nil || value.OriginalAuthorJSON != string(input) || (value.Error == nil) != confirmed {
				t.Fatal("transport publication state/intent lost", mode, value)
			}
			wantCalls := 1
			if !attempted {
				wantCalls = 0
			}
			if calls != wantCalls || attempted && mode != "transport-timeout" && body.closes != 1 {
				t.Fatal("publication repeated transport or leaked body", calls, body.closes)
			}
		})
	}
}

func TestPatternAuthorAdversarialSaveCancellationAfterHTTPArrival(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var enteredOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer unblock() // Must release before Server.Close even after t.Fatal.
	d, err := newDaemon(server.URL, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := authorFixture(t, "requests/save-orbit-red-blue.json")
	result := make(chan patternAuthorSaveOutput, 1)
	go func() { result <- d.patternAuthorSave(ctx, input) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("save never reached the fake HTTP server")
	}
	cancel()
	select {
	case value := <-result:
		if !value.PublicationAttempted || value.PublicationConfirmed || !value.PublicationUnknown || value.PatternRef == nil || value.OriginalAuthorJSON != string(input) || value.Error == nil || calls.Load() != 1 {
			t.Fatal("post-arrival cancellation lost uncertainty or replayed", value, calls.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled publication did not return")
	}
}
