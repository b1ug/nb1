package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/devicebackend"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/jsoninput"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/spf13/cobra"
)

type controlFlags struct {
	mode, endpoint, lockDir, policy string
	timeout                         time.Duration
}

func newControlCommand() *cobra.Command {
	options := &controlFlags{}
	command := &cobra.Command{Use: "control", Aliases: []string{"ctl"}, Short: "Control devices through the shared executor", Long: "Use the local daemon by default. Direct mode embeds the same executor and cooperates with the same device ownership lock. Colors are exact raw #RRGGBB values without implicit gamma conversion. Jobs belong to the daemon or the lifetime of one direct command. Use --wait to await terminal set/run/cancel results; a client timeout does not cancel a daemon job."}
	command.PersistentFlags().StringVar(&options.mode, "mode", "daemon", "daemon or direct; never silently falls back to USB")
	command.PersistentFlags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.PersistentFlags().StringVar(&options.lockDir, "lock-dir", os.Getenv("NB1_LOCK_DIR"), "shared ownership directory for direct mode (NB1_LOCK_DIR or per-user cache)")
	command.PersistentFlags().StringVar(&options.policy, "policy", "replace", "replace, queue or reject when a device already has work")
	command.PersistentFlags().DurationVar(&options.timeout, "timeout", 30*time.Second, "client request/direct execution deadline")
	command.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if options.mode != "daemon" && options.mode != "direct" {
			return fmt.Errorf("--mode must be daemon or direct")
		}
		if options.timeout <= 0 {
			return fmt.Errorf("--timeout must be positive")
		}
		if options.policy != "replace" && options.policy != "queue" && options.policy != "reject" {
			return fmt.Errorf("--policy must be replace, queue or reject")
		}
		return nil
	}
	command.AddCommand(&cobra.Command{Use: "devices", Short: "List daemon-selected devices or discover metadata in direct mode", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if options.mode == "direct" {
			infos := devicebackend.Discover()
			out := make([]control.DeviceState, 0, len(infos))
			for _, info := range infos {
				out = append(out, control.DeviceState{DeviceInfo: info, Connection: "discovered"})
			}
			return printControlJSON(cmd.OutOrStdout(), out)
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Devices(ctx)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	command.AddCommand(&cobra.Command{Use: "state <serial>", Short: "Read the latest timestamped raw RGB snapshot", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if options.mode == "direct" {
			return options.direct(cmd, args[0], true, func(ctx context.Context, engine *control.Engine) error {
				state, err := waitDirectDevice(ctx, engine, args[0])
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), state)
			})
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Device(ctx, args[0])
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	var led, fadeMS, holdMS int
	set := &cobra.Command{Use: "set <serial> <#RRGGBB>", Short: "Set an exact RGB color and retain it after successful completion", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		program, err := control.Normalize(control.Program{Steps: []control.Step{{LED: led, Color: args[1], FadeMS: fadeMS, HoldMS: holdMS}}, Repeat: 1, OnFinish: "hold"})
		if err != nil {
			return err
		}
		return options.submit(cmd, args[0], program)
	}}
	set.Flags().IntVar(&led, "led", 0, "0=both, 1=top, 2=bottom")
	set.Flags().IntVar(&fadeMS, "fade-ms", 0, "fade duration in milliseconds, in multiples of 10")
	set.Flags().IntVar(&holdMS, "hold-ms", 0, "additional time before the job completes, in milliseconds")
	command.AddCommand(set)
	command.AddCommand(&cobra.Command{Use: "run <serial> <program.json>", Short: "Submit a strict structured program", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		program, err := readControlProgram(args[1])
		if err != nil {
			return err
		}
		program, err = control.Normalize(program)
		if err != nil {
			return err
		}
		return options.submit(cmd, args[0], program)
	}})
	command.AddCommand(&cobra.Command{Use: "job <id>", Short: "Inspect a daemon job", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if options.mode != "daemon" {
			return fmt.Errorf("job history belongs to the daemon; use --mode daemon")
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Job(ctx, args[0])
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	command.AddCommand(&cobra.Command{Use: "cancel <id>", Short: "Request cancellation; terminal cancelled follows confirmed hardware cleanup", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if options.mode != "daemon" {
			return fmt.Errorf("cancel a persistent job with --mode daemon")
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Cancel(ctx, args[0])
		if err != nil {
			return err
		}
		if waitComplete {
			value, err = waitRemoteJob(ctx, client, value.ID)
			if err != nil {
				return err
			}
			if value.Status == "failed" {
				_ = printControlJSON(cmd.OutOrStdout(), value)
				return fmt.Errorf("job cleanup failed: %s", value.Error)
			}
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	return command
}

func (o *controlFlags) client(parent context.Context) (*daemonclient.Client, context.Context, func(), error) {
	client, err := daemonclient.New(o.endpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, o.timeout)
	return client, ctx, func() { cancel(); client.Close() }, nil
}

func (o *controlFlags) submit(cmd *cobra.Command, serial string, program control.Program) error {
	if o.mode == "direct" {
		return o.direct(cmd, serial, false, func(ctx context.Context, engine *control.Engine) error {
			if _, err := waitDirectDevice(ctx, engine, serial); err != nil {
				return err
			}
			job, err := engine.Submit(ctx, serial, program, o.policy)
			if err != nil {
				return err
			}
			for {
				job, err = engine.Job(job.ID)
				if err != nil {
					return err
				}
				switch job.Status {
				case "succeeded", "completed":
					return printControlJSON(cmd.OutOrStdout(), job)
				case "failed", "cancelled":
					_ = printControlJSON(cmd.OutOrStdout(), job)
					return fmt.Errorf("job %s: %s (%s)", job.ID, job.Status, job.Error)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		})
	}
	client, ctx, done, err := o.client(cmd.Context())
	if err != nil {
		return err
	}
	defer done()
	job, err := client.Submit(ctx, serial, program, o.policy)
	if err != nil {
		return err
	}
	if waitComplete {
		job, err = waitRemoteJob(ctx, client, job.ID)
		if err != nil {
			return err
		}
		if job.Status == "failed" || job.Status == "cancelled" {
			_ = printControlJSON(cmd.OutOrStdout(), job)
			return fmt.Errorf("job %s: %s (%s)", job.ID, job.Status, job.Error)
		}
	}
	return printControlJSON(cmd.OutOrStdout(), job)
}

func waitRemoteJob(ctx context.Context, client *daemonclient.Client, id string) (control.Job, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := client.Job(ctx, id)
		if err != nil {
			return job, err
		}
		switch job.Status {
		case "succeeded", "completed", "cancelled", "failed":
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (o *controlFlags) direct(cmd *cobra.Command, serial string, readOnly bool, run func(context.Context, *control.Engine) error) (result error) {
	lockDir := o.lockDir
	var err error
	if lockDir == "" {
		lockDir, err = ownership.DefaultDir()
		if err != nil {
			return err
		}
	}
	engine, err := control.New(devicebackend.New(lockDir), control.Options{Serials: []string{serial}, ShutdownMode: "hold", ReadOnly: readOnly})
	if err != nil {
		return err
	}
	signalCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, o.timeout)
	defer cancel()
	if err = engine.Start(ctx); err != nil {
		return err
	}
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := engine.Close(shutdown); err != nil {
			if result != nil {
				result = fmt.Errorf("%v; cleanup: %w", result, err)
			} else {
				result = fmt.Errorf("cleanup: %w", err)
			}
		}
	}()
	return run(ctx, engine)
}

func waitDirectDevice(ctx context.Context, engine *control.Engine, serial string) (control.DeviceState, error) {
	var state control.DeviceState
	for {
		var err error
		state, err = engine.Device(serial)
		if err != nil {
			return state, err
		}
		if state.Connection == "online" && state.Observed != nil {
			return state, nil
		}
		if state.Connection == "busy" {
			return state, fmt.Errorf("%w: %s", control.ErrBusy, state.LastError)
		}
		select {
		case <-ctx.Done():
			return state, fmt.Errorf("device %s is not ready (%s): %w", serial, state.LastError, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func readControlProgram(path string) (control.Program, error) {
	var program control.Program
	file, err := os.Open(path)
	if err != nil {
		return program, err
	}
	defer file.Close()
	reader := io.LimitReader(file, httpapi.MaxBodyBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return program, err
	}
	if len(data) > httpapi.MaxBodyBytes {
		return program, fmt.Errorf("program file exceeds 1 MiB")
	}
	if err := jsoninput.DecodeObject(data, &program); err != nil {
		return program, err
	}
	return program, nil
}

func printControlJSON(output io.Writer, value interface{}) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func init() { rootCmd.AddCommand(newControlCommand()) }
