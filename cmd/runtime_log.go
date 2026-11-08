package cmd

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/b1ug/nb1/internal/control"
)

// One mutex covers complete JSON records, including the startup announcement.
// A blocked writer can block this logging goroutine, never a device worker.
type runtimeJSONWriter struct {
	mu     sync.Mutex
	output io.Writer
}

func (w *runtimeJSONWriter) encode(value interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return json.NewEncoder(w.output).Encode(value)
}

type runtimeLogging struct {
	startupErrors chan error
	done          chan struct{}
}

type runtimeConnectionRecord struct {
	Event string `json:"event"`
	control.ConnectionEvent
	DroppedEvents uint64 `json:"dropped_events"`
}

// startRuntimeLogging writes startup first, then consumes the bounded connection
// stream. Startup write errors retain the daemon's existing failure behavior;
// later log errors only end best-effort connection logging. Callers must not
// wait for done as part of hardware shutdown: arbitrary io.Writers cannot be
// interrupted and may still be blocked after native cleanup has completed.
func startRuntimeLogging(engine *control.Engine, output io.Writer, announcement interface{}) *runtimeLogging {
	logging := &runtimeLogging{startupErrors: make(chan error, 1), done: make(chan struct{})}
	writer := &runtimeJSONWriter{output: output}
	go func() {
		defer close(logging.done)
		if err := writer.encode(announcement); err != nil {
			logging.startupErrors <- err
			return
		}
		for event := range engine.Events() {
			if err := writer.encode(runtimeConnectionRecord{Event: "device_connection", ConnectionEvent: event, DroppedEvents: engine.DroppedEvents()}); err != nil {
				return
			}
		}
		if dropped := engine.DroppedEvents(); dropped > 0 {
			_ = writer.encode(map[string]interface{}{"event": "device_connection_events_dropped", "dropped_events": dropped})
		}
	}()
	return logging
}
