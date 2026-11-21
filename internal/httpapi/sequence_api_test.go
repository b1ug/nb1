package httpapi_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
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
	if len(welcome.Endpoints) != 19 {
		t.Fatalf("directory has %d endpoints, want 19", len(welcome.Endpoints))
	}
	want := map[string]bool{
		"GET /": true, "GET /v1/triggers": true, "GET /v1/triggers/check": true,
		"POST /v1/jobs": true, "GET /v1/devices/{serial}": true,
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
		`{"sequence":{"timing":"serial","steps":[],"frames":[]}}`,
	} {
		code, data, _ := sequenceRouteDo(t, server, "POST", "/v1/preview", body)
		if code != 400 {
			t.Fatalf("strict input accepted: %d %s", code, data)
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
