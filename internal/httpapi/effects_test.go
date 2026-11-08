package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
)

func TestEffectsCompileAtHTTPBoundary(t *testing.T) {
	for _, body := range []string{
		`{"effect":{"kind":"steady","color":"#AaBbCc","led":1,"fade_ms":100}}`,
		`{"effect":{"kind":"pulse","color":"#AaBbCc","cycles":2,"period_ms":800}}`,
	} {
		s := &fakeService{}
		w := receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", body)
		var job control.Job
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || w.Code != 202 || s.submitCount != 1 {
			t.Fatalf("effect submission: %d %s %v", w.Code, w.Body, err)
		}
		if strings.Contains(body, "steady") {
			if job.Program.OnFinish != "hold" || job.Program.Repeat != 1 || len(job.Program.Steps) != 1 || job.Program.Steps[0].Color != "#aabbcc" || job.Program.Steps[0].FadeMS != 100 {
				t.Fatalf("steady not compiled: %+v", job.Program)
			}
		} else if job.Program.OnFinish != "off" || job.Program.Repeat != 2 || len(job.Program.Steps) != 3 || job.Program.Steps[1].FadeMS != 380 || job.Program.Steps[2].FadeMS != 390 {
			t.Fatalf("pulse not compiled: %+v", job.Program)
		}
	}
}

var invalidEffectEnvelopes = []string{
	`{}`,
	`{"policy":"replace"}`,
	`{"program":{"steps":[{"color":"#102030"}]},"effect":{"kind":"steady","color":"#102030"}}`,
	`{"program":null,"effect":{"kind":"steady","color":"#102030"}}`,
	`{"effect":null}`,
	`{"effect":{"kind":"steady","color":"#102030","fade_ms":null}}`,
	`{"effect":{"kind":"steady","color":"#102030","cycles":0}}`,
	`{"effect":{"kind":"steady","color":"#102030","on_finish":"hold"}}`,
	`{"effect":{"kind":"pulse","color":"#102030","fade_ms":0}}`,
	`{"effect":{"kind":"pulse","color":"#102030","cycles":0}}`,
	`{"effect":{"kind":"pulse","color":"#102030","period_ms":61}}`,
	`{"effect":{"kind":"pulse","color":"#102030","period_ms":1200000,"cycles":10000}}`,
	`{"effect":{"kind":"unknown","color":"#102030"}}`,
	`{"effect":{"kind":"steady","color":"#102030","led":3}}`,
	`{"effect":{"kind":"steady","color":"red"}}`,
	`{"effect":{"kind":"steady","color":"#102030","Kind":"steady"}}`,
	`{"effect":{"kind":"steady","color":"#102030","co\u006cor":"#102030"}}`,
	`{"effect":{"kind":"pulse","color":"#102030","extra":true}}`,
}

func TestInvalidEffectShapeAndCompileNeverReachSubmission(t *testing.T) {
	for _, body := range invalidEffectEnvelopes {
		s := &receiptService{}
		w := receiptHTTP(s, "POST", "/v1/devices/20005601/jobs", body)
		if w.Code != 400 || s.submitCount != 0 || s.calls != 0 {
			t.Fatalf("invalid effect reached executor: body=%s code=%d calls=%d/%d result=%s", body, w.Code, s.submitCount, s.calls, w.Body)
		}
	}
}

type effectTestBackend struct {
	mu          sync.Mutex
	sets        int
	entered     chan struct{}
	proceed     chan struct{}
	enteredOnce sync.Once
}

func (*effectTestBackend) Info() control.DeviceInfo { return control.DeviceInfo{Serial: "20005601"} }
func (b *effectTestBackend) Set(control.Step) error {
	b.mu.Lock()
	b.sets++
	b.mu.Unlock()
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.proceed
	return nil
}
func (*effectTestBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now()}, nil
}
func (*effectTestBackend) Stop(string) error { return nil }
func (*effectTestBackend) Close()            {}

type effectTestDriver struct{ backend *effectTestBackend }

func (d effectTestDriver) Open(string) (control.Backend, error) { return d.backend, nil }

func TestEffectAndNormalizedRawProgramShareReceiptWithoutReplaceSideEffects(t *testing.T) {
	b := &effectTestBackend{entered: make(chan struct{}), proceed: make(chan struct{})}
	e, err := control.New(effectTestDriver{b}, control.Options{Serials: []string{"20005601"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(b.proceed)
		ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := e.Device("20005601")
		if err == nil && state.Connection == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake device not online: %+v %v", state, err)
		}
		time.Sleep(time.Millisecond)
	}
	server := httptest.NewServer(httpapi.New(e))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	ticket, err := client.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.SubmitEffectWithReceipt(ctx, "20005601", control.Effect{Kind: "steady", Color: "#AaBbCc"}, "replace", ticket)
	if err != nil || first.Replayed {
		t.Fatalf("first effect: %+v %v", first, err)
	}
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("compiled effect never reached fake worker")
	}
	raw := first.Job.Program
	raw.Steps = append([]control.Step(nil), raw.Steps...)
	raw.Steps[0].Color = "#AABBCC"
	raw.Repeat = 0 // Normalize's equivalent default.
	second, err := client.SubmitWithReceipt(ctx, "20005601", raw, "", ticket)
	if err != nil || !second.Replayed || second.Job.ID != first.Job.ID {
		t.Fatalf("raw equivalent did not replay: %+v %v", second, err)
	}
	for _, body := range invalidEffectEnvelopes {
		w := receiptHTTP(e, "POST", "/v1/devices/20005601/jobs", body)
		if w.Code != 400 {
			t.Fatalf("invalid replacement: %d %s", w.Code, w.Body)
		}
	}
	job, err := e.Job(first.Job.ID)
	if err != nil || job.Status != "running" {
		t.Fatalf("invalid effect cancelled existing job: %+v %v", job, err)
	}
	b.mu.Lock()
	sets := b.sets
	b.mu.Unlock()
	if sets != 1 {
		t.Fatalf("replay/invalid replacements executed %d Set calls, want 1", sets)
	}
}
