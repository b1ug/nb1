package control

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/b1ug/nb1/internal/transporttrace"
)

type record struct {
	origin                                     recordOrigin
	pattern                                    patternAdmissionIdentity
	kind                                       recordKind
	state                                      recordState
	program                                    *Program
	frames                                     *FrameProgram
	completedSteps, totalSteps                 int
	completedFrames, totalFrames               int
	completedTargetWrites, plannedTargetWrites int
	cancel                                     chan struct{}
	cancelled                                  bool
	receipt                                    *receiptEntry
}

type worker struct {
	engine *Engine
	serial string
	wake   chan struct{}
	state  DeviceState
	queue  []*record
	active *record
	// Only this worker goroutine touches its native backend.
	backend         Backend
	nextObservation time.Time // Worker-owned monotonic deadline, shared across steps.
	eventReported   bool
	eventConnection string
	eventPath       string
}

type Engine struct {
	mu            sync.Mutex
	driver        Driver
	options       Options
	workers       map[string]*worker
	jobs          map[string]*record
	history       []string
	started       bool
	closed        bool
	stop          chan struct{}
	done          chan struct{}
	shutdownErr   error
	wg            sync.WaitGroup
	events        chan ConnectionEvent
	eventsClosed  bool
	eventSequence uint64
	droppedEvents uint64
	receipts      *receiptState
}

func New(driver Driver, options Options) (*Engine, error) {
	if driver == nil || len(options.Serials) == 0 || len(options.Serials) > 64 {
		return nil, fmt.Errorf("%w: driver and 1–64 explicit device serials required", ErrInvalid)
	}
	if options.QueueSize == 0 {
		options.QueueSize = 8
	}
	if options.MaxHistory == 0 {
		options.MaxHistory = 128
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Second
	}
	if options.ShutdownMode == "" {
		options.ShutdownMode = "off"
	}
	if options.QueueSize < 1 || options.QueueSize > 1024 || options.MaxHistory < 1 || options.MaxHistory > 100000 || options.PollInterval < 10*time.Millisecond || (options.ShutdownMode != "off" && options.ShutdownMode != "hold") {
		return nil, fmt.Errorf("%w: invalid runtime limits or shutdown mode", ErrInvalid)
	}
	e := &Engine{driver: driver, options: options, workers: make(map[string]*worker), jobs: make(map[string]*record), stop: make(chan struct{}), done: make(chan struct{}), events: make(chan ConnectionEvent, connectionEventBufferSize)}
	for _, serial := range options.Serials {
		if !ValidDeviceSerial(serial) {
			return nil, fmt.Errorf("%w: invalid device serial", ErrInvalid)
		}
		if _, exists := e.workers[serial]; exists {
			return nil, fmt.Errorf("%w: duplicate device %s", ErrInvalid, serial)
		}
		e.workers[serial] = &worker{engine: e, serial: serial, wake: make(chan struct{}, 1), state: DeviceState{DeviceInfo: DeviceInfo{Serial: serial, Capabilities: []string{}}, Connection: "connecting"}}
	}
	var err error
	e.receipts, err = newReceiptState(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize daemon instance: %w", err)
	}
	return e, nil
}

func (e *Engine) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.started {
		return ErrClosed
	}
	e.started = true
	for _, w := range e.workers {
		e.emitConnectionLocked(w)
		e.wg.Add(1)
		go w.loop()
	}
	go func() {
		e.wg.Wait()
		e.mu.Lock()
		defer e.mu.Unlock()
		e.closeEventsLocked()
		close(e.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			e.beginClose()
		case <-e.done:
		}
	}()
	return nil
}

func (e *Engine) Done() <-chan struct{} { return e.done }

func (e *Engine) beginClose() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	close(e.stop)
	for _, w := range e.workers {
		w.connectionLocked("closing")
		if w.active != nil {
			e.cancelLocked(w.active)
		}
		for _, r := range w.queue {
			e.finishLocked(r, "cancelled", nil)
		}
		w.queue = nil
		w.state.QueueDepth = 0
	}
	if !e.started {
		e.closeEventsLocked()
		close(e.done)
	}
}

