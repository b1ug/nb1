package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/patternidentity"
)

// All drivers and handles here are in-memory fakes. No discovery, USB, runtime
// subprocess, user library or HTTP listener participates in this software slice.
func startPatternEngine(t *testing.T, options Options) (*Engine, *frameIntegrationBackend, *receiptClock) {
	t.Helper()
	return startFrameIntegration(t, options, "set", "program", "raw-rgb", "mk2-basic", "hold", "cancel", "observe")
}
func patternRef(n int) patternidentity.Ref {
	return patternidentity.Ref{RecordContract: patternidentity.RecordContract, ID: fmt.Sprintf("pattern-%d", n), Revision: fmt.Sprintf("sha256:%064x", n)}
}
func patternResolved(t *testing.T, kind string, ref patternidentity.Ref, color string) ResolvedPattern {
	t.Helper()
	var execution PatternExecution
	if kind == "program" {
		p, err := Normalize(Program{Steps: []Step{{Color: color}}, OnFinish: "hold"})
		if err != nil {
			t.Fatal(err)
		}
		execution = PatternExecution{Kind: "program", Program: &p}
	} else {
		p := frameIntegrationProgram()
		p.Initial[0].Color = color
		p, err := NormalizeFrameProgram(p)
		if err != nil {
			t.Fatal(err)
		}
		execution = PatternExecution{Kind: "frame_program", FrameProgram: &p}
	}
	digest, err := execution.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return ResolvedPattern{Ref: ref, Execution: execution, ExecutionDigest: digest}
}
func patternResolver(result ResolvedPattern) PatternResolver {
	return func(context.Context, patternidentity.Ref) (ResolvedPattern, error) { return result, nil }
}
func patternEnvelopeID(j PatternJobEnvelope) string {
	if j.ProgramJob != nil {
		return j.ProgramJob.ID
	}
	return j.FrameJob.ID
}
func patternEnvelopeStatus(j PatternJobEnvelope) string {
	if j.ProgramJob != nil {
		return j.ProgramJob.Status
	}
	return j.FrameJob.Status
}
func patternEnvelopeCreated(j PatternJobEnvelope) string {
	if j.ProgramJob != nil {
		return j.ProgramJob.CreatedAt.String()
	}
	return j.FrameJob.CreatedAt.String()
}
func mustPattern(t *testing.T, e *Engine, resolved ResolvedPattern, policy string, ticket ReceiptRequest) PatternReceiptResult {
	t.Helper()
	result, err := e.SubmitPatternWithReceipt(context.Background(), "a", resolved.Ref, policy, ticket, patternResolver(resolved))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func queryPattern(t *testing.T, e *Engine, ref patternidentity.Ref, ticket ReceiptRequest) PatternJobEnvelope {
	t.Helper()
	job, err := e.PatternRequest("a", ticket, ref)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func awaitPattern(t *testing.T, e *Engine, ref patternidentity.Ref, ticket ReceiptRequest, status string) PatternJobEnvelope {
	t.Helper()
	var job PatternJobEnvelope
	awaitEngineCondition(t, func() bool {
		var err error
		job, err = e.PatternRequest("a", ticket, ref)
		if err != nil {
			return false
		}
		return job.ProgramJob != nil && job.ProgramJob.FinishedAt != nil || job.FrameJob != nil && job.FrameJob.FinishedAt != nil
	})
	if patternEnvelopeStatus(job) != status {
		details, err := json.Marshal(job)
		if err != nil {
			t.Fatalf("Pattern status=%s, want %s; encode terminal job: %v", patternEnvelopeStatus(job), status, err)
		}
		t.Fatalf("Pattern status=%s, want %s: %s", patternEnvelopeStatus(job), status, details)
	}
	return job
}
func assertPatternEmpty(t *testing.T, e *Engine) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.jobs) != 0 || len(e.receipts.entries) != 0 || e.receipts.steps != 0 || len(e.workers["a"].queue) != 0 || e.workers["a"].active != nil {
		t.Fatal("failed admission left Job/receipt/retained cost/queue/active record")
	}
}

