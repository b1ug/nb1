package httpapi_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/buildmeta"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/remoteaccess"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/b1ug/nb1/internal/triggerv1"
)

type sequenceRouteBackend struct {
	frameRouteBackend
	steps []control.Step
}

func (b *sequenceRouteBackend) Set(step control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.operations = append(b.operations, "set")
	b.steps = append(b.steps, step)
	return nil
}

func (*sequenceRouteBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20000001", Capabilities: []string{"set", "raw-rgb", "program", "hold", "mk2-basic"}}
}

type sequenceRouteDriver struct{ backend *sequenceRouteBackend }

func (d sequenceRouteDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type routedSequenceDriver map[string]*sequenceRouteBackend

func (d routedSequenceDriver) Open(serial string) (control.Backend, error) {
	if b := d[serial]; b != nil {
		return b, nil
	}
	return nil, control.ErrOffline
}

func routedSequenceServer(t *testing.T, serials []string, driver routedSequenceDriver, defaultSerial string, fallback bool) (*httptest.Server, string) {
	t.Helper()
	engine, err := control.New(driver, control.Options{Serials: serials})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for _, serial := range serials {
		for {
			state, err := engine.Device(serial)
			if err != nil {
				t.Fatal(err)
			}
			if (driver[serial] != nil && state.Connection == "online") || (driver[serial] == nil && state.LastError != "") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("device %s did not publish connection: %+v", serial, state)
			}
			time.Sleep(time.Millisecond)
		}
	}
	dir := filepath.Join(t.TempDir(), "library")
	server := httptest.NewServer(httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(dir), httpapi.WithSequenceRouting(defaultSerial, fallback, dir)))
	t.Cleanup(server.Close)
	return server, dir
}