// A deadline stops waiting, not the native operation. The worker keeps its
// handle and ownership lease until native I/O and cleanup really finish.
func (e *Engine) Close(ctx context.Context) error {
	e.beginClose()
	select {
	case <-e.done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneProgram(p Program) Program { p.Steps = append([]Step(nil), p.Steps...); return p }
func cloneJob(j Job) Job {
	j.Program = cloneProgram(j.Program)
	if j.StartedAt != nil {
		t := *j.StartedAt
		j.StartedAt = &t
	}
	if j.FinishedAt != nil {
		t := *j.FinishedAt
		j.FinishedAt = &t
	}
	return j
}
func cloneState(s DeviceState) DeviceState {
	s.Capabilities = append([]string{}, s.Capabilities...)
	if s.Desired != nil {
		v := *s.Desired
		s.Desired = &v
	}
	if s.Observed != nil {
		v := *s.Observed
		s.Observed = &v
	}
	return s
}

func (e *Engine) Devices() []DeviceState {
	e.mu.Lock()
	defer e.mu.Unlock()
	states := make([]DeviceState, 0, len(e.workers))
	for _, w := range e.workers {
		states = append(states, cloneState(w.state))
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Serial < states[j].Serial })
	return states
}
func (e *Engine) Device(serial string) (DeviceState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.workers[serial]
	if !ok {
		return DeviceState{}, ErrNotFound
	}
	return cloneState(w.state), nil
}
func (e *Engine) Job(id string) (Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.jobs[id]
	if !ok || r.origin != originLegacy || r.kind != recordSequential {
		return Job{}, ErrNotFound
	}
	return r.legacySnapshot(), nil
}

func Normalize(p Program) (Program, error) {
	if len(p.Steps) == 0 || len(p.Steps) > MaxProgramSteps {
		return Program{}, fmt.Errorf("%w: program requires 1–1024 steps", ErrInvalid)
	}
	p = cloneProgram(p)
	if p.Repeat == 0 {
		p.Repeat = 1
	}
	if p.Repeat < 1 || p.Repeat > MaxProgramRepeat {
		return Program{}, fmt.Errorf("%w: repeat must be 1–10000", ErrInvalid)
	}
	if p.OnFinish == "" {
		p.OnFinish = "off"
	}
	if p.OnFinish != "off" && p.OnFinish != "hold" {
		return Program{}, fmt.Errorf("%w: on_finish must be off or hold", ErrInvalid)
	}
	var total int64
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.LED < 0 || s.LED > 2 || s.FadeMS < 0 || s.FadeMS > MaxFadeMS || s.FadeMS%FadeQuantumMS != 0 || s.HoldMS < 0 || s.HoldMS > MaxHoldMS {
			return Program{}, fmt.Errorf("%w: step %d requires led 0–2, fade_ms 0–600000 in 10ms units and hold_ms 0–3600000", ErrInvalid, i)
		}
		if len(s.Color) != 7 || s.Color[0] != '#' {
			return Program{}, fmt.Errorf("%w: step %d color must be #RRGGBB", ErrInvalid, i)
		}
		if _, err := hex.DecodeString(s.Color[1:]); err != nil {
			return Program{}, fmt.Errorf("%w: step %d color must be #RRGGBB", ErrInvalid, i)
		}
		s.Color = strings.ToLower(s.Color)
		total += int64(s.FadeMS) + int64(s.HoldMS) + StepOverheadMS // firmware update tick
	}
	if total*int64(p.Repeat) > MaxNominalDurationMS {
		return Program{}, fmt.Errorf("%w: program duration exceeds 24 hours", ErrInvalid)
	}
	return p, nil
}

