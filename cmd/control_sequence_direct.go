package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/libraryv1"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/spf13/cobra"
)

// A direct Job belongs to one foreground invocation. It is not a daemon record
// and cannot be queried or deduplicated by another process after this one exits.
type sequenceDirectResult struct {
	Mode         string               `json:"mode"`
	Job          *control.SequenceJob `json:"job,omitempty"`
	EngineClosed bool                 `json:"engine_closed"`
}

func compileSequenceDirect(input daemonclient.SequenceInput, libraryDir string) (sequence.Sequence, error) {
	store := libraryv1.New(libraryDir)
	if input.Action != nil {
		return actionv1.Compile(input.Action, store.ResolveColor)
	}
	if input.PatternID != nil {
		pattern, err := store.GetPattern(*input.PatternID)
		if err != nil {
			return sequence.Sequence{}, err
		}
		return sequence.Normalize(pattern.Sequence)
	}
	return sequence.DecodeAuthoring(input.Sequence, func(raw json.RawMessage) (string, error) {
		return actionv1.ResolveColor(raw, store.ResolveColor)
	})
}

func (options *sequenceControlFlags) runSequenceDirect(cmd *cobra.Command, explicit string, input daemonclient.SequenceInput) error {
	serial := explicit
	var err error
	if serial == "" {
		serial, err = preferredSerial(cmd, options.settings)
		if err != nil {
			return err
		}
	}
	if serial == "" {
		return fmt.Errorf("direct run requires a serial argument, --device, NB1_DEVICE or configuration device; it does not discover a default device")
	}
	libraryDir := options.settings.LibraryDir
	if value := os.Getenv("NB1_LIBRARY_DIR"); value != "" {
		libraryDir = value
	}
	if !filepath.IsAbs(libraryDir) {
		return fmt.Errorf("library directory must be an absolute path")
	}
	// Freeze author input, named colors and local Pattern before creating any
	// native driver or Engine. Compilation uses the same core as the HTTP API.
	compiled, err := compileSequenceDirect(input, libraryDir)
	if err != nil {
		return err
	}
	lockDir := options.lockDir
	if lockDir == "" {
		lockDir, err = ownership.DefaultDir()
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(lockDir) {
		return fmt.Errorf("ownership directory must be an absolute path")
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	id, err := control.NewSequenceJobID()
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(compiled)
	if err != nil {
		return err
	}
	engine, err := control.New(options.driver(lockDir), control.Options{
		Serials: []string{serial}, ShutdownMode: "hold", PollInterval: time.Hour,
	})
	if err != nil {
		return err
	}
	signalCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx := signalCtx
	if cmd.Flags().Changed("timeout") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(signalCtx, options.timeout)
		defer cancel()
	}
	if err := engine.Start(ctx); err != nil {
		return err
	}
	result := sequenceDirectResult{Mode: "direct"}
	_, runErr := waitSequenceDirectDevice(ctx, engine, serial)
	if runErr == nil {
		job, _, submitErr := engine.SubmitSequence(ctx, serial, id, sha256.Sum256(canonical), func() (sequence.Sequence, error) { return compiled, nil })
		runErr = submitErr
		if submitErr == nil {
			result.Job = &job
			job, runErr = waitSequenceDirectJob(ctx, engine, id)
			result.Job = &job
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Direct run interrupted; waiting for native I/O and device cleanup. This process's Job is not retained by the daemon.")
	}
	// Engine.Close requests cancellation but never closes a handle or releases
	// its lease ahead of native I/O. The timeout only bounds the caller's wait.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), options.shutdownTimeout)
	closeErr := engine.Close(cleanupCtx)
	cancel()
	select {
	case <-engine.Done():
		result.EngineClosed = true
	default:
	}
	if result.Job != nil {
		if final, err := engine.SequenceJob(id); err == nil {
			result.Job = &final
		}
	}
	if closeErr != nil {
		if result.EngineClosed {
			runErr = fmt.Errorf("direct engine closed with a cleanup error; device state is unknown: %w", closeErr)
		} else {
			runErr = fmt.Errorf("direct device cleanup unconfirmed; device state is unknown and native I/O may still be running: %w", closeErr)
		}
	}
	if err := printControlJSON(cmd.OutOrStdout(), result); err != nil {
		if runErr != nil {
			return fmt.Errorf("%w; write direct result: %v", runErr, err)
		}
		return err
	}
	if runErr != nil {
		return runErr
	}
	if result.Job != nil && result.Job.Status != "succeeded" {
		return fmt.Errorf("direct job %s %s: %s", result.Job.ID, result.Job.Status, result.Job.Error)
	}
	return nil
}

func waitSequenceDirectDevice(ctx context.Context, engine *control.Engine, serial string) (control.DeviceState, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := engine.Device(serial)
		if err != nil {
			return state, err
		}
		switch state.Connection {
		case "online":
			return state, nil
		case "busy":
			return state, fmt.Errorf("%w: %s; use the owning daemon or stop it explicitly", control.ErrBusy, state.LastError)
		case "offline":
			return state, fmt.Errorf("%w: %s", control.ErrOffline, state.LastError)
		}
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitSequenceDirectJob(ctx context.Context, engine *control.Engine, id string) (control.SequenceJob, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := engine.SequenceJob(id)
		if err != nil {
			return job, err
		}
		switch job.Status {
		case "succeeded", "cancelled", "failed":
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}
