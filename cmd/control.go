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
	requestID, expectedInstance     string
	timeout                         time.Duration
}

func newControlCommand() *cobra.Command {
	options := &controlFlags{}
	command := &cobra.Command{Use: "control", Aliases: []string{"ctl"}, Short: "Control devices through the shared executor", Long: "Use the local daemon by default. Direct mode embeds the same executor and cooperates with the same device ownership lock. Colors are exact raw #RRGGBB values without implicit gamma conversion. Jobs belong to the daemon or the lifetime of one direct command. Use --wait to await terminal set/run/effect/cancel results; a client timeout does not cancel a daemon job."}
	command.PersistentFlags().StringVar(&options.mode, "mode", "daemon", "daemon or direct; never silently falls back to USB")
	command.PersistentFlags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.PersistentFlags().StringVar(&options.lockDir, "lock-dir", os.Getenv("NB1_LOCK_DIR"), "shared ownership directory for direct mode (NB1_LOCK_DIR or persistent per-user config)")
	command.PersistentFlags().StringVar(&options.policy, "policy", "replace", "replace, queue or reject when a device already has work")
	command.PersistentFlags().DurationVar(&options.timeout, "timeout", 30*time.Second, "client request/direct execution deadline")
	command.PersistentFlags().StringVar(&options.requestID, "request-id", "", "optional prepared request ID for daemon set/run/effect; requires the matching instance")
	command.PersistentFlags().StringVar(&options.expectedInstance, "expected-daemon-instance-id", "", "bind a daemon submission or receipt lookup to this prepared instance")
	command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if options.mode != "daemon" && options.mode != "direct" {
			return fmt.Errorf("--mode must be daemon or direct")
		}
		if options.timeout <= 0 {
			return fmt.Errorf("--timeout must be positive")
		}
		if options.policy != "replace" && options.policy != "queue" && options.policy != "reject" {
			return fmt.Errorf("--policy must be replace, queue or reject")
		}
		if isPatternCommand(cmd) {
			if options.mode != "daemon" {
				return fmt.Errorf("Pattern library commands require --mode daemon; no direct fallback")
			}
			if cmd.Name() == "run" && cmd.Flags().Changed("policy") && options.policy != "reject" {
				return fmt.Errorf("patterns run supports only --policy reject")
			}
			if cmd.Name() != "run" && (cmd.Flags().Changed("policy") || cmd.Flags().Changed("wait")) {
				return fmt.Errorf("%s does not accept --policy or --wait", cmd.Name())
			}
		}
		if isLightCommand(cmd) && cmd.Name() == "run" && cmd.Flags().Changed("policy") && options.policy != "reject" {
			return fmt.Errorf("light run supports only --policy reject")
		}
		if isLightCommand(cmd) && cmd.Name() == "preview" && cmd.Flags().Changed("wait") {
			return fmt.Errorf("light preview is read-only and does not accept --wait")
		}
		if (cmd.Name() == "preview" || cmd.Name() == "capabilities" || cmd.Name() == "color") && cmd.Flags().Changed("policy") {
			return fmt.Errorf("%s is read-only and does not accept --policy", cmd.Name())
		}
		requestFlag := cmd.Flags().Changed("request-id")
		instanceFlag := cmd.Flags().Changed("expected-daemon-instance-id")
		if options.mode == "direct" && (requestFlag || instanceFlag || cmd.Name() == "prepare" || cmd.Name() == "request") {
			return fmt.Errorf("request receipts belong to the daemon; direct mode cannot prepare, query or submit a request binding")
		}
		switch cmd.Name() {
		case "set", "run", "effect":
			if requestFlag != instanceFlag || requestFlag && (options.requestID == "" || options.expectedInstance == "") {
				return fmt.Errorf("--request-id and --expected-daemon-instance-id must both be nonempty or both omitted")
			}
		case "request":
			if isLightCommand(cmd) {
				if !requestFlag || !instanceFlag || options.requestID == "" || options.expectedInstance == "" {
					return fmt.Errorf("light request requires nonempty --request-id and --expected-daemon-instance-id")
				}
				if cmd.Flags().Changed("policy") {
					return fmt.Errorf("light request is read-only and does not accept --policy")
				}
				break
			}
			if requestFlag || !instanceFlag || options.expectedInstance == "" {
				return fmt.Errorf("request takes a positional request ID and requires nonempty --expected-daemon-instance-id")
			}
		default:
			if requestFlag || instanceFlag {
				return fmt.Errorf("request binding flags apply only to set/run/effect or the request lookup command")
			}
		}
		return nil
	}
	command.AddCommand(newControlCapabilitiesCommand(options), newControlPreviewCommand(options), newControlColorCommand(options), newControlLightCommand(options), newControlPatternsCommand(options))
	command.AddCommand(&cobra.Command{Use: "prepare", Short: "Prepare an optional daemon request binding without submitting a job", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Prepare(ctx)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	command.AddCommand(&cobra.Command{Use: "request <serial> <request_id>", Short: "Look up a request receipt in its original daemon instance", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.Request(ctx, args[0], control.ReceiptRequest{RequestID: args[1], ExpectedDaemonInstanceID: options.expectedInstance})
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
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
	command.AddCommand(&cobra.Command{Use: "effect <serial> <effect.json>", Short: "Submit a strict steady or pulse effect through the same executor", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		effect, err := readControlEffect(args[1])
		if err != nil {
			return err
		}
		if options.mode == "direct" {
			// All direct shape/compile errors precede lock acquisition or USB.
			program, err := control.CompileEffect(effect)
			if err != nil {
				return err
			}
			return options.submit(cmd, args[0], program)
		}
		return options.submitRemote(cmd, args[0], nil, &effect)
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
	return o.submitRemote(cmd, serial, &program, nil)
}

func (o *controlFlags) submitRemote(cmd *cobra.Command, serial string, program *control.Program, effect *control.Effect) error {
	client, ctx, done, err := o.client(cmd.Context())
	if err != nil {
		return err
	}
	defer done()
	var job control.Job
	var receipt *control.ReceiptRequest
	if o.requestID != "" {
		receipt = &control.ReceiptRequest{RequestID: o.requestID, ExpectedDaemonInstanceID: o.expectedInstance}
		var result control.ReceiptResult
		if effect != nil {
			result, err = client.SubmitEffectWithReceipt(ctx, serial, *effect, o.policy, *receipt)
		} else {
			result, err = client.SubmitWithReceipt(ctx, serial, *program, o.policy, *receipt)
		}
		job = result.Job
	} else {
		if effect != nil {
			job, err = client.SubmitEffect(ctx, serial, *effect, o.policy)
		} else {
			job, err = client.Submit(ctx, serial, *program, o.policy)
		}
	}
	if err != nil {
		return err
	}
	if waitComplete {
		if receipt != nil {
			job, err = waitRemoteSnapshot(ctx, func() (control.Job, error) { return client.Request(ctx, serial, *receipt) })
		} else {
			job, err = waitRemoteJob(ctx, client, job.ID)
		}
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
	return waitRemoteSnapshot(ctx, func() (control.Job, error) { return client.Job(ctx, id) })
}

func waitRemoteSnapshot(ctx context.Context, snapshot func() (control.Job, error)) (control.Job, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := snapshot()
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
	if o.requestID != "" || o.expectedInstance != "" {
		return fmt.Errorf("request receipts require --mode daemon")
	}
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
	err := readControlObject(path, "program", &program)
	return program, err
}

func readControlEffect(path string) (control.Effect, error) {
	var effect control.Effect
	err := readControlObject(path, "effect", &effect)
	return effect, err
}

func readControlObject(path, kind string, out interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := io.LimitReader(file, httpapi.MaxBodyBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(data) > httpapi.MaxBodyBytes {
		return fmt.Errorf("%s file exceeds 1 MiB", kind)
	}
	return jsoninput.DecodeObject(data, out)
}

func printControlJSON(output io.Writer, value interface{}) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func init() { rootCmd.AddCommand(newControlCommand()) }
