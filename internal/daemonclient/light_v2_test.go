package daemonclient

import (
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

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

var v2ClientInput = []byte(`{"contract":"nb1-light-action/experimental-v2","action":{"kind":"orbit","colors":[{"space":"hsv","h_deg":120.500000,"s_pct":100,"v_pct":50},"blue"],"period_ms":200,"cycles":1,"segments":4}}`)

func v2ClientPreview(t *testing.T, serial *string) LightV2Preview {
	t.Helper()
	p, err := LocalLightV2Preview(v2ClientInput)
	if err != nil {
		t.Fatal(err)
	}
	p.Preview.Execution = control.FrameExecutionAvailability{Reason: "not_selected"}
	if serial != nil {
		p.Preview.Device = &LightV2PreviewDevice{Serial: *serial, Connection: "online"}
		p.Preview.Execution = control.FrameExecutionAvailability{Available: true, Reason: "cached_profile_supported"}
	}
	return p
}

func v2ClientJob(t *testing.T) control.FrameJob {
	t.Helper()
	expected := v2ClientPreview(t, nil).Result
	return control.FrameJob{ID: "framejob_0123456789abcdef0123456789abcdef", Device: "demo", Status: "queued", Program: expected.Program,
		TotalFrames: expected.TotalFrames, PlannedTargetWrites: expected.PlannedTargetWrites, CreatedAt: time.Date(2026, 11, 10, 1, 2, 3, 0, time.UTC)}
}

func v2ClientHeaders(w http.ResponseWriter, submit bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
	if submit {
		w.Header().Set("X-NB1-Replayed", "false")
		w.Header().Set("Location", lightV2ReceiptPath("demo", lightReceipt))
	}
}

func TestLightV2ClientOriginalActionPreviewAndReceiptRecovery(t *testing.T) {
	serial := "demo"
	preview, job := v2ClientPreview(t, &serial), v2ClientJob(t)
	var posts, gets int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&posts, 1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `120.500000`) || strings.Contains(string(body), `"frames"`) || strings.Contains(string(body), `"program"`) {
				t.Errorf("original high-level action was replaced: %s", body)
			}
			if r.URL.Path == "/v2/light/preview" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(preview)
				return
			}
			if r.URL.Path != "/v2/devices/demo/light" {
				t.Errorf("unexpected POST %s", r.URL)
			}
			v2ClientHeaders(w, true)
			w.WriteHeader(http.StatusAccepted)
		} else {
			atomic.AddInt32(&gets, 1)
			if r.URL.RequestURI() != lightV2ReceiptPath("demo", lightReceipt) {
				t.Errorf("wrong original receipt %s", r.URL)
			}
			v2ClientHeaders(w, false)
		}
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	got, err := client.PreviewLightV2(context.Background(), v2ClientInput, &serial)
	if err != nil || !reflect.DeepEqual(got, preview) {
		t.Fatalf("%#v %v", got, err)
	}
	accepted, err := client.SubmitLightV2WithReceipt(context.Background(), serial, v2ClientInput, lightReceipt)
	if err != nil || !reflect.DeepEqual(accepted.Job, job) {
		t.Fatalf("%#v %v", accepted, err)
	}
	for _, id := range []string{"", job.ID} {
		found, err := client.LookupLightV2Request(context.Background(), serial, v2ClientInput, lightReceipt, id)
		if err != nil || !reflect.DeepEqual(found, job) {
			t.Fatalf("%#v %v", found, err)
		}
	}
	if posts != 2 || gets != 2 {
		t.Fatalf("unexpected retries/prepares: posts=%d gets=%d", posts, gets)
	}
}

func TestLightV2ClientRejectsPreviewMutationAndMetadata(t *testing.T) {
	for _, name := range []string{"recipe", "program", "nominal", "writes", "available-without-serial", "device-without-serial", "unknown", "duplicate", "null", "header", "non-json"} {
		t.Run(name, func(t *testing.T) {
			value := v2ClientPreview(t, nil)
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "application/json")
				switch name {
				case "recipe":
					value.Result.RecipeID = "other"
				case "program":
					value.Preview.Program.Initial[0].Color = "#ff0000"
				case "nominal":
					value.Preview.NominalDurationMS++
				case "writes":
					value.Preview.PlannedTargetWrites++
				case "available-without-serial":
					value.Preview.Execution.Available = true
				case "device-without-serial":
					value.Preview.Device = &LightV2PreviewDevice{Serial: "demo", Connection: "online"}
				case "header":
					w.Header().Set("Location", "")
				case "non-json":
					w.Header().Set("Content-Type", "text/plain")
				}
				body, _ := json.Marshal(value)
				switch name {
				case "unknown":
					body = append(body[:len(body)-1], []byte(`,"unknown":true}`)...)
				case "duplicate":
					body = []byte(strings.Replace(string(body), `"total_frames":4`, `"total_frames":4,"total_frames":4`, 1))
				case "null":
					body = []byte(strings.Replace(string(body), `"available":false`, `"available":null`, 1))
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			if _, err := client.PreviewLightV2(context.Background(), v2ClientInput, nil); err == nil || calls != 1 {
				t.Fatalf("mutation accepted: %v calls=%d", err, calls)
			}
		})
	}
}

