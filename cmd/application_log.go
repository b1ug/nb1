package cmd

import (
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"bitbucket.org/neiku/hlog"
	charmlog "charm.land/log/v2"
	"github.com/b1ug/nb1/util"
	"github.com/charmbracelet/colorprofile"
	"github.com/muesli/termenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newApplicationLogger keeps the established JSON file core and legacy logger
// setters. Only the application's human stderr destination uses Charm Log.
func newApplicationLogger(output io.Writer, file string, debug bool) *hlog.Logger {
	profile := util.OutputColorProfile(output)
	pid := zap.Int("pid", os.Getpid())
	options := []hlog.Option{
		hlog.WithNormalLogOutput(io.Discard),
		hlog.WithErrorLogOutput(io.Discard),
		hlog.WithOutputFile(file),
		hlog.WithOutputFileLogFormat(hlog.LogFormatJSON),
		hlog.WithOutputFileMaxSizeInMB(10),
		hlog.WithOutputFileRetainNum(0),
		hlog.WithZapOptions(
			// Operational PID belongs in the file schema. Add it before the
			// human core so the normal console remains concise.
			zap.Fields(pid),
			zap.WrapCore(func(fileCore zapcore.Core) zapcore.Core {
				console := newCharmConsoleCore(fileCore, output, profile, debug)
				if debug {
					console = console.With([]zapcore.Field{pid})
				}
				return zapcore.NewTee(fileCore, console)
			}),
		),
	}
	if debug {
		options = append(options, hlog.WithDevelopmentMode(), hlog.WithLevel(zapcore.DebugLevel))
	}
	return hlog.NewLogger(options...)
}

// charmOutput intentionally exposes Write only, not Fd or Read. The renderer
// receives an explicit profile selected from the actual destination, without
// needing terminal input or background detection.
type charmOutput struct {
	output io.Writer
	err    error
}

func (w *charmOutput) Write(data []byte) (int, error) {
	n, err := w.output.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	w.err = err
	// Charm does not return writer errors and may retain unwritten buffer bytes.
	// Consume its buffer once; Core.Write returns the actual error to Zap.
	return len(data), nil
}

type charmSink struct {
	mu     sync.Mutex
	logger *charmlog.Logger
	writer *charmOutput
	time   time.Time
}

type charmConsoleCore struct {
	enabled zapcore.LevelEnabler
	sink    *charmSink
	fields  []zapcore.Field
	debug   bool
}

func newCharmConsoleCore(enabled zapcore.LevelEnabler, output io.Writer, profile termenv.Profile, debug bool) zapcore.Core {
	sink := &charmSink{writer: &charmOutput{output: output}}
	sink.logger = charmlog.NewWithOptions(sink.writer, charmlog.Options{
		Level:           charmlog.DebugLevel,
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
		TimeFunction:    func(time.Time) time.Time { return sink.time },
	})
	sink.logger.SetColorProfile(charmColorProfile(profile))
	return &charmConsoleCore{enabled: enabled, sink: sink, debug: debug}
}

func (c *charmConsoleCore) Enabled(level zapcore.Level) bool { return c.enabled.Enabled(level) }

func (c *charmConsoleCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	clone.fields = make([]zapcore.Field, 0, len(c.fields)+len(fields))
	clone.fields = append(clone.fields, c.fields...)
	clone.fields = append(clone.fields, fields...)
	return &clone
}

func (c *charmConsoleCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

// A distinct key type preserves business fields named time, level, caller,
// prefix or msg instead of treating them as Charm's reserved header keys.
type charmFieldKey string

func (key charmFieldKey) String() string { return string(key) }

func (c *charmConsoleCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range c.fields {
		field.AddTo(encoder)
	}
	for _, field := range fields {
		field.AddTo(encoder)
	}
	keys := make([]string, 0, len(encoder.Fields))
	for key := range encoder.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]any, 0, 2*len(keys)+4)
	if c.debug && entry.Caller.Defined {
		values = append(values, charmlog.CallerKey, entry.Caller.TrimmedPath())
	}
	if c.debug && entry.Stack != "" {
		values = append(values, charmFieldKey("stacktrace"), entry.Stack)
	}
	for _, key := range keys {
		values = append(values, charmFieldKey(key), encoder.Fields[key])
	}
	sink := c.sink
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.time = entry.Time
	sink.writer.err = nil
	sink.logger.SetPrefix(entry.LoggerName)
	// Log does not exit, unlike Charm's Fatal/Fatalf methods. Zap retains the
	// existing command lifecycle and level filtering around this core.
	sink.logger.Log(charmLevel(entry.Level), entry.Message, values...)
	return sink.writer.err
}

func (c *charmConsoleCore) Sync() error { return nil } // Charm writes every record immediately.

func charmLevel(level zapcore.Level) charmlog.Level {
	switch level {
	case zapcore.DebugLevel:
		return charmlog.DebugLevel
	case zapcore.InfoLevel:
		return charmlog.InfoLevel
	case zapcore.WarnLevel:
		return charmlog.WarnLevel
	case zapcore.FatalLevel:
		return charmlog.FatalLevel
	default:
		return charmlog.ErrorLevel
	}
}

// NoTTY removes all ANSI, including bold/faint attributes. colorprofile.ASCII
// only removes colors, which would violate nb1's established plain-output path.
func charmColorProfile(profile termenv.Profile) colorprofile.Profile {
	switch profile {
	case termenv.TrueColor:
		return colorprofile.TrueColor
	case termenv.ANSI256:
		return colorprofile.ANSI256
	case termenv.ANSI:
		return colorprofile.ANSI
	default:
		return colorprofile.NoTTY
	}
}
