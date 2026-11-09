package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

type noPreviewIO struct{}

func (noPreviewIO) Devices() []control.DeviceState { panic("preview called Devices") }
func (noPreviewIO) Device(string) (control.DeviceState, error) {
	panic("untargeted preview called Device")
}
func (noPreviewIO) Submit(context.Context, string, control.Program, string) (control.Job, error) {
	panic("preview submitted a job")
}
func (noPreviewIO) Job(string) (control.Job, error) { panic("preview read a job") }
func (noPreviewIO) Cancel(context.Context, string) (control.Job, error) {
	panic("preview cancelled a job")
}
func (noPreviewIO) Instance() control.InstanceInfo { panic("preview prepared a receipt") }

type previewSnapshot struct {
	noPreviewIO
	calls    int
	snapshot control.DeviceState
	err      error
}

func (s *previewSnapshot) Device(serial string) (control.DeviceState, error) {
	s.calls++
	return s.snapshot, s.err
}

const previewPulseBody = `{"effect":{"kind":"pulse","color":"#00A000","cycles":2,"period_ms":800}}`
const previewProgramBody = `{"program":{"steps":[{"color":"#00A000","fade_ms":200,"hold_ms":30}]}}`

func previewRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	return out
}
func decodePreview(t *testing.T, out *httptest.ResponseRecorder) control.PreviewResult {
	t.Helper()
	if out.Code != http.StatusOK {
		t.Fatal(out.Code, out.Body.String())
	}
	var result control.PreviewResult
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestPreviewAndCatalogNeverCallServiceWithoutTarget(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	catalog := previewRequest(h, http.MethodGet, "/v1/capabilities", "")
	var value control.CapabilityCatalog
	if catalog.Code != 200 || json.Unmarshal(catalog.Body.Bytes(), &value) != nil || control.ValidateCapabilityCatalog(value) != nil || value.PatternLibrary || value.HardwarePatternUpload {
		t.Fatal(catalog.Code, catalog.Body.String())
	}
	if catalog.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(catalog.Header())
	}
	for _, c := range []struct {
		body, kind string
		ms         int64
		steps      int
	}{{previewPulseBody, "effect", 1600, 6}, {previewProgramBody, "program", 240, 1}} {
		out := previewRequest(h, http.MethodPost, "/v1/programs/preview", c.body)
		r := decodePreview(t, out)
		if r.NominalDurationMS != c.ms || r.TotalSteps != c.steps || control.ValidatePreviewResult(r, c.kind, nil) != nil || out.Header().Get("Location") != "" || out.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
			t.Fatal(r, out.Header())
		}
	}
	// No engine, driver or ownership manager is needed to compile.
	decodePreview(t, previewRequest(httpapi.New(nil), http.MethodPost, "/v1/programs/preview", previewProgramBody))
}
func TestPreviewChecksOnlyOneConfiguredSnapshot(t *testing.T) {
	serial := "demo"
	for _, c := range []struct {
		name, connection string
		caps             []string
		err              error
		status, reason   string
		code             int
	}{
		{"missing", "", nil, control.ErrNotFound, "unknown", "not_configured", 200},
		{"offline", "offline", []string{"set", "program", "raw-rgb"}, nil, "unknown", "not_online", 200},
		{"empty", "", nil, nil, "unknown", "not_configured", 200},
		{"unavailable", "online", nil, nil, "unknown", "capabilities_unavailable", 200},
		{"unsupported", "online", []string{"set", "program"}, nil, "unsupported", "capabilities_missing", 200},
		{"busy-supported", "online", []string{"set", "program", "raw-rgb"}, nil, "supported", "", 200},
		{"closed", "", nil, control.ErrClosed, "", "", 503},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &previewSnapshot{snapshot: control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial, Capabilities: c.caps}, Connection: c.connection, ActiveJob: "another-job", QueueDepth: 8}, err: c.err}
			out := previewRequest(httpapi.New(service), http.MethodPost, "/v1/programs/preview", strings.TrimSuffix(previewPulseBody, "}")+`,"serial":"demo"}`)
			if service.calls != 1 || out.Code != c.code {
				t.Fatal(service.calls, out.Code, out.Body.String())
			}
			if c.code == 200 {
				r := decodePreview(t, out)
				if r.Validation.Device != c.status || r.Validation.Reason != c.reason || control.ValidatePreviewResult(r, "effect", &serial) != nil {
					t.Fatal(r)
				}
			}
		})
	}
}
func TestPreviewInputAndTransportBoundary(t *testing.T) {
	h := httpapi.New(noPreviewIO{})
	for _, body := range []string{`{}`, `null`, `[]`, `{"program":null}`, `{"effect":null}`, `{"Program":{"steps":[]}}`, `{"effect":{"kind":"steady","color":"#ffffff"},"e\u0066fect":{"kind":"pulse","color":"#ffffff"}}`, `{"effect":{"kind":"steady","color":"#ffffff"},"program":{"steps":[]}}`, `{"program":{"steps":[{"color":"#ffffff"}]},"policy":"reject"}`, `{"effect":{"kind":"steady","color":"#ffffff"},"serial":null}`, `{"effect":{"kind":"steady","color":"#ffffff"},"serial":""}`, `{"effect":{"kind":"steady","color":"#ffffff"},"serial":"a/b"}`, `{"pattern_ref":{"id":"not-supported"}}`, `{"program":{"steps":[{"color":"#ffffff"}],"repeat":10001}}`, `{"effect":{"kind":"pulse","color":"#ffffff","cycles":0}}`, previewPulseBody + ` {}`, strings.Repeat("{\"x\":", 65) + `0` + strings.Repeat("}", 65)} {
		out := previewRequest(h, http.MethodPost, "/v1/programs/preview", body)
		if out.Code != 400 {
			t.Fatal(body, out.Code, out.Body.String())
		}
	}
	for _, c := range []struct {
		method, path, body string
		code               int
	}{{http.MethodGet, "/v1/programs/preview", "", 405}, {http.MethodPost, "/v1/capabilities", "{}", 405}, {http.MethodGet, "/v1/capabilities?x=1", "", 400}, {http.MethodGet, "/v1/capabilities", "{}", 400}, {http.MethodPost, "/v1/programs/preview?serial=demo", previewPulseBody, 400}, {http.MethodPost, "/v1/programs/preview/", previewPulseBody, 404}, {http.MethodPost, "/v1/programs/preview", strings.Repeat(" ", httpapi.MaxBodyBytes) + `{}`, 413}} {
		out := previewRequest(h, c.method, c.path, c.body)
		if out.Code != c.code {
			t.Fatal(c.path, out.Code, out.Body.String())
		}
	}
	for _, c := range []struct {
		header, value string
		code          int
	}{{"Content-Type", "text/plain", 415}, {"Origin", "http://evil.example", 403}, {"Host", "evil.example", 403}} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/programs/preview", strings.NewReader(previewPulseBody))
		req.Header.Set("Content-Type", "application/json")
		if c.header == "Host" {
			req.Host = c.value
		} else {
			req.Header.Set(c.header, c.value)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != c.code {
			t.Fatal(c.header, out.Code, out.Body.String())
		}
	}
}

