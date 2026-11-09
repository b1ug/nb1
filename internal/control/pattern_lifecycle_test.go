package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/patternidentity"
)

func TestPatternMixedOriginQueueAndQueuedCancellation(t *testing.T) {
	e, b, _ := startPatternEngine(t, Options{})
	native := newEngineGate()
	defer native.open()
	b.hooks(func(Step) error { return native.block() }, nil, nil)
	first := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
	awaitEngineSignal(t, native.entered)
	ref1, ref2 := patternRef(101), patternRef(102)
	ticket1, ticket2 := receiptTicket(e, 101), receiptTicket(e, 102)
	cancelled := mustPattern(t, e, patternResolved(t, "program", ref1, "#222222"), "queue", ticket1)
	frame := mustPattern(t, e, patternResolved(t, "frame", ref2, "#333333"), "queue", ticket2)
	last := submitEngineJob(t, e, "a", engineProgram("#444444", "hold"), "queue")
	if _, err := e.CancelPatternRequest(context.Background(), "a", ticket1, ref1, patternEnvelopeID(cancelled.Job)); err != nil {
		t.Fatal(err)
	}
	if patternEnvelopeStatus(queryPattern(t, e, ref1, ticket1)) != "cancelled" {
		t.Fatal("queued cancellation did not terminate")
	}
	if patternEnvelopeStatus(queryPattern(t, e, ref2, ticket2)) != "queued" {
		t.Fatal("cancelled wrong queued source")
	}
	state, _ := e.Device("a")
	if state.QueueDepth != 2 {
		t.Fatalf("queue depth=%d", state.QueueDepth)
	}
	native.open()
	awaitEngineJob(t, e, first.ID, "succeeded")
	awaitPattern(t, e, ref2, ticket2, "succeeded")
	awaitEngineJob(t, e, last.ID, "succeeded")
	calls, _, _, max, _, unsafe := b.snapshot()
	colors := engineSetColors(calls)
	want := []string{"#111111", "#333333", "#000099", "#000099", "#990000", "#444444"}
	if !reflect.DeepEqual(colors, want) || max != 1 || unsafe {
		t.Fatalf("shared FIFO/backend violated: %v", colors)
	}
	if frame.Job.PatternRef != ref2 {
		t.Fatal("queued provenance lost")
	}
}

func TestPatternReplaceAcrossAllOriginsAndExecutionKinds(t *testing.T) {
	for _, origin := range []string{"legacy", "pattern"} {
		for _, oldKind := range []string{"program", "frame"} {
			for _, newKind := range []string{"program", "frame"} {
				t.Run(origin+"/"+oldKind+"/"+newKind, func(t *testing.T) {
					e, b, _ := startPatternEngine(t, Options{})
					native := newEngineGate()
					defer native.open()
					b.hooks(func(Step) error { return native.block() }, nil, nil)
					oldRef, oldTicket := patternRef(110), receiptTicket(e, 110)
					oldResolved := patternResolved(t, oldKind, oldRef, "#111111")
					oldID := ""
					if origin == "pattern" {
						oldID = patternEnvelopeID(mustPattern(t, e, oldResolved, "queue", oldTicket).Job)
					} else if oldKind == "program" {
						oldID = submitEngineJob(t, e, "a", *oldResolved.Execution.Program, "queue").ID
					} else {
						r, err := e.SubmitFrameWithReceipt(context.Background(), "a", *oldResolved.Execution.FrameProgram, "reject", oldTicket)
						if err != nil {
							t.Fatal(err)
						}
						oldID = r.Job.ID
					}
					awaitEngineSignal(t, native.entered)
					legacyQueued := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
					queuedRef, queuedTicket := patternRef(111), receiptTicket(e, 111)
					mustPattern(t, e, patternResolved(t, "frame", queuedRef, "#333333"), "queue", queuedTicket)
					newRef, newTicket := patternRef(112), receiptTicket(e, 112)
					replacement := mustPattern(t, e, patternResolved(t, newKind, newRef, "#777777"), "replace", newTicket)
					e.mu.Lock()
					oldStatus := e.jobs[oldID].state.Status
					e.mu.Unlock()
					if oldStatus != "cancelling" {
						t.Fatalf("active not cancelling: %s", oldStatus)
					}
					oldQueued, _ := e.Job(legacyQueued.ID)
					if oldQueued.Status != "cancelled" || patternEnvelopeStatus(queryPattern(t, e, queuedRef, queuedTicket)) != "cancelled" {
						t.Fatal("replace did not atomically cancel mixed queue")
					}
					if patternEnvelopeStatus(replacement.Job) != "queued" {
						t.Fatal("replacement ran before native returned")
					}
					calls, _, _, max, closes, unsafe := b.snapshot()
					if len(engineSetColors(calls)) != 1 || max != 1 || closes != 0 || unsafe {
						t.Fatal("replacement touched native backend early")
					}
					native.open()
					terminal := awaitPattern(t, e, newRef, newTicket, "succeeded")
					if terminal.ExecutionDigest != replacement.Job.ExecutionDigest {
						t.Fatal("replacement lost identity")
					}
					if origin == "pattern" {
						awaitPattern(t, e, oldRef, oldTicket, "cancelled")
					} else if oldKind == "program" {
						awaitEngineJob(t, e, oldID, "cancelled")
					} else {
						awaitFrameIntegration(t, e, oldID, "cancelled")
					}
					calls, _, _, max, _, unsafe = b.snapshot()
					off, read, next := -1, -1, -1
					for i, c := range calls {
						if c.kind == "stop" && c.mode == "off" && off < 0 {
							off = i
						}
						if off >= 0 && c.kind == "read" && read < 0 {
							read = i
						}
						if c.kind == "set" && c.step.Color == "#777777" && next < 0 {
							next = i
						}
					}
					if off < 0 || read <= off || next <= read || max != 1 || unsafe {
						t.Fatalf("replacement bypassed cleanup: %#v", calls)
					}
				})
			}
		}
	}
}