func TestPatternAdmissionOriginIsolationAndBoundCancel(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			gate := newEngineGate()
			defer gate.open()
			b.hooks(func(Step) error { return gate.block() }, nil, nil)
			ref, ticket := patternRef(1), receiptTicket(e, 1)
			resolved := patternResolved(t, kind, ref, "#112233")
			accepted := mustPattern(t, e, resolved, "reject", ticket)
			awaitEngineSignal(t, gate.entered)
			id := patternEnvelopeID(accepted.Job)
			if accepted.Replayed || accepted.DaemonInstanceID != ticket.ExpectedDaemonInstanceID || accepted.Job.PatternRef != ref || accepted.Job.ExecutionDigest != resolved.ExecutionDigest || accepted.Job.JobKind != kind {
				t.Fatalf("wrong accepted identity: %#v", accepted)
			}
			if _, err := e.Job(id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy Job leaked source: %v", err)
			}
			if _, err := e.FrameJob(id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("bare FrameJob leaked source: %v", err)
			}
			if _, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy receipt leaked source: %v", err)
			}
			if _, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("bare frame receipt leaked source: %v", err)
			}
			if _, err := e.Cancel(context.Background(), id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy cancel changed Pattern: %v", err)
			}
			if _, err := e.CancelFrame(context.Background(), id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("bare frame cancel changed Pattern: %v", err)
			}
			if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "running" {
				t.Fatal("legacy access mutated source job")
			}
			legacy := engineProgram("#112233", "hold")
			if _, err := e.SubmitWithReceipt(context.Background(), "a", legacy, "reject", ticket); !errors.Is(err, ErrRequestConflict) {
				t.Fatalf("legacy submit reused Pattern key: %v", err)
			}
			if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", ticket); !errors.Is(err, ErrRequestConflict) {
				t.Fatalf("frame submit reused Pattern key: %v", err)
			}
			replay, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "reject", ticket, nil)
			if err != nil || !replay.Replayed || patternEnvelopeID(replay.Job) != id {
				t.Fatalf("known key required resolver: %#v %v", replay, err)
			}
			for _, policy := range []string{"replace", "queue"} {
				if _, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, policy, ticket, nil); !errors.Is(err, ErrRequestConflict) {
					t.Fatalf("policy changed identity: %s %v", policy, err)
				}
			}
			if _, err := e.PatternRequest("a", ticket, patternRef(2)); !errors.Is(err, ErrRequestConflict) {
				t.Fatalf("wrong source query=%v", err)
			}
			if _, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, "job_"+strings.Repeat("f", 32)); !errors.Is(err, ErrRequestConflict) {
				t.Fatalf("wrong known ID cancel=%v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := e.CancelPatternRequest(ctx, "a", ticket, ref, id); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled context mutated job: %v", err)
			}
			if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "running" {
				t.Fatal("bad bound cancel mutated job")
			}
			wire, err := json.Marshal(queryPattern(t, e, ref, ticket))
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(wire, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope) != 4 || envelope["job"] == nil || envelope["pattern_ref"] == nil {
				t.Fatal(string(wire))
			}
			var bare map[string]json.RawMessage
			if err := json.Unmarshal(envelope["job"], &bare); err != nil {
				t.Fatal(err)
			}
			if bare["pattern_ref"] != nil || bare["execution_digest"] != nil || bare["job_kind"] != nil {
				t.Fatal("provenance polluted bare Job JSON")
			}
			cancelling, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, id)
			if err != nil || patternEnvelopeStatus(cancelling) != "cancelling" {
				t.Fatalf("cancel=%#v %v", cancelling, err)
			}
			_, _, _, max, closes, unsafe := b.snapshot()
			if max != 1 || closes != 0 || unsafe {
				t.Fatal("native ownership released before Set returned")
			}
			gate.open()
			terminal := awaitPattern(t, e, ref, ticket, "cancelled")
			if terminal.PatternRef != ref || terminal.ExecutionDigest != resolved.ExecutionDigest {
				t.Fatal("terminal lost source identity")
			}
		})
	}
}

