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
	"github.com/b1ug/nb1/internal/lightaction"
)

var lightInput = []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":"lime","period_ms":1000,"cycles":2}}`)
var lightReceipt = control.ReceiptRequest{RequestID: "req_60000_0123456789abcdef0123456789abcdef", ExpectedDaemonInstanceID: "daemon_0123456789abcdef0123456789abcdef"}

func expectedLight(t *testing.T, data []byte, serial *string) LightPreview {
	t.Helper()
	result, err := lightaction.CompileRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := control.Preview(&result.Program, nil)
	if err != nil {
		t.Fatal(err)
	}
	if serial != nil {
		preview = control.WithDeviceSnapshot(preview, *serial, &control.DeviceState{DeviceInfo: control.DeviceInfo{Serial: *serial, Capabilities: []string{"set", "program", "raw-rgb", "hold"}}, Connection: "online"})
	}
	return LightPreview{Result: result, Preview: preview}
}
func lightJob(t *testing.T, data []byte) control.Job {
	result := expectedLight(t, data, nil).Result
	return control.Job{ID: "job-light", Device: "demo", Status: "queued", Program: result.Program, TotalSteps: result.TotalSteps, CreatedAt: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)}
}
func lightBody(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func lightHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
	w.Header().Set("X-NB1-Replayed", "false")
	w.Header().Set("Location", "/v1/devices/demo/requests/"+lightReceipt.RequestID+"?expected_daemon_instance_id="+lightReceipt.ExpectedDaemonInstanceID)
}

func TestLightClientPreservesOriginalActionAndBindsPreview(t *testing.T) {
	data := []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"breathe","color":{"space":"hsv","h_deg":120.500000,"s_pct":100,"v_pct":50},"period_ms":1000}}`)
	serial := "demo"
	expected := expectedLight(t, data, &serial)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/light/preview" || !strings.Contains(string(body), `120.500000`) || strings.Contains(string(body), `"program"`) {
			t.Errorf("not original action: %s %s %s", r.Method, r.URL, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	actual, err := client.PreviewLight(context.Background(), data, &serial)
	if err != nil || !reflect.DeepEqual(actual, expected) || atomic.LoadInt32(&calls) != 1 {
		t.Fatal(actual, err, calls)
	}
}

func TestLightClientPreviewRejectsMismatchedOrMalformedResult(t *testing.T) {
	valid := expectedLight(t, lightInput, nil)
	other := expectedLight(t, []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"blink","color":"red"}}`), nil)
	text := lightBody(t, valid)
	for _, tc := range []struct {
		name, body, media string
		status            int
	}{
		{"other-self-consistent-action", lightBody(t, other), "application/json", 200},
		{"wrong-kind", strings.Replace(text, `"kind":"breathe"`, `"kind":"pulse"`, 1), "application/json", 200},
		{"unknown-contract", strings.Replace(text, lightaction.Contract, "future/v2", 1), "application/json", 200},
		{"missing-default", strings.Replace(text, `"led":0,`, "", 1), "application/json", 200},
		{"unknown-field", strings.TrimSuffix(text, "}") + `,"extra":1}`, "application/json", 200},
		{"trailing", text + `{}`, "application/json", 200},
		{"wrong-media", text, "text/plain", 200},
		{"admission-status", text, "application/json", 202},
		{"oversized", text + strings.Repeat(" ", 2<<20), "application/json", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", tc.media)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			out, err := client.PreviewLight(context.Background(), lightInput, nil)
			if err == nil || !reflect.DeepEqual(out, LightPreview{}) || atomic.LoadInt32(&calls) != 1 {
				t.Fatal(out, err, calls)
			}
		})
	}
}

