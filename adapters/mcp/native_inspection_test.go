package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func nativeToolTestOutput(t *testing.T, result *mcp.CallToolResult) nativeInspectionOutput {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("missing lossless JSON fallback: %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content %T", result.Content[0])
	}
	var out nativeInspectionOutput
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func nativeToolTestCall(t *testing.T, session *mcp.ClientSession, name, raw string) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func nativeCompact(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, data); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestNativeInspectionCatalogPreservesCompleteLegacyTwenty(t *testing.T) {
	before, err := os.ReadFile("testdata/native-inspection/legacy20-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var legacy []json.RawMessage
	if err = json.Unmarshal(before, &legacy); err != nil || len(legacy) != 20 {
		t.Fatal(err)
	}
	s := connect(t, "http://127.0.0.1:1")
	list, err := s.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 22 {
		t.Fatal(err, list)
	}
	wants := map[string]any{}
	for _, raw := range legacy {
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("bad legacy fixture")
		}
		wants[v["name"].(string)] = v
	}
	seen := 0
	for _, item := range list.Tools {
		if want, old := wants[item.Name]; old {
			data, _ := json.Marshal(item)
			var got any
			_ = json.Unmarshal(data, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("complete legacy tool changed: %s", item.Name)
			}
			seen++
			continue
		}
		if !isNativeInspectionTool(item.Name) || item.OutputSchema == nil || item.Annotations == nil || !item.Annotations.ReadOnlyHint || !item.Annotations.IdempotentHint || item.Annotations.DestructiveHint == nil || *item.Annotations.DestructiveHint || item.Annotations.OpenWorldHint == nil || *item.Annotations.OpenWorldHint {
			t.Fatal("native tool metadata", item)
		}
	}
	if seen != 20 {
		t.Fatal("legacy tool missing", seen)
	}
}

