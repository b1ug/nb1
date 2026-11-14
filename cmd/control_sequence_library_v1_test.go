package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/sequence"
)

func sequenceLibraryFixture(t *testing.T) sequence.Sequence {
	t.Helper()
	input, err := daemonclient.DecodeSequenceInput([]byte(sequenceControlInput))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := sequence.Decode(input.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestSequenceControlV1PatternReferenceRetainsOriginalInputAndJobID(t *testing.T) {
	compiled := sequenceLibraryFixture(t)
	var previews, posts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if string(input["pattern_id"]) != `"saved-pattern"` || input["action"] != nil || input["sequence"] != nil {
			t.Errorf("pattern reference was changed: %s", input)
		}
		switch r.URL.Path {
		case "/v1/preview":
			atomic.AddInt32(&previews, 1)
			sequenceControlJSON(w, http.StatusOK, daemonclient.SequencePreview{Sequence: compiled, TotalSteps: 1, NominalDurationMS: compiled.NominalDurationMS(), RequiredCapabilities: []string{"set", "program", "raw-rgb"}, DeviceCheck: daemonclient.SequenceDeviceCheck{Status: "not_requested"}})
		case "/v1/jobs":
			atomic.AddInt32(&posts, 1)
			if string(input["id"]) != `"original-job"` || string(input["serial"]) != `"20000001"` {
				t.Errorf("run lost ID or device binding: %s", input)
			}
			sequenceControlJSON(w, http.StatusAccepted, sequenceControlJob("original-job", "succeeded"))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	path := sequenceControlFile(t, `{"pattern_id":"saved-pattern"}`)
	if _, err := executeSequenceControl(t, server.URL, "preview", path); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSequenceControl(t, server.URL, "run", "20000001", path, "--id", "original-job", "--wait"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&previews) != 1 || atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("preview calls=%d, run calls=%d", previews, posts)
	}
}

func TestSequenceControlV1ColorsKeepRawColorInputs(t *testing.T) {
	for _, tc := range []struct {
		input string
		raw   string
	}{
		{"lime", `"lime"`},
		{"#0f0", `"#0f0"`},
		{"@existing", `"@existing"`},
		{`{"space":"rgb8","r":0,"g":255,"b":0}`, `{"space":"rgb8","r":0,"g":255,"b":0}`},
		{`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}`, `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var puts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/v1/colors/status-ok" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				atomic.AddInt32(&puts, 1)
				var input struct {
					Color json.RawMessage `json:"color"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				var actual bytes.Buffer
				if err := json.Compact(&actual, input.Color); err != nil || actual.String() != tc.raw {
					t.Errorf("color input=%s, want %s, error=%v", input.Color, tc.raw, err)
				}
				sequenceControlJSON(w, http.StatusOK, daemonclient.SequenceColor{Name: "status-ok", Color: "#00ff00"})
			}))
			defer server.Close()
			output, err := executeSequenceControl(t, server.URL, "colors", "set", "status-ok", tc.input)
			if err != nil || !strings.Contains(output, "#00ff00") || atomic.LoadInt32(&puts) != 1 {
				t.Fatalf("color save = %s, %v, PUTs %d", output, err, puts)
			}
		})
	}
}

