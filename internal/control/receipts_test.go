package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type receiptClock struct{ ms int64 }

func (c *receiptClock) now() int64       { return atomic.LoadInt64(&c.ms) }
func (c *receiptClock) advance(ms int64) { atomic.AddInt64(&c.ms, ms) }

func receiptTestEngine(t *testing.T, options Options, b *engineBackend) (*Engine, *receiptClock) {
	t.Helper()
	e := startEngineTest(t, options, b)
	clock := &receiptClock{ms: 100}
	e.mu.Lock()
	e.receipts.nowMS = clock.now
	e.mu.Unlock()
	return e, clock
}

func receiptTicket(e *Engine, nonce int) ReceiptRequest {
	info := e.Instance()
	return ReceiptRequest{RequestID: info.Idempotency.RequestIDPrefix + fmt.Sprintf("%032x", nonce), ExpectedDaemonInstanceID: info.DaemonInstanceID}
}

func submitReceipt(t *testing.T, e *Engine, p Program, policy string, ticket ReceiptRequest) ReceiptResult {
	t.Helper()
	result, err := e.SubmitWithReceipt(context.Background(), "a", p, policy, ticket)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func receiptLookup(t *testing.T, e *Engine, ticket ReceiptRequest) Job {
	t.Helper()
	job, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func assertReceiptUsage(t *testing.T, e *Engine, entries, steps int) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.receipts.entries) != entries || e.receipts.steps != steps {
		t.Fatalf("receipt usage=%d entries/%d steps, want %d/%d", len(e.receipts.entries), e.receipts.steps, entries, steps)
	}
}

func TestReceiptConcurrentAdmissionAndNormalizedPayload(t *testing.T) {
	for _, policy := range []string{"replace", "queue", "reject"} {
		t.Run(policy, func(t *testing.T) {
			b := newEngineBackend("a")
			e, _ := receiptTestEngine(t, Options{}, b)
			gate := newEngineGate()
			defer gate.open()
			b.hooks(func(Step) error { return gate.block() }, nil, nil)
			ticket := receiptTicket(e, 1)
			start := make(chan struct{})
			results := make(chan ReceiptResult, 100)
			errs := make(chan error, 100)
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					p := engineProgram("#aabbcc", "off")
					selectedPolicy := policy
					if i%2 == 0 {
						p.Steps[0].Color, p.Repeat, p.OnFinish = "#AABBCC", 1, ""
						if policy == "replace" {
							selectedPolicy = ""
						}
					}
					<-start
					result, err := e.SubmitWithReceipt(context.Background(), "a", p, selectedPolicy, ticket)
					results <- result
					errs <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			var id string
			fresh := 0
			for result := range results {
				if id == "" {
					id = result.Job.ID
				}
				if result.Job.ID != id || result.DaemonInstanceID != ticket.ExpectedDaemonInstanceID {
					t.Fatalf("concurrent admission returned a different job/instance: %+v", result)
				}
				if !result.Replayed {
					fresh++
				}
			}
			if fresh != 1 {
				t.Fatalf("fresh admissions=%d, want 1", fresh)
			}
			awaitEngineSignal(t, gate.entered)
			assertReceiptUsage(t, e, 1, 1)
			for _, change := range []string{"color", "led", "fade", "hold", "repeat", "on_finish", "policy"} {
				p, changedPolicy := engineProgram("#aabbcc", "off"), policy
				switch change {
				case "color":
					p.Steps[0].Color = "#112233"
				case "led":
					p.Steps[0].LED = 1
				case "fade":
					p.Steps[0].FadeMS = 10
				case "hold":
					p.Steps[0].HoldMS = 1
				case "repeat":
					p.Repeat = 2
				case "on_finish":
					p.OnFinish = "hold"
				case "policy":
					changedPolicy = "queue"
					if policy == "queue" {
						changedPolicy = "replace"
					}
				}
				if _, err := e.SubmitWithReceipt(context.Background(), "a", p, changedPolicy, ticket); !errors.Is(err, ErrRequestConflict) {
					t.Errorf("%s conflict=%v", change, err)
				}
			}
			job := receiptLookup(t, e, ticket)
			if job.Status != "running" {
				t.Fatalf("conflict changed running job: %+v", job)
			}
			job.Program.Steps[0].Color = "#ffffff"
			if receiptLookup(t, e, ticket).Program.Steps[0].Color != "#aabbcc" {
				t.Fatal("receipt returned aliased program storage")
			}
			gate.open()
			awaitEngineJob(t, e, id, "succeeded")
			calls, _, _, _, _, _ := b.snapshot()
			if colors := engineSetColors(calls); len(colors) != 1 {
				t.Fatalf("same request executed %d times", len(colors))
			}
		})
	}
}

