package service

import (
	"context"
	"errors"
	"os/exec"
)

// The CLI cannot override the launchctl executable or invoke a shell. The
// common adapter remains buildable on other platforms, where the production
// manager constructor rejects service management before any invocation.
type launchctlRunner struct{}

type boundedOutput struct {
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	remaining := (64 << 10) - len(b.data)
	if len(data) > remaining {
		b.data = append(b.data, data[:remaining]...)
		b.truncated = true
	} else {
		b.data = append(b.data, data...)
	}
	return len(data), nil // discard overflow without blocking the child
}

func (launchctlRunner) Run(ctx context.Context, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, "/bin/launchctl", args...)
	var output boundedOutput
	command.Stdout = &output
	// Stderr is deliberately discarded: do not collect arbitrary diagnostic
	// environment data or copy launchctl's output into persisted receipts.
	err := command.Run()
	result := CommandResult{Output: string(output.data), ExitCode: 0, Truncated: output.truncated}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}