func (e *Engine) Submit(ctx context.Context, serial string, program Program, policy string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	p, policy, err := normalizeSubmission(program, policy)
	if err != nil {
		return Job{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	w, err := e.canAdmitLocked(serial, policy)
	if err != nil {
		return Job{}, err
	}
	r, err := e.newRecordLocked(serial, p)
	if err != nil {
		return Job{}, err
	}
	e.publishLocked(w, r, policy)
	return r.legacySnapshot(), nil
}

func normalizeSubmission(program Program, policy string) (Program, string, error) {
	p, err := Normalize(program)
	if err != nil {
		return Program{}, "", err
	}
	if policy == "" {
		policy = "replace"
	}
	if policy != "replace" && policy != "queue" && policy != "reject" {
		return Program{}, "", fmt.Errorf("%w: policy must be replace, queue or reject", ErrInvalid)
	}
	return p, policy, nil
}

func (e *Engine) canAdmitLocked(serial, policy string) (*worker, error) {
	if !e.started || e.closed {
		return nil, ErrClosed
	}
	if e.options.ReadOnly {
		return nil, fmt.Errorf("%w: runtime is read-only", ErrInvalid)
	}
	w, ok := e.workers[serial]
	if !ok {
		return nil, ErrNotFound
	}
	if w.state.Connection != "online" {
		if w.state.Connection == "busy" {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("%w: %s", ErrOffline, w.state.LastError)
	}
	if policy == "reject" && (w.active != nil || len(w.queue) > 0) {
		return nil, ErrBusy
	}
	if policy != "replace" && len(w.queue) >= e.options.QueueSize {
		return nil, ErrQueueFull
	}
	return w, nil
}

// Nothing after newRecordLocked may fail admission: replace must not cancel
// existing work when capacity, state checks or random ID generation fail.
func (e *Engine) publishLocked(w *worker, r *record, policy string) {
	if policy == "replace" {
		if w.active != nil {
			e.cancelLocked(w.active)
		}
		for _, r := range w.queue {
			e.finishLocked(r, "cancelled", nil)
		}
		w.queue = nil
	}
	e.jobs[r.state.ID] = r
	w.queue = append(w.queue, r)
	w.state.QueueDepth = len(w.queue)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) cancelLocked(r *record) {
	if r.state.FinishedAt != nil || r.cancelled {
		return
	}
	r.cancelled = true
	close(r.cancel)
	r.state.Status = "cancelling"
}
func (e *Engine) finishLocked(r *record, status string, err error) {
	if r.state.FinishedAt != nil {
		return
	}
	now := time.Now().UTC()
	r.state.Status = status
	r.state.FinishedAt = &now
	if err != nil {
		r.state.Error = err.Error()
	}
	if r.receipt != nil {
		r.receipt.expiresAtMS = e.receipts.nowMS() + e.receipts.retentionMS
		if r.receipt.expiresAtMS < r.receipt.acceptUntilMS {
			r.receipt.expiresAtMS = r.receipt.acceptUntilMS
		}
	}
	e.history = append(e.history, r.state.ID)
	for len(e.history) > e.options.MaxHistory {
		id := e.history[0]
		e.history = e.history[1:]
		delete(e.jobs, id)
	}
}
func (e *Engine) Cancel(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.jobs[id]
	if !ok || r.origin != originLegacy || r.kind != recordSequential {
		return Job{}, ErrNotFound
	}
	e.cancelRecordLocked(r)
	return r.legacySnapshot(), nil
}

func (e *Engine) cancelRecordLocked(r *record) {
	if r.state.FinishedAt != nil {
		return
	}
	w := e.workers[r.state.Device]
	if w.active == r {
		e.cancelLocked(r)
	} else {
		for i, pending := range w.queue {
			if pending == r {
				w.queue = append(w.queue[:i], w.queue[i+1:]...)
				break
			}
		}
		w.state.QueueDepth = len(w.queue)
		e.finishLocked(r, "cancelled", nil)
	}
}

func (w *worker) offline(err error) {
	e := w.engine
	e.mu.Lock()
	connection := "offline"
	if errors.Is(err, ErrBusy) {
		connection = "busy"
	}
	w.state.LastError = err.Error()
	if !e.closed {
		w.connectionLocked(connection)
	}
	for _, r := range w.queue {
		e.finishLocked(r, "failed", fmt.Errorf("%w: %v", ErrOffline, err))
	}
	w.queue = nil
	w.state.QueueDepth = 0
	e.mu.Unlock()
	// Publish the known failure before native Close, which may block. New
	// requests must not queue behind a handle already known to have failed.
	// The worker still owns this handle and its lease until Close returns;
	// only then may its loop reopen the serial or complete shutdown.
	if w.backend != nil {
		w.backend.Close()
		w.backend = nil
	}
}
func (w *worker) connect() bool {
	b, err := w.engine.driver.Open(w.serial)
	if err != nil {
		w.offline(err)
		return false
	}
	if b == nil {
		w.offline(fmt.Errorf("driver returned nil backend"))
		return false
	}
	w.backend = b
	obs, err := w.scopedRead(nil, transporttrace.ConnectRead, transporttrace.NoPosition())
	if err != nil {
		w.offline(err)
		return false
	}
	w.nextObservation = time.Now().Add(w.engine.options.PollInterval)
	e := w.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	w.state.DeviceInfo = b.Info()
	w.state.Serial = w.serial
	w.state.Observed = &obs
	w.state.LastError = ""
	if !e.closed {
		w.connectionLocked("online")
	} else {
		// A connect already in flight may reveal a new path during shutdown.
		e.emitConnectionLocked(w)
	}
	return true
}
func (w *worker) loop() {
	defer w.engine.wg.Done()
	e := w.engine
	for {
		e.mu.Lock()
		closing := e.closed
		e.mu.Unlock()
		if closing {
			var err error
			if w.backend != nil {
				// Successful jobs have already quiesced and verified output;
				// interrupted jobs have already cleaned up with off. Direct hold
				// release therefore only closes the handle, avoiding duplicate I/O.
				if !e.options.ReadOnly && e.options.ShutdownMode != "hold" {
					err = w.scopedStop(nil, transporttrace.Shutdown, e.options.ShutdownMode)
				}
				w.backend.Close()
				w.backend = nil
			}
			e.mu.Lock()
			if err != nil {
				w.state.LastError = err.Error()
				if e.shutdownErr == nil {
					e.shutdownErr = err
				}
			}
			w.connectionLocked("closed")
			e.mu.Unlock()
			return
		}
		if w.backend == nil && !w.connect() {
			select {
			case <-time.After(e.options.PollInterval):
			case <-e.stop:
			}
			continue
		}
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			continue
		}
		if len(w.queue) > 0 {
			r := w.queue[0]
			w.queue = w.queue[1:]
			w.state.QueueDepth = len(w.queue)
			w.active = r
			w.state.ActiveJob = r.state.ID
			now := time.Now().UTC()
			r.state.StartedAt = &now
			r.state.Status = "running"
			e.mu.Unlock()
			w.execute(r)
			continue
		}
		e.mu.Unlock()
		select {
		case <-w.wake:
		case <-e.stop:
		case <-time.After(e.options.PollInterval):
			obs, err := w.scopedRead(nil, transporttrace.IdleObserve, transporttrace.NoPosition())
			if err != nil {
				w.offline(err)
			} else {
				w.nextObservation = time.Now().Add(e.options.PollInterval)
				e.mu.Lock()
				w.state.Observed = &obs
				e.mu.Unlock()
			}
		}
	}
}

func (w *worker) execute(r *record) {
	if r.kind == recordFrames {
		w.executeFrames(r)
		return
	}
	e := w.engine
	// Device discovery and monitoring do not change output. First quiesce any
	// autonomous activity only when an actual control job takes ownership.
	runErr := w.scopedStop(r, transporttrace.PreflightHold, "hold")
	cancelled := false
	for repeat := 0; repeat < r.program.Repeat && runErr == nil && !cancelled; repeat++ {
		for index, step := range r.program.Steps {
			select {
			case <-r.cancel:
				cancelled = true
			default:
			}
			if cancelled {
				break
			}
			position := transporttrace.NoPosition()
			position.Repeat, position.Step = int32(repeat), int32(index)
			if runErr = w.scopedSet(r, transporttrace.ProgramStep, position, step); runErr != nil {
				break
			}
			e.mu.Lock()
			desired := step
			w.state.Desired = &desired
			e.mu.Unlock()
			// The mk2 applies even zero-time fades at its next 10ms LED update.
			// Allow that update before freezing a completed output.
			wait := time.Duration(step.FadeMS+step.HoldMS)*time.Millisecond + time.Duration(StepOverheadMS)*time.Millisecond
			if wait > 0 {
				cancelled, runErr = w.wait(r, wait, position)
			}
			if cancelled || runErr != nil {
				break
			}
			e.mu.Lock()
			r.completedSteps++
			e.mu.Unlock()
		}
	}
	w.finishExecution(r, runErr, cancelled)
}

// Both record kinds share the same cleanup, cancellation and lifetime rules.
func (w *worker) finishExecution(r *record, runErr error, cancelled bool) {
	e := w.engine
	// A cancellation arriving while native Set was in flight must still clean
	// up with off, even if the program's normal finish policy is hold.
	e.mu.Lock()
	cancelled = cancelled || r.cancelled
	if runErr != nil {
		w.state.LastError = runErr.Error()
		w.connectionLocked("error")
	}
	e.mu.Unlock()
	for {
		mode := r.finishMode()
		if cancelled || runErr != nil {
			mode = "off"
		}
		cleanupErr := w.scopedStop(r, transporttrace.Cleanup, mode)
		var obs Observation
		if cleanupErr == nil {
			obs, cleanupErr = w.scopedRead(r, transporttrace.CleanupVerify, transporttrace.NoPosition())
			if cleanupErr == nil {
				w.nextObservation = time.Now().Add(e.options.PollInterval)
			}
		}
		e.mu.Lock()
		// Cancellation may arrive during finish/hold cleanup. Serialize the
		// final decision with Cancel, and perform off before acknowledging it.
		if r.cancelled && !cancelled && cleanupErr == nil {
			cancelled = true
			e.mu.Unlock()
			continue
		}
		if cleanupErr != nil {
			if e.closed && e.shutdownErr == nil {
				e.shutdownErr = cleanupErr
			}
			e.finishLocked(r, "failed", fmt.Errorf("hardware cleanup/readback failed: %v (execution: %v)", cleanupErr, runErr))
		} else if runErr != nil {
			e.finishLocked(r, "failed", runErr)
		} else if cancelled {
			e.finishLocked(r, "cancelled", nil)
		} else {
			e.finishLocked(r, "succeeded", nil)
		}
		if cleanupErr == nil {
			w.state.Observed = &obs
			if mode == "off" {
				w.state.Desired = &Step{Color: "#000000"}
			}
		}
		w.active = nil
		w.state.ActiveJob = ""
		e.mu.Unlock()
		if cleanupErr != nil {
			w.offline(cleanupErr)
		} else if runErr != nil {
			w.offline(runErr)
		}
		return
	}
}

// Keep one observation deadline across short steps as well as long holds.
// Reads stay on this worker and may extend wall time beyond the nominal budget.
func (w *worker) wait(r *record, duration time.Duration, position ...transporttrace.Position) (bool, error) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		// Never issue a new observation when cancellation/shutdown is already
		// known. An in-flight native Read still has to return before cleanup.
		select {
		case <-r.cancel:
			return true, nil
		case <-w.engine.stop:
			return true, nil
		default:
		}
		if !time.Now().Before(w.nextObservation) {
			pos := transporttrace.NoPosition()
			if len(position) > 0 {
				pos = position[0]
			}
			obs, err := w.scopedRead(r, transporttrace.ProgramObserve, pos)
			if err != nil {
				return false, err
			}
			// Schedule from completion, not an old missed tick: slow native I/O
			// must not cause a burst of catch-up reads.
			w.nextObservation = time.Now().Add(w.engine.options.PollInterval)
			w.engine.mu.Lock()
			w.state.Observed = &obs
			w.engine.mu.Unlock()
			continue
		}
		poll := time.NewTimer(time.Until(w.nextObservation))
		select {
		case <-timer.C:
			poll.Stop()
			return false, nil
		case <-r.cancel:
			poll.Stop()
			return true, nil
		case <-w.engine.stop:
			poll.Stop()
			return true, nil
		case <-poll.C:
		}
	}
}
