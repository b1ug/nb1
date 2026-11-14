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
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

func v2RunningCancelJob(t *testing.T) control.FrameJob {
	job := v2ClientJob(t)
	start := job.CreatedAt.Add(time.Millisecond)
	job.Status, job.StartedAt = "running", &start
	job.CompletedFrames, job.CompletedTargetWrites = 1, 4
	return job
}

func TestLightV2CancelConfirmsOriginalReceiptAndStopsAtTerminal(t *testing.T) {
	for _, initial := range []string{"running", "cancelling", "succeeded", "cancelled", "failed"} {
		t.Run(initial, func(t *testing.T) {
			before := v2RunningCancelJob(t)
			before.Status = initial
			if initial == "succeeded" || initial == "cancelled" || initial == "failed" {
				finish := before.StartedAt.Add(time.Millisecond)
				before.FinishedAt = &finish
				if initial == "succeeded" {
					before.CompletedFrames, before.CompletedTargetWrites = before.TotalFrames, before.PlannedTargetWrites
				}
				if initial == "failed" {
					before.Error = "already failed"
				}
			}
			post := v2RunningCancelJob(t)
			post.Status = "cancelling"
			confirmed := v2RunningCancelJob(t)
			confirmed.Status = "cancelled"
			finish := confirmed.StartedAt.Add(time.Millisecond)
			confirmed.FinishedAt = &finish
			var gets, posts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					n := atomic.AddInt32(&gets, 1)
					if r.URL.RequestURI() != lightV2ReceiptPath("demo", lightReceipt) {
						t.Errorf("wrong receipt route: %s", r.URL)
					}
					v2ClientHeaders(w, false)
					if n == 1 {
						_ = json.NewEncoder(w).Encode(before)
					} else {
						_ = json.NewEncoder(w).Encode(confirmed)
					}
					return
				}
				atomic.AddInt32(&posts, 1)
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/frame-jobs/"+before.ID+"/cancel" || string(body) != `{"expected_daemon_instance_id":"`+lightReceipt.ExpectedDaemonInstanceID+`"}` {
					t.Errorf("wrong cancellation POST: %s %s", r.URL, body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
				w.WriteHeader(202)
				_ = json.NewEncoder(w).Encode(post)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			result, err := client.CancelLightV2(context.Background(), "demo", v2ClientInput, lightReceipt, "")
			if err != nil || result.DaemonInstanceID != lightReceipt.ExpectedDaemonInstanceID {
				t.Fatalf("%#v %v", result, err)
			}
			if initial == "running" {
				if !result.CancelAttempted || result.AlreadyTerminal || !reflect.DeepEqual(result.Job, confirmed) || gets != 2 || posts != 1 {
					t.Fatalf("missing receipt confirmation: %#v gets=%d posts=%d", result, gets, posts)
				}
			} else {
				terminal := initial != "cancelling"
				if result.CancelAttempted || result.AlreadyTerminal != terminal || !reflect.DeepEqual(result.Job, before) || gets != 1 || posts != 0 {
					t.Fatalf("unnecessary cancellation: %#v gets=%d posts=%d", result, gets, posts)
				}
			}
		})
	}
}