func TestPatternLegacyReplaceUsesSameCleanup(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(120), receiptTicket(e, 120)
			mustPattern(t, e, patternResolved(t, kind, ref, "#111111"), "queue", ticket)
			awaitEngineSignal(t, native.entered)
			replacement := submitEngineJob(t, e, "a", engineProgram("#777777", "hold"), "replace")
			if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "cancelling" {
				t.Fatal("old replace failed to cancel new source")
			}
			native.open()
			awaitPattern(t, e, ref, ticket, "cancelled")
			awaitEngineJob(t, e, replacement.ID, "succeeded")
			calls, _, _, max, _, unsafe := b.snapshot()
			if max != 1 || unsafe {
				t.Fatal("cross-origin lease violated")
			}
			colors := engineSetColors(calls)
			if colors[len(colors)-1] != "#777777" {
				t.Fatal(colors)
			}
		})
	}
}

func TestPatternFailedReplacementNeverCancelsPriorWork(t *testing.T) {
	for _, failure := range []string{"entries", "cost", "random", "collision", "capabilities", "integrity"} {
		t.Run(failure, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			active := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
			awaitEngineSignal(t, native.entered)
			queued := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
			ref, ticket := patternRef(130), receiptTicket(e, 130)
			resolved := patternResolved(t, "program", ref, "#777777")
			entropyErr := errors.New("fake entropy failure")
			e.mu.Lock()
			switch failure {
			case "entries":
				e.receipts.maxEntries = 0
			case "cost":
				e.receipts.maxSteps = 0
			case "random":
				e.receipts.random = receiptFailReader{entropyErr}
			case "collision":
				raw, _ := hex.DecodeString(strings.TrimPrefix(active.ID, "job_"))
				e.receipts.random = bytes.NewReader(bytes.Repeat(raw, 4))
			case "capabilities":
				e.workers["a"].state.Capabilities = []string{"set"}
			}
			e.mu.Unlock()
			if failure == "integrity" {
				resolved.ExecutionDigest = "invalid"
			}
			if _, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "replace", ticket, patternResolver(resolved)); err == nil {
				t.Fatal("rejected replacement succeeded")
			}
			for id, want := range map[string]string{active.ID: "running", queued.ID: "queued"} {
				j, err := e.Job(id)
				if err != nil || j.Status != want {
					t.Fatalf("failure preempted old work: %s %#v %v", failure, j, err)
				}
			}
			assertReceiptUsage(t, e, 0, 0)
			e.mu.Lock()
			if len(e.jobs) != 2 || len(e.workers["a"].queue) != 1 {
				t.Error("failed replacement reserved publication")
			}
			e.mu.Unlock()
			native.open()
		})
	}
}

