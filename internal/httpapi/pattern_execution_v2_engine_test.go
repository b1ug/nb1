package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

// Only an in-memory driver is injected: all hardware-like work is channel
// controlled. The handler is invoked directly, without a socket or daemon.
type publicPatternBackend struct{ patternBackend }

func (b *publicPatternBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "20005601", Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold"}}
}

type publicPatternDriver struct{ backend *publicPatternBackend }

func (d publicPatternDriver) Open(string) (control.Backend, error) { return d.backend, nil }
func startPublicPatternEngine(t *testing.T) (*control.Engine, *publicPatternBackend, func()) {
	t.Helper()
	b := &publicPatternBackend{patternBackend: patternBackend{entered: make(chan struct{}), release: make(chan struct{})}}
	e, err := control.New(publicPatternDriver{b}, control.Options{Serials: []string{"20005601"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		state, err := e.Device("20005601")
		if err == nil && state.Connection == "online" {
			return e, b, release
		}
		select {
		case <-timer.C:
			t.Fatal("memory Engine did not connect")
		case <-tick.C:
		}
	}
}
func publicEngineTicket(e *control.Engine, n int) control.ReceiptRequest {
	info := e.Instance()
	return control.ReceiptRequest{RequestID: info.Idempotency.RequestIDPrefix + fmt.Sprintf("%032x", n), ExpectedDaemonInstanceID: info.DaemonInstanceID}
}
func publicEngineRequest(ref patternidentity.Ref, key control.ReceiptRequest, policy string) map[string]interface{} {
	return map[string]interface{}{"pattern_ref": ref, "policy": policy, "request_id": key.RequestID, "expected_daemon_instance_id": key.ExpectedDaemonInstanceID}
}
func publicEnvelopeFields(t *testing.T, data []byte) (string, string, patternidentity.Ref, string) {
	t.Helper()
	var e struct {
		Ref    patternidentity.Ref `json:"pattern_ref"`
		Digest string              `json:"execution_digest"`
		Kind   string              `json:"job_kind"`
		Job    struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	if e.Job.ID == "" || e.Digest == "" {
		t.Fatal(string(data))
	}
	return e.Job.ID, e.Job.Status, e.Ref, e.Kind
}

func TestPatternExecutionV2MemoryEngineReceiptSurvivesStoreFailureAndClose(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, release := startPublicPatternEngine(t)
			r := publicRecord(t, kind)
			ref := publicRef(r)
			key := publicEngineTicket(e, 1)
			store := patternV2Store(t)
			snapshot, err := patternsv2.EncodeSnapshot([]patternsv2.Record{r})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Import(snapshot); err != nil {
				t.Fatal(err)
			}
			h := httpapi.New(e, httpapi.WithPatternStoreV2(store))
			body := publicBytes(t, publicEngineRequest(ref, key, "reject"))
			w := patternHTTP(h, "POST", "/v2/devices/20005601/pattern-jobs", body)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			id, _, gotRef, gotKind := publicEnvelopeFields(t, w.Body.Bytes())
			if gotRef != ref || gotKind != kind {
				t.Fatal(gotRef, gotKind)
			}
			location := w.Header().Get("Location")
			u, err := http.NewRequest("GET", "http://127.0.0.1"+location, nil)
			if err != nil {
				t.Fatal(err)
			}
			q := u.URL.Query()
			if len(q) != 4 || q.Get("record_contract") != ref.RecordContract || q.Get("pattern_id") != ref.ID || q.Get("revision") != ref.Revision || q.Get("expected_daemon_instance_id") != key.ExpectedDaemonInstanceID {
				t.Fatal(location)
			}
			select {
			case <-b.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("memory Set did not enter")
			}
			// An accepted identity is hidden from both old unbound Job families.
			for _, path := range []string{"/v1/jobs/" + id, "/v1/frame-jobs/" + id} {
				patternV2Error(t, patternHTTP(h, "GET", path, nil), 404, "not_found")
			}
			// Destroy only this test's private snapshot. Replay must never reopen it.
			if err = os.Remove(filepath.Join(store.Path(), "patterns.json")); err != nil {
				t.Fatal(err)
			}
			for _, options := range [][]httpapi.Option{nil, {httpapi.WithPatternStoreV2(store)}, {httpapi.WithPatternDirectoryV2("invalid-relative")}} {
				handler := httpapi.New(e, options...)
				w = patternHTTP(handler, "POST", "/v2/devices/20005601/pattern-jobs", body)
				if w.Code != 202 || w.Header().Get("X-NB1-Replayed") != "true" {
					t.Fatal(w.Code, w.Body.String())
				}
				same, _, _, _ := publicEnvelopeFields(t, w.Body.Bytes())
				if same != id {
					t.Fatal("duplicate admission", same, id)
				}
				w = patternHTTP(handler, "GET", location, nil)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				bad := publicBytes(t, map[string]interface{}{"pattern_ref": ref, "expected_daemon_instance_id": key.ExpectedDaemonInstanceID, "confirmed_job_id": "job_" + strings.Repeat("f", 32)})
				patternV2Error(t, patternHTTP(handler, "POST", strings.Split(location, "?")[0]+"/cancel", bad), 409, "idempotency_conflict")
			}
			handler := httpapi.New(e, httpapi.WithPatternDirectoryV2("invalid-relative"))
			cancel := publicBytes(t, map[string]interface{}{"pattern_ref": ref, "expected_daemon_instance_id": key.ExpectedDaemonInstanceID, "confirmed_job_id": id})
			w = patternHTTP(handler, "POST", strings.Split(location, "?")[0]+"/cancel", cancel)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			same, status, _, _ := publicEnvelopeFields(t, w.Body.Bytes())
			if same != id || status != "cancelling" {
				t.Fatal(same, status)
			}
			release()
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			if err = e.Close(ctx); err != nil {
				t.Error(err)
			}
			stop()
			for _, route := range []struct {
				method, path string
				body         []byte
				status       int
			}{{"GET", location, nil, 200}, {"POST", "/v2/devices/20005601/pattern-jobs", body, 202}, {"POST", strings.Split(location, "?")[0] + "/cancel", cancel, 202}} {
				w = patternHTTP(handler, route.method, route.path, route.body)
				if w.Code != route.status {
					t.Fatal(w.Code, w.Body.String())
				}
				same, status, _, _ = publicEnvelopeFields(t, w.Body.Bytes())
				if same != id || status != "cancelled" {
					t.Fatal(same, status)
				}
			}
			b.mu.Lock()
			sets := b.sets
			b.mu.Unlock()
			if sets != 1 {
				t.Fatal("replay started more Set operations", sets)
			}
		})
	}
}