func TestLightV2CancelRejectsChangedOriginalBeforeAnyPost(t *testing.T) {
	for _, kind := range []string{"program", "job-id", "instance", "expired", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			job := v2RunningCancelJob(t)
			var posts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					atomic.AddInt32(&posts, 1)
				}
				v2ClientHeaders(w, false)
				switch kind {
				case "program":
					job.Program.Initial[0].Color = "#ffffff"
				case "job-id":
					job.ID = "framejob_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				case "instance":
					w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
				case "expired":
					w.WriteHeader(410)
					_, _ = io.WriteString(w, `{"error":{"code":"request_expired","message":"expired"}}`)
					return
				case "unsupported":
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"route"}}`)
					return
				}
				_ = json.NewEncoder(w).Encode(job)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			id := v2ClientJob(t).ID
			result, err := client.CancelLightV2(context.Background(), "demo", v2ClientInput, lightReceipt, id)
			var unknown *ReceiptError
			if !errors.As(err, &unknown) || !unknown.OutcomeUnknown || unknown.Receipt != lightReceipt || result.CancelAttempted || posts != 0 || result.Job.ID != "" {
				t.Fatalf("bad original allowed cancellation: %#v %v posts=%d", result, err, posts)
			}
		})
	}
}

func TestLightV2CancelBadPostOrConfirmationKeepsTrustedJob(t *testing.T) {
	for _, kind := range []string{"post-program", "post-time", "post-running", "post-instance", "post-duplicate-instance", "post-missing-instance", "post-status", "post-404", "post-invalid-json", "confirm-instance", "confirm-backwards", "confirm-404"} {
		t.Run(kind, func(t *testing.T) {
			before, post, after := v2RunningCancelJob(t), v2RunningCancelJob(t), v2RunningCancelJob(t)
			post.Status, after.Status = "cancelling", "cancelling"
			var gets, posts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					n := atomic.AddInt32(&gets, 1)
					v2ClientHeaders(w, false)
					if n == 1 {
						_ = json.NewEncoder(w).Encode(before)
						return
					}
					switch kind {
					case "confirm-instance":
						w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
					case "confirm-backwards":
						after.Status = "running"
					case "confirm-404":
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `{"error":{"code":"request_not_found","message":"not found"}}`)
						return
					}
					_ = json.NewEncoder(w).Encode(after)
					return
				}
				atomic.AddInt32(&posts, 1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
				status := 202
				switch kind {
				case "post-program":
					post.Program.Initial[0].Color = "#ffffff"
				case "post-time":
					post.CreatedAt = post.CreatedAt.Add(-time.Millisecond)
				case "post-running":
					post.Status = "running"
				case "post-instance":
					w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
				case "post-missing-instance":
					w.Header().Del("X-NB1-Daemon-Instance-ID")
				case "post-duplicate-instance":
					w.Header().Add("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
					w.Header().Add("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
				case "post-status":
					status = 200
				case "post-404":
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"route"}}`)
					return
				case "post-invalid-json":
					w.WriteHeader(202)
					_, _ = io.WriteString(w, `{"bad":`)
					return
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(post)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			result, err := client.CancelLightV2(context.Background(), "demo", v2ClientInput, lightReceipt, "")
			var unknown *ReceiptError
			if !errors.As(err, &unknown) || !unknown.OutcomeUnknown || unknown.Receipt != lightReceipt || !result.CancelAttempted || !reflect.DeepEqual(result.Job, before) || posts != 1 {
				t.Fatalf("bad cancel response trusted: %#v %v posts=%d", result, err, posts)
			}
			wantGets := int32(1)
			if strings.HasPrefix(kind, "confirm-") {
				wantGets = 2
			}
			if gets != wantGets {
				t.Fatalf("unexpected query/retry count %d", gets)
			}
		})
	}
}

func TestLightV2CancelTransportFailureDoesNotReplayEmptyPost(t *testing.T) {
	before := v2RunningCancelJob(t)
	payload, _ := json.Marshal(before)
	client, _ := New("http://127.0.0.1:8931")
	defer client.Close()
	gets, posts := 0, 0
	client.http.Transport = v2RoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Nb1-Daemon-Instance-Id": []string{lightReceipt.ExpectedDaemonInstanceID}}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
		}
		posts++
		body, _ := io.ReadAll(r.Body)
		if r.GetBody != nil || r.ContentLength <= 2 || string(body) != `{"expected_daemon_instance_id":"`+lightReceipt.ExpectedDaemonInstanceID+`"}` {
			t.Error("cancellation POST is replayable or empty")
		}
		return nil, io.ErrUnexpectedEOF
	})
	result, err := client.CancelLightV2(context.Background(), "demo", v2ClientInput, lightReceipt, "")
	var unknown *ReceiptError
	if !errors.As(err, &unknown) || !unknown.OutcomeUnknown || gets != 1 || posts != 1 || !reflect.DeepEqual(result.Job, before) {
		t.Fatalf("unexpected retry or lost job: %#v %v gets=%d posts=%d", result, err, gets, posts)
	}
}

func TestLightV2CancelRealHTTPFakeEngine(t *testing.T) {
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
		closing, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := engine.Close(closing); err != nil {
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
	var cancelPosts int32
	handler := httpapi.New(engine)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel") {
			atomic.AddInt32(&cancelPosts, 1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	ticket, err := client.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := client.SubmitLightV2WithReceipt(ctx, "demo", v2ClientInput, ticket)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CancelLightV2(ctx, "demo", v2ClientInput, ticket, accepted.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CancelAttempted || cancelPosts != 1 {
		t.Fatalf("cancel not issued exactly once: %#v posts=%d", result, cancelPosts)
	}
	for {
		job, err := client.LookupLightV2Request(ctx, "demo", v2ClientInput, ticket, accepted.Job.ID)
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
}