func TestPatternFinalBusyAndQueueBoundaries(t *testing.T) {
	for _, policy := range []string{"reject", "queue", "replace"} {
		t.Run(policy, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{QueueSize: 1})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(140), receiptTicket(e, 140)
			resolved := patternResolved(t, "program", ref, "#777777")
			entered, release := make(chan struct{}), make(chan struct{})
			type outcome struct {
				r   PatternReceiptResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				r, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, policy, ticket, func(context.Context, patternidentity.Ref) (ResolvedPattern, error) {
					close(entered)
					<-release
					return resolved, nil
				})
				done <- outcome{r, err}
			}()
			awaitEngineSignal(t, entered)
			active := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
			awaitEngineSignal(t, native.entered)
			queued := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
			close(release)
			r := <-done
			if policy == "replace" {
				if r.err != nil {
					t.Fatal(r.err)
				}
				a, _ := e.Job(active.ID)
				q, _ := e.Job(queued.ID)
				if a.Status != "cancelling" || q.Status != "cancelled" {
					t.Fatal("replace did not recheck final mixed queue")
				}
			} else {
				want := ErrBusy
				if policy == "queue" {
					want = ErrQueueFull
				}
				if !errors.Is(r.err, want) {
					t.Fatalf("final policy error=%v want %v", r.err, want)
				}
				assertReceiptUsage(t, e, 0, 0)
			}
			native.open()
			if policy == "replace" {
				awaitPattern(t, e, ref, ticket, "succeeded")
			}
		})
	}
}

func TestPatternTerminalWaitsForReadAndCancellationDuringHold(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			readGate := newEngineGate()
			defer readGate.open()
			var hasSet, cleaning int32
			b.hooks(func(Step) error { atomic.StoreInt32(&hasSet, 1); return nil }, func(mode string) error {
				if mode == "hold" && atomic.LoadInt32(&hasSet) != 0 {
					atomic.StoreInt32(&cleaning, 1)
				}
				return nil
			}, func() error {
				if atomic.LoadInt32(&cleaning) != 0 {
					return readGate.block()
				}
				return nil
			})
			ref, ticket := patternRef(150), receiptTicket(e, 150)
			resolved := patternResolved(t, kind, ref, "#123456")
			if kind == "frame" {
				resolved.Execution.FrameProgram.OnFinish = "hold"
				resolved.ExecutionDigest, _ = resolved.Execution.Digest()
			}
			accepted := mustPattern(t, e, resolved, "reject", ticket)
			awaitEngineSignal(t, readGate.entered)
			before := queryPattern(t, e, ref, ticket)
			if patternEnvelopeStatus(before) != "running" {
				t.Fatal("terminal acknowledged before readback")
			}
			if kind == "frame" && (before.FrameJob.CompletedFrames != before.FrameJob.TotalFrames || before.FrameJob.FinishedAt != nil) {
				t.Fatal("tail/cleanup ended too early")
			}
			cancelled, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, patternEnvelopeID(accepted.Job))
			if err != nil || patternEnvelopeStatus(cancelled) != "cancelling" {
				t.Fatal(err)
			}
			readGate.open()
			terminal := awaitPattern(t, e, ref, ticket, "cancelled")
			if terminal.ExecutionDigest != resolved.ExecutionDigest {
				t.Fatal("cleanup lost provenance")
			}
			calls, one, two, _, _, _ := b.snapshot()
			foundOff := false
			for _, c := range calls {
				if c.kind == "stop" && c.mode == "off" {
					foundOff = true
				}
			}
			if !foundOff || one != "#000000" || two != "#000000" {
				t.Fatal("cancel during hold did not redo off+read")
			}
		})
	}
}

func TestPatternCleanupFailureRetainsSource(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		for _, operation := range []string{"stop", "read"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				e, b, _ := startPatternEngine(t, Options{})
				var doneSet int32
				failure := errors.New("fake cleanup/read failure")
				b.hooks(func(Step) error { atomic.StoreInt32(&doneSet, 1); return nil }, func(string) error {
					if operation == "stop" && atomic.LoadInt32(&doneSet) != 0 {
						return failure
					}
					return nil
				}, func() error {
					if operation == "read" && atomic.LoadInt32(&doneSet) != 0 {
						return failure
					}
					return nil
				})
				ref, ticket := patternRef(160), receiptTicket(e, 160)
				resolved := patternResolved(t, kind, ref, "#123456")
				mustPattern(t, e, resolved, "reject", ticket)
				job := awaitPattern(t, e, ref, ticket, "failed")
				if job.PatternRef != ref || job.ExecutionDigest != resolved.ExecutionDigest {
					t.Fatal("failure stripped source")
				}
				msg := ""
				if job.ProgramJob != nil {
					msg = job.ProgramJob.Error
				} else {
					msg = job.FrameJob.Error
				}
				if !strings.Contains(msg, failure.Error()) {
					t.Fatal("cleanup diagnostic missing")
				}
			})
		}
	}
}