func TestPatternConcurrentAdmissionAllPoliciesAndKinds(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, policy := range []string{"replace", "queue", "reject"} {
			t.Run(kind+"/"+policy, func(t *testing.T) {
				e, b, _ := startPatternEngine(t, Options{})
				native := newEngineGate()
				defer native.open()
				b.hooks(func(Step) error { return native.block() }, nil, nil)
				ref, ticket := patternRef(10), receiptTicket(e, 10)
				resolved := patternResolved(t, kind, ref, "#123456")
				const n = 8
				allResolved, release := make(chan struct{}), make(chan struct{})
				var calls int32
				resolver := func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					if atomic.AddInt32(&calls, 1) == n {
						close(allResolved)
					}
					<-release
					return resolved, nil
				}
				results := make(chan PatternReceiptResult, n)
				errs := make(chan error, n)
				var wg sync.WaitGroup
				for i := 0; i < n; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, policy, ticket, resolver)
						results <- r
						errs <- err
					}()
				}
				awaitEngineSignal(t, allResolved)
				close(release)
				done := make(chan struct{})
				go func() { wg.Wait(); close(done) }()
				awaitEngineSignal(t, done)
				close(results)
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				id, created, fresh := "", "", 0
				for r := range results {
					if id == "" {
						id = patternEnvelopeID(r.Job)
						created = patternEnvelopeCreated(r.Job)
					}
					if id != patternEnvelopeID(r.Job) || created != patternEnvelopeCreated(r.Job) || r.Job.PatternRef != ref || r.Job.ExecutionDigest != resolved.ExecutionDigest {
						t.Fatal("concurrent identity drift")
					}
					if !r.Replayed {
						fresh++
					}
				}
				if fresh != 1 || atomic.LoadInt32(&calls) != n {
					t.Fatalf("fresh=%d resolve=%d", fresh, calls)
				}
				cost := 1
				if kind == "frame" {
					cost = 4
				}
				assertReceiptUsage(t, e, 1, cost)
				e.mu.Lock()
				if len(e.jobs) != 1 {
					t.Error("multiple Jobs published")
				}
				e.mu.Unlock()
				awaitEngineSignal(t, native.entered)
				native.open()
				awaitPattern(t, e, ref, ticket, "succeeded")
			})
		}
	}
}

func TestPatternConcurrentDifferentSourcePolicyOrExecutionConflicts(t *testing.T) {
	for _, change := range []string{"ref", "policy", "execution"} {
		t.Run(change, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(20), receiptTicket(e, 20)
			a := patternResolved(t, "program", ref, "#111111")
			other := a
			policyA, policyB := "queue", "queue"
			if change == "ref" {
				other.Ref = patternRef(21)
			}
			if change == "policy" {
				policyB = "replace"
			}
			if change == "execution" {
				other = patternResolved(t, "program", ref, "#222222")
			}
			all, release := make(chan struct{}), make(chan struct{})
			var calls int32
			resolver := func(value ResolvedPattern) PatternResolver {
				return func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					if atomic.AddInt32(&calls, 1) == 2 {
						close(all)
					}
					<-release
					return value, nil
				}
			}
			type outcome struct {
				result PatternReceiptResult
				err    error
			}
			results := make(chan outcome, 2)
			go func() {
				r, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, policyA, ticket, resolver(a))
				results <- outcome{r, err}
			}()
			go func() {
				r, err := e.SubmitPatternWithReceipt(context.Background(), "a", other.Ref, policyB, ticket, resolver(other))
				results <- outcome{r, err}
			}()
			awaitEngineSignal(t, all)
			close(release)
			first, second := <-results, <-results
			success, conflicts := 0, 0
			for _, r := range []outcome{first, second} {
				if r.err == nil && !r.result.Replayed {
					success++
				} else if errors.Is(r.err, ErrRequestConflict) {
					conflicts++
				} else {
					t.Fatalf("wrong race result: %#v", r)
				}
			}
			if success != 1 || conflicts != 1 {
				t.Fatalf("accepted=%d conflicts=%d", success, conflicts)
			}
			assertReceiptUsage(t, e, 1, 1)
			native.open()
		})
	}
}

