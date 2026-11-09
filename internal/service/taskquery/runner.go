package taskquery

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

type nativeRunner struct {
	binary      string
	directory   string
	environment []string
	checkTrust  func() error
}

type cappedWriter struct {
	data      []byte
	limit     int
	truncated bool
	cancel    context.CancelFunc
	keep      bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit
	if w.keep {
		remaining -= len(w.data)
	}
	if len(p) > remaining {
		w.truncated = true
		if w.keep && remaining > 0 {
			w.data = append(w.data, p[:remaining]...)
		}
		w.limit = 0
		w.cancel()
	} else if w.keep {
		w.data = append(w.data, p...)
	} else {
		w.limit -= len(p)
	}
	return len(p), nil
}

func (r nativeRunner) run(ctx context.Context, input []byte) (bridgeResult, error) {
	if err := ctx.Err(); err != nil {
		return bridgeResult{}, err
	}
	if r.checkTrust == nil || r.checkTrust() != nil {
		return bridgeResult{}, ErrTrust
	}
	if err := ctx.Err(); err != nil {
		return bridgeResult{}, err
	}
	return execute(ctx, r.binary, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-InputFormat", "Text", "-OutputFormat", "Text", "-Command", queryScript}, r.directory, r.environment, input)
}

// execute is internal so the process budgets can be tested without PowerShell.
// The production caller supplies only the fixed script and verified system path.
func execute(ctx context.Context, binary string, args []string, directory string, environment []string, input []byte) (bridgeResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env, command.Stdin = directory, environment, bytes.NewReader(input)
	output := &cappedWriter{limit: maxOutput, keep: true, cancel: cancel}
	// Raw PowerShell/COM exception formatting never leaves this process.
	diagnostic := &cappedWriter{limit: 4096, cancel: cancel}
	command.Stdout, command.Stderr = output, diagnostic
	err := command.Run()
	result := bridgeResult{output: output.data, truncated: output.truncated || diagnostic.truncated}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.exitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}