func TestLightClientBoundSubmitAndLookup(t *testing.T) {
	job := lightJob(t, lightInput)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		lightHeaders(w)
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(body, &fields)
			if r.URL.Path != "/v1/devices/demo/light" || len(fields) != 5 || string(fields["policy"]) != `"reject"` || fields["program"] != nil || string(fields["request_id"]) != `"`+lightReceipt.RequestID+`"` {
				t.Errorf("bad action submit: %s %s", r.URL, body)
			}
			w.WriteHeader(202)
		} else {
			if !strings.HasPrefix(r.URL.Path, "/v1/devices/demo/requests/") || r.URL.Query().Get("expected_daemon_instance_id") != lightReceipt.ExpectedDaemonInstanceID {
				t.Errorf("bad receipt lookup %s", r.URL)
			}
		}
		_ = json.NewEncoder(w).Encode(job)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	result, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
	if err != nil || result.Replayed || result.DaemonInstanceID != lightReceipt.ExpectedDaemonInstanceID || !reflect.DeepEqual(result.Job, job) {
		t.Fatal(result, err)
	}
	actual, err := client.LookupLightRequest(context.Background(), "demo", lightInput, lightReceipt, job.ID)
	if err != nil || !reflect.DeepEqual(actual, job) || atomic.LoadInt32(&calls) != 2 {
		t.Fatal(actual, err, calls)
	}
}

func TestLightClientBadAcceptancePreservesUnknownReceipt(t *testing.T) {
	valid := lightJob(t, lightInput)
	for _, name := range []string{"status200", "status204", "wrong-instance", "duplicate-instance", "bad-replay", "duplicate-replay", "wrong-location", "wrong-device", "wrong-program", "wrong-total", "negative-completed", "excess-completed", "incomplete-success", "missing-created", "unknown-status", "other-json", "wrong-media"} {
		t.Run(name, func(t *testing.T) {
			job := valid
			status := 202
			media := "application/json"
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				lightHeaders(w)
				switch name {
				case "status200":
					status = 200
				case "status204":
					status = 204
				case "wrong-instance":
					w.Header().Set("X-NB1-Daemon-Instance-ID", "other")
				case "duplicate-instance":
					w.Header().Add("X-NB1-Daemon-Instance-ID", lightReceipt.ExpectedDaemonInstanceID)
				case "bad-replay":
					w.Header().Set("X-NB1-Replayed", "maybe")
				case "duplicate-replay":
					w.Header().Add("X-NB1-Replayed", "false")
				case "wrong-location":
					w.Header().Set("Location", "/other")
				case "wrong-device":
					job.Device = "other"
				case "wrong-program":
					job.Program = control.Program{Steps: []control.Step{{Color: "#ff0000"}}, Repeat: 1, OnFinish: "off"}
				case "wrong-total":
					job.TotalSteps++
				case "negative-completed":
					job.CompletedSteps = -1
				case "excess-completed":
					job.CompletedSteps = job.TotalSteps + 1
				case "incomplete-success":
					job.Status = "succeeded"
				case "missing-created":
					job.CreatedAt = time.Time{}
				case "unknown-status":
					job.Status = "done"
				case "wrong-media":
					media = "text/plain"
				}
				w.Header().Set("Content-Type", media)
				w.WriteHeader(status)
				if name == "other-json" {
					_, _ = io.WriteString(w, lightBody(t, job)+`{}`)
				} else {
					_ = json.NewEncoder(w).Encode(job)
				}
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			result, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
			var receiptErr *ReceiptError
			if !errors.As(err, &receiptErr) || !receiptErr.OutcomeUnknown || receiptErr.Receipt != lightReceipt || !reflect.DeepEqual(result, control.ReceiptResult{}) || atomic.LoadInt32(&calls) != 1 {
				t.Fatal(result, err, calls)
			}
		})
	}
}

type lightRoundTripper func(*http.Request) (*http.Response, error)

func (fn lightRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestLightClientPostIsNotAutomaticallyReplayable(t *testing.T) {
	client, _ := New("http://127.0.0.1:1")
	defer client.Close()
	var calls int32
	client.http.Transport = lightRoundTripper(func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		if request.Method != "POST" || request.GetBody != nil {
			t.Errorf("action POST has a replayable request body")
		}
		return nil, errors.New("connection failed after request creation")
	})
	_, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
	var bound *ReceiptError
	if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt || atomic.LoadInt32(&calls) != 1 {
		t.Fatal(err, calls)
	}
}

