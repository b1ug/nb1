package control

import "time"

const connectionEventBufferSize = 64

// ConnectionEvent is a transition in connection state or device path. Sequence
// is global to an Engine and increases even when a full buffer drops an event.
// Error is the state's last error at this transition, not a retry-by-retry log.
type ConnectionEvent struct {
	Sequence   uint64    `json:"sequence"`
	Time       time.Time `json:"time"`
	Serial     string    `json:"serial"`
	Connection string    `json:"connection"`
	Path       string    `json:"path"`
	Error      string    `json:"error"`
}

// Events returns one bounded, best-effort stream, intended for one consumer.
// Native workers never wait for its reader. It closes after all workers finish,
// including Close called before Start. Multiple readers share rather than
// broadcast events; use Device/Devices for a current authoritative snapshot.
func (e *Engine) Events() <-chan ConnectionEvent { return e.events }

// DroppedEvents counts transitions discarded because the event buffer was full.
// It does not count failures in a consumer's downstream log writer.
func (e *Engine) DroppedEvents() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.droppedEvents
}

// connectionLocked centralizes changes to Connection. The caller holds Engine.mu
// and has already updated Path/LastError so the event is one consistent snapshot.
func (w *worker) connectionLocked(connection string) {
	w.state.Connection = connection
	w.engine.emitConnectionLocked(w)
}

func (e *Engine) emitConnectionLocked(w *worker) {
	if e.eventsClosed || (w.eventReported && w.eventConnection == w.state.Connection && w.eventPath == w.state.Path) {
		return
	}
	w.eventReported = true
	w.eventConnection, w.eventPath = w.state.Connection, w.state.Path
	e.eventSequence++
	event := ConnectionEvent{Sequence: e.eventSequence, Time: time.Now().UTC(), Serial: w.serial, Connection: w.state.Connection, Path: w.state.Path, Error: w.state.LastError}
	select {
	case e.events <- event:
	default:
		e.droppedEvents++
	}
}

func (e *Engine) closeEventsLocked() {
	if !e.eventsClosed {
		e.eventsClosed = true
		close(e.events)
	}
}
