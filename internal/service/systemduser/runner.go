package systemduser

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

type nativeRunner struct{ binary string }

type boundedOutput struct {
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	remaining := maxOutput - len(b.data)
	if len(data) > remaining {
		b.data = append(b.data, data[:remaining]...)
		b.truncated = true
	} else {
		b.data = append(b.data, data...)
	}
	return len(data), nil
}

func (r nativeRunner) Run(ctx context.Context, args ...string) (Result, error) {
	command := exec.CommandContext(ctx, r.binary, args...)
	var output boundedOutput
	command.Stdout = &output
	// No shell, pager, authentication prompt or raw diagnostic persistence.
	// Normal session bus variables are inherited, never copied into reports.
	command.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=cat")
	err := command.Run()
	result := Result{Output: string(output.data), Truncated: output.truncated}
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