func TestReceiptCapacityRejectsBeforeReplaceSideEffects(t *testing.T) {
	for _, bound := range []string{"entries", "steps"} {
		t.Run(bound, func(t *testing.T) {
			b := newEngineBackend("a")
			e, _ := receiptTestEngine(t, Options{}, b)
			gate := newEngineGate()
			defer gate.open()
			b.hooks(func(Step) error { return gate.block() }, nil, nil)
			active := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
			awaitEngineSignal(t, gate.entered)
			queued := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
			e.mu.Lock()
			if bound == "entries" {
				e.receipts.maxEntries = 1
			} else {
				e.receipts.maxSteps = 1
			}
			e.mu.Unlock()
			p, ticket := engineProgram("#333333", "hold"), receiptTicket(e, 1)
			if bound == "steps" {
				p.Repeat = 10000 // Charge stored steps, not repeat-expanded work.
			}
			accepted := submitReceipt(t, e, p, "queue", ticket)
			if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "replace", receiptTicket(e, 2)); !errors.Is(err, ErrReceiptCapacity) {
				t.Fatalf("capacity error=%v", err)
			}
			if result := submitReceipt(t, e, p, "queue", ticket); !result.Replayed || result.Job.ID != accepted.Job.ID {
				t.Fatal("full cache rejected a known key")
			}
			for _, id := range []string{active.ID, queued.ID, accepted.Job.ID} {
				job, _ := e.Job(id)
				if job.Status != "running" && job.Status != "queued" {
					t.Fatalf("capacity rejection changed old work: %+v", job)
				}
			}
			assertReceiptUsage(t, e, 1, 1)
			if state, _ := e.Device("a"); state.QueueDepth != 2 {
				t.Fatalf("capacity rejection changed queue: %+v", state)
			}
			// Unkeyed submissions remain independent of receipt quotas.
			submitEngineJob(t, e, "a", engineProgram("#444444", "hold"), "queue")
			assertReceiptUsage(t, e, 1, 1)
			e.beginClose()
		})
	}
}

type receiptFailReader struct{ err error }

func (r receiptFailReader) Read([]byte) (int, error) { return 0, r.err }

