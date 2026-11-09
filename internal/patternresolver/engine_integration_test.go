package patternresolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/b1ug/nb1/internal/patternsv2"
)

type testGate struct {
	entered, release chan struct{}
	enter, open      sync.Once
}

func gate() *testGate        { return &testGate{entered: make(chan struct{}), release: make(chan struct{})} }
func (g *testGate) block()   { g.enter.Do(func() { close(g.entered) }); <-g.release }
func (g *testGate) unblock() { g.open.Do(func() { close(g.release) }) }
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture channel timed out")
	}
}

type backend struct {
	mu                                  sync.Mutex
	one, two                            string
	sets, closes, inFlight, maxInFlight int
	closedWhileBusy                     bool
	setGate                             *testGate
	onSet                               func() error
}

func (b *backend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "fixture", Firmware: 204, Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold", "cancel", "observe"}}
}
func (b *backend) Set(s control.Step) error {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.maxInFlight {
		b.maxInFlight = b.inFlight
	}
	b.sets++
	g, hook := b.setGate, b.onSet
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.inFlight--; b.mu.Unlock() }()
	if g != nil {
		g.block()
	}
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.LED == 0 || s.LED == 1 {
		b.one = s.Color
	}
	if s.LED == 0 || s.LED == 2 {
		b.two = s.Color
	}
	return nil
}
func (b *backend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mode == "off" {
		b.one, b.two = "#000000", "#000000"
	}
	return nil
}
func (b *backend) Read() (control.Observation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return control.Observation{LED1: b.one, LED2: b.two, SampledAt: time.Now().UTC()}, nil
}
func (b *backend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closedWhileBusy = b.closedWhileBusy || b.inFlight != 0
	b.closes++
}

type driver struct{ b *backend }

func (d driver) Open(string) (control.Backend, error) {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	if d.b.closes > 0 {
		return nil, control.ErrOffline
	}
	return d.b, nil
}
func startEngine(t *testing.T, b *backend) *control.Engine {
	t.Helper()
	b.one, b.two = "#000000", "#000000"
	e, err := control.New(driver{b}, control.Options{Serials: []string{"fixture"}, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		state, _ := e.Device("fixture")
		if state.Connection == "online" {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("fake device not online")
		}
	}
	return e
}
func ticket(e *control.Engine, n int) control.ReceiptRequest {
	info := e.Instance()
	return control.ReceiptRequest{RequestID: info.Idempotency.RequestIDPrefix + fmt.Sprintf("%032x", n), ExpectedDaemonInstanceID: info.DaemonInstanceID}
}
func jobID(j control.PatternJobEnvelope) string {
	if j.ProgramJob != nil {
		return j.ProgramJob.ID
	}
	return j.FrameJob.ID
}
func status(j control.PatternJobEnvelope) string {
	if j.ProgramJob != nil {
		return j.ProgramJob.Status
	}
	return j.FrameJob.Status
}
func awaitJob(t *testing.T, e *control.Engine, ref patternidentity.Ref, key control.ReceiptRequest, want string) control.PatternJobEnvelope {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		j, err := e.PatternRequest("fixture", key, ref)
		if err != nil {
			t.Fatal(err)
		}
		if status(j) == "succeeded" || status(j) == "failed" || status(j) == "cancelled" {
			if status(j) != want {
				t.Fatalf("job=%#v want=%s", j, want)
			}
			return j
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("fake Pattern did not terminate")
		}
	}
}
func privateStore(t *testing.T, records ...patternsv2.Record) (*patternstore.StoreV2, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "library")
	s, err := patternstore.NewV2(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := patternsv2.EncodeSnapshot(records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(data); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestResolverRealStoreFeedsSameEngineForProgramAndFrame(t *testing.T) {
	for _, kind := range []string{"program", "frame"} {
		t.Run(kind, func(t *testing.T) {
			r := recordFixture(t, kind)
			if kind == "frame" {
				var err error
				r, err = patternsv2.NewRecord(r.ID, r.Title, patternsv2.Source{Kind: "light_action", Contract: "nb1-light-action/experimental-v2", Action: []byte(`{"kind":"orbit","period_ms":800}`)})
				if err != nil {
					t.Fatal(err)
				}
			}
			s, _ := privateStore(t, r)
			b := &backend{}
			if kind == "program" {
				b.onSet = func() error { _, err := s.Export(); return err }
			} // short Store lock must already be released.
			e := startEngine(t, b)
			key, ref := ticket(e, 1), refFor(r)
			var calls int32
			wrapped := storeFunc(func(id, revision string) (patternsv2.Record, error) {
				atomic.AddInt32(&calls, 1)
				return s.Resolve(id, revision)
			})
			accepted, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "reject", key, New(wrapped))
			if err != nil {
				t.Fatal(err)
			}
			terminal := awaitJob(t, e, ref, key, "succeeded")
			want, err := patternsv2.ExecutionDigest(r)
			if err != nil {
				t.Fatal(err)
			}
			if terminal.PatternRef != ref || terminal.ExecutionDigest != want || jobID(terminal) != jobID(accepted.Job) || atomic.LoadInt32(&calls) != 1 {
				t.Fatal("source identity or Resolve count changed")
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.maxInFlight != 1 || b.closedWhileBusy {
				t.Fatal("resolver created competing native executor")
			}
		})
	}
}

func TestResolverAcceptedReceiptSurvivesStoreDeletionAndConfigurationLoss(t *testing.T) {
	r := recordFixture(t, "program")
	s, dir := privateStore(t, r)
	native := gate()
	defer native.unblock()
	b := &backend{setGate: native}
	e := startEngine(t, b)
	key, ref := ticket(e, 2), refFor(r)
	var calls int32
	resolve := New(storeFunc(func(id, revision string) (patternsv2.Record, error) {
		atomic.AddInt32(&calls, 1)
		return s.Resolve(id, revision)
	}))
	accepted, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, resolve)
	if err != nil {
		t.Fatal(err)
	}
	await(t, native.entered)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, retry := range []control.PatternResolver{resolve, New(nil)} {
		replayed, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, retry)
		if err != nil || !replayed.Replayed || jobID(replayed.Job) != jobID(accepted.Job) {
			t.Fatalf("accepted receipt required library/config: %#v %v", replayed, err)
		}
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("replay reread deleted store")
	}
	if _, err := e.PatternRequest("fixture", key, ref); err != nil {
		t.Fatal(err)
	}
	cancelled, err := e.CancelPatternRequest(context.Background(), "fixture", key, ref, jobID(accepted.Job))
	if err != nil || status(cancelled) != "cancelling" {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("query/cancel consulted Store")
	}
	native.unblock()
	awaitJob(t, e, ref, key, "cancelled")
}

