package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

func executePatternV2Command(ctx context.Context, args ...string) (string, error) {
	previous := waitComplete
	defer func() { waitComplete = previous }()
	command := newControlCommand()
	// The actual application root owns this inherited flag. This isolated fixture
	// registers it too, so wait and guard assertions exercise real command logic.
	command.PersistentFlags().BoolVar(&waitComplete, "wait", false, "fixture root wait flag")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

type cliPatternBackendV2 struct {
	mu               sync.Mutex
	entered, release chan struct{}
	once             sync.Once
	sets             int
	fail             error
}

func (*cliPatternBackendV2) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "fixture", Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold"}}
}
func (b *cliPatternBackendV2) Set(control.Step) error {
	b.mu.Lock()
	b.sets++
	b.mu.Unlock()
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.fail
}
func (*cliPatternBackendV2) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (*cliPatternBackendV2) Stop(string) error { return nil }
func (*cliPatternBackendV2) Close()            {}

type cliPatternDriverV2 struct{ b *cliPatternBackendV2 }

func (d cliPatternDriverV2) Open(string) (control.Backend, error) { return d.b, nil }
func cliPatternEngineV2(t *testing.T) (*control.Engine, *cliPatternBackendV2, func()) {
	t.Helper()
	b := &cliPatternBackendV2{entered: make(chan struct{}), release: make(chan struct{})}
	e, err := control.New(cliPatternDriverV2{b}, control.Options{Serials: []string{"fixture"}, PollInterval: time.Hour})
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
		state, err := e.Device("fixture")
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
func cliPatternRecordV2(t *testing.T, frame bool) patternsv2.Record {
	t.Helper()
	contract, action := "nb1-light-action/v1", `{"kind":"set","color":"lime"}`
	if frame {
		contract, action = "nb1-light-action/experimental-v2", `{"kind":"orbit","period_ms":400,"segments":4,"cycles":1,"colors":["red","blue"]}`
	}
	r, err := patternsv2.NewRecord("cli-pattern", "Source fixture", patternsv2.Source{Kind: "light_action", Contract: contract, Action: json.RawMessage(action)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func cliPatternStoreV2(t *testing.T, r patternsv2.Record) *patternstore.StoreV2 {
	t.Helper()
	s, err := patternstore.NewV2(filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := patternsv2.EncodeSnapshot([]patternsv2.Record{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Import(data); err != nil {
		t.Fatal(err)
	}
	return s
}

type cliPatternEnvelopeV2 struct {
	Ref    patternidentity.Ref `json:"pattern_ref"`
	Digest string              `json:"execution_digest"`
	Kind   string              `json:"job_kind"`
	Job    struct {
		ID      string          `json:"id"`
		Status  string          `json:"status"`
		Program json.RawMessage `json:"program"`
	} `json:"job"`
}
type cliPatternOutputV2 struct {
	Ref       patternidentity.Ref    `json:"pattern_ref"`
	Policy    string                 `json:"policy"`
	Accepted  bool                   `json:"accepted"`
	Unknown   bool                   `json:"outcome_unknown"`
	Ticket    control.ReceiptRequest `json:"request_ticket"`
	Instance  string                 `json:"daemon_instance_id"`
	Replayed  *bool                  `json:"replayed"`
	Job       *cliPatternEnvelopeV2  `json:"job"`
	Attempted *bool                  `json:"cancel_attempted"`
	Error     string                 `json:"error"`
}

func cliPatternDecodeV2(t *testing.T, text string) cliPatternOutputV2 {
	t.Helper()
	var out cliPatternOutputV2
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(text, err)
	}
	return out
}
func cliPatternArgsV2(endpoint string, args ...string) []string {
	return append([]string{"--endpoint", endpoint, "patterns-v2"}, args...)
}
func cliPatternReceiptArgsV2(key control.ReceiptRequest) []string {
	return []string{"--request-id", key.RequestID, "--expected-daemon-instance-id", key.ExpectedDaemonInstanceID}
}

type cliPatternTraceV2 struct {
	mu       sync.Mutex
	rows     []string
	policies []string
}

func (x *cliPatternTraceV2) record(r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.rows = append(x.rows, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/pattern-jobs") {
		data, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(data))
		var b struct {
			Policy string `json:"policy"`
		}
		json.Unmarshal(data, &b)
		x.policies = append(x.policies, b.Policy)
	}
}
func (x *cliPatternTraceV2) counts() (prepare, submit, query, cancel, other int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, row := range x.rows {
		switch {
		case strings.HasPrefix(row, "GET /v1/instance?"):
			prepare++
		case strings.HasPrefix(row, "POST /v2/devices/fixture/pattern-jobs?"):
			submit++
		case strings.HasPrefix(row, "GET /v2/devices/fixture/pattern-requests/"):
			query++
		case strings.HasPrefix(row, "POST /v2/devices/fixture/pattern-requests/") && strings.Contains(row, "/cancel?"):
			cancel++
		default:
			other++
		}
	}
	return
}

func TestControlPatternsV2ExecutionRealEngineRetainsOriginalSourceAndReceipt(t *testing.T) {
	for _, frame := range []bool{false, true} {
		t.Run(fmt.Sprint(frame), func(t *testing.T) {
			e, b, release := cliPatternEngineV2(t)
			r := cliPatternRecordV2(t, frame)
			store := cliPatternStoreV2(t, r)
			var handlerMu sync.RWMutex
			var handler http.Handler = httpapi.New(e, httpapi.WithPatternStoreV2(store))
			trace := &cliPatternTraceV2{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				trace.record(r)
				handlerMu.RLock()
				h := handler
				handlerMu.RUnlock()
				h.ServeHTTP(w, r)
			}))
			defer server.Close()
			args := []string{"run", "fixture", r.ID, "--revision", r.Revision}
			text, err := executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, args...)...)
			out := cliPatternDecodeV2(t, text)
			if err != nil || !out.Accepted || out.Unknown || out.Policy != "reject" || out.Job == nil || out.Job.Ref != out.Ref || out.Ref.Revision != r.Revision || out.Replayed == nil || *out.Replayed {
				t.Fatal(text, err)
			}
			select {
			case <-b.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("memory Set not entered")
			}
			originalID, digest, key := out.Job.Job.ID, out.Job.Digest, out.Ticket
			// Accepted replay/query/cancel remain available after this private snapshot
			// disappears and v2 directory configuration fails. No client Get/preview.
			if err = os.Remove(filepath.Join(store.Path(), "patterns.json")); err != nil {
				t.Fatal(err)
			}
			handlerMu.Lock()
			handler = httpapi.New(e, httpapi.WithPatternDirectoryV2("invalid-relative"))
			handlerMu.Unlock()
			replayArgs := append(append([]string(nil), args...), cliPatternReceiptArgsV2(key)...)
			text, err = executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, replayArgs...)...)
			out = cliPatternDecodeV2(t, text)
			if err != nil || !out.Accepted || out.Replayed == nil || !*out.Replayed || out.Job.Job.ID != originalID || out.Job.Digest != digest {
				t.Fatal(text, err)
			}
			lookup := append([]string{"request", "fixture", r.ID, "--revision", r.Revision, "--job-id", originalID, "--execution-digest", digest}, cliPatternReceiptArgsV2(key)...)
			text, err = executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, lookup...)...)
			out = cliPatternDecodeV2(t, text)
			if err != nil || out.Job == nil || out.Job.Job.ID != originalID || out.Ticket != key || out.Unknown {
				t.Fatal(text, err)
			}
			cancel := append([]string{"cancel", "fixture", r.ID, "--revision", r.Revision, "--job-id", originalID, "--execution-digest", digest}, cliPatternReceiptArgsV2(key)...)
			text, err = executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, cancel...)...)
			out = cliPatternDecodeV2(t, text)
			if err != nil || out.Job == nil || out.Job.Job.Status != "cancelling" || out.Attempted == nil || !*out.Attempted || out.Ticket != key {
				t.Fatal(text, err)
			}
			release()
			text, err = executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, append(lookup, "--wait")...)...)
			out = cliPatternDecodeV2(t, text)
			if err == nil || out.Unknown || out.Job == nil || out.Job.Job.Status != "cancelled" || out.Ticket != key {
				t.Fatal("terminal state not preserved", text, err)
			}
			text, err = executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, cancel...)...)
			out = cliPatternDecodeV2(t, text)
			if err != nil || out.Attempted == nil || *out.Attempted || out.Job.Job.Status != "cancelled" {
				t.Fatal(text, err)
			}
			prepares, submits, queries, cancels, other := trace.counts()
			if prepares != 1 || submits != 2 || queries < 4 || cancels != 1 || other != 0 {
				t.Fatal(prepares, submits, queries, cancels, other, trace.rows)
			}
			for _, p := range trace.policies {
				if p != "reject" {
					t.Fatal("default policy changed", trace.policies)
				}
			}
			b.mu.Lock()
			sets := b.sets
			b.mu.Unlock()
			if sets != 1 {
				t.Fatal("replay wrote hardware-like targets again", sets)
			}
		})
	}
}

