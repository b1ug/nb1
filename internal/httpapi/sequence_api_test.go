package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/sequence"
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