// The same effect submitted through the real Engine must retain the exact
// normalized program returned by preview. This backend is memory-only.
type previewMemoryBackend struct {
	mu          sync.Mutex
	observation control.Observation
}

func (b *previewMemoryBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "demo", Capabilities: []string{"set", "program", "raw-rgb", "hold"}}
}
func (b *previewMemoryBackend) Set(s control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observation.LED1 = s.Color
	b.observation.LED2 = s.Color
	return nil
}
func (b *previewMemoryBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.observation, nil
}
func (b *previewMemoryBackend) Stop(mode string) error { return nil }
func (b *previewMemoryBackend) Close()                 {}

type previewMemoryDriver struct{ backend *previewMemoryBackend }

func (d previewMemoryDriver) Open(string) (control.Backend, error) { return d.backend, nil }
func TestPreviewMatchesActualEngineAcceptedProgram(t *testing.T) {
	e, err := control.New(previewMemoryDriver{&previewMemoryBackend{}}, control.Options{Serials: []string{"demo"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := e.Close(c); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		s, _ := e.Device("demo")
		if s.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake did not open")
		}
		time.Sleep(time.Millisecond)
	}
	h := httpapi.New(e)
	for _, body := range []string{previewPulseBody, previewProgramBody} {
		r := decodePreview(t, previewRequest(h, http.MethodPost, "/v1/programs/preview", body))
		submitted := previewRequest(h, http.MethodPost, "/v1/devices/demo/jobs", strings.TrimSuffix(body, "}")+`,"policy":"replace"}`)
		var job control.Job
		if submitted.Code != 202 || json.Unmarshal(submitted.Body.Bytes(), &job) != nil || !reflect.DeepEqual(r.Program, job.Program) {
			t.Fatal(submitted.Code, r.Program, job, submitted.Body.String())
		}
	}
}