func TestPatternHistoryRetentionCloseRestartAndTerminalNoOp(t *testing.T) {
	e, b, clock := startPatternEngine(t, Options{MaxHistory: 1})
	ref, ticket := patternRef(170), receiptTicket(e, 170)
	resolved := patternResolved(t, "program", ref, "#123456")
	accepted := mustPattern(t, e, resolved, "queue", ticket)
	awaitPattern(t, e, ref, ticket, "succeeded")
	other := submitEngineJob(t, e, "a", engineProgram("#777777", "hold"), "queue")
	awaitEngineJob(t, e, other.ID, "succeeded")
	id := patternEnvelopeID(accepted.Job)
	e.mu.Lock()
	_, inHistory := e.jobs[id]
	e.mu.Unlock()
	if inHistory {
		t.Fatal("small history did not evict Pattern record")
	}
	before, _, _, _, _, _ := b.snapshot()
	terminal, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, id)
	if err != nil || patternEnvelopeStatus(terminal) != "succeeded" {
		t.Fatal("terminal receipt inaccessible after history eviction")
	}
	after, one, _, _, _, _ := b.snapshot()
	if len(before) != len(after) || one != "#777777" {
		t.Fatal("terminal hold cancel operated hardware")
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	replay, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "queue", ticket, nil)
	if err != nil || !replay.Replayed {
		t.Fatalf("closed runtime lost accepted receipt: %v", err)
	}
	if _, err := e.PatternRequest("a", ticket, ref); err != nil {
		t.Fatal(err)
	}
	otherEngine, err := New(frameIntegrationDriver{backend: &frameIntegrationBackend{engineBackend: newEngineBackend("a")}}, Options{Serials: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	defer otherEngine.Close(context.Background())
	if _, err := otherEngine.PatternRequest("a", ticket, ref); !errors.Is(err, ErrInstanceChanged) {
		t.Fatal(err)
	}
	clock.advance(receiptRetentionMS)
	if _, err := e.PatternRequest("a", ticket, ref); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("expired receipt=%v", err)
	}
	assertReceiptUsage(t, e, 0, 0)
	if _, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "queue", ticket, nil); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("expired original key rebuilt: %v", err)
	}
}

func TestPatternActivePinnedAndNativeOwnershipDuringClose(t *testing.T) {
	e, b, clock := startPatternEngine(t, Options{})
	native := newEngineGate()
	defer native.open()
	b.hooks(func(Step) error { return native.block() }, nil, nil)
	ref, ticket := patternRef(180), receiptTicket(e, 180)
	resolved := patternResolved(t, "frame", ref, "#123456")
	mustPattern(t, e, resolved, "queue", ticket)
	awaitEngineSignal(t, native.entered)
	clock.advance(3 * receiptWindowMS)
	if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "running" {
		t.Fatal("active receipt expired")
	}
	if r, err := e.SubmitPatternWithReceipt(context.Background(), "a", ref, "queue", ticket, nil); err != nil || !r.Replayed {
		t.Fatalf("active replay after deadline=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fake native close should still wait: %v", err)
	}
	if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "cancelling" {
		t.Fatal("close did not preserve cancelling source")
	}
	_, _, _, _, closes, unsafe := b.snapshot()
	if closes != 0 || unsafe {
		t.Fatal("Close timeout released native ownership")
	}
	native.open()
	awaitPattern(t, e, ref, ticket, "cancelled")
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, _, max, closes, unsafe := b.snapshot()
	if max != 1 || closes != 1 || unsafe {
		t.Fatal("native ownership cleanup order failed")
	}
	clock.advance(receiptRetentionMS - 1)
	queryPattern(t, e, ref, ticket)
	clock.advance(1)
	if _, err := e.PatternRequest("a", ticket, ref); !errors.Is(err, ErrRequestExpired) {
		t.Fatal("query slid terminal TTL")
	}
	assertReceiptUsage(t, e, 0, 0)
}

func TestPatternStoredCostNotRepeatExpanded(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ref, ticket := patternRef(190), receiptTicket(e, 190)
			resolved := patternResolved(t, kind, ref, "#123456")
			cost := 1
			if kind == "program" {
				resolved.Execution.Program.Repeat = 10000
			} else {
				resolved.Execution.FrameProgram.Repeat = 2
				cost = 4
			}
			resolved.ExecutionDigest, _ = resolved.Execution.Digest()
			e.mu.Lock()
			e.receipts.maxSteps = cost
			e.mu.Unlock()
			mustPattern(t, e, resolved, "queue", ticket)
			awaitEngineSignal(t, native.entered)
			assertReceiptUsage(t, e, 1, cost)
			if _, err := e.SubmitPatternWithReceipt(context.Background(), "a", patternRef(191), "replace", receiptTicket(e, 191), patternResolver(patternResolved(t, kind, patternRef(191), "#777777"))); !errors.Is(err, ErrReceiptCapacity) {
				t.Fatalf("retained budget=%v", err)
			}
			if patternEnvelopeStatus(queryPattern(t, e, ref, ticket)) != "running" {
				t.Fatal("capacity failure cancelled original")
			}
			if _, err := e.CancelPatternRequest(context.Background(), "a", ticket, ref, ""); err != nil {
				t.Fatal(err)
			}
			native.open()
			awaitPattern(t, e, ref, ticket, "cancelled")
		})
	}
}