func TestNativeInspectionMemoryPreservesCompleteAndPartialEnvelopes(t *testing.T) {
	ram, playback := nativeWireTestFixture(t, "ram-complete"), nativeWireTestFixture(t, "playback-partial")
	var mu sync.Mutex
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		if r.Method != "GET" || !r.Close || r.Header.Get("Accept") != "application/json" || r.ContentLength != 0 {
			t.Error("not one-shot query", r.Method, r.Close, r.Header, r.ContentLength)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_fixture")
		switch r.URL.Path {
		case "/v1/devices/20000001/native-sequence/ram":
			_, _ = w.Write(ram)
		case "/v1/devices/20000001/native-sequence/playback":
			_, _ = w.Write(playback)
		default:
			t.Error("unexpected request", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := connect(t, srv.URL)
	for _, tc := range []struct {
		name, args string
		fixture    []byte
		partial    bool
	}{
		{"read_device_sequence_ram", `{"serial":"20000001","start_line":0,"line_count":2}`, ram, false},
		{"read_device_playback_state", `{"serial":"20000001"}`, playback, true},
	} {
		res := nativeToolTestCall(t, s, tc.name, tc.args)
		out := nativeToolTestOutput(t, res)
		if res.IsError != tc.partial || out.Accepted || (out.Error != nil) != tc.partial || !bytes.Equal(nativeCompact(t, out.Data), nativeCompact(t, tc.fixture)) {
			t.Fatalf("raw envelope lost: %+v %s", out, out.Data)
		}
		if tc.partial && !bytes.Contains(out.Data, []byte(`"native_code":"18446744073709551615"`)) {
			t.Fatal("native uint64 string lost")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /v1/devices/20000001/native-sequence/ram?line_count=2&start_line=0", "GET /v1/devices/20000001/native-sequence/playback"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatal(requests)
	}
}

func TestNativeInspectionRawArgumentsFailBeforeAnyHTTP(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("invalid args reached HTTP")
		http.Error(w, "unexpected", 500)
	}))
	defer srv.Close()
	s := connect(t, srv.URL)
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"serial":"20000001"}`, `{"serial":"20000001","start_line":0}`, `{"serial":"20000001","start_line":null,"line_count":2}`,
		`{"serial":"20000001","start_line":0.0,"line_count":2}`, `{"serial":"20000001","start_line":0e0,"line_count":2}`, `{"serial":"20000001","start_line":-0,"line_count":2}`,
		`{"serial":"20000001","start_line":0,"start_line":1,"line_count":2}`, `{"serial":"20000001","start_line":0,"sta\u0072t_line":1,"line_count":2}`,
		`{"serial":"20000001","start_line":31,"line_count":2}`, `{"serial":"../bad/1","start_line":0,"line_count":2}`, `{"serial":"20000001","start_line":0,"line_count":2,"policy":"replace"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			res := nativeToolTestCall(t, s, "read_device_sequence_ram", raw)
			out := nativeToolTestOutput(t, res)
			if !res.IsError || out.Error == nil || out.Error.Code != "invalid_request" || out.Accepted || len(out.Data) != 0 {
				t.Fatal(out)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"serial":null}`, `{"serial":"20000001","range":{}}`, `{"serial":"20000001","Serial":"20000002"}`} {
		res := nativeToolTestCall(t, s, "read_device_playback_state", raw)
		if !res.IsError {
			t.Fatal("bad playback args accepted", raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
}

func TestNativeInspectionHTTPBoundaryRejectsWithoutRetryOrFallback(t *testing.T) {
	base := nativeWireTestFixture(t, "ram-complete")
	tests := []struct {
		name    string
		status  int
		body    []byte
		headers func(http.Header)
	}{
		{"no-instance", 200, base, func(h http.Header) { h.Del("X-NB1-Daemon-Instance-ID") }},
		{"wrong-instance", 200, base, func(h http.Header) { h.Set("X-NB1-Daemon-Instance-ID", "other") }},
		{"duplicate-instance", 200, base, func(h http.Header) { h.Add("X-NB1-Daemon-Instance-ID", "daemon_fixture") }},
		{"duplicate-media", 200, base, func(h http.Header) { h.Add("Content-Type", "application/json") }},
		{"wrong-media", 200, base, func(h http.Header) { h.Set("Content-Type", "text/plain") }},
		{"location", 200, base, func(h http.Header) { h.Set("Location", "/v1/jobs/forbidden") }},
		{"replay", 200, base, func(h http.Header) { h.Set("X-NB1-Replayed", "false") }},
		{"accepted-is-not-collection", 202, base, nil},
		{"too-large", 200, bytes.Repeat([]byte(" "), maxNativeInspectionResponse+1), nil},
		{"duplicate-error-response", 503, []byte(`{"error":{"code":"busy","code":"other","message":"no"}}`), nil},
		{"API-unsupported", 501, []byte(`{"error":{"code":"native_inspection_unsupported","message":"unavailable"}}`), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_fixture")
				if tc.headers != nil {
					tc.headers(w.Header())
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()
			res := nativeToolTestCall(t, connect(t, srv.URL), "read_device_sequence_ram", `{"serial":"20000001","start_line":0,"line_count":2}`)
			out := nativeToolTestOutput(t, res)
			if !res.IsError || out.Error == nil || out.Accepted || len(out.Data) != 0 || calls.Load() != 1 {
				t.Fatal(out, calls.Load())
			}
		})
	}
}

func TestNativeInspectionRedirectAndEarlyEOFNeverReplayGET(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	var first atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	if res := nativeToolTestCall(t, connect(t, source.URL), "read_device_playback_state", `{"serial":"20000001"}`); !res.IsError || first.Load() != 1 || redirected.Load() != 0 {
		t.Fatal("redirect followed or replayed")
	}
	var eof atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eof.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	res := nativeToolTestCall(t, connect(t, srv.URL), "read_device_playback_state", `{"serial":"20000001"}`)
	if !res.IsError || eof.Load() != 1 {
		t.Fatal("GET replayed after EOF", eof.Load())
	}
}

func TestNativeInspectionOneShotTransportLeavesLegacyClientUnchanged(t *testing.T) {
	body := nativeWireTestFixture(t, "ram-complete")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_fixture")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	d, err := newDaemon(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	transport := d.client.Transport.(*http.Transport)
	keepalive := transport.DisableKeepAlives
	if res := d.readNativeInspection(context.Background(), nativeReadArgs{Serial: "20000001", LineCount: 2}, "ram"); res.IsError {
		t.Fatal(nativeToolTestOutput(t, res))
	}
	if d.client.Transport != transport || transport.DisableKeepAlives != keepalive {
		t.Fatal("legacy transport mutated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := d.readNativeInspection(ctx, nativeReadArgs{Serial: "20000001", LineCount: 2}, "ram"); !res.IsError {
		t.Fatal("cancelled request ran")
	}
}

func TestNativeInspectionActualStdioPreservesInt64AndNativeCodeString(t *testing.T) {
	ram := bytes.Replace(nativeWireTestFixture(t, "ram-complete"), []byte(`"duration_ns":1000000`), []byte(`"duration_ns":9223372036854775807`), 1)
	playback := nativeWireTestFixture(t, "playback-partial")
	var mu sync.Mutex
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_fixture")
		if strings.HasSuffix(r.URL.Path, "/ram") {
			_, _ = w.Write(ram)
		} else if strings.HasSuffix(r.URL.Path, "/playback") {
			_, _ = w.Write(playback)
		} else {
			t.Error("unexpected HTTP", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestStdioHelperProcess$")
	command.Env = append(os.Environ(), "NB1_MCP_TEST_HELPER=1", "NB1_MCP_TEST_ENDPOINT="+srv.URL)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			t.Error(err, stderr.String())
		}
	}()
	reader := bufio.NewReader(io.LimitReader(stdout, 2<<20))
	encoder := json.NewEncoder(stdin)
	read := func() json.RawMessage {
		t.Helper()
		line := make(chan []byte, 1)
		errs := make(chan error, 1)
		go func() {
			data, err := reader.ReadBytes('\n')
			if err != nil {
				errs <- err
				return
			}
			line <- data
		}()
		select {
		case data := <-line:
			return data
		case err := <-errs:
			t.Fatal(err, stderr.String())
		case <-time.After(5 * time.Second):
			t.Fatal("stdio response timed out")
		}
		return nil
	}
	send := func(id int, method string, params any) {
		t.Helper()
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
	}
	send(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "native-wire-test", "version": "1"}})
	_ = read()
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"read_device_sequence_ram", "read_device_playback_state"} {
		args := map[string]any{"serial": "20000001"}
		if index == 0 {
			args["start_line"], args["line_count"] = 0, 2
		}
		send(index+2, "tools/call", map[string]any{"name": name, "arguments": args})
		data := read()
		var wire struct {
			Result struct {
				IsError           bool            `json:"isError"`
				StructuredContent json.RawMessage `json:"structuredContent"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(data, &wire); err != nil || len(wire.Error) != 0 {
			t.Fatalf("bad MCP wire %s %v", data, err)
		}
		var out nativeInspectionOutput
		if json.Unmarshal(wire.Result.StructuredContent, &out) != nil || out.Accepted || len(out.Data) == 0 || wire.Result.IsError != (index == 1) {
			t.Fatalf("lost native output %s", data)
		}
		if index == 0 && !bytes.Contains(out.Data, []byte(`"duration_ns":9223372036854775807`)) {
			t.Fatal("int64 rounded on stdio", string(out.Data))
		}
		if index == 1 && !bytes.Contains(out.Data, []byte(`"native_code":"18446744073709551615"`)) {
			t.Fatal("uint64 code string lost", string(out.Data))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(paths)
	if !reflect.DeepEqual(paths, []string{"GET /v1/devices/20000001/native-sequence/playback", "GET /v1/devices/20000001/native-sequence/ram"}) {
		t.Fatal(paths)
	}
}