func TestResolverConcurrentCoreNewKeysResolveOncePerCallAndPublishOnce(t *testing.T) {
	r := recordFixture(t, "program")
	s, _ := privateStore(t, r)
	native := gate()
	defer native.unblock()
	e := startEngine(t, &backend{setGate: native})
	key, ref := ticket(e, 3), refFor(r)
	const n = 8
	var calls int32
	entered := make(chan struct{})
	releaseGate := gate()
	defer releaseGate.unblock()
	resolve := New(storeFunc(func(id, revision string) (patternsv2.Record, error) {
		if atomic.AddInt32(&calls, 1) == n {
			close(entered)
		}
		releaseGate.block()
		return s.Resolve(id, revision)
	}))
	type result struct {
		r   control.PatternReceiptResult
		err error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			r, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, resolve)
			results <- result{r, err}
		}()
	}
	await(t, entered)
	// A blocked resolver must not hold Engine.mu or prevent cached state reads.
	stateDone := make(chan struct{})
	go func() { _, _ = e.Device("fixture"); close(stateDone) }()
	await(t, stateDone)
	releaseGate.unblock()
	id, fresh := "", 0
	for i := 0; i < n; i++ {
		value := <-results
		if value.err != nil {
			t.Fatal(value.err)
		}
		if id == "" {
			id = jobID(value.r.Job)
		}
		if id != jobID(value.r.Job) {
			t.Fatal("concurrent callbacks published multiple Jobs")
		}
		if !value.r.Replayed {
			fresh++
		}
	}
	if fresh != 1 || atomic.LoadInt32(&calls) != n {
		t.Fatalf("fresh=%d Resolve=%d", fresh, calls)
	}
	await(t, native.entered)
	if _, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, New(storeFunc(func(string, string) (patternsv2.Record, error) { panic("known receipt re-resolved") }))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CancelPatternRequest(context.Background(), "fixture", key, ref, id); err != nil {
		t.Fatal(err)
	}
	native.unblock()
	awaitJob(t, e, ref, key, "cancelled")
}