func TestPatternResolveFailureRaceClassificationAndDiagnostic(t *testing.T) {
	cause := errors.New("original resolver diagnostic")
	for _, name := range []string{"availability", "availability_other_policy", "unknown", "integrity", "unsafe_nested", "unsupported", "configuration", "cancelled", "wrong_ref", "bad_digest", "bad_program"} {
		t.Run(name, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(30), receiptTicket(e, 30)
			good := patternResolved(t, "program", ref, "#123456")
			entered, release := make(chan struct{}), make(chan struct{})
			type outcome struct {
				r   PatternReceiptResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				r, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "queue", ticket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					close(entered)
					<-release
					switch name {
					case "availability", "availability_other_policy":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveAvailability, Err: cause}
					case "unknown":
						return ResolvedPattern{}, cause
					case "integrity":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveIntegrity, Err: cause}
					case "unsafe_nested":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveAvailability, Err: &PatternResolveError{Kind: PatternResolveIntegrity, Err: cause}}
					case "unsupported":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveUnsupported, Err: cause}
					case "configuration":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveConfiguration, Err: cause}
					case "cancelled":
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveAvailability, Err: context.Canceled}
					case "wrong_ref":
						v := good
						v.Ref = patternRef(31)
						return v, nil
					case "bad_digest":
						v := good
						v.ExecutionDigest = "sha256:" + strings.Repeat("0", 64)
						return v, nil
					case "bad_program":
						v := good
						p := cloneProgram(*v.Execution.Program)
						p.Repeat = 0
						v.Execution.Program = &p
						return v, nil
					}
					panic("unreachable")
				})
				done <- outcome{r, err}
			}()
			awaitEngineSignal(t, entered)
			winnerPolicy := "queue"
			if name == "availability_other_policy" {
				winnerPolicy = "replace"
			}
			winner := mustPattern(t, e, good, winnerPolicy, ticket)
			awaitEngineSignal(t, native.entered)
			close(release)
			loser := <-done
			if name == "availability" {
				if loser.err != nil || !loser.r.Replayed || patternEnvelopeID(loser.r.Job) != patternEnvelopeID(winner.Job) {
					t.Fatalf("availability did not recover original: %#v", loser)
				}
			} else if name == "availability_other_policy" {
				if !errors.Is(loser.err, ErrRequestConflict) {
					t.Fatalf("policy mismatch replayed: %v", loser.err)
				}
			} else {
				if loser.err == nil || loser.r.Replayed {
					t.Fatalf("diagnostic hidden by concurrent receipt: %s %#v", name, loser)
				}
				if name == "cancelled" {
					if !errors.Is(loser.err, context.Canceled) {
						t.Fatal(loser.err)
					}
				} else if strings.HasPrefix(name, "wrong_") || strings.HasPrefix(name, "bad_") {
					if !errors.Is(loser.err, ErrInvalid) {
						t.Fatal(loser.err)
					}
				} else if !errors.Is(loser.err, cause) {
					t.Fatalf("lost diagnostic: %v", loser.err)
				}
			}
			original := queryPattern(t, e, ref, ticket)
			if patternEnvelopeID(original) != patternEnvelopeID(winner.Job) || patternEnvelopeStatus(original) != "running" {
				t.Fatal("error mutated accepted job")
			}
			assertReceiptUsage(t, e, 1, 1)
			native.open()
		})
	}
}