func TestReceiptRandomFailureCannotCancelOrReserve(t *testing.T) {
	failure := errors.New("injected entropy failure")
	if _, err := newReceiptState(receiptFailReader{failure}, nil); !errors.Is(err, failure) {
		t.Fatalf("instance initialization failure=%v", err)
	}
	b := newEngineBackend("a")
	e, _ := receiptTestEngine(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	active := submitEngineJob(t, e, "a", engineProgram("#111111", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	p, ticket := engineProgram("#222222", "hold"), receiptTicket(e, 1)
	queued := submitReceipt(t, e, p, "queue", ticket)
	e.mu.Lock()
	e.receipts.random = receiptFailReader{failure}
	e.mu.Unlock()
	if result := submitReceipt(t, e, p, "queue", ticket); !result.Replayed {
		t.Fatal("replay read the failed random source")
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "replace", receiptTicket(e, 2)); !errors.Is(err, failure) {
		t.Fatalf("new request random failure=%v", err)
	}
	if _, err := e.Submit(context.Background(), "a", p, "replace"); !errors.Is(err, failure) {
		t.Fatalf("legacy random failure=%v", err)
	}
	assertReceiptUsage(t, e, 1, 1)
	for id, want := range map[string]string{active.ID: "running", queued.Job.ID: "queued"} {
		job, _ := e.Job(id)
		if job.Status != want {
			t.Fatalf("random failure changed job: %+v want %s", job, want)
		}
	}
	e.beginClose()
}

func TestReceiptRetentionIndependentOfHistoryAndRestart(t *testing.T) {
	b := newEngineBackend("a")
	e, clock := receiptTestEngine(t, Options{MaxHistory: 1}, b)
	p := engineProgram("#112233", "hold")
	var first ReceiptRequest
	var firstID string
	for i := 1; i <= 3; i++ {
		ticket := receiptTicket(e, i)
		result := submitReceipt(t, e, p, "queue", ticket)
		awaitEngineJob(t, e, result.Job.ID, "succeeded")
		if i == 1 {
			first, firstID = ticket, result.Job.ID
		}
	}
	if _, err := e.Job(firstID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("normal history did not evict: %v", err)
	}
	if job := receiptLookup(t, e, first); job.ID != firstID || job.Status != "succeeded" {
		t.Fatalf("history eviction broke receipt: %+v", job)
	}
	clock.advance(59999)
	if result := submitReceipt(t, e, p, "queue", first); !result.Replayed {
		t.Fatal("valid terminal receipt was not replayed")
	}
	clock.advance(1)
	if _, err := e.Request("a", first.RequestID, first.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("expired receipt lookup=%v", err)
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "queue", first); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("old token became a new task: %v", err)
	}
	assertReceiptUsage(t, e, 0, 0)
	fresh := receiptTicket(e, 4)
	accepted := submitReceipt(t, e, p, "queue", fresh)
	awaitEngineJob(t, e, accepted.Job.ID, "succeeded")
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := submitReceipt(t, e, p, "queue", fresh); !result.Replayed {
		t.Fatal("closed Engine rejected known receipt")
	}
	if receiptLookup(t, e, fresh).ID != accepted.Job.ID {
		t.Fatal("closed Engine lost receipt")
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "queue", receiptTicket(e, 5)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Engine accepted new key: %v", err)
	}
	other, err := New(engineDriver{"a": newEngineBackend("a")}, Options{Serials: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	if other.Instance().DaemonInstanceID == e.Instance().DaemonInstanceID {
		t.Fatal("new Engine reused instance ID")
	}
	if _, err := other.Request("a", fresh.RequestID, fresh.ExpectedDaemonInstanceID); !errors.Is(err, ErrInstanceChanged) {
		t.Fatalf("restart lookup=%v", err)
	}
	if _, err := other.SubmitWithReceipt(context.Background(), "a", p, "queue", fresh); !errors.Is(err, ErrInstanceChanged) {
		t.Fatalf("restart submission=%v", err)
	}
}

func TestReceiptActivePinnedAndTerminalTTLDoesNotSlide(t *testing.T) {
	b := newEngineBackend("a")
	e, clock := receiptTestEngine(t, Options{}, b)
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	e.mu.Lock()
	e.receipts.maxEntries = 2
	e.mu.Unlock()
	p := engineProgram("#112233", "hold")
	activeTicket, queuedTicket := receiptTicket(e, 1), receiptTicket(e, 2)
	active := submitReceipt(t, e, p, "queue", activeTicket)
	awaitEngineSignal(t, gate.entered)
	queued := submitReceipt(t, e, p, "queue", queuedTicket)
	clock.advance(120000)
	if receiptLookup(t, e, activeTicket).Status != "running" || receiptLookup(t, e, queuedTicket).Status != "queued" {
		t.Fatal("active/queued receipt was expired")
	}
	if !submitReceipt(t, e, p, "queue", activeTicket).Replayed {
		t.Fatal("expired admission window blocked active replay")
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "replace", receiptTicket(e, 3)); !errors.Is(err, ErrReceiptCapacity) {
		t.Fatalf("pinned capacity=%v", err)
	}
	if _, err := e.Cancel(context.Background(), queued.Job.ID); err != nil {
		t.Fatal(err)
	}
	if !submitReceipt(t, e, p, "queue", queuedTicket).Replayed || receiptLookup(t, e, queuedTicket).Status != "cancelled" {
		t.Fatal("queued cancellation did not preserve receipt")
	}
	clock.advance(59999)
	if receiptLookup(t, e, queuedTicket).Status != "cancelled" {
		t.Fatal("terminal TTL was anchored to admission instead of finish")
	}
	if _, err := e.Cancel(context.Background(), active.Job.ID); err != nil {
		t.Fatal(err)
	}
	if receiptLookup(t, e, activeTicket).Status != "cancelling" {
		t.Fatal("active cancellation lost current status")
	}
	gate.open()
	awaitEngineJob(t, e, active.Job.ID, "cancelled")
	clock.advance(1)
	if _, err := e.Request("a", queuedTicket.RequestID, queuedTicket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("queries renewed queued receipt TTL: %v", err)
	}
	assertReceiptUsage(t, e, 1, 1)
	clock.advance(59998)
	if receiptLookup(t, e, activeTicket).Status != "cancelled" {
		t.Fatal("active terminal receipt expired too early")
	}
	clock.advance(1)
	if _, err := e.Request("a", activeTicket.RequestID, activeTicket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("queries renewed active receipt TTL: %v", err)
	}
	assertReceiptUsage(t, e, 0, 0)
}

func TestReceiptFailureOfflineAndBusyReplay(t *testing.T) {
	b := newEngineBackend("a")
	e, _ := receiptTestEngine(t, Options{}, b)
	p, ticket := engineProgram("#112233", "hold"), receiptTicket(e, 1)
	b.hooks(func(Step) error { return ErrOffline }, nil, nil)
	accepted := submitReceipt(t, e, p, "queue", ticket)
	awaitEngineJob(t, e, accepted.Job.ID, "failed")
	awaitEngineCondition(t, func() bool { state, _ := e.Device("a"); return state.Connection == "offline" })
	if result := submitReceipt(t, e, p, "queue", ticket); !result.Replayed || result.Job.Status != "failed" {
		t.Fatal("offline replay retried a failed program")
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "queue", receiptTicket(e, 2)); !errors.Is(err, ErrOffline) {
		t.Fatalf("new offline request=%v", err)
	}
	e.mu.Lock()
	e.workers["a"].state.Connection = "busy"
	e.mu.Unlock()
	if result := submitReceipt(t, e, p, "queue", ticket); !result.Replayed {
		t.Fatal("busy state rejected known receipt")
	}
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "queue", receiptTicket(e, 3)); !errors.Is(err, ErrBusy) {
		t.Fatalf("new busy request=%v", err)
	}
	assertReceiptUsage(t, e, 1, 1)
}

func TestReceiptInputValidationAndMissingKey(t *testing.T) {
	e, clock := receiptTestEngine(t, Options{}, newEngineBackend("a"))
	info := e.Instance().Idempotency
	if !info.Supported || info.AcceptUntilUptimeMS != 60100 || info.MaxAcceptWindowMS != 60000 || info.ReceiptRetentionMS != 60000 || info.MaxEntries != 4096 || info.MaxRetainedSteps != 65536 {
		t.Fatalf("unexpected default receipt capabilities: %+v", info)
	}
	ticket := receiptTicket(e, 1)
	if _, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("unaccepted request lookup=%v", err)
	}
	invalid := []ReceiptRequest{
		{}, {RequestID: ticket.RequestID}, {ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: strings.ToUpper(ticket.RequestID), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: "req_0_" + strings.Repeat("a", 32), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: "req_060100_" + strings.Repeat("a", 32), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: "req_-1_" + strings.Repeat("a", 32), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: "req_9223372036854775808_" + strings.Repeat("a", 32), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: "req_9223372036854775807_" + strings.Repeat("a", 32), ExpectedDaemonInstanceID: ticket.ExpectedDaemonInstanceID},
		{RequestID: ticket.RequestID, ExpectedDaemonInstanceID: "daemon_" + strings.Repeat("A", 32)},
	}
	for _, input := range invalid {
		if _, err := e.Request("a", input.RequestID, input.ExpectedDaemonInstanceID); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid lookup %+v: %v", input, err)
		}
		if _, err := e.SubmitWithReceipt(context.Background(), "a", engineProgram("#112233", "off"), "queue", input); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid submission %+v: %v", input, err)
		}
	}
	clock.advance(60000)
	if _, err := e.Request("a", ticket.RequestID, ticket.ExpectedDaemonInstanceID); !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("expired unknown request=%v", err)
	}
	assertReceiptUsage(t, e, 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.SubmitWithReceipt(ctx, "a", engineProgram("#112233", "off"), "queue", receiptTicket(e, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled client context=%v", err)
	}
	if _, err := newReceiptState(io.LimitReader(strings.NewReader("short"), 5), clock.now); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial instance randomness=%v", err)
	}
}