func TestResolverRaceAvailabilityCanReplayButIntegrityAndUnknownRemain(t *testing.T) {
	for _, failure := range []string{"deleted_store", "wrong_ref", "source_corrupt", "unsafe", "fake_availability_unknown", "unknown", "unsupported"} {
		t.Run(failure, func(t *testing.T) {
			r := recordFixture(t, "program")
			s, dir := privateStore(t, r)
			native := gate()
			defer native.unblock()
			e := startEngine(t, &backend{setGate: native})
			key, ref := ticket(e, 4), refFor(r)
			resolveGate := gate()
			defer resolveGate.unblock()
			unknown := errors.New("original unclassified Store cause")
			var calls int32
			resolve := New(storeFunc(func(id, revision string) (patternsv2.Record, error) {
				call := atomic.AddInt32(&calls, 1)
				if call == 1 {
					resolveGate.block()
					switch failure {
					case "deleted_store":
						return s.Resolve(id, revision)
					case "wrong_ref":
						bad := cloneRecord(t, r)
						bad.ID = "wrong"
						return bad, nil
					case "source_corrupt":
						bad := cloneRecord(t, r)
						bad.Source.Action = []byte(`{"kind":"set","color":"blue"}`)
						return bad, nil
					case "unsafe":
						return patternsv2.Record{}, patternstore.ErrUnsafe
					case "fake_availability_unknown":
						return patternsv2.Record{}, &control.PatternResolveError{Kind: control.PatternResolveAvailability, Err: unknown}
					case "unknown":
						return patternsv2.Record{}, unknown
					case "unsupported":
						return patternsv2.Record{}, patternsv2.ErrUnsupported
					}
				}
				return s.Resolve(id, revision)
			}))
			type result struct {
				r   control.PatternReceiptResult
				err error
			}
			loser := make(chan result, 1)
			go func() {
				r, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, resolve)
				loser <- result{r, err}
			}()
			await(t, resolveGate.entered)
			winner, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, resolve)
			if err != nil {
				t.Fatal(err)
			}
			await(t, native.entered)
			if failure == "deleted_store" {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
			resolveGate.unblock()
			value := <-loser
			if failure == "deleted_store" {
				if value.err != nil || !value.r.Replayed || jobID(value.r.Job) != jobID(winner.Job) {
					t.Fatal("availability did not recover original receipt")
				}
			} else {
				if value.err == nil || value.r.Replayed {
					t.Fatal("concurrent winner hid resolver diagnostic")
				}
				want := control.PatternResolveIntegrity
				if failure == "unknown" || failure == "fake_availability_unknown" {
					want = control.PatternResolveUnknown
					if !errors.Is(value.err, unknown) {
						t.Fatal("unknown cause lost")
					}
				} else if failure == "unsupported" {
					want = control.PatternResolveUnsupported
				}
				if kindOf(t, value.err) != want {
					t.Fatalf("wrong final error: %v", value.err)
				}
			}
			original, err := e.PatternRequest("fixture", key, ref)
			if err != nil || jobID(original) != jobID(winner.Job) || status(original) != "running" {
				t.Fatal("failed loser mutated accepted job")
			}
			if atomic.LoadInt32(&calls) != 2 {
				t.Fatal("more than one Resolve per new-key call")
			}
			if _, err := e.CancelPatternRequest(context.Background(), "fixture", key, ref, jobID(winner.Job)); err != nil {
				t.Fatal(err)
			}
			native.unblock()
			awaitJob(t, e, ref, key, "cancelled")
		})
	}
}

func TestResolverContextRaceCannotBeHiddenByGoodReceipt(t *testing.T) {
	r := recordFixture(t, "program")
	s, _ := privateStore(t, r)
	native := gate()
	defer native.unblock()
	e := startEngine(t, &backend{setGate: native})
	key, ref := ticket(e, 5), refFor(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolveGate := gate()
	defer resolveGate.unblock()
	var calls int32
	resolve := New(storeFunc(func(id, revision string) (patternsv2.Record, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			resolveGate.block()
		}
		return s.Resolve(id, revision)
	}))
	done := make(chan error, 1)
	go func() { _, err := e.SubmitPatternWithReceipt(ctx, "fixture", ref, "queue", key, resolve); done <- err }()
	await(t, resolveGate.entered)
	winner, err := e.SubmitPatternWithReceipt(context.Background(), "fixture", ref, "queue", key, resolve)
	if err != nil {
		t.Fatal(err)
	}
	await(t, native.entered)
	cancel()
	resolveGate.unblock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled adapter callback became replay success: %v", err)
	}
	if _, err := e.CancelPatternRequest(context.Background(), "fixture", key, ref, jobID(winner.Job)); err != nil {
		t.Fatal(err)
	}
	native.unblock()
	awaitJob(t, e, ref, key, "cancelled")
}

func TestResolverRealCorruptStoreErrorIsNotAvailability(t *testing.T) {
	r := recordFixture(t, "program")
	s, dir := privateStore(t, r)
	poison := []byte(`{"not":"a frozen snapshot"}`)
	if err := os.WriteFile(filepath.Join(dir, "patterns.json"), poison, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := New(s)(context.Background(), refFor(r))
	zeroResult(t, out)
	if kindOf(t, err) != control.PatternResolveIntegrity || !errors.Is(err, patternstore.ErrCorrupt) {
		t.Fatalf("real corrupt store downgraded: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "patterns.json"))
	if err != nil || string(after) != string(poison) {
		t.Fatal("resolver rewrote corrupt library")
	}
}