func TestPatternExecutionV2MemoryEngineNewFailuresDoNotReplaceActiveJob(t *testing.T) {
	e, b, release := startPublicPatternEngine(t)
	r := publicRecord(t, "program")
	ref := publicRef(r)
	store := &publicPatternStore{record: r}
	h := httpapi.New(e, httpapi.WithPatternStoreV2(store))
	key := publicEngineTicket(e, 1)
	body := publicBytes(t, publicEngineRequest(ref, key, "reject"))
	w := patternHTTP(h, "POST", "/v2/devices/20005601/pattern-jobs", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	originalID, _, _, _ := publicEnvelopeFields(t, w.Body.Bytes())
	select {
	case <-b.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Set did not enter")
	}
	newKey := publicEngineTicket(e, 2)
	for _, options := range [][]httpapi.Option{nil, {httpapi.WithPatternDirectoryV2("invalid-relative")}, {httpapi.WithPatternStoreV2(&publicPatternStore{fail: patternstore.ErrCorrupt})}} {
		handler := httpapi.New(e, options...)
		w = patternHTTP(handler, "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, publicEngineRequest(ref, newKey, "replace")))
		if w.Code != 501 && w.Code != 503 {
			t.Fatal(w.Code, w.Body.String())
		}
		original, err := e.PatternRequest("20005601", key, ref)
		if err != nil || original.ProgramJob.ID != originalID || original.ProgramJob.Status != "running" {
			t.Fatal("failed replace changed active identity", original, err)
		}
		if _, err = e.PatternRequest("20005601", newKey, ref); err != control.ErrRequestNotFound {
			t.Fatal("failure left receipt", err)
		}
	}
	// Same ticket with different source identity or policy always conflicts;
	// neither may be silently replayed nor steal an already active operation.
	changed := ref
	changed.ID = "different"
	for _, v := range []map[string]interface{}{publicEngineRequest(changed, key, "reject"), publicEngineRequest(ref, key, "queue")} {
		w = patternHTTP(h, "POST", "/v2/devices/20005601/pattern-jobs", publicBytes(t, v))
		patternV2Error(t, w, 409, "idempotency_conflict")
	}
	if store.calls != 1 {
		t.Fatal("preflight conflict re-resolved", store.calls)
	}
	release()
}
