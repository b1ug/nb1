package cmd

import (
	"encoding/json"
	"io"
	"runtime/debug"

	"github.com/b1ug/nb1/internal/transporttrace"
)

func transportTraceBuildStamp(r *transporttrace.Recorder) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if modified == "true" || modified == "false" {
		r.SetSource(revision, modified == "true")
	}
}

// All formatting/output belongs to the logging goroutine, not a device worker.
// The cap applies only to new trace records, not existing connection logs.
type transportLog struct {
	writer      *runtimeJSONWriter
	recorder    *transporttrace.Recorder
	used, limit int
}

func (l *transportLog) write(value interface{}, final bool) bool {
	line, err := json.Marshal(value)
	if err != nil || len(line)+1 > transporttrace.MaxLineBytes {
		l.recorder.NoteSuppressed()
		return true
	}
	line = append(line, '\n')
	limit := l.limit
	if !final {
		limit -= transporttrace.SummaryReserve
	}
	if l.used+len(line) > limit {
		l.recorder.NoteSuppressed()
		return true
	}
	l.writer.mu.Lock()
	n, err := l.writer.output.Write(line)
	l.writer.mu.Unlock()
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		l.recorder.NoteWriterError()
		return false
	}
	l.used += len(line)
	return true
}