func TestControlPatternsV2ExecutionGuardsBeforeNetwork(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	base := []string{"patterns-v2"}
	for _, args := range [][]string{
		{"--mode", "direct", "patterns-v2", "run", "fixture", "id", "--revision", revision},
		{"patterns-v2", "run", "fixture", "id"},
		{"patterns-v2", "run", "bad.serial", "id", "--revision", revision},
		{"patterns-v2", "run", "fixture", "id", "--revision", revision, "--request-id", "missing-instance"},
		{"patterns-v2", "preview", "id", "--revision", revision, "--wait=false"},
		{"patterns-v2", "preview", "id", "--revision", revision, "--policy", "reject"},
		{"patterns-v2", "request", "fixture", "id", "--revision", revision},
		{"patterns-v2", "cancel", "fixture", "id", "--revision", revision},
		append(append([]string(nil), base...), "get", "id", "--policy", "reject"),
	} {
		text, err := executePatternV2Command(context.Background(), append([]string{"--endpoint", "http://127.0.0.1:1"}, args...)...)
		if err == nil || strings.Contains(err.Error(), "contact") || text != "" {
			t.Fatal(args, text, err)
		}
	}
}

func TestControlPatternsV2WaitStopsDuringOriginalReceiptWithoutCancelOrResubmit(t *testing.T) {
	e, b, release := cliPatternEngineV2(t)
	defer release()
	r := cliPatternRecordV2(t, false)
	store := cliPatternStoreV2(t, r)
	h := httpapi.New(e, httpapi.WithPatternStoreV2(store))
	trace := &cliPatternTraceV2{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	receiptReached, releaseReceipt := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace.record(r)
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/pattern-requests/") {
			probe := httptest.NewRecorder()
			h.ServeHTTP(probe, r)
			if probe.Code != 200 {
				w.WriteHeader(probe.Code)
				return
			}
			once.Do(func() { close(receiptReached) })
			select {
			case <-r.Context().Done():
			case <-releaseReceipt:
			}
			return
		}
		h.ServeHTTP(w, r)
	}))
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	finished := make(chan struct{})
	defer func() {
		cancel()
		close(releaseReceipt)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("CLI did not stop after caller cancellation")
		}
		server.Close()
	}()
	go func() {
		defer close(finished)
		text, err := executePatternV2Command(ctx, cliPatternArgsV2(server.URL, "run", "fixture", r.ID, "--revision", r.Revision, "--wait")...)
		done <- result{text, err}
	}()
	select {
	case <-receiptReached:
		cancel()
	case early := <-done:
		t.Fatal("ended before receipt wait", early)
	case <-time.After(5 * time.Second):
		t.Fatal("did not reach receipt wait")
	}
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop after receipt cancellation")
	}
	out := cliPatternDecodeV2(t, got.text)
	if got.err == nil || !out.Accepted || !out.Unknown || out.Job == nil || out.Ticket.RequestID == "" || out.Ref.Revision != r.Revision {
		t.Fatal(got.text, got.err)
	}
	select {
	case <-b.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture Set did not enter")
	}
	prepares, submits, queries, cancels, other := trace.counts()
	if prepares != 1 || submits != 1 || queries != 1 || cancels != 0 || other != 0 {
		t.Fatal(trace.rows)
	}
}

