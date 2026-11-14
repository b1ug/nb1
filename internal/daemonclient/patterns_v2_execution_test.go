package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
)

const executionInstanceV2 = "daemon_11111111111111111111111111111111"

func executionKeyV2() control.ReceiptRequest {
	return control.ReceiptRequest{RequestID: "req_60000_" + strings.Repeat("2", 32), ExpectedDaemonInstanceID: executionInstanceV2}
}
func executionRecordV2(t *testing.T, frame bool) patternsv2.Record {
	t.Helper()
	contract, action := "nb1-light-action/v1", `{"kind":"set","color":"lime"}`
	if frame {
		contract, action = "nb1-light-action/experimental-v2", `{"kind":"orbit","colors":["red","blue"],"period_ms":400,"cycles":1,"segments":4}`
	}
	r, err := patternsv2.NewRecord("client-fixture", "Pure source fixture", patternsv2.Source{Kind: "light_action", Contract: contract, Action: json.RawMessage(action)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func executionRefV2(r patternsv2.Record) patternidentity.Ref {
	return patternidentity.Ref{RecordContract: r.Contract, ID: r.ID, Revision: r.Revision}
}
func executionEnvelopeV2(t *testing.T, r patternsv2.Record) control.PatternJobEnvelope {
	t.Helper()
	digest, err := patternsv2.ExecutionDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	e := control.PatternJobEnvelope{PatternRef: executionRefV2(r), ExecutionDigest: digest}
	created := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if r.Compiled.Execution.Program != nil {
		p, err := control.Preview(r.Compiled.Execution.Program, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.JobKind = "program"
		e.ProgramJob = &control.Job{ID: "job_" + strings.Repeat("3", 32), Device: "fixture", Status: "queued", Program: p.Program, TotalSteps: p.TotalSteps, CreatedAt: created}
	} else {
		p, err := control.PreviewFrameProgram(*r.Compiled.Execution.FrameProgram)
		if err != nil {
			t.Fatal(err)
		}
		e.JobKind = "frame"
		e.FrameJob = &control.FrameJob{ID: "framejob_" + strings.Repeat("3", 32), Device: "fixture", Status: "queued", Program: p.Program, TotalFrames: p.TotalFrames, PlannedTargetWrites: p.PlannedTargetWrites, CreatedAt: created}
	}
	return e
}
func executionJSONV2(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func executionHeadersV2(w http.ResponseWriter, kind string, ref patternidentity.Ref) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NB1-Daemon-Instance-ID", executionInstanceV2)
	if kind == "submit" {
		w.Header().Set("X-NB1-Replayed", "false")
		w.Header().Set("Location", patternReceiptPathV2("fixture", ref, executionKeyV2()))
	}
}
func executionClientV2(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, s
}

type executionStoreV2 struct {
	httpapi.PatternStoreV2
	r     patternsv2.Record
	calls int
	fail  error
}

func (s *executionStoreV2) Resolve(id, revision string) (patternsv2.Record, error) {
	s.calls++
	if s.fail != nil {
		return patternsv2.Record{}, s.fail
	}
	if id != s.r.ID || revision != s.r.Revision {
		return patternsv2.Record{}, fmt.Errorf("wrong exact ref")
	}
	return s.r, nil
}

type executionServiceV2 struct {
	e                               control.PatternJobEnvelope
	submits, gets, cancels, devices int
	replay                          bool
	policy                          string
}

func (*executionServiceV2) Devices() []control.DeviceState { panic("unexpected Devices") }
func (s *executionServiceV2) Device(serial string) (control.DeviceState, error) {
	s.devices++
	return control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: serial, Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold"}}, Connection: "online"}, nil
}
func (*executionServiceV2) Submit(context.Context, string, control.Program, string) (control.Job, error) {
	panic("legacy Submit")
}
func (*executionServiceV2) Job(string) (control.Job, error) { panic("legacy Job") }
func (*executionServiceV2) Cancel(context.Context, string) (control.Job, error) {
	panic("legacy Cancel")
}
func (*executionServiceV2) Instance() control.InstanceInfo {
	panic("execution client must not prepare")
}
func (s *executionServiceV2) SubmitPatternWithReceipt(ctx context.Context, serial string, ref patternidentity.Ref, policy string, key control.ReceiptRequest, resolve control.PatternResolver) (control.PatternReceiptResult, error) {
	s.submits++
	s.policy = policy
	if !s.replay {
		if _, err := resolve(ctx, ref); err != nil {
			return control.PatternReceiptResult{}, err
		}
	}
	return control.PatternReceiptResult{Job: s.e, Replayed: s.replay, DaemonInstanceID: key.ExpectedDaemonInstanceID}, nil
}
func (s *executionServiceV2) PatternRequest(string, control.ReceiptRequest, patternidentity.Ref) (control.PatternJobEnvelope, error) {
	s.gets++
	return s.e, nil
}
func (s *executionServiceV2) CancelPatternRequest(_ context.Context, _ string, _ control.ReceiptRequest, _ patternidentity.Ref, id string) (control.PatternJobEnvelope, error) {
	s.cancels++
	if id != patternJobIDV2(s.e) {
		panic("cancel not bound to original Job")
	}
	if s.e.ProgramJob != nil {
		j := *s.e.ProgramJob
		j.Status = "cancelled"
		finished := j.CreatedAt.Add(time.Second)
		j.FinishedAt = &finished
		s.e.ProgramJob = &j
	} else {
		j := *s.e.FrameJob
		j.Status = "cancelled"
		finished := j.CreatedAt.Add(time.Second)
		j.FinishedAt = &finished
		s.e.FrameJob = &j
	}
	return s.e, nil
}

func TestPatternExecutionClientV2ActualHandlerAndStoreFreeReceipts(t *testing.T) {
	for _, frame := range []bool{false, true} {
		t.Run(fmt.Sprint(frame), func(t *testing.T) {
			r := executionRecordV2(t, frame)
			ref := executionRefV2(r)
			store := &executionStoreV2{r: r}
			service := &executionServiceV2{e: executionEnvelopeV2(t, r)}
			h := httpapi.New(service, httpapi.WithPatternStoreV2(store))
			var requests []string
			c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				h.ServeHTTP(w, r)
			}))
			for _, selected := range []bool{false, true} {
				var serial *string
				if selected {
					x := "fixture"
					serial = &x
				}
				p, err := c.PreviewPatternRefV2(context.Background(), ref, serial)
				if err != nil || p.PatternRef != ref || p.ExecutionDigest != service.e.ExecutionDigest {
					t.Fatal(p, err)
				}
			}
			for _, policy := range []string{"replace", "queue", "reject"} {
				result, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, policy, executionKeyV2())
				if err != nil || result.Job.PatternRef != ref || result.Job.ExecutionDigest != service.e.ExecutionDigest || service.policy != policy {
					t.Fatal(result, err)
				}
			}
			if store.calls != 5 || service.devices != 1 || service.submits != 3 {
				t.Fatal(store.calls, service)
			}
			service.replay = true
			h = httpapi.New(service, httpapi.WithPatternDirectoryV2("invalid-relative"))
			result, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
			if err != nil || !result.Replayed {
				t.Fatal(result, err)
			}
			got, err := c.LookupPatternV2Request(context.Background(), "fixture", ref, executionKeyV2(), patternJobIDV2(service.e), service.e.ExecutionDigest)
			if err != nil || got.PatternRef != ref {
				t.Fatal(got, err)
			}
			cancel, err := c.CancelPatternV2Request(context.Background(), "fixture", ref, executionKeyV2(), "", "")
			if err != nil || !cancel.CancelAttempted || cancel.Job == nil || patternStatusV2(*cancel.Job) != "cancelled" {
				t.Fatal(cancel, err)
			}
			again, err := c.CancelPatternV2Request(context.Background(), "fixture", ref, executionKeyV2(), "", "")
			if err != nil || again.CancelAttempted || !again.AlreadyTerminal || service.cancels != 1 || store.calls != 5 {
				t.Fatal(again, err, service)
			}
			for _, path := range requests {
				if path == "GET /v1/instance" || strings.Contains(path, "/v1/jobs") || strings.HasPrefix(path, "GET /v2/patterns/") {
					t.Fatal("unexpected prepare/library/history", requests)
				}
			}
		})
	}
}

