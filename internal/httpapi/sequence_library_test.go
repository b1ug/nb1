package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/libraryv1"
)

func sequenceLibraryResponse(t *testing.T, server *httptest.Server, method, path, body string, wanted int) []byte {
	t.Helper()
	status, data, _ := sequenceRouteDo(t, server, method, path, body)
	if status != wanted {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, status, wanted, data)
	}
	return data
}

func waitSequenceLibraryJob(t *testing.T, server *httptest.Server, id string) control.SequenceJob {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		data := sequenceLibraryResponse(t, server, "GET", "/v1/jobs/"+id, "", 200)
		var job control.SequenceJob
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
		if job.FinishedAt != nil {
			if job.Status != "succeeded" {
				t.Fatalf("job failed: %+v", job)
			}
			return job
		}
		if time.Now().After(deadline) {
			t.Fatal("fake job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertSequenceLibrarySteps(t *testing.T, backend *sequenceRouteBackend, colors ...string) {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.steps) != len(colors) {
		t.Fatalf("device received %d steps, want %d: %+v", len(backend.steps), len(colors), backend.steps)
	}
	for i, color := range colors {
		if backend.steps[i].Color != color {
			t.Fatalf("device step %d color = %s, want %s", i, backend.steps[i].Color, color)
		}
	}
}

func makeSequenceLibraryUnavailable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Rename(dir, dir+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSequenceLibraryAbsentKeepsPurePreviewAndColorResolutionAvailable(t *testing.T) {
	server, _ := sequenceRouteServer(t, httpapi.WithLibraryDirectory(""))
	for _, request := range []struct{ method, path, body string }{
		{"GET", "/v1/colors", ""},
		{"PUT", "/v1/colors/ready", `{"color":"lime"}`},
		{"GET", "/v1/patterns", ""},
		{"GET", "/v1/patterns/ready", ""},
		{"PUT", "/v1/patterns/ready", `{"action":{"kind":"off"}}`},
		{"POST", "/v1/colors/resolve", `{"color":"@ready"}`},
		{"POST", "/v1/preview", `{"action":{"kind":"set","color":"@ready"}}`},
		{"POST", "/v1/preview", `{"pattern_id":"ready"}`},
	} {
		data := sequenceLibraryResponse(t, server, request.method, request.path, request.body, 503)
		if !strings.Contains(string(data), `"code":"unavailable"`) {
			t.Fatalf("absent library returned an ambiguous error: %s", data)
		}
	}
	sequenceLibraryResponse(t, server, "POST", "/v1/colors/resolve", `{"color":{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":100}}`, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"action":{"kind":"set","color":"lime"}}`, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#00ff00"}]}]}}`, 200)
}

func TestSequenceLibraryColorAndPatternAuthoringIsLazyAndFrozen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	for _, path := range []string{"/v1/colors", "/v1/patterns"} {
		if data := sequenceLibraryResponse(t, server, "GET", path, "", 200); strings.TrimSpace(string(data)) != "[]" {
			t.Fatalf("missing library must list empty: %s", data)
		}
	}
	sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"action":{"kind":"set","color":"red"}}`, 200)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("construction, list or pure preview created the library", err)
	}
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"#0F0"}`, 200)
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/copied", `{"color":"@ready"}`, 200)
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/resolve", `{"color":"@ready"}`, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/colors/resolve", `{"color":"@resolve"}`, 200)
	data := sequenceLibraryResponse(t, server, "POST", "/v1/colors/resolve", `{"color":"@copied"}`, 200)
	if strings.TrimSpace(string(data)) != `{"color":"#00ff00"}` {
		t.Fatalf("alias resolution = %s", data)
	}
	data = sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/greeting", `{"title":"提醒","action":{"kind":"set","led":1,"color":"@ready"}}`, 200)
	var pattern libraryv1.Pattern
	if err := json.Unmarshal(data, &pattern); err != nil || pattern.ID != "greeting" || pattern.Title != "提醒" || pattern.Sequence.Steps[0].Targets[0].Color != "#00ff00" {
		t.Fatalf("saved pattern = %s, error=%v", data, err)
	}
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"blue"}`, 200)
	data = sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"pattern_id":"greeting"}`, 200)
	if !strings.Contains(string(data), `"color":"#00ff00"`) {
		t.Fatalf("saved pattern was recompiled through a changed alias: %s", data)
	}
	sequenceLibraryResponse(t, server, "GET", "/v1/patterns/greeting", "", 200)
	data = sequenceLibraryResponse(t, server, "GET", "/v1/patterns", "", 200)
	var summaries []map[string]json.RawMessage
	if err := json.Unmarshal(data, &summaries); err != nil || len(summaries) != 1 || len(summaries[0]) != 2 || summaries[0]["id"] == nil || summaries[0]["title"] == nil || summaries[0]["sequence"] != nil {
		t.Fatalf("Pattern list must contain id/title summaries: %s %v", data, err)
	}
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/title-limit", `{"title":"`+strings.Repeat("灯", 256)+`","action":{"kind":"off"}}`, 200)
	sequenceLibraryResponse(t, server, "GET", "/v1/colors", "", 200)
	assertSequenceLibrarySteps(t, backend)
}