func TestSequenceRoutingDefaultAndFallback(t *testing.T) {
	const a, b, absent = "20000001", "20000002", "20000003"
	for _, tc := range []struct {
		name, defaultSerial    string
		managed, online        []string
		fallback               bool
		wantSerial, wantSource string
		status                 int
		code                   string
	}{
		{name: "configured_default", defaultSerial: b, managed: []string{a, b}, online: []string{a, b}, wantSerial: b, wantSource: "default", status: 202},
		{name: "offline_strict", defaultSerial: a, managed: []string{a, b}, online: []string{b}, status: 503, code: "device_offline"},
		{name: "offline_fallback", defaultSerial: a, managed: []string{a, b}, online: []string{b}, fallback: true, wantSerial: b, wantSource: "fallback", status: 202},
		{name: "not_managed_strict", defaultSerial: absent, managed: []string{a, b}, online: []string{b}, status: 404, code: "device_not_managed"},
		{name: "not_managed_fallback", defaultSerial: absent, managed: []string{a, b}, online: []string{b}, fallback: true, wantSerial: b, wantSource: "fallback", status: 202},
		{name: "no_alternative", defaultSerial: a, managed: []string{a, b}, fallback: true, status: 503, code: "device_offline"},
		{name: "ambiguous_alternatives", defaultSerial: absent, managed: []string{a, b}, online: []string{a, b}, fallback: true, status: 503, code: "device_offline"},
		{name: "no_default_one_online_of_two", managed: []string{a, b}, online: []string{b}, fallback: true, status: 400, code: "target_required"},
		{name: "one_managed", managed: []string{b}, online: []string{b}, wantSerial: b, wantSource: "single_device", status: 202},
		{name: "sole_managed_offline", managed: []string{b}, status: 503, code: "device_offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := routedSequenceDriver{}
			for _, serial := range tc.online {
				driver[serial] = &sequenceRouteBackend{}
			}
			server, _ := routedSequenceServer(t, tc.managed, driver, tc.defaultSerial, tc.fallback)
			status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"implicit","action":{"kind":"off"}}`)
			if status != tc.status {
				t.Fatalf("HTTP %d, want %d: %s", status, tc.status, body)
			}
			if tc.code != "" {
				if !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
					t.Fatalf("expected %s: %s", tc.code, body)
				}
				if status, _, _ := sequenceRouteDo(t, server, "GET", "/v1/jobs/implicit", ""); status != 404 {
					t.Fatalf("rejected submission retained a Job: HTTP %d", status)
				}
				if status, _, _ := sequenceRouteDo(t, server, "GET", "/healthz", ""); status != 200 {
					t.Fatal("routing error stopped the daemon")
				}
				return
			}
			var job control.SequenceJob
			if err := json.Unmarshal(body, &job); err != nil || job.Serial != tc.wantSerial || job.TargetSource != tc.wantSource {
				t.Fatalf("unexpected routing: %s", body)
			}
		})
	}
}

func TestSequenceAPIWelcomeDirectory(t *testing.T) {
	oldGitCommit, oldGitSummary := buildmeta.GitCommit, buildmeta.GitSummary
	buildmeta.GitCommit, buildmeta.GitSummary = "test-source-commit", "v0.1.4-test"
	t.Cleanup(func() {
		buildmeta.GitCommit, buildmeta.GitSummary = oldGitCommit, oldGitSummary
	})
	server, _ := routedSequenceServer(t, []string{"20000001", "20000002"}, routedSequenceDriver{
		"20000001": &sequenceRouteBackend{},
	}, "20000001", false)
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /: HTTP %d", response.StatusCode)
	}
	var welcome struct {
		Service        string `json:"service"`
		Status         string `json:"status"`
		ConsoleName    string `json:"console_name"`
		ConsoleURL     string `json:"console_url"`
		DefaultDevice  string `json:"default_device"`
		DeviceFallback string `json:"device_fallback"`
		Build          struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
			Modified string `json:"modified"`
		} `json:"build"`
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"endpoints"`
	}
	if err := json.NewDecoder(response.Body).Decode(&welcome); err != nil {
		t.Fatal(err)
	}
	if welcome.Service != "nb1" || welcome.Status != "running" || welcome.ConsoleName != "nb1 Workbench" || welcome.ConsoleURL != "/demo/" {
		t.Fatalf("unexpected welcome: %+v", welcome)
	}
	if welcome.DefaultDevice != "20000001" || welcome.DeviceFallback != "error" {
		t.Fatalf("unexpected routing directory: default=%q fallback=%q", welcome.DefaultDevice, welcome.DeviceFallback)
	}
	if welcome.Build.Version == "" {
		t.Fatalf("missing build version: %+v", welcome.Build)
	}
	if welcome.Build.Version != "v0.1.4-test" {
		t.Fatalf("welcome build version = %q, want injected release version", welcome.Build.Version)
	}
	if len(welcome.Endpoints) != 22 {
		t.Fatalf("directory has %d endpoints, want 22", len(welcome.Endpoints))
	}
	want := map[string]bool{
		"GET /": true, "GET /v1/triggers": true, "GET /v1/triggers/check": true,
		"POST /v1/jobs": true, "GET /v1/devices/{serial}": true,
		"GET /v1/devices/{serial}/flash/reset-ticket": true,
		"POST /v1/devices/{serial}/flash/reset":       true,
		"PUT /v1/colors/resolve":                      true,
	}
	for _, endpoint := range welcome.Endpoints {
		delete(want, endpoint.Method+" "+endpoint.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing directory endpoints: %v", want)
	}
	response, err = http.Post(server.URL+"/", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: HTTP %d, want 405", response.StatusCode)
	}
	response.Body.Close()
	for _, target := range []string{server.URL + "/?unexpected=1", server.URL + "/"} {
		var body io.Reader
		if target == server.URL+"/" {
			body = strings.NewReader(`{}`)
		}
		request, err := http.NewRequest(http.MethodGet, target, body)
		if err != nil {
			t.Fatal(err)
		}
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET %s: HTTP %d, want 400", request.URL, response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestSequenceRoutingUnavailableBindingNeverSubstitutes(t *testing.T) {
	const a, b = "20000001", "20000002"
	alternate := &sequenceRouteBackend{}
	server, dir := routedSequenceServer(t, []string{a, b}, routedSequenceDriver{b: alternate}, a, true)
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/ready", `{"action":{"kind":"off"}}`, 200)
	if err := os.WriteFile(filepath.Join(dir, "triggers.json"), []byte(`{"schema_version":1,"triggers":{"bound":{"serial":"20000001","pattern_id":"ready"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, code string }{
		{`{"id":"explicit-offline","serial":"20000001","action":{"kind":"off"}}`, "unavailable"},
		{`{"id":"bound-offline","trigger":"bound"}`, "device_offline"},
		{`{"id":"explicit-trigger-offline","serial":"20000001","trigger":"bound"}`, "device_offline"},
	} {
		body := sequenceLibraryResponse(t, server, "POST", "/v1/jobs", tc.body, 503)
		if !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Fatalf("rejection %s: %s", tc.code, body)
		}
	}
	assertSequenceLibrarySteps(t, alternate)
	status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"override","serial":"20000002","trigger":"bound"}`)
	var job control.SequenceJob
	if status != 202 || json.Unmarshal(body, &job) != nil || job.Serial != b || job.TargetSource != "request" {
		t.Fatalf("explicit override: %d %s", status, body)
	}
	waitSequenceLibraryJob(t, server, "override")
	assertSequenceLibrarySteps(t, alternate, "#000000")
}

func TestSequenceRoutingTriggerReplayAndExplicitOverride(t *testing.T) {
	const a, b = "20000001", "20000002"
	server, dir := routedSequenceServer(t, []string{a, b}, routedSequenceDriver{a: {}, b: {}}, a, true)
	for _, name := range []string{"one", "two"} {
		status, body, _ := sequenceRouteDo(t, server, "PUT", "/v1/patterns/"+name, `{"sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#112233"}],"hold_ms":10000}]}}`)
		if status != 200 {
			t.Fatalf("save %s: HTTP %d %s", name, status, body)
		}
	}
	writeMapping := func(value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "triggers.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMapping(`{"schema_version":1,"triggers":{"build.done":{"pattern_id":"one","serial":"20000001"}}}`)
	status, body, _ := sequenceRouteDo(t, server, "GET", "/v1/triggers", "")
	if status != 200 || !strings.Contains(string(body), `"name":"build.done"`) {
		t.Fatalf("list triggers: %d %s", status, body)
	}
	const submit = `{"id":"original","trigger":"build.done"}`
	assertJob := func(raw string, expected int, serial, source string) {
		t.Helper()
		status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs", raw)
		var job control.SequenceJob
		if status != expected || json.Unmarshal(body, &job) != nil || job.Serial != serial || job.TargetSource != source {
			t.Fatalf("job: HTTP %d %s", status, body)
		}
	}
	assertJob(submit, 202, a, "trigger")
	status, body, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"busy","action":{"kind":"off"}}`)
	if status != 409 || !strings.Contains(string(body), `"code":"device_busy"`) {
		t.Fatalf("busy default should not select B: %d %s", status, body)
	}
	writeMapping(`{"schema_version":1,"triggers":{"build.done":{"pattern_id":"two","serial":"20000002"}}}`)
	assertJob(submit, 200, a, "trigger")
	status, body, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"original","trigger":"build.done","serial":"20000001"}`)
	if status != 409 || !strings.Contains(string(body), `"code":"job_conflict"`) {
		t.Fatalf("explicit target changed accepted intent: %d %s", status, body)
	}
	assertJob(`{"id":"next","trigger":"build.done","serial":"20000002"}`, 202, b, "request")
	writeMapping(`{"schema_version":1,"triggers":`)
	assertJob(submit, 200, a, "trigger")
	status, body, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"bad-mapping","trigger":"build.done"}`)
	if status != 503 || !strings.Contains(string(body), `"code":"invalid_trigger_config"`) {
		t.Fatalf("malformed trigger map: %d %s", status, body)
	}
}

// Only the cached device-list operation is available to this handler fixture.
type triggerCheckService struct {
	control.Service
	snapshots int
}

func (s *triggerCheckService) Devices() []control.DeviceState {
	s.snapshots++
	return []control.DeviceState{
		{DeviceInfo: control.DeviceInfo{Serial: "20000001"}, Connection: "offline"},
		{DeviceInfo: control.DeviceInfo{Serial: "20000002"}, Connection: "online"},
	}
}

func TestSequenceTriggerCheckReportsExactReferencesWithoutExecution(t *testing.T) {
	service := &triggerCheckService{}
	dir := filepath.Join(t.TempDir(), "library")
	handler := httpapi.New(service, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(dir), httpapi.WithSequenceRouting("", false, dir))
	request := func(method, path, body, origin string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8931"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	const path = "/v1/triggers/check"
	for _, tc := range []struct {
		name, method, path, body, origin string
		status                           int
	}{
		{"Method", "POST", path, "", "", 405},
		{"Query", "GET", path + "?extra=1", "", "", 400},
		{"Body", "GET", path, "{}", "", 400},
		{"Origin", "GET", path, "", "https://foreign.invalid", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := request(tc.method, tc.path, tc.body, tc.origin)
			if res.Code != tc.status || service.snapshots != 0 {
				t.Fatalf("unexpected admission: HTTP %d %s, cached snapshots %d", res.Code, res.Body.String(), service.snapshots)
			}
		})
	}
	res := request("GET", path, "", "http://127.0.0.1:8931")
	if res.Code != 200 || strings.TrimSpace(res.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty report: HTTP %d %s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty preflight changed the library: %v", err)
	}
	for _, id := range []string{"Ready", "Corrupt"} {
		res := request("PUT", "/v1/patterns/"+id, `{"action":{"kind":"off"}}`, "")
		if res.Code != 200 {
			t.Fatalf("save fixture Pattern: HTTP %d %s", res.Code, res.Body.String())
		}
	}
	corrupt := filepath.Join(dir, "patterns", hex.EncodeToString([]byte("Corrupt"))+".json")
	if err := os.WriteFile(corrupt, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	const mapping = `{"schema_version":1,"triggers":{
		"unbound":{"pattern_id":"Ready"},
		"offline":{"pattern_id":"Ready","serial":"20000001"},
		"bad":{"pattern_id":"Corrupt"},
		"both":{"pattern_id":"ready","serial":"20000003","title":"人力映射 🌈"},
		"good":{"pattern_id":"Ready","serial":"20000002"}}}`
	filename := filepath.Join(dir, "triggers.json")
	if err := os.WriteFile(filename, []byte(mapping), 0600); err != nil {
		t.Fatal(err)
	}
	before := service.snapshots
	res = request("GET", path, "", "")
	var report triggerv1.CheckReport
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &report) != nil || len(report.Items) != 5 || service.snapshots-before != 1 {
		t.Fatalf("report: HTTP %d %s, cached snapshots %d", res.Code, res.Body.String(), service.snapshots-before)
	}
	for i, expected := range []struct {
		name, pattern, serial, connection, title string
		issueCodes                               string
	}{
		{name: "bad", pattern: "Corrupt", issueCodes: "pattern_unavailable"},
		{name: "both", pattern: "ready", serial: "20000003", title: "人力映射 🌈", issueCodes: "pattern_not_found,device_not_managed"},
		{name: "good", pattern: "Ready", serial: "20000002", connection: "online"},
		{name: "offline", pattern: "Ready", serial: "20000001", connection: "offline"},
		{name: "unbound", pattern: "Ready"},
	} {
		row := report.Items[i]
		var codes []string
		for _, issue := range row.Issues {
			if issue.Message == "" {
				t.Fatalf("no diagnostic: %+v", issue)
			}
			codes = append(codes, issue.Code)
		}
		if row.Name != expected.name || row.PatternID != expected.pattern || row.Serial != expected.serial ||
			row.Connection != expected.connection || row.Title != expected.title || row.Issues == nil || strings.Join(codes, ",") != expected.issueCodes {
			t.Fatalf("row %d: %+v, want %+v", i, row, expected)
		}
	}
	if after, err := os.ReadFile(filename); err != nil || string(after) != mapping {
		t.Fatalf("preflight changed its input: %s, %v", after, err)
	}
	if err := os.WriteFile(filename, []byte(`{"schema_version":1,"triggers":`), 0600); err != nil {
		t.Fatal(err)
	}
	before = service.snapshots
	res = request("GET", path, "", "")
	if res.Code != 503 || !strings.Contains(res.Body.String(), `"code":"invalid_trigger_config"`) || service.snapshots != before {
		t.Fatalf("damaged mapping reused prior data: HTTP %d %s", res.Code, res.Body.String())
	}
}

func TestSequenceCancellationSerialGuardPreservesOtherDevice(t *testing.T) {
	const first, second = "20000001", "20000002"
	server, _ := routedSequenceServer(t, []string{first, second}, routedSequenceDriver{
		first: &sequenceRouteBackend{}, second: &sequenceRouteBackend{},
	}, first, false)
	for id, serial := range map[string]string{"guarded-a": first, "guarded-b": second} {
		input := `{"id":"` + id + `","serial":"` + serial + `","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red"}],"hold_ms":8000}]}}`
		status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs", input)
		if status != http.StatusAccepted {
			t.Fatalf("submit %s: HTTP %d %s", id, status, body)
		}
	}
	assertActive := func(id, serial string) {
		t.Helper()
		status, body, _ := sequenceRouteDo(t, server, "GET", "/v1/jobs/"+id, "")
		var job control.SequenceJob
		if status != http.StatusOK || json.Unmarshal(body, &job) != nil || job.Serial != serial ||
			(job.Status != "queued" && job.Status != "running") || job.FinishedAt != nil {
			t.Fatalf("expected active Job on %s: HTTP %d %s", serial, status, body)
		}
	}
	for _, tc := range []struct {
		query  string
		status int
		code   string
	}{
		{"?serial=" + second, http.StatusConflict, "job_target_mismatch"},
		{"?serial=", http.StatusBadRequest, "invalid_request"},
		{"?serial=" + first + "&serial=" + second, http.StatusBadRequest, "invalid_request"},
		{"?serial=" + first + "&typo=1", http.StatusBadRequest, "invalid_request"},
	} {
		status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs/guarded-a/cancel"+tc.query, "")
		if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Fatalf("guard %s: HTTP %d %s", tc.query, status, body)
		}
		assertActive("guarded-a", first)
		assertActive("guarded-b", second)
	}
	status, body, _ := sequenceRouteDo(t, server, "POST", "/v1/jobs/guarded-a/cancel?serial="+first, "")
	var job control.SequenceJob
	if (status != http.StatusOK && status != http.StatusAccepted) || json.Unmarshal(body, &job) != nil ||
		job.Serial != first || (job.Status != "cancelling" && job.Status != "cancelled") {
		t.Fatalf("matching guard: HTTP %d %s", status, body)
	}
	assertActive("guarded-b", second)
}

func sequenceRouteServer(t *testing.T, options ...httpapi.Option) (*httptest.Server, *sequenceRouteBackend) {
	t.Helper()
	backend := &sequenceRouteBackend{}
	engine, err := control.New(sequenceRouteDriver{backend}, control.Options{Serials: []string{"20000001"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := engine.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := engine.Device("20000001")
		if state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake device did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	options = append([]httpapi.Option{httpapi.WithSequenceAPI()}, options...)
	server := httptest.NewServer(httpapi.New(engine, options...))
	t.Cleanup(server.Close)
	return server, backend
}

func sequenceRouteDo(t *testing.T, server *httptest.Server, method, path, body string) (int, []byte, http.Header) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}

func TestSequenceAPISolidPairIsOnePersistentHoldJob(t *testing.T) {
	server, backend := sequenceRouteServer(t)
	input := `{"timing":"serial","steps":[{"targets":[{"led":1,"color":"#112233"}],"fade_ms":0,"hold_ms":0},{"targets":[{"led":2,"color":"#aabbcc"}],"fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"hold"}`
	for _, path := range []string{"/v1/preview", "/v1/jobs"} {
		bad := strings.Replace(input, "#aabbcc", "not-a-color", 1)
		body := `{"serial":"20000001","sequence":` + bad + `}`
		if path == "/v1/jobs" {
			body = `{"id":"invalid-second-color","serial":"20000001","sequence":` + bad + `}`
		}
		if code, data, _ := sequenceRouteDo(t, server, "POST", path, body); code != http.StatusBadRequest {
			t.Fatalf("malformed second color %s: %d %s", path, code, data)
		}
	}
	backend.mu.Lock()
	for _, operation := range backend.operations {
		if operation == "set" || strings.HasPrefix(operation, "stop:") {
			backend.mu.Unlock()
			t.Fatal("invalid pair reached execution or cleanup")
		}
	}
	backend.mu.Unlock()
	code, data, _ := sequenceRouteDo(t, server, "POST", "/v1/preview", `{"serial":"20000001","sequence":`+input+`}`)
	var preview struct {
		Sequence          sequence.Sequence `json:"sequence"`
		TotalSteps        int               `json:"total_steps"`
		NominalDurationMS int64             `json:"nominal_duration_ms"`
	}
	if code != http.StatusOK || json.Unmarshal(data, &preview) != nil || preview.TotalSteps != 2 || preview.NominalDurationMS != 20 {
		t.Fatalf("pair preview: HTTP %d %s", code, data)
	}
	plan, err := sequence.Plan(preview.Sequence)
	if err != nil || plan.TotalSteps != 2 || plan.TotalTargetWrites != 2 || plan.NominalDurationMS != 20 || len(plan.Initial) != 0 || len(plan.Finish) != 0 || plan.OnFinish != "hold" {
		t.Fatalf("pair plan changed its hold/write contract: %+v %v", plan, err)
	}
	encodedPlan, _ := json.Marshal(plan)
	t.Log("NATIVE_SOLID_PAIR_PLAN " + string(encodedPlan))
	encodedSequence, _ := json.Marshal(preview.Sequence)
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"solid-pair-one-job","serial":"20000001","sequence":`+string(encodedSequence)+`}`)
	if code != http.StatusAccepted {
		t.Fatalf("one-click compatible admission: %d %s", code, data)
	}
	deadline := time.Now().Add(time.Second)
	for {
		code, data, _ = sequenceRouteDo(t, server, "GET", "/v1/jobs/solid-pair-one-job", "")
		var job control.SequenceJob
		if code != http.StatusOK || json.Unmarshal(data, &job) != nil {
			t.Fatalf("pair Job lookup: %d %s", code, data)
		}
		if job.Status == "succeeded" {
			if job.TotalSteps != 2 || job.CompletedSteps != 2 {
				t.Fatalf("pair Job progress: %+v", job)
			}
			break
		}
		if job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("pair Job did not succeed: %s", data)
		}
		time.Sleep(time.Millisecond)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.steps) != 2 || backend.steps[0] != (control.Step{LED: 1, Color: "#112233"}) || backend.steps[1] != (control.Step{LED: 2, Color: "#aabbcc"}) {
		t.Fatalf("pair target writes were not exactly the ordered captured pair: %+v", backend.steps)
	}
	holds := 0
	for _, operation := range backend.operations {
		if operation == "stop:hold" {
			holds++
		} else if strings.HasPrefix(operation, "stop:") {
			t.Fatalf("hold pair requested off cleanup: %v", backend.operations)
		}
	}
	if holds != 2 {
		t.Fatalf("pair requires one preflight hold and one finish hold: %v", backend.operations)
	}
}

func TestSequenceAPIQueueAndPreemptionStayOnTheAcceptedDevice(t *testing.T) {
	const a, b = "20000001", "20000002"
	devices := routedSequenceDriver{a: {}, b: {}}
	server, dir := routedSequenceServer(t, []string{a, b}, devices, a, false)
	sequenceLibraryResponse(t, server, "PUT", "/v1/patterns/green", `{"action":{"kind":"set","color":"lime"}}`, 200)
	if err := os.WriteFile(filepath.Join(dir, "triggers.json"), []byte(`{"schema_version":1,"triggers":{"urgent":{"pattern_id":"green","serial":"20000001"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	post := func(body string, status int, code string) {
		t.Helper()
		data := sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, status)
		if code != "" && !strings.Contains(string(data), `"code":"`+code+`"`) {
			t.Fatalf("expected %s: %s", code, data)
		}
	}
	await := func(id, state string) control.SequenceJob {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var job control.SequenceJob
			data := sequenceLibraryResponse(t, server, "GET", "/v1/jobs/"+id, "", 200)
			if err := json.Unmarshal(data, &job); err != nil {
				t.Fatal(err)
			}
			if job.Status == state {
				return job
			}
			if job.FinishedAt != nil || time.Now().After(deadline) {
				t.Fatalf("waiting for %s to be %s: %s", id, state, data)
			}
			time.Sleep(time.Millisecond)
		}
	}
	const first = `{"id":"first","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"red"}],"hold_ms":8000}]}}`
	const independent = `{"id":"independent","serial":"20000002","action":{"kind":"blink","color":"blue","on_ms":8000}}`
	const urgent = `{"id":"fourth","trigger":"urgent","policy":"preempt"}`
	post(first, 202, "")
	post(strings.Replace(first, `"id"`, `"policy":"reject","id"`, 1), 200, "")
	post(independent, 202, "")
	await("first", "running")
	await("independent", "running")
	post(`{"id":"second","serial":"20000001","policy":"queue","action":{"kind":"off"}}`, 202, "")
	post(`{"id":"third","policy":"queue","pattern_id":"green"}`, 202, "")
	post(`{"id":"bad-source","policy":"preempt","pattern_id":"missing"}`, 404, "not_found")
	await("first", "running")

	// Block the fake device's cleanup so both HTTP requests observe the same handover.
	func() {
		devices[a].mu.Lock()
		defer devices[a].mu.Unlock()
		post(urgent, 202, "")
		post(urgent, 200, "")
		post(strings.Replace(urgent, "fourth", "another", 1), 409, "preemption_pending")
		post(strings.Replace(urgent, "preempt", "queue", 1), 409, "job_conflict")
		await("first", "cancelling")
		await("fourth", "queued")
	}()

	previous := await("first", "cancelled")
	for _, id := range []string{"fourth", "second", "third"} {
		next := await(id, "succeeded")
		if next.Serial != a || next.StartedAt == nil || next.StartedAt.Before(*previous.FinishedAt) {
			t.Fatalf("incorrect execution order on %s: %+v then %+v", a, previous, next)
		}
		previous = next
	}
	post(strings.Replace(first, "first", "later", 1), 202, "")
	await("later", "running")
	post(urgent, 200, "")
	await("later", "running")
	if job := await("independent", "running"); job.Serial != b {
		t.Fatalf("independent Job changed target: %+v", job)
	}
}

func TestSequenceAPIRejectsFullQueueBeforeInterrupting(t *testing.T) {
	server, _ := sequenceRouteServer(t)
	const held = `{"serial":"20000001","id":"held","action":{"kind":"blink","color":"red","on_ms":8000}}`
	sequenceLibraryResponse(t, server, "POST", "/v1/jobs", held, 202)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(string(sequenceLibraryResponse(t, server, "GET", "/v1/jobs/held", "", 200)), `"status":"running"`) {
		if time.Now().After(deadline) {
			t.Fatal("first Job never started")
		}
		time.Sleep(time.Millisecond)
	}
	const waiting = `{"serial":"20000001","action":{"kind":"off"},"policy":"queue"}`
	for range 8 {
		sequenceLibraryResponse(t, server, "POST", "/v1/jobs", waiting, 202)
	}
	for _, body := range []string{waiting, strings.Replace(waiting, "queue", "preempt", 1)} {
		rejected := sequenceLibraryResponse(t, server, "POST", "/v1/jobs", body, 409)
		if !strings.Contains(string(rejected), `"code":"queue_full"`) {
			t.Fatalf("full queue: %s", rejected)
		}
	}
	active := sequenceLibraryResponse(t, server, "GET", "/v1/jobs/held", "", 200)
	if !strings.Contains(string(active), `"status":"running"`) {
		t.Fatalf("unsuccessful preemption changed active Job: %s", active)
	}
}

func TestSequenceAPIRealEngineAdmissionReplayAndCancel(t *testing.T) {
	server, backend := sequenceRouteServer(t)
	code, data, _ := sequenceRouteDo(t, server, "POST", "/v1/preview", `{"serial":"20000001","action":{"kind":"orbit","colors":["red","blue"],"period_ms":400}}`)
	if code != 200 || !strings.Contains(string(data), `"timing":"interval"`) || !strings.Contains(string(data), `"status":"supported"`) {
		t.Fatalf("preview: %d %s", code, data)
	}
	backend.mu.Lock()
	for _, op := range backend.operations {
		if op == "set" || strings.HasPrefix(op, "stop:") {
			t.Fatal("preview mutated device")
		}
	}
	backend.mu.Unlock()
	body := `{"id":"j_7K3XJNY9ZM","serial":"20000001","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#112233"}],"hold_ms":10000}]}}`
	code, data, headers := sequenceRouteDo(t, server, "POST", "/v1/jobs", body)
	if code != 202 || headers.Get("Location") != "/v1/jobs/j_7K3XJNY9ZM" {
		t.Fatalf("submit: %d %s", code, data)
	}
	var accepted control.SequenceJob
	if err := json.Unmarshal(data, &accepted); err != nil {
		t.Fatal(err)
	}
	reordered := `{"sequence":{"steps":[{"hold_ms":10000,"targets":[{"color":"#112233","led":0}]}],"timing":"serial"},"serial":"20000001","id":"j_7K3XJNY9ZM"}`
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", reordered)
	if code != 200 {
		t.Fatalf("key ordering changed identity: %d %s", code, data)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", strings.Replace(body, "#112233", "#112234", 1))
	if code != 409 || !strings.Contains(string(data), `"code":"job_conflict"`) {
		t.Fatalf("conflict: %d %s", code, data)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", strings.Replace(body, "j_7K3XJNY9ZM", "another-job", 1))
	if code != 409 || !strings.Contains(string(data), `"code":"device_busy"`) {
		t.Fatalf("busy: %d %s", code, data)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs/"+accepted.ID+"/cancel", "")
	if code != 202 && code != 200 {
		t.Fatalf("cancel: %d %s", code, data)
	}
	deadline := time.Now().Add(time.Second)
	for {
		code, data, _ = sequenceRouteDo(t, server, "GET", "/v1/jobs/"+accepted.ID, "")
		var job control.SequenceJob
		if err := json.Unmarshal(data, &job); code != 200 || err != nil {
			t.Fatalf("lookup: %d %s %v", code, data, err)
		}
		if job.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs/"+accepted.ID+"/cancel", "")
	if code != 200 {
		t.Fatalf("terminal cancel: %d %s", code, data)
	}
}

func TestSequenceAPIStrictInputsAndGeneratedIDs(t *testing.T) {
	server, _ := sequenceRouteServer(t)
	for _, body := range []string{
		`{"action":{"kind":"set","color":"red"},"action":{"kind":"off"}}`,
		`{"action":{"kind":"set","color":"red"},"sequence":{}}`,
		`{"action":{"kind":"alternate","colors":["red","blue"],"transition":"cut"}}`,
		`{"action":{"kind":"off"},"id":"unexpected-in-preview"}`,
		`{"action":{"kind":"off"},"policy":"preempt"}`,
		`{"sequence":{"timing":"serial","steps":[],"frames":[]}}`,
	} {
		code, data, _ := sequenceRouteDo(t, server, "POST", "/v1/preview", body)
		if code != 400 {
			t.Fatalf("strict input accepted: %d %s", code, data)
		}
	}
	for _, value := range []string{`"replace"`, `""`, `"Preempt"`, `null`, `false`} {
		data := sequenceLibraryResponse(t, server, "POST", "/v1/jobs",
			`{"action":{"kind":"off"},"serial":"20000001","policy":`+value+`}`, 400)
		if !strings.Contains(string(data), `"code":"invalid_request"`) {
			t.Fatalf("invalid policy %s: %s", value, data)
		}
	}
	code, data, _ := sequenceRouteDo(t, server, "POST", "/v1/preview", `{"action":{"kind":"fade","color":"red","duration_ms":-1}}`)
	if code != 400 || !strings.Contains(string(data), `"field":"action.duration_ms"`) {
		t.Fatalf("field error: %d %s", code, data)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", `{"id":"","serial":"20000001","action":{"kind":"off"}}`)
	var job control.SequenceJob
	if err := json.Unmarshal(data, &job); code != 202 || err != nil || len(job.ID) != 12 || !strings.HasPrefix(job.ID, "j_") {
		t.Fatalf("generated ID: %d %s %v", code, data, err)
	}
	for _, path := range []string{"/v1/programs/preview", "/v1/frame-jobs/id", "/v2/light/preview"} {
		code, _, _ = sequenceRouteDo(t, server, "POST", path, `{}`)
		if code != 404 {
			t.Fatalf("unexpected route %s: %d", path, code)
		}
	}
}

func TestSequenceAPILostResponseKeepsOneAcceptedJob(t *testing.T) {
	server, backend := sequenceRouteServer(t)
	lost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(result, r)
		if result.Code != http.StatusAccepted {
			t.Errorf("first request was not accepted: %d %s", result.Code, result.Body.String())
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(lost.Close)
	body := `{"id":"j_RESPONSE01","serial":"20000001","sequence":{"timing":"serial","steps":[{"targets":[{"led":0,"color":"#123456"}],"hold_ms":10000}]}}`
	request, err := http.NewRequest("POST", lost.URL+"/v1/jobs", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := lost.Client().Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("fixture delivered a response")
	}
	code, data, _ := sequenceRouteDo(t, server, "GET", "/v1/jobs/j_RESPONSE01", "")
	if code != 200 {
		t.Fatalf("lost response also lost job: %d %s", code, data)
	}
	code, data, _ = sequenceRouteDo(t, server, "POST", "/v1/jobs", body)
	if code != 200 {
		t.Fatalf("replay did not recover job: %d %s", code, data)
	}
	deadline := time.Now().Add(time.Second)
	for {
		backend.mu.Lock()
		sets := 0
		for _, op := range backend.operations {
			if op == "set" {
				sets++
			}
		}
		backend.mu.Unlock()
		if sets > 1 {
			t.Fatal("replay repeated the device step")
		}
		if sets == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted job did not reach worker")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSequenceAPICancelledCallerIsUnavailable(t *testing.T) {
	server, _ := sequenceRouteServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("POST", "http://127.0.0.1/v1/jobs", strings.NewReader(`{"serial":"20000001","action":{"kind":"off"}}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(response, request)
	if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"unavailable"`) {
		t.Fatalf("caller cancellation: %d %s", response.Code, response.Body.String())
	}
}

type flashRouteFixture struct {
	control.Service
	calls          int
	serial, preset string
	result         flashreset.Result
	err            error
}

func (f *flashRouteFixture) Device(serial string) (control.DeviceState, error) {
	if serial != "20000002" && serial != "20000003" {
		return control.DeviceState{}, control.ErrNotFound
	}
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial}}, nil
}

func issueTestFlashTicket(t *testing.T, h http.Handler, serial, token string) string {
	t.Helper()
	r := httptest.NewRequest("GET", "http://127.0.0.1/v1/devices/"+serial+"/flash/reset-ticket", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var ticket httpapi.FlashResetTicket
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &ticket) != nil || len(ticket.Ticket) != 32 {
		t.Fatalf("issue ticket %d: %s", w.Code, w.Body.String())
	}
	return ticket.Ticket
}

func (f *flashRouteFixture) ResetFlash(_ context.Context, serial, preset string) (flashreset.Result, error) {
	f.calls++
	f.serial, f.preset = serial, preset
	return f.result, f.err
}

func TestFlashResetRouting(t *testing.T) {
	const route = "/v1/devices/20000002/flash/reset"
	const token = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for _, tc := range []struct {
		name, method, path, body, media, origin string
		token                                   bool
		status                                  int
		preset                                  string
	}{
		{"default", "POST", route, "{}", "application/json", "", true, 200, "off"},
		{"explicit", "POST", route, `{"preset":"fireflies"}`, "application/json", "", true, 200, "fireflies"},
		{"missing body", "POST", route, "", "application/json", "", true, 400, ""},
		{"null", "POST", route, `{"preset":null}`, "application/json", "", true, 400, ""},
		{"empty preset", "POST", route, `{"preset":""}`, "application/json", "", true, 400, ""},
		{"unknown preset", "POST", route, `{"preset":"factory"}`, "application/json", "", true, 400, ""},
		{"duplicate", "POST", route, `{"preset":"off","preset":"rainbow"}`, "application/json", "", true, 400, ""},
		{"unknown field", "POST", route, `{"presets":"rainbow"}`, "application/json", "", true, 400, ""},
		{"trailing query", "POST", route + "?", "{}", "application/json", "", true, 400, ""},
		{"empty serial", "POST", "/v1/devices//flash/reset", "{}", "application/json", "", true, 400, ""},
		{"wrong serial", "POST", "/v1/devices/default/flash/reset", "{}", "application/json", "", true, 400, ""},
		{"get", "GET", route, "{}", "application/json", "", true, 405, ""},
		{"bad media", "POST", route, "{}", "text/plain", "", true, 415, ""},
		{"missing token", "POST", route, "{}", "application/json", "", false, 401, ""},
		{"bad origin", "POST", route, "{}", "application/json", "https://example.test", true, 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &flashRouteFixture{result: flashreset.Result{Serial: "20000002", Preset: tc.preset, Firmware: 204, Lines: 16, Staged: 16, Verified: 16, Status: flashreset.Acknowledged}}
			handler := httpapi.New(f, httpapi.WithSequenceAPI(), httpapi.WithLocalProtection(func(h http.Handler) http.Handler { return remoteaccess.ProtectLocal(token, h) }))
			req := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.media)
			req.Header.Set(httpapi.FlashTicketHeader, issueTestFlashTicket(t, handler, "20000002", token))
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.token {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status || (tc.preset == "" && f.calls != 0) || (tc.preset != "" && (f.calls != 1 || f.serial != "20000002" || f.preset != tc.preset)) {
				t.Fatalf("HTTP %d, %d calls to %q/%q: %s", w.Code, f.calls, f.serial, f.preset, w.Body.String())
			}
		})
	}
}

func TestFlashResetFailurePreservesProgress(t *testing.T) {
	const serial = "20000002"
	errWrite := errors.New("device transfer returned an error")
	for _, tc := range []struct {
		outcome          string
		staged, verified int
		err              error
		status           int
		code             string
	}{
		{flashreset.Unconfirmed, 16, 16, errWrite, 502, "flash_unconfirmed"},
		{flashreset.StagingFailed, 5, 0, errWrite, 502, "flash_staging_failed"},
		{flashreset.Unconfirmed, 2, 0, errWrite, 500, "internal_error"},
		{"", 0, 0, flashreset.ErrUnsupported, 422, "unsupported_capability"},
		{"", 0, 0, control.ErrBusy, 409, "device_busy"},
		{"", 0, 0, control.ErrNotFound, 404, "device_not_managed"},
		{"", 0, 0, control.ErrOffline, 503, "device_offline"},
		{"", 0, 0, context.DeadlineExceeded, 503, "flash_outcome_unknown"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			f := &flashRouteFixture{err: tc.err}
			if tc.outcome != "" {
				f.result = flashreset.Result{Serial: serial, Preset: "off", Firmware: 204, Lines: 16, Staged: tc.staged, Verified: tc.verified, Status: tc.outcome}
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1/v1/devices/"+serial+"/flash/reset", strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler := httpapi.New(f, httpapi.WithSequenceAPI())
			r.Header.Set(httpapi.FlashTicketHeader, issueTestFlashTicket(t, handler, serial, ""))
			handler.ServeHTTP(w, r)
			var data httpapi.FlashResetFailure
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != tc.status || data.Error.Code != tc.code || f.calls != 1 || (w.Code == 502 && data.Result != f.result) || (w.Code != 502 && data.Result.Status != "") {
				t.Fatalf("HTTP %d, %d calls: %s", w.Code, f.calls, w.Body.String())
			}
		})
	}
}

func TestFlashResetTicketReplayAndRestart(t *testing.T) {
	const serial = "20000002"
	f := &flashRouteFixture{err: errors.New("save transfer uncertain"), result: flashreset.Result{Serial: serial, Preset: "off", Firmware: 204, Lines: 16, Staged: 16, Verified: 16, Status: flashreset.Unconfirmed}}
	h := httpapi.New(f, httpapi.WithSequenceAPI())
	token := issueTestFlashTicket(t, h, serial, "")
	post := func(handler http.Handler, target string, keys ...string) int {
		r := httptest.NewRequest("POST", "http://127.0.0.1/v1/devices/"+target+"/flash/reset", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		for _, key := range keys {
			r.Header.Add(httpapi.FlashTicketHeader, key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == 409 && !strings.Contains(w.Body.String(), `"code":"flash_ticket_used_or_expired"`) {
			t.Errorf("unexpected ticket error %s", w.Body.String())
		}
		return w.Code
	}
	if post(h, serial) != 409 || post(h, serial, token, token) != 409 || post(h, "20000003", token) != 409 || post(httpapi.New(f, httpapi.WithSequenceAPI()), serial, token) != 409 || f.calls != 0 {
		t.Fatal("a missing, ambiguous, wrongly targeted or prior-daemon ticket admitted a reset")
	}
	const attempts = 16
	results := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		go func() { results <- post(h, serial, token) }()
	}
	admitted := 0
	for i := 0; i < attempts; i++ {
		switch status := <-results; status {
		case 502:
			admitted++
		case 409:
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if admitted != 1 || f.calls != 1 || post(h, serial, token) != 409 {
		t.Fatalf("replayed a used ticket: %d valid responses, %d backend calls", admitted, f.calls)
	}
}

func TestFlashResetTicketRoutes(t *testing.T) {
	f := &flashRouteFixture{}
	h := httpapi.New(f, httpapi.WithSequenceAPI(), httpapi.WithLocalProtection(func(h http.Handler) http.Handler {
		return remoteaccess.ProtectLocal("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", h)
	}))
	for _, tc := range []struct {
		method, serial, query, body string
		auth                        bool
		status                      int
	}{
		{"GET", "20000002", "", "", false, 401},
		{"GET", "20000001", "", "", true, 404},
		{"GET", "default", "", "", true, 400},
		{"POST", "20000002", "", "", true, 405},
		{"GET", "20000002", "?serial=20000001", "", true, 400},
		{"GET", "20000002", "", "{}", true, 400},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1/v1/devices/"+tc.serial+"/flash/reset-ticket"+tc.query, strings.NewReader(tc.body))
		if tc.auth {
			r.Header.Set("Authorization", "Bearer 0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || f.calls != 0 {
			t.Errorf("%s %s: %d, %s", tc.method, r.URL, w.Code, w.Body)
		}
	}
}