func TestSequenceControlV1LibraryListsAndPatternSave(t *testing.T) {
	compiled := sequenceLibraryFixture(t)
	pattern := daemonclient.SequencePattern{ID: "saved", Title: "任务完成", Sequence: compiled}
	var puts, lists, gets int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/colors":
			atomic.AddInt32(&lists, 1)
			sequenceControlJSON(w, http.StatusOK, []daemonclient.SequenceColor{{Name: "status-ok", Color: "#00ff00"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/patterns":
			atomic.AddInt32(&lists, 1)
			sequenceControlJSON(w, http.StatusOK, []daemonclient.SequencePatternSummary{{ID: pattern.ID, Title: pattern.Title}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/patterns/saved":
			atomic.AddInt32(&gets, 1)
			sequenceControlJSON(w, http.StatusOK, pattern)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/patterns/saved":
			atomic.AddInt32(&puts, 1)
			var input struct {
				Action json.RawMessage `json:"action"`
				Title  string          `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if !strings.Contains(string(input.Action), "@status-ok") || input.Title != pattern.Title {
				t.Errorf("saved author input changed: %+v", input)
			}
			if _, err := actionv1.Compile(input.Action, func(string) (string, error) { return "#00ff00", nil }); err != nil {
				t.Error(err)
			}
			sequenceControlJSON(w, http.StatusOK, pattern)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	path := sequenceControlFile(t, `{"action":{"kind":"set","color":"@status-ok"}}`)
	for _, args := range [][]string{
		{"colors", "list"},
		{"patterns", "list"},
		{"patterns", "get", "saved"},
		{"patterns", "save", "saved", path, "--title", pattern.Title},
	} {
		if output, err := executeSequenceControl(t, server.URL, args...); err != nil {
			t.Fatalf("%v = %s, %v", args, output, err)
		}
	}
	if atomic.LoadInt32(&puts) != 1 || atomic.LoadInt32(&lists) != 2 || atomic.LoadInt32(&gets) != 1 {
		t.Fatalf("PUTs=%d, lists=%d, gets=%d", puts, lists, gets)
	}
}

func TestSequenceControlV1RejectsInvalidLibraryInputBeforeHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"colors", "set", "BadName", "red"},
		{"colors", "set", "ok", `{"space":"rgb8","r":0,"r":1,"g":0,"b":0}`},
		{"colors", "set", "ok", `{"space":"rgb8","r":0,"g":0,"b":0,"extra":1}`},
		{"colors", "set", "ok", "@BadAlias"},
		{"patterns", "save", "ok", sequenceControlFile(t, `{"pattern_id":"another"}`)},
		{"patterns", "save", "bad/id", sequenceControlFile(t, sequenceControlInput)},
		{"patterns", "save", "ok", sequenceControlFile(t, sequenceControlInput), "--title", strings.Repeat("灯", 257)},
		{"preview", sequenceControlFile(t, `{"pattern_id":""}`)},
		{"preview", sequenceControlFile(t, `{"pattern_id":null}`)},
		{"preview", sequenceControlFile(t, `{"pattern_id":"bad/id"}`)},
		{"preview", sequenceControlFile(t, `{"pattern_id":"ok","action":{"kind":"off"}}`)},
	} {
		if _, err := executeSequenceControl(t, server.URL, args...); err == nil {
			t.Errorf("accepted invalid input %v", args)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("invalid input made %d HTTP requests", got)
	}
}

func TestSequenceControlV1ListsRejectMalformedLibraryResponses(t *testing.T) {
	for _, input := range []string{
		`null`,
		`[{"name":"ok","color":"#00ff00","color":"#ff0000"}]`,
		`[{"name":"ok","color":"#00ff00","extra":1}]`,
		`[{"name":"ok","color":"#00ff00"},{"name":"ok","color":"#ff0000"}]`,
		`[{"name":"ok","color":"#00FF00"}]`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(input))
		}))
		if _, err := executeSequenceControl(t, server.URL, "colors", "list"); err == nil {
			t.Errorf("accepted malformed library response %s", input)
		}
		server.Close()
	}
}

func TestSequenceControlV1EmptyLibrariesRemainArrays(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sequenceControlJSON(w, http.StatusOK, []interface{}{})
	}))
	defer server.Close()
	for _, kind := range []string{"colors", "patterns"} {
		output, err := executeSequenceControl(t, server.URL, kind, "list")
		if err != nil || strings.TrimSpace(output) != "[]" {
			t.Fatalf("empty %s = %q, %v", kind, output, err)
		}
	}
}

func TestSequenceControlV1LostLibrarySaveResponseKeepsTargetWithoutRetry(t *testing.T) {
	path := sequenceControlFile(t, sequenceControlInput)
	for _, tc := range []struct {
		args   []string
		lookup string
	}{
		{[]string{"colors", "set", "status-ok", "red"}, "colors list"},
		{[]string{"patterns", "save", "saved", path}, "patterns get saved"},
	} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
		}))
		_, err := executeSequenceControl(t, server.URL, tc.args...)
		if err == nil || !strings.Contains(err.Error(), "save outcome unknown") || !strings.Contains(err.Error(), "control --endpoint "+server.URL+" "+tc.lookup) || atomic.LoadInt32(&calls) != 1 {
			t.Errorf("lost save response = %v, requests=%d", err, calls)
		}
		server.Close()
	}
}