func TestSequenceLibraryAliasUpdateDoesNotRecompileAnAcceptedJob(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"red"}`, 200)
	body := `{"id":"alias-job","serial":"20000001","action":{"kind":"set","color":"@ready"}}`
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 202)
	first := waitSequenceLibraryJob(t, server, "alias-job")
	sequenceLibraryResponse(t, server, "PUT", "/v1/colors/ready", `{"color":"blue"}`, 200)
	data := sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 200)
	var replayed control.SequenceJob
	if err := json.Unmarshal(data, &replayed); err != nil || replayed.ID != first.ID || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("alias replay changed Job identity: %s %v", data, err)
	}
	assertSequenceLibrarySteps(t, backend, "#ff0000")
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", strings.Replace(body, "alias-job", "alias-new", 1), 202)
	waitSequenceLibraryJob(t, server, "alias-new")
	assertSequenceLibrarySteps(t, backend, "#ff0000", "#0000ff")
	makeSequenceLibraryUnavailable(t, dir)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", strings.Replace(body, "alias-job", "alias-later", 1), 503)
	assertSequenceLibrarySteps(t, backend, "#ff0000", "#0000ff")
}

func TestSequenceLibraryPatternUpdateDoesNotRecompileAnAcceptedJob(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, backend := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/ready", `{"action":{"kind":"set","color":"red"}}`, 200)
	body := `{"id":"pattern-job","serial":"20000001","pattern_id":"ready"}`
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 202)
	first := waitSequenceLibraryJob(t, server, "pattern-job")
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/ready", `{"action":{"kind":"set","color":"blue"}}`, 200)
	data := sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 200)
	var replayed control.SequenceJob
	if err := json.Unmarshal(data, &replayed); err != nil || replayed.ID != first.ID || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("pattern replay changed Job identity: %s %v", data, err)
	}
	assertSequenceLibrarySteps(t, backend, "#ff0000")
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", strings.Replace(body, "pattern-job", "pattern-new", 1), 202)
	waitSequenceLibraryJob(t, server, "pattern-new")
	assertSequenceLibrarySteps(t, backend, "#ff0000", "#0000ff")
	makeSequenceLibraryUnavailable(t, dir)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", strings.Replace(body, "pattern-job", "pattern-later", 1), 503)
	assertSequenceLibrarySteps(t, backend, "#ff0000", "#0000ff")
}

func TestSequenceLibraryRejectsInvalidInputsAndMissingRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	server, _ := sequenceRouteServer(t, httpapi.WithLibraryDirectory(dir))
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/v1/colors/Upper", `{"color":"red"}`},
		{"PUT", "/v1/colors/ready", `{"color":"red","color":"blue"}`},
		{"PUT", "/v1/colors/ready", `{"color":"red","unknown":true}`},
		{"PUT", "/v1/patterns/ready", `{"action":{"kind":"off"},"pattern_id":"another"}`},
		{"PUT", "/v1/patterns/ready", `{"action":{"kind":"off"},"sequence":{}}`},
		{"PUT", "/v1/patterns/ready", `{"sequence":{"timing":"serial","steps":[]}}`},
		{"GET", "/v1/patterns/ready?unused=1", ""},
		{"GET", "/v1/colors", `{}`},
		{"POST", "/v1/preview", `{"pattern_id":""}`},
		{"POST", "/v1/preview", `{"pattern_id":"../outside"}`},
		{"POST", "/v1/preview", `{"pattern_id":"ready","action":{"kind":"off"}}`},
		{"POST", "/v1/jobs", `{"id":"invalid-pattern","serial":"20000001","pattern_id":null}`},
	} {
		sequenceLibraryResponse(t, server, tc.method, tc.path, tc.body, 400)
	}
	sequenceLibraryResponse(t, server, "GET", "/v1/patterns/missing", "", 404)
	sequenceLibraryResponse(t, server, "POST", "/v1/preview", `{"pattern_id":"missing"}`, 404)
	sequenceLibraryResponse(t, server, "POST", "/v1/colors/resolve", `{"color":"@missing"}`, 400)
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/oversized", `{"title":"`+strings.Repeat("灯", 257)+`","action":{"kind":"off"}}`, 400)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("rejected requests created the library", err)
	}
	body := `{"id":"create-later","serial":"20000001","pattern_id":"missing"}`
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 404)
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/missing", `{"action":{"kind":"off"}}`, 200)
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 202)
}