func TestPatternAdmissionInputsAndNewKeyFailureLeaveNoPublication(t *testing.T) {
	for _, name := range []string{"empty_policy", "unknown_policy", "bad_ref", "bad_serial", "missing_serial", "bad_ticket", "wrong_instance", "expired", "future_deadline", "nil_resolver", "unknown_error", "availability_error", "wrong_ref", "bad_digest", "union", "noncanonical"} {
		t.Run(name, func(t *testing.T) {
			e, _, clock := startPatternEngine(t, Options{})
			ref, ticket := patternRef(40), receiptTicket(e, 40)
			resolved := patternResolved(t, "program", ref, "#123456")
			serial, policy := "a", "reject"
			var calls int
			resolver := func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
				calls++
				switch name {
				case "unknown_error":
					return ResolvedPattern{}, errors.New("unclassified")
				case "availability_error":
					return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveAvailability, Err: errors.New("missing")}
				case "wrong_ref":
					resolved.Ref = patternRef(41)
				case "bad_digest":
					resolved.ExecutionDigest = "bad"
				case "union":
					resolved.Execution.FrameProgram = &FrameProgram{}
				case "noncanonical":
					resolved.Execution.Program.Repeat = 0
				}
				return resolved, nil
			}
			switch name {
			case "empty_policy":
				policy = ""
			case "unknown_policy":
				policy = "steal"
			case "bad_ref":
				ref.Revision = "sha256:BAD"
			case "bad_serial":
				serial = "../a"
			case "missing_serial":
				serial = "b"
			case "bad_ticket":
				ticket.RequestID = "req_bad"
			case "wrong_instance":
				ticket.ExpectedDaemonInstanceID = "daemon_" + strings.Repeat("f", 32)
			case "expired":
				clock.advance(receiptWindowMS + 1)
			case "future_deadline":
				ticket.RequestID = "req_999999_" + strings.Repeat("a", 32)
			case "nil_resolver":
				resolver = nil
			}
			if _, err := e.SubmitPatternWithReceipt(context.Background(), serial, ref, policy, ticket, resolver); err == nil {
				t.Fatal("invalid new key admitted")
			}
			assertPatternEmpty(t, e)
			beforeResolve := name == "empty_policy" || name == "unknown_policy" || name == "bad_ref" || name == "bad_serial" || name == "missing_serial" || name == "bad_ticket" || name == "wrong_instance" || name == "expired" || name == "future_deadline" || name == "nil_resolver"
			if beforeResolve && calls != 0 || !beforeResolve && calls != 1 {
				t.Fatalf("resolver calls=%d", calls)
			}
		})
	}
}

func TestPatternFinalEligibilityRechecksAfterResolver(t *testing.T) {
	for _, change := range []string{"deadline", "context", "close", "offline", "read_only", "entries", "targets", "capabilities"} {
		t.Run(change, func(t *testing.T) {
			e, _, clock := startPatternEngine(t, Options{})
			ref, ticket := patternRef(50), receiptTicket(e, 50)
			resolved := patternResolved(t, "program", ref, "#112233")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := e.SubmitPatternWithReceipt(ctx, "a", ref, "replace", ticket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					close(entered)
					<-release
					return resolved, nil
				})
				done <- err
			}()
			awaitEngineSignal(t, entered)
			switch change {
			case "deadline":
				clock.advance(receiptWindowMS + 1)
			case "context":
				cancel()
			case "close":
				e.beginClose()
			default:
				e.mu.Lock()
				switch change {
				case "offline":
					e.workers["a"].state.Connection = "error"
				case "read_only":
					e.options.ReadOnly = true
				case "entries":
					e.receipts.maxEntries = 0
				case "targets":
					e.receipts.maxSteps = 0
				case "capabilities":
					e.workers["a"].state.Capabilities = []string{"set"}
				}
				e.mu.Unlock()
			}
			close(release)
			if err := <-done; err == nil {
				t.Fatal("changed final eligibility admitted")
			}
			assertPatternEmpty(t, e)
		})
	}
}