func TestLightClientRejectsInputAndCompleteWireBudgetBeforeHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	for _, data := range [][]byte{
		[]byte(`{}`), []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":-0}}`), []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":1.0}}`), []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off","led":1e0}}`), []byte(`{"contract":"nb1-light-action/v1","action":{"kind":"off","kind":"set"}}`),
	} {
		if _, err := client.PreviewLight(context.Background(), data, nil); err == nil {
			t.Fatal("invalid preview accepted")
		}
		if _, err := client.SubmitLightWithReceipt(context.Background(), "demo", data, lightReceipt); err == nil {
			t.Fatal("invalid submit accepted")
		}
	}
	large := lightReceipt
	large.RequestID = strings.Repeat("x", lightaction.MaxRequestBytes)
	if _, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, large); err == nil {
		t.Fatal("over-budget final wire accepted")
	}
	invalidSerial := "../wrong"
	if _, err := client.PreviewLight(context.Background(), lightInput, &invalidSerial); err == nil {
		t.Fatal("invalid serial accepted")
	}
	if _, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, control.ReceiptRequest{}); err == nil {
		t.Fatal("empty binding accepted")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid input contacted HTTP %d times", calls)
	}
}

func TestLightClientOldDaemonAndKnownRejectionsNeverFallback(t *testing.T) {
	for _, status := range []int{404, 501, 409, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"busy","message":"fixture"}}`)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.PreviewLight(context.Background(), lightInput, nil)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if status == 404 || status == 501 {
				var api *Error
				if !errors.As(err, &api) || api.Code != "light_preview_unsupported" {
					t.Fatal(err)
				}
			}
			_, err = client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
			var bound *ReceiptError
			if !errors.As(err, &bound) || bound.Receipt != lightReceipt {
				t.Fatal(err)
			}
			if status == 404 || status == 501 {
				var api *Error
				if !errors.As(err, &api) || api.Code != "light_run_unsupported" {
					t.Fatal(err)
				}
			}
			if bound.OutcomeUnknown != (status >= 500) || atomic.LoadInt32(&calls) != 2 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestLightClientLookupCannotChangeAcceptedJobIdentity(t *testing.T) {
	job := lightJob(t, lightInput)
	job.ID = "other-job"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { lightHeaders(w); _ = json.NewEncoder(w).Encode(job) }))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	_, err := client.LookupLightRequest(context.Background(), "demo", lightInput, lightReceipt, "job-light")
	var bound *ReceiptError
	if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt {
		t.Fatal(err)
	}
}

func TestLightClientConflictingErrorCodesRemainUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"daemon_instance_changed","code":"invalid_request","message":"fixture"}}`)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	_, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
	var bound *ReceiptError
	if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt {
		t.Fatalf("conflicting error codes lost uncertainty or original receipt: %v", err)
	}
}