func TestLightV2ClientBadAcceptanceAlwaysPreservesOriginalTicket(t *testing.T) {
	for _, name := range []string{"program", "device", "job-id", "counts", "write-progress", "queued-start", "active-no-start", "terminal-no-finish", "backward-time", "failed-no-error", "instance", "replay", "location", "status", "trailing"} {
		t.Run(name, func(t *testing.T) {
			job := v2ClientJob(t)
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				v2ClientHeaders(w, true)
				status := 202
				switch name {
				case "program":
					job.Program.Frames[0].FadeMS += 10
				case "device":
					job.Device = "other"
				case "job-id":
					job.ID = "job-old"
				case "counts":
					job.TotalFrames++
				case "write-progress":
					job.CompletedTargetWrites = job.PlannedTargetWrites
				case "queued-start":
					now := job.CreatedAt.Add(time.Second)
					job.StartedAt = &now
				case "active-no-start":
					job.Status = "running"
				case "terminal-no-finish":
					job.Status = "succeeded"
				case "backward-time":
					now := job.CreatedAt.Add(-time.Second)
					job.Status = "cancelled"
					job.FinishedAt = &now
				case "failed-no-error":
					now := job.CreatedAt.Add(time.Second)
					job.Status = "failed"
					job.FinishedAt = &now
				case "instance":
					w.Header().Add("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
				case "replay":
					w.Header().Add("X-NB1-Replayed", "false")
				case "location":
					w.Header().Set("Location", "/v1/other")
				case "status":
					status = 200
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(job)
				if name == "trailing" {
					_, _ = io.WriteString(w, `{}`)
				}
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.SubmitLightV2WithReceipt(context.Background(), "demo", v2ClientInput, lightReceipt)
			var failure *ReceiptError
			if !errors.As(err, &failure) || !failure.OutcomeUnknown || failure.Receipt != lightReceipt || calls != 1 {
				t.Fatalf("lost uncertain receipt: %v calls=%d", err, calls)
			}
		})
	}
}

type v2RoundTrip func(*http.Request) (*http.Response, error)

func (f v2RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLightV2ClientTransportFailureCannotReplayPost(t *testing.T) {
	client, _ := New("http://127.0.0.1:8931")
	defer client.Close()
	calls := 0
	client.http.Transport = v2RoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil || r.Method != http.MethodPost || r.URL.Path != "/v2/devices/demo/light" {
			t.Error("replayable or low-level POST")
		}
		return nil, io.ErrUnexpectedEOF
	})
	_, err := client.SubmitLightV2WithReceipt(context.Background(), "demo", v2ClientInput, lightReceipt)
	var failure *ReceiptError
	if calls != 1 || !errors.As(err, &failure) || !failure.OutcomeUnknown || failure.Receipt != lightReceipt {
		t.Fatalf("%v calls=%d", err, calls)
	}
}

type v2EngineBackend struct {
	mu         sync.Mutex
	led1, led2 string
	writes     int
	first      chan struct{}
	once       sync.Once
}

func (b *v2EngineBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "demo", Capabilities: []string{"set", "raw-rgb", "mk2-basic", "hold"}}
}
func (b *v2EngineBackend) Set(s control.Step) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.LED == 0 || s.LED == 1 {
		b.led1 = s.Color
	}
	if s.LED == 0 || s.LED == 2 {
		b.led2 = s.Color
	}
	b.writes++
	b.once.Do(func() { close(b.first) })
	return nil
}
func (b *v2EngineBackend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.led1, LED2: b.led2, SampledAt: time.Now()}, nil
}
func (b *v2EngineBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "off" {
		b.led1, b.led2 = "#000000", "#000000"
	}
	return nil
}
func (b *v2EngineBackend) Close() {}

type v2EngineDriver struct{ backend *v2EngineBackend }

func (d v2EngineDriver) Open(string) (control.Backend, error) { return d.backend, nil }

func TestLightV2ClientRealHTTPFakeEngineAndCancelledReceipt(t *testing.T) {
	backend := &v2EngineBackend{led1: "#000000", led2: "#000000", first: make(chan struct{})}
	engine, err := control.New(v2EngineDriver{backend}, control.Options{Serials: []string{"demo"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := engine.Close(shutdown); err != nil {
			t.Error(err)
		}
	}()
	for {
		state, _ := engine.Device("demo")
		if state.Connection == "online" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	var highPosts, lowPosts int32
	handler := httpapi.New(engine)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.URL.Path == "/v2/devices/demo/light" {
				atomic.AddInt32(&highPosts, 1)
			} else if strings.Contains(r.URL.Path, "frame-jobs") {
				atomic.AddInt32(&lowPosts, 1)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	serial := "demo"
	preview, err := client.PreviewLightV2(ctx, v2ClientInput, &serial)
	if err != nil || !preview.Preview.Execution.Available {
		t.Fatalf("%#v %v", preview, err)
	}
	ticket, err := client.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := client.SubmitLightV2WithReceipt(ctx, serial, v2ClientInput, ticket)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.first:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = engine.CancelFrame(ctx, accepted.Job.ID); err != nil {
		t.Fatal(err)
	}
	for {
		job, err := client.LookupLightV2Request(ctx, serial, v2ClientInput, ticket, "")
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "cancelled" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if highPosts != 1 || lowPosts != 0 {
		t.Fatalf("request left high-level API: high=%d low=%d", highPosts, lowPosts)
	}
}