func TestPatternEnvelopeDeepCopyAndUnion(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(60), receiptTicket(e, 60)
			resolved := patternResolved(t, kind, ref, "#123456")
			accepted := mustPattern(t, e, resolved, "reject", ticket)
			awaitEngineSignal(t, native.entered)
			if kind == "program" {
				resolved.Execution.Program.Steps[0].Color = "#ffffff"
			} else {
				resolved.Execution.FrameProgram.Initial[0].Color = "#ffffff"
				resolved.Execution.FrameProgram.Frames[0].Targets[0].Color = "#ffffff"
			}
			first := queryPattern(t, e, ref, ticket)
			var started string
			if kind == "program" {
				started = first.ProgramJob.StartedAt.String()
			} else {
				started = first.FrameJob.StartedAt.String()
			}
			if kind == "program" {
				if first.ProgramJob.Program.Steps[0].Color != "#123456" {
					t.Fatal("resolver input aliased")
				}
				first.ProgramJob.Program.Steps[0].Color = "#abcdef"
				*first.ProgramJob.StartedAt = first.ProgramJob.StartedAt.AddDate(1, 0, 0)
			} else {
				if first.FrameJob.Program.Initial[0].Color != "#123456" {
					t.Fatal("resolver frames aliased")
				}
				first.FrameJob.Program.Initial[0].Color = "#abcdef"
				first.FrameJob.Program.Frames[0].Targets[0].Color = "#abcdef"
				*first.FrameJob.StartedAt = first.FrameJob.StartedAt.AddDate(1, 0, 0)
			}
			again := queryPattern(t, e, ref, ticket)
			if kind == "program" {
				if again.ProgramJob.Program.Steps[0].Color != "#123456" || again.ProgramJob.StartedAt.String() != started {
					t.Fatal("program slice or timestamp was aliased")
				}
			} else if again.FrameJob.Program.Initial[0].Color != "#123456" || again.FrameJob.Program.Frames[0].Targets[0].Color != "#000099" || again.FrameJob.StartedAt.String() != started {
				t.Fatal("initial/target slice or timestamp was aliased")
			}
			if reflect.DeepEqual(first, again) {
				t.Fatal("caller mutation changed stored snapshot")
			}
			if again.ExecutionDigest != accepted.Job.ExecutionDigest || again.PatternRef != ref {
				t.Fatal("identity mutated")
			}
			invalid := again
			invalid.JobKind = "unknown"
			if _, err := json.Marshal(invalid); !errors.Is(err, ErrInvalid) {
				t.Fatalf("bad envelope union accepted: %v", err)
			}
			invalid = again
			invalid.FrameJob = &FrameJob{}
			invalid.ProgramJob = &Job{}
			if _, err := json.Marshal(invalid); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			native.open()
		})
	}
}

// Multi-Unwrap is expressible in Go1.18 without relying on errors.Join. These
// fixtures ensure the replay whitelist behaves identically across toolchains.
type patternMultiError []error

func (e patternMultiError) Error() string   { return "multiple fixture errors" }
func (e patternMultiError) Unwrap() []error { return []error(e) }

func TestPatternResolutionReplayWhitelistRejectsHiddenIntegrityAndUnknown(t *testing.T) {
	cause := errors.New("temporary fixture read")
	available := &PatternResolveError{Kind: PatternResolveAvailability, Err: cause}
	integrity := &PatternResolveError{Kind: PatternResolveIntegrity, Err: errors.New("unsafe fixture")}
	var nilClassified *PatternResolveError
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"explicit", available, true},
		{"ordinary_wrap", fmt.Errorf("context: %w", available), true},
		{"ordinary_unknown", cause, false},
		{"nil_classified", nilClassified, false},
		{"empty_kind", &PatternResolveError{Err: cause}, false},
		{"nested_integrity", &PatternResolveError{Kind: PatternResolveAvailability, Err: integrity}, false},
		{"multi_integrity", &PatternResolveError{Kind: PatternResolveAvailability, Err: patternMultiError{cause, integrity}}, false},
		{"multi_context", &PatternResolveError{Kind: PatternResolveAvailability, Err: patternMultiError{cause, context.Canceled}}, false},
		{"multi_unclassified_branch", patternMultiError{available, cause}, false},
		{"multi_classified_only", patternMultiError{available, available}, true},
		{"invalid_body", &PatternResolveError{Kind: PatternResolveAvailability, Err: fmt.Errorf("bad input: %w", ErrInvalid)}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := replayablePatternResolution(c.err); got != c.want {
				t.Fatalf("replayable=%t want=%t", got, c.want)
			}
		})
	}
}