func TestReceiptReplayDoesNotReplaceNewerWorkOrRejectFullQueue(t *testing.T) {
	b := newEngineBackend("a")
	e, _ := receiptTestEngine(t, Options{QueueSize: 1}, b)
	p, ticket := engineProgram("#112233", "hold"), receiptTicket(e, 1)
	old := submitReceipt(t, e, p, "replace", ticket)
	awaitEngineJob(t, e, old.Job.ID, "succeeded")
	gate := newEngineGate()
	defer gate.open()
	b.hooks(func(Step) error { return gate.block() }, nil, nil)
	active := submitEngineJob(t, e, "a", engineProgram("#222222", "hold"), "queue")
	awaitEngineSignal(t, gate.entered)
	queued := submitEngineJob(t, e, "a", engineProgram("#333333", "hold"), "queue")
	if _, err := e.SubmitWithReceipt(context.Background(), "a", p, "queue", receiptTicket(e, 2)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue new key=%v", err)
	}
	if result := submitReceipt(t, e, p, "replace", ticket); !result.Replayed || result.Job.ID != old.Job.ID || result.Job.Status != "succeeded" {
		t.Fatalf("old receipt did not return original success: %+v", result)
	}
	for id, want := range map[string]string{active.ID: "running", queued.ID: "queued"} {
		job, _ := e.Job(id)
		if job.Status != want {
			t.Fatalf("replay applied replace to newer work: %+v", job)
		}
	}
	assertReceiptUsage(t, e, 1, 1)
	e.beginClose()
}

func TestReceiptKeyScopeIncludesSerial(t *testing.T) {
	a, b := newEngineBackend("a"), newEngineBackend("b")
	e := startEngineTest(t, Options{}, a, b)
	ticket := receiptTicket(e, 1)
	p := engineProgram("#112233", "hold")
	first := submitReceipt(t, e, p, "queue", ticket)
	second, err := e.SubmitWithReceipt(context.Background(), "b", p, "queue", ticket)
	if err != nil || second.Replayed || first.Job.ID == second.Job.ID || second.Job.Device != "b" {
		t.Fatalf("serial scope did not produce independent job: %+v err=%v", second, err)
	}
	for serial, id := range map[string]string{"a": first.Job.ID, "b": second.Job.ID} {
		job, err := e.Request(serial, ticket.RequestID, ticket.ExpectedDaemonInstanceID)
		if err != nil || job.ID != id {
			t.Fatalf("serial %s resolved to wrong job: %+v err=%v", serial, job, err)
		}
		awaitEngineJob(t, e, id, "succeeded")
	}
	assertReceiptUsage(t, e, 2, 2)
}