func TestPatternLegacyReceiptFingerprintBytesUnchanged(t *testing.T) {
	// These are frozen old payload bytes, not a copy of a new hashing struct.
	// They guard both legacy domains when Pattern origin joins the key space.
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			e, b, _ := startPatternEngine(t, Options{})
			native := newEngineGate()
			defer native.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			ticket := receiptTicket(e, 200)
			var payload []byte
			if kind == "program" {
				p := Program{Steps: []Step{{LED: 0, Color: "#123456", FadeMS: 0, HoldMS: 0}}, Repeat: 1, OnFinish: "hold"}
				submitReceipt(t, e, p, "queue", ticket)
				payload = []byte(`{"Version":1,"Serial":"a","Program":{"steps":[{"led":0,"color":"#123456","fade_ms":0,"hold_ms":0}],"repeat":1,"on_finish":"hold"},"Policy":"queue"}`)
			} else {
				if _, err := e.SubmitFrameWithReceipt(context.Background(), "a", frameIntegrationProgram(), "reject", ticket); err != nil {
					t.Fatal(err)
				}
				payload = append([]byte("nb1-frame-admission/experimental-v1\n"), []byte(`{"Serial":"a","Program":{"contract":"nb1-frame-program/experimental-v1","initial":[{"led":1,"color":"#990000"},{"led":2,"color":"#000099"}],"frames":[{"targets":[{"led":1,"color":"#000099"},{"led":2,"color":"#990000"}],"fade_ms":50,"interval_ms":50}],"repeat":1,"on_finish":"off"},"Policy":"reject"}`)...)
			}
			e.mu.Lock()
			got := e.receipts.entries[receiptKey{"a", ticket.RequestID}].digest
			e.mu.Unlock()
			if got != sha256.Sum256(payload) {
				t.Fatalf("legacy %s fingerprint changed", kind)
			}
			native.open()
		})
	}
}

type patternBlockedRandom struct {
	gate *engineGate
	data *bytes.Reader
}

func (r patternBlockedRandom) Read(p []byte) (int, error) {
	if err := r.gate.block(); err != nil {
		return 0, err
	}
	return r.data.Read(p)
}

func TestPatternCancellationOrExpiryBeforeAtomicPublishDoesNotPreempt(t *testing.T) {
	for _, reason := range []string{"context", "deadline"} {
		t.Run(reason, func(t *testing.T) {
			e, b, clock := startPatternEngine(t, Options{})
			native, entropy := newEngineGate(), newEngineGate()
			defer native.open()
			defer entropy.open()
			b.hooks(func(Step) error { return native.block() }, nil, nil)
			active := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
			awaitEngineSignal(t, native.entered)
			queued := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
			ref, ticket := patternRef(210), receiptTicket(e, 210)
			resolved := patternResolved(t, "program", ref, "#777777")
			nonce, _ := hex.DecodeString(strings.TrimPrefix(active.ID, "job_"))
			nonce[0] ^= 0xff
			e.mu.Lock()
			e.receipts.random = patternBlockedRandom{gate: entropy, data: bytes.NewReader(nonce)}
			e.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := e.SubmitPatternWithReceipt(ctx, "a", ref, "replace", ticket, patternResolver(resolved))
				done <- err
			}()
			awaitEngineSignal(t, entropy.entered)
			want := context.Canceled
			if reason == "context" {
				cancel()
			} else {
				clock.advance(receiptWindowMS + 1)
				want = ErrRequestExpired
			}
			entropy.open()
			if err := <-done; !errors.Is(err, want) {
				t.Fatalf("cancelled/expired final publication=%v", err)
			}
			for id, want := range map[string]string{active.ID: "running", queued.ID: "queued"} {
				j, err := e.Job(id)
				if err != nil || j.Status != want {
					t.Fatalf("failure before publication preempted prior work: %#v %v", j, err)
				}
			}
			assertReceiptUsage(t, e, 0, 0)
			native.open()
		})
	}
}