func TestPatternExecutionClientV2RejectsMalformedAcceptedResponses(t *testing.T) {
	for _, frame := range []bool{false, true} {
		r := executionRecordV2(t, frame)
		ref := executionRefV2(r)
		e := executionEnvelopeV2(t, r)
		base := string(executionJSONV2(t, e))
		for name, mutate := range map[string]func(map[string]interface{}){
			"wrong-ref":    func(m map[string]interface{}) { m["pattern_ref"].(map[string]interface{})["id"] = "other" },
			"digest":       func(m map[string]interface{}) { m["execution_digest"] = "sha256:" + strings.Repeat("0", 64) },
			"wrong-device": func(m map[string]interface{}) { m["job"].(map[string]interface{})["device"] = "other" },
			"wrong-kind":   func(m map[string]interface{}) { m["job_kind"] = "future" },
			"bad-id":       func(m map[string]interface{}) { m["job"].(map[string]interface{})["id"] = "job_wrong" },
			"wrong-total": func(m map[string]interface{}) {
				j := m["job"].(map[string]interface{})
				if frame {
					j["total_frames"] = 100
				} else {
					j["total_steps"] = 100
				}
			},
			"queued-progress": func(m map[string]interface{}) {
				j := m["job"].(map[string]interface{})
				if frame {
					j["completed_target_writes"] = 1
				} else {
					j["completed_steps"] = 1
				}
			},
			"active-no-start": func(m map[string]interface{}) { m["job"].(map[string]interface{})["status"] = "running" },
			"failed-no-finish": func(m map[string]interface{}) {
				j := m["job"].(map[string]interface{})
				j["status"] = "failed"
			},
			"unknown-job-field": func(m map[string]interface{}) { m["job"].(map[string]interface{})["raw_usb"] = true },
			"missing-zero-field": func(m map[string]interface{}) {
				j := m["job"].(map[string]interface{})
				if frame {
					delete(j, "completed_frames")
				} else {
					delete(j, "completed_steps")
				}
			},
			"missing-frozen-field": func(m map[string]interface{}) {
				delete(m["job"].(map[string]interface{})["program"].(map[string]interface{}), "repeat")
			},
		} {
			t.Run(fmt.Sprint(frame)+"/"+name, func(t *testing.T) {
				var m map[string]interface{}
				if json.Unmarshal([]byte(base), &m) != nil {
					t.Fatal("fixture")
				}
				mutate(m)
				calls := 0
				c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					executionHeadersV2(w, "submit", ref)
					w.WriteHeader(202)
					w.Write(executionJSONV2(t, m))
				}))
				_, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
				var failure *ReceiptError
				var bad *DecodeError
				if !errors.As(err, &failure) || !failure.OutcomeUnknown || failure.Receipt != executionKeyV2() || !errors.As(err, &bad) || calls != 1 {
					t.Fatal(err, calls)
				}
			})
		}
		for name, body := range map[string]string{"duplicate": strings.TrimSuffix(base, "}") + `,"job_kind":"program"}`, "surrogate": strings.Replace(base, ref.ID, `\ud800`, 1), "trailing": base + " {}", "invalid-utf8": base + string([]byte{255}), "too-large": base + strings.Repeat(" ", 2<<20)} {
			t.Run(fmt.Sprint(frame)+"/raw-"+name, func(t *testing.T) {
				c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					executionHeadersV2(w, "submit", ref)
					w.WriteHeader(202)
					w.Write([]byte(body))
				}))
				_, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
				var failure *ReceiptError
				if !errors.As(err, &failure) || !failure.OutcomeUnknown {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestPatternExecutionClientV2HeadersAndErrorUncertaintyWhitelist(t *testing.T) {
	r := executionRecordV2(t, false)
	ref := executionRefV2(r)
	e := executionEnvelopeV2(t, r)
	for _, kind := range []string{"instance-missing", "instance-duplicate", "location", "location-duplicate", "replayed", "media-duplicate", "media-wrong", "wrong-status"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				executionHeadersV2(w, "submit", ref)
				switch kind {
				case "instance-missing":
					w.Header().Del("X-NB1-Daemon-Instance-ID")
				case "instance-duplicate":
					w.Header().Add("X-NB1-Daemon-Instance-ID", executionInstanceV2)
				case "location":
					w.Header().Set("Location", "/v1/jobs/other")
				case "location-duplicate":
					w.Header().Add("Location", patternReceiptPathV2("fixture", ref, executionKeyV2()))
				case "replayed":
					w.Header().Set("X-NB1-Replayed", "yes")
				case "media-duplicate":
					w.Header().Add("Content-Type", "application/json")
				case "media-wrong":
					w.Header().Set("Content-Type", "text/plain")
				}
				status := 202
				if kind == "wrong-status" {
					status = 200
				}
				w.WriteHeader(status)
				w.Write(executionJSONV2(t, e))
			}))
			_, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
			var failure *ReceiptError
			if !errors.As(err, &failure) || !failure.OutcomeUnknown {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		status  int
		code    string
		unknown bool
	}{{400, "invalid_pattern_request", false}, {400, "future", true}, {404, "pattern_not_found", false}, {404, "not_found", true}, {409, "busy", false}, {409, "idempotency_conflict", false}, {409, "daemon_instance_changed", true}, {410, "request_expired", true}, {429, "idempotency_capacity", false}, {501, "pattern_library_unsupported", true}, {500, "internal_error", true}, {302, "invalid_pattern_request", true}, {401, "invalid_request", true}} {
		t.Run(fmt.Sprint(tc.status)+tc.code, func(t *testing.T) {
			calls := 0
			c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(httpapi.ErrorResponse{Error: httpapi.ErrorBody{Code: tc.code, Message: "fixture"}})
			}))
			_, err := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
			var failure *ReceiptError
			if !errors.As(err, &failure) || failure.OutcomeUnknown != tc.unknown || failure.Receipt != executionKeyV2() || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestPatternExecutionClientV2CancelFailureRetainsBeforeAndDoesNotRecover(t *testing.T) {
	for _, frame := range []bool{false, true} {
		r := executionRecordV2(t, frame)
		ref := executionRefV2(r)
		before := executionEnvelopeV2(t, r)
		gets, posts := 0, 0
		c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionHeadersV2(w, "query", ref)
			if r.Method == "GET" {
				gets++
				w.Write(executionJSONV2(t, before))
				return
			}
			posts++
			var input struct {
				PatternRef patternidentity.Ref `json:"pattern_ref"`
				Instance   string              `json:"expected_daemon_instance_id"`
				JobID      string              `json:"confirmed_job_id"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.PatternRef != ref || input.Instance != executionInstanceV2 || input.JobID != patternJobIDV2(before) {
				t.Error("cancel lost identity", input)
			}
			w.WriteHeader(202)
			bad := string(executionJSONV2(t, before))
			bad = strings.Replace(bad, ref.ID, "other", 1)
			w.Write([]byte(bad))
		}))
		result, err := c.CancelPatternV2Request(context.Background(), "fixture", ref, executionKeyV2(), "", "")
		var uncertain *ReceiptError
		if !errors.As(err, &uncertain) || !uncertain.OutcomeUnknown || !result.CancelAttempted || result.Job == nil || !reflect.DeepEqual(*result.Job, before) || gets != 1 || posts != 1 {
			t.Fatal(result, err, gets, posts)
		}
	}
}

func TestPatternExecutionClientV2ProgressRequiresImmutableLifecycle(t *testing.T) {
	for _, frame := range []bool{false, true} {
		before := executionEnvelopeV2(t, executionRecordV2(t, frame))
		for _, kind := range []string{"ref", "digest", "created", "queued-progress", "terminal-no-finish"} {
			after := executionEnvelopeV2(t, executionRecordV2(t, frame))
			switch kind {
			case "ref":
				after.PatternRef.ID = "other"
			case "digest":
				after.ExecutionDigest = "sha256:" + strings.Repeat("0", 64)
			case "created":
				if frame {
					after.FrameJob.CreatedAt = after.FrameJob.CreatedAt.Add(time.Second)
				} else {
					after.ProgramJob.CreatedAt = after.ProgramJob.CreatedAt.Add(time.Second)
				}
			case "queued-progress":
				if frame {
					after.FrameJob.CompletedTargetWrites = 1
				} else {
					after.ProgramJob.CompletedSteps = 1
				}
			case "terminal-no-finish":
				if frame {
					after.FrameJob.Status = "succeeded"
				} else {
					after.ProgramJob.Status = "succeeded"
				}
			}
			if err := ValidatePatternV2Progress(before, after); err == nil {
				t.Fatal(frame, kind)
			}
		}
		if err := ValidatePatternV2Progress(before, before); err != nil {
			t.Fatal(err)
		}
	}
}

type executionRoundTripV2 func(*http.Request) (*http.Response, error)

func (f executionRoundTripV2) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type executionCloseBodyV2 struct{ *strings.Reader }

func (*executionCloseBodyV2) Close() error { return fmt.Errorf("fixture close failed") }
func TestPatternExecutionClientV2UnreadableClosureAndContradictoryErrorsStayUnknown(t *testing.T) {
	r := executionRecordV2(t, false)
	ref := executionRefV2(r)
	for _, kind := range []string{"close", "empty-message", "error-location"} {
		t.Run(kind, func(t *testing.T) {
			c, err := New("http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			calls := 0
			c.http.Transport = executionRoundTripV2(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.GetBody != nil || req.Method != "POST" {
					t.Error("execution POST replay enabled")
				}
				header := http.Header{"Content-Type": []string{"application/json"}}
				body := `{"error":{"code":"invalid_pattern_request","message":"fixture"}}`
				if kind == "empty-message" {
					body = `{"error":{"code":"invalid_pattern_request","message":""}}`
				}
				if kind == "error-location" {
					header.Set("Location", "/unexpected")
				}
				response := &http.Response{StatusCode: 400, Header: header, Body: io.NopCloser(strings.NewReader(body))}
				if kind == "close" {
					response.Body = &executionCloseBodyV2{strings.NewReader(body)}
				}
				return response, nil
			})
			_, err = c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", executionKeyV2())
			var unknown *ReceiptError
			var bad *DecodeError
			if !errors.As(err, &unknown) || !unknown.OutcomeUnknown || !errors.As(err, &bad) || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestPatternExecutionClientV2InvalidTypedInputMakesZeroHTTPCalls(t *testing.T) {
	c, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	calls := 0
	c.http.Transport = executionRoundTripV2(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("must not contact daemon")
	})
	ref := executionRefV2(executionRecordV2(t, false))
	badRef := ref
	badRef.Revision = "latest"
	key := executionKeyV2()
	serial := "bad.serial"
	_, a := c.PreviewPatternRefV2(context.Background(), badRef, nil)
	_, b := c.PreviewPatternRefV2(context.Background(), ref, &serial)
	_, d := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "", key)
	_, e := c.SubmitPatternV2WithReceipt(context.Background(), "fixture", ref, "reject", control.ReceiptRequest{})
	_, f := c.LookupPatternV2Request(context.Background(), "fixture", ref, key, "bad-id", "")
	_, g := c.CancelPatternV2Request(context.Background(), "fixture", ref, key, "", "bad-digest")
	for _, err := range []error{a, b, d, e, f, g} {
		if err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestPatternExecutionClientV2PreviewBindsFullFrozenFactsAndSelectedDevice(t *testing.T) {
	for _, frame := range []bool{false, true} {
		r := executionRecordV2(t, frame)
		ref := executionRefV2(r)
		service := &executionServiceV2{e: executionEnvelopeV2(t, r)}
		store := &executionStoreV2{r: r}
		handler := httpapi.New(service, httpapi.WithPatternStoreV2(store))
		input := executionJSONV2(t, map[string]interface{}{"pattern_ref": ref, "serial": "fixture"})
		request := httptest.NewRequest("POST", "http://127.0.0.1/v2/patterns/preview-ref", strings.NewReader(string(input)))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
		base := recorder.Body.Bytes()
		for name, mutate := range map[string]func(map[string]interface{}){
			"ref":    func(m map[string]interface{}) { m["pattern_ref"].(map[string]interface{})["id"] = "other" },
			"digest": func(m map[string]interface{}) { m["execution_digest"] = "sha256:" + strings.Repeat("0", 64) },
			"facts":  func(m map[string]interface{}) { m["preview"].(map[string]interface{})["nominal_duration_ms"] = 1 },
			"device": func(m map[string]interface{}) {
				m["preview"].(map[string]interface{})["device"].(map[string]interface{})["serial"] = "other"
			},
			"old-availability": func(m map[string]interface{}) {
				m["pattern_execution"] = map[string]interface{}{"available": false, "reason": "pattern_identity_admission_not_integrated"}
			},
		} {
			t.Run(fmt.Sprint(frame)+name, func(t *testing.T) {
				var m map[string]interface{}
				if err := json.Unmarshal(base, &m); err != nil {
					t.Fatal(err)
				}
				mutate(m)
				calls := 0
				c, _ := executionClientV2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(m)
				}))
				serial := "fixture"
				_, err := c.PreviewPatternRefV2(context.Background(), ref, &serial)
				var bad *DecodeError
				if !errors.As(err, &bad) || calls != 1 {
					t.Fatal(err, calls)
				}
			})
		}
	}
}