func TestLightClientMalformedRejectionsRetainOriginalTicket(t *testing.T) {
	valid := `{"error":{"code":"invalid_request","message":"fixture"}}`
	for _, tc := range []struct {
		name, body string
		media      []string
	}{
		{"duplicate-code", `{"error":{"code":"invalid_request","code":"daemon_instance_changed","message":"fixture"}}`, []string{"application/json"}},
		{"escaped-duplicate-code", `{"error":{"code":"daemon_instance_changed","\u0063ode":"invalid_request","message":"fixture"}}`, []string{"application/json"}},
		{"duplicate-envelope", `{"error":{"code":"daemon_instance_changed","message":"fixture"},"error":{"code":"invalid_request","message":"fixture"}}`, []string{"application/json"}},
		{"unknown-envelope-field", strings.TrimSuffix(valid, "}") + `,"extra":true}`, []string{"application/json"}},
		{"unknown-error-field", `{"error":{"code":"invalid_request","message":"fixture","extra":true}}`, []string{"application/json"}},
		{"noncanonical-field", `{"error":{"Code":"invalid_request","message":"fixture"}}`, []string{"application/json"}},
		{"missing-message", `{"error":{"code":"invalid_request"}}`, []string{"application/json"}},
		{"empty-code", `{"error":{"code":"","message":"fixture"}}`, []string{"application/json"}},
		{"wrong-code-type", `{"error":{"code":12,"message":"fixture"}}`, []string{"application/json"}},
		{"null-envelope", `null`, []string{"application/json"}},
		{"null-error", `{"error":null}`, []string{"application/json"}},
		{"null-message", `{"error":{"code":"invalid_request","message":null}}`, []string{"application/json"}},
		{"trailing-object", valid + `{}`, []string{"application/json"}},
		{"invalid-utf8", strings.Replace(valid, "fixture", string([]byte{0xff}), 1), []string{"application/json"}},
		{"unpaired-high-surrogate", strings.Replace(valid, "fixture", `\ud800`, 1), []string{"application/json"}},
		{"unpaired-low-surrogate", strings.Replace(valid, "fixture", `\udc00`, 1), []string{"application/json"}},
		{"broken-surrogate-pair", strings.Replace(valid, "fixture", `\ud800\u1234`, 1), []string{"application/json"}},
		{"wrong-media", valid, []string{"text/plain"}},
		{"missing-media", valid, nil},
		{"duplicate-media", valid, []string{"application/json", "application/json"}},
		{"oversized-error", valid + strings.Repeat(" ", 2<<20), []string{"application/json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header()["Content-Type"] = tc.media
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, previewErr := client.PreviewLight(context.Background(), lightInput, nil)
			var malformed *DecodeError
			if !errors.As(previewErr, &malformed) {
				t.Fatalf("malformed rejection was trusted: %v", previewErr)
			}
			_, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
			var bound *ReceiptError
			if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt || !errors.As(err, &malformed) || atomic.LoadInt32(&calls) != 2 {
				t.Fatalf("malformed rejection lost uncertainty, retried or rebound: %v; calls=%d", err, atomic.LoadInt32(&calls))
			}
		})
	}
}

func TestLightClientValidRejectionClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		unknown          bool
	}{
		{"known-rejected", `{"error":{"code":"invalid_request","message":"fixture"}}`, "invalid_request", false},
		{"instance-changed", `{"error":{"code":"daemon_instance_changed","message":"fixture"}}`, "daemon_instance_changed", true},
		{"escaped-keys-and-unicode", `{"\u0065rror":{"\u0063ode":"invalid_request","message":"颜色 \ud83d\udca1"}}`, "invalid_request", false},
		{"literal-replacement-is-valid", `{"error":{"code":"invalid_request","message":"�"}}`, "invalid_request", false},
		{"literal-escape-is-valid", `{"error":{"code":"invalid_request","message":"\\ud800"}}`, "invalid_request", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
			var bound *ReceiptError
			var api *Error
			if !errors.As(err, &bound) || bound.OutcomeUnknown != tc.unknown || bound.Receipt != lightReceipt || !errors.As(err, &api) || api.Code != tc.code || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("valid rejection incorrectly classified: %v; calls=%d", err, atomic.LoadInt32(&calls))
			}
		})
	}
}

func TestLightClientMalformedOldDaemonIsUnsupportedButUnknown(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if r.URL.Path != "/v1/light/preview" && r.URL.Path != "/v1/devices/demo/light" {
					t.Errorf("unexpected fallback to %s", r.URL)
				}
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "legacy response")
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			_, err := client.PreviewLight(context.Background(), lightInput, nil)
			var api *Error
			if !errors.As(err, &api) || api.Code != "light_preview_unsupported" {
				t.Fatalf("old preview route not explicit: %v", err)
			}
			_, err = client.SubmitLightWithReceipt(context.Background(), "demo", lightInput, lightReceipt)
			var bound *ReceiptError
			if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt || !errors.As(err, &api) || api.Code != "light_run_unsupported" || atomic.LoadInt32(&calls) != 2 {
				t.Fatalf("old route lost uncertainty, retried or rebound: %v; calls=%d", err, atomic.LoadInt32(&calls))
			}
		})
	}
}