func TestPatternCancelledResolverContextNotHiddenByConcurrentReceipt(t *testing.T) {
	for _, outcome := range []string{"valid", "availability"} {
		t.Run(outcome, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(70), receiptTicket(e, 70)
			resolved := patternResolved(t, "program", ref, "#123456")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := e.SubmitPatternWithReceipt(ctx, "a", ref, "queue", ticket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					close(entered)
					<-release
					if outcome == "availability" {
						return ResolvedPattern{}, &PatternResolveError{Kind: PatternResolveAvailability, Err: errors.New("temporary fixture miss")}
					}
					return resolved, nil
				})
				done <- err
			}()
			awaitEngineSignal(t, entered)
			winner := mustPattern(t, e, resolved, "queue", ticket)
			awaitEngineSignal(t, native.entered)
			cancel()
			close(release)
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled caller was hidden by concurrent receipt: %v", err)
			}
			original := queryPattern(t, e, ref, ticket)
			if patternEnvelopeID(original) != patternEnvelopeID(winner.Job) || patternEnvelopeStatus(original) != "running" {
				t.Fatal("cancelled submit mutated original accepted job")
			}
			assertReceiptUsage(t, e, 1, 1)
			native.open()
		})
	}
}

func TestPatternUsesCachedProgramFrameAndHoldCapabilities(t *testing.T) {
	for _, c := range []struct {
		name, kind, missing string
		hold                bool
	}{
		{"program", "program", "program", false},
		{"program_hold", "program", "hold", true},
		{"frame_profile", "frame", "mk2-basic", false},
		{"frame_hold", "frame", "hold", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			ref, ticket := patternRef(80), receiptTicket(e, 80)
			resolved := patternResolved(t, c.kind, ref, "#123456")
			if c.kind == "program" && !c.hold {
				resolved.Execution.Program.OnFinish = "off"
			}
			if c.kind == "frame" && c.hold {
				resolved.Execution.FrameProgram.OnFinish = "hold"
			}
			resolved.ExecutionDigest, _ = resolved.Execution.Digest()
			e.mu.Lock()
			caps := e.workers["a"].state.Capabilities
			filtered := make([]string, 0, len(caps))
			for _, capability := range caps {
				if capability != c.missing {
					filtered = append(filtered, capability)
				}
			}
			e.workers["a"].state.Capabilities = filtered
			e.mu.Unlock()
			if _, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "replace", ticket, patternResolver(resolved)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("missing cached capability admitted: %v", err)
			}
			assertPatternEmpty(t, e)
			calls, _, _, _, _, _ := b.snapshot()
			if len(engineSetColors(calls)) != 0 {
				t.Fatal("capability rejection wrote device")
			}
		})
	}
}

func TestPatternLegacyKeysConflictBeforeResolverAcrossKinds(t *testing.T) {
	for _, legacyKind := range []string{"program", "frame"} {
		for _, patternKind := range []string{"program", "frame"} {
			t.Run(legacyKind+"/"+patternKind, func(t *testing.T) {
				e, b, _ := startPatternEngine(t, Options{})
				native := newEngineGate()
				defer native.open()
				b.hooks(func(Step) error { return native.block() }, nil, nil)
				ticket := receiptTicket(e, 90)
				if legacyKind == "program" {
					submitReceipt(t, e, engineProgram("#123456", "hold"), "queue", ticket)
				} else {
					if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", ticket); err != nil {
						t.Fatal(err)
					}
				}
				awaitEngineSignal(t, native.entered)
				ref := patternRef(90)
				resolved := patternResolved(t, patternKind, ref, "#123456")
				calls := 0
				_, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "reject", ticket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) { calls++; return resolved, nil })
				if !errors.Is(err, ErrRequestConflict) || calls != 0 {
					t.Fatalf("legacy key consulted resolver or lost conflict: calls=%d err=%v", calls, err)
				}
				if _, err := e.PatternRequest("a", ticket, ref); !errors.Is(err, ErrRequestConflict) {
					t.Fatalf("legacy receipt projected as Pattern: %v", err)
				}
				if _, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, ""); !errors.Is(err, ErrRequestConflict) {
					t.Fatalf("Pattern cancellation crossed legacy origin: %v", err)
				}
				var status string
				if legacyKind == "program" {
					j, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
					if err != nil {
						t.Fatal(err)
					}
					status = j.Status
				} else {
					j, err := e.FrameRequest("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
					if err != nil {
						t.Fatal(err)
					}
					status = j.Status
				}
				if status != "running" {
					t.Fatal("cross-origin request mutated legacy job")
				}
				native.open()
			})
		}
	}
}