func cliPatternQueuedV2(t *testing.T, r patternsv2.Record) control.PatternJobEnvelope {
	t.Helper()
	p, err := control.Preview(r.Compiled.Execution.Program, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := patternsv2.ExecutionDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	return control.PatternJobEnvelope{PatternRef: patternidentity.Ref{RecordContract: r.Contract, ID: r.ID, Revision: r.Revision}, ExecutionDigest: digest, JobKind: "program", ProgramJob: &control.Job{ID: "job_" + strings.Repeat("3", 32), Device: "fixture", Status: "queued", Program: p.Program, TotalSteps: p.TotalSteps, CreatedAt: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}}
}
func cliPatternLocationV2(e control.PatternJobEnvelope, key control.ReceiptRequest) string {
	return "/v2/devices/fixture/pattern-requests/" + key.RequestID + "?" + url.Values{"expected_daemon_instance_id": {key.ExpectedDaemonInstanceID}, "record_contract": {e.PatternRef.RecordContract}, "pattern_id": {e.PatternRef.ID}, "revision": {e.PatternRef.Revision}}.Encode()
}
func TestControlPatternsV2RunExplicitPoliciesAndProvidedTicketUseOnePOST(t *testing.T) {
	r := cliPatternRecordV2(t, false)
	envelope := cliPatternQueuedV2(t, r)
	key := control.ReceiptRequest{RequestID: commandRequestID, ExpectedDaemonInstanceID: commandInstance}
	for _, policy := range []string{"", "reject", "queue", "replace"} {
		t.Run("policy-"+policy, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != "POST" || req.URL.Path != "/v2/devices/fixture/pattern-jobs" {
					t.Error("unexpected preflight", req.Method, req.URL)
				}
				var body struct {
					Ref      patternidentity.Ref `json:"pattern_ref"`
					Policy   string              `json:"policy"`
					Request  string              `json:"request_id"`
					Instance string              `json:"expected_daemon_instance_id"`
				}
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Error("bad request")
				}
				expected := policy
				if expected == "" {
					expected = "reject"
				}
				if body.Policy != expected || body.Ref != envelope.PatternRef || body.Request != key.RequestID || body.Instance != key.ExpectedDaemonInstanceID {
					t.Error(body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", key.ExpectedDaemonInstanceID)
				w.Header().Set("X-NB1-Replayed", "false")
				w.Header().Set("Location", cliPatternLocationV2(envelope, key))
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			args := append([]string{"run", "fixture", r.ID, "--revision", r.Revision}, cliPatternReceiptArgsV2(key)...)
			if policy != "" {
				args = append(args, "--policy", policy)
			}
			text, err := executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, args...)...)
			out := cliPatternDecodeV2(t, text)
			expected := policy
			if expected == "" {
				expected = "reject"
			}
			if err != nil || calls != 1 || !out.Accepted || out.Policy != expected || out.Ticket != key {
				t.Fatal(text, err, calls)
			}
		})
	}
}
func TestControlPatternsV2CancelMalformedResponseDoesNotPollAwayEvidence(t *testing.T) {
	r := cliPatternRecordV2(t, false)
	e := cliPatternQueuedV2(t, r)
	key := control.ReceiptRequest{RequestID: commandRequestID, ExpectedDaemonInstanceID: commandInstance}
	gets, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", key.ExpectedDaemonInstanceID)
		if r.Method == "GET" {
			gets++
			json.NewEncoder(w).Encode(e)
			return
		}
		posts++
		w.WriteHeader(202)
		w.Write([]byte(`{"wrong":"response"}`))
	}))
	defer server.Close()
	args := append([]string{"cancel", "fixture", r.ID, "--revision", r.Revision, "--wait"}, cliPatternReceiptArgsV2(key)...)
	text, err := executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, args...)...)
	out := cliPatternDecodeV2(t, text)
	if err == nil || !out.Accepted || !out.Unknown || out.Job == nil || out.Job.Job.ID != e.ProgramJob.ID || out.Job.Job.Status != "queued" || out.Ticket != key || out.Attempted == nil || !*out.Attempted || gets != 1 || posts != 1 {
		t.Fatal(text, err, gets, posts)
	}
}
func TestControlPatternsV2ExpiredWaitContextKeepsKnownAcceptedSnapshot(t *testing.T) {
	r := cliPatternRecordV2(t, false)
	e := cliPatternQueuedV2(t, r)
	c, err := daemonclient.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	out := patternRunOutputV2{PatternRef: e.PatternRef, Accepted: true, Job: &e, RequestTicket: control.ReceiptRequest{RequestID: commandRequestID, ExpectedDaemonInstanceID: commandInstance}}
	err = followPatternReceiptV2(ctx, c, "fixture", &out, "", "")
	if !errors.Is(err, context.DeadlineExceeded) || out.Job != &e || !out.Accepted {
		t.Fatal(out, err)
	}
}