func TestLightClientSuccessWireBoundaries(t *testing.T) {
	serial := "demo"
	for _, route := range []string{"preview", "submit", "lookup"} {
		t.Run(route, func(t *testing.T) {
			for _, tc := range []struct {
				name, text, suffix, decoded string
				media                       []string
				valid                       bool
			}{
				{name: "raw-invalid-utf8", text: string([]byte{0xff}), media: []string{"application/json"}},
				{name: "unpaired-high-surrogate", text: `\ud800`, media: []string{"application/json"}},
				{name: "unpaired-low-surrogate", text: `\udc00`, media: []string{"application/json"}},
				{name: "broken-surrogate-pair", text: `\ud800\u1234`, media: []string{"application/json"}},
				{name: "duplicate-json-media", text: "fixture", media: []string{"application/json", "application/json"}},
				{name: "conflicting-media", text: "fixture", media: []string{"application/json", "text/plain"}},
				{name: "wrong-media", text: "fixture", media: []string{"text/plain"}},
				{name: "missing-media", text: "fixture"},
				{name: "oversized", text: "fixture", suffix: strings.Repeat(" ", 2<<20), media: []string{"application/json"}},
				{name: "literal-unicode", text: "颜色💡", decoded: "颜色💡", media: []string{"application/json; charset=utf-8"}, valid: true},
				{name: "paired-surrogate", text: `\ud83d\udca1`, decoded: "💡", media: []string{"application/json"}, valid: true},
				{name: "literal-replacement", text: "�", decoded: "�", media: []string{"application/json"}, valid: true},
				{name: "literal-escaped-text", text: `\\ud800`, decoded: `\ud800`, media: []string{"application/json"}, valid: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					const marker = "wire-text-fixture"
					preview := expectedLight(t, lightInput, &serial)
					// Extra capability names are preserved by the snapshot contract;
					// they give preview a real string field independent of the Action.
					preview.Preview.Device.Capabilities = append(preview.Preview.Device.Capabilities, marker)
					job := lightJob(t, lightInput)
					job.Error = marker
					body, status := lightBody(t, job), http.StatusOK
					if route == "preview" {
						body = lightBody(t, preview)
					} else if route == "submit" {
						status = http.StatusAccepted
					}
					body = strings.Replace(body, marker, tc.text, 1) + tc.suffix
					var calls int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						atomic.AddInt32(&calls, 1)
						if route != "preview" {
							lightHeaders(w)
						}
						w.Header()["Content-Type"] = tc.media
						w.WriteHeader(status)
						_, _ = io.WriteString(w, body)
					}))
					defer server.Close()
					client, _ := New(server.URL)
					defer client.Close()
					var err error
					var actual string
					switch route {
					case "preview":
						var result LightPreview
						result, err = client.PreviewLight(context.Background(), lightInput, &serial)
						if err == nil {
							values := result.Preview.Device.Capabilities
							actual = values[len(values)-1]
						}
					case "submit":
						var result control.ReceiptResult
						result, err = client.SubmitLightWithReceipt(context.Background(), serial, lightInput, lightReceipt)
						actual = result.Job.Error
					case "lookup":
						var result control.Job
						result, err = client.LookupLightRequest(context.Background(), serial, lightInput, lightReceipt, job.ID)
						actual = result.Error
					}
					if atomic.LoadInt32(&calls) != 1 {
						t.Fatalf("response validation retried: %d", atomic.LoadInt32(&calls))
					}
					if tc.valid {
						if err != nil || actual != tc.decoded {
							t.Fatalf("valid Unicode changed or refused: actual=%q err=%v", actual, err)
						}
						return
					}
					var malformed *DecodeError
					if !errors.As(err, &malformed) || actual != "" {
						t.Fatalf("malformed success accepted: actual=%q err=%v", actual, err)
					}
					if route != "preview" {
						var bound *ReceiptError
						if !errors.As(err, &bound) || !bound.OutcomeUnknown || bound.Receipt != lightReceipt {
							t.Fatalf("malformed success lost uncertainty or ticket: %v", err)
						}
					}
				})
			}
		})
	}
}