func TestControlPatternsV2KnownTerminalPrecedesEndedWaitContext(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled"} {
		for _, ended := range []string{"deadline", "cancelled"} {
			e := cliPatternQueuedV2(t, cliPatternRecordV2(t, false))
			j := e.ProgramJob
			started, finished := j.CreatedAt.Add(time.Millisecond), j.CreatedAt.Add(2*time.Millisecond)
			j.Status, j.StartedAt, j.FinishedAt = status, &started, &finished
			if status == "succeeded" {
				j.CompletedSteps = j.TotalSteps
			}
			if err := daemonclient.ValidatePatternV2Progress(e, e); err != nil {
				t.Fatal("terminal fixture invalid", err)
			}
			c, err := daemonclient.New("http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			var ctx context.Context
			var stop context.CancelFunc
			if ended == "deadline" {
				ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				ctx, stop = context.WithCancel(context.Background())
				stop()
			}
			out := patternRunOutputV2{PatternRef: e.PatternRef, Accepted: true, Job: &e, RequestTicket: control.ReceiptRequest{RequestID: commandRequestID, ExpectedDaemonInstanceID: commandInstance}}
			err = followPatternReceiptV2(ctx, c, "fixture", &out, "", "")
			stop()
			c.Close()
			if err != nil || out.Job != &e || !out.Accepted {
				t.Fatal(status, ended, out, err)
			}
		}
	}
}
func TestControlPatternsV2RealBackendEmptyErrorIsKnownFailedOutcome(t *testing.T) {
	for _, frame := range []bool{false, true} {
		t.Run(fmt.Sprint(frame), func(t *testing.T) {
			e, b, release := cliPatternEngineV2(t)
			b.fail = errors.New("")
			release()
			r := cliPatternRecordV2(t, frame)
			store := cliPatternStoreV2(t, r)
			server := httptest.NewServer(httpapi.New(e, httpapi.WithPatternStoreV2(store)))
			defer server.Close()
			text, err := executePatternV2Command(context.Background(), cliPatternArgsV2(server.URL, "run", "fixture", r.ID, "--revision", r.Revision, "--wait")...)
			out := cliPatternDecodeV2(t, text)
			if err == nil || !out.Accepted || out.Unknown || out.Job == nil || out.Job.Job.Status != "failed" || out.Ticket.RequestID == "" {
				t.Fatal(text, err)
			}
		})
	}
}
