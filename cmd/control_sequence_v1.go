package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/devicebackend"
	"github.com/b1ug/nb1/internal/remoteaccess"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/spf13/cobra"
)

type sequenceControlFlags struct {
	endpoint        string
	tokenFile       string
	timeout         time.Duration
	settings        appconfig.Config
	direct          bool
	lockDir         string
	shutdownTimeout time.Duration
	driver          func(string) control.Driver
}

func newSequenceControlCommand() *cobra.Command {
	return newSequenceControlCommandWithDriver(devicebackend.New)
}

func newSequenceControlCommandWithDriver(driver func(string) control.Driver) *cobra.Command {
	options := &sequenceControlFlags{driver: driver}
	command := &cobra.Command{Use: "control", Short: "Preview and run light actions and sequences",
		Long: "Use a running local daemon over HTTP, or explicitly choose control --direct run for one foreground Action, Sequence or local Pattern. Direct execution always waits and keeps its Job only in this process; it does not contact a daemon. Other control commands use HTTP. No command starts a daemon or silently switches to USB."}
	command.PersistentFlags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.PersistentFlags().StringVar(&options.tokenFile, "token-file", "", "private Bearer token file for the loopback API")
	command.PersistentFlags().DurationVar(&options.timeout, "timeout", 30*time.Second, "daemon request/wait limit; direct has no overall deadline unless explicitly set")
	command.PersistentFlags().BoolVar(&options.direct, "direct", false, "run in the foreground using USB; only supported by run")
	command.PersistentFlags().StringVar(&options.lockDir, "lock-dir", os.Getenv("NB1_LOCK_DIR"), "shared device ownership directory for explicit direct run")
	command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if options.direct && cmd.Name() != "run" {
			return fmt.Errorf("--direct is only supported by control run; Job queries and library commands use the daemon")
		}
		if options.direct && cmd.Flags().Changed("endpoint") {
			return fmt.Errorf("--endpoint cannot be combined with --direct")
		}
		if options.direct && cmd.Flags().Changed("token-file") {
			return fmt.Errorf("--token-file cannot be combined with --direct")
		}
		if !options.direct && cmd.Flags().Changed("lock-dir") {
			return fmt.Errorf("--lock-dir requires control --direct run")
		}
		return options.configureConnection(cmd)
	}
	var previewSerial string
	preview := &cobra.Command{Use: "preview FILE", Short: "Preview an action, sequence or saved pattern", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("serial") && !control.ValidDeviceSerial(previewSerial) {
			return fmt.Errorf("--serial must be a valid device serial")
		}
		input, err := readSequenceControlFile(args[0])
		if err != nil {
			return err
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.PreviewSequence(ctx, input, previewSerial)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	preview.Flags().StringVar(&previewSerial, "serial", "", "check the selected device's capabilities")
	var id string
	var runWait bool
	var daemonDefault bool
	run := &cobra.Command{Use: "run [SERIAL] FILE", Short: "Run one action, sequence or saved pattern", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		explicit := ""
		if len(args) == 2 {
			explicit = args[0]
		}
		if len(args) == 2 && !control.ValidDeviceSerial(explicit) {
			return fmt.Errorf("invalid device serial")
		}
		input, err := readSequenceControlFile(args[len(args)-1])
		if err != nil {
			return err
		}
		if options.direct {
			if daemonDefault {
				return fmt.Errorf("--daemon-default requires the daemon")
			}
			if cmd.Flags().Changed("id") {
				return fmt.Errorf("--id requires the daemon; each direct invocation is a new foreground run without retained deduplication")
			}
			if options.shutdownTimeout <= 0 {
				return fmt.Errorf("--shutdown-timeout must be positive")
			}
			return options.runSequenceDirect(cmd, explicit, input)
		}
		if cmd.Flags().Changed("shutdown-timeout") {
			return fmt.Errorf("--shutdown-timeout requires --direct")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			serial := explicit
			var err error
			if daemonDefault {
				if serial == "" {
					serial, err = options.explicitSerial(cmd)
				}
			} else {
				serial, err = resolveControlSerial(ctx, client, cmd, options.settings, explicit)
			}
			if err != nil {
				return err
			}
			return options.runDaemonJob(ctx, client, cmd, id, runWait, func(jobID string) (control.SequenceJob, error) {
				if serial == "" {
					return client.SubmitDefaultSequence(ctx, input, jobID)
				}
				return client.SubmitSequence(ctx, input, serial, jobID)
			})
		})
	}}
	run.Flags().StringVar(&id, "id", "", "daemon-only Job ID to preserve for queries and repeated submissions")
	run.Flags().BoolVar(&runWait, "wait", false, "wait for a daemon Job's terminal state; direct always waits")
	run.Flags().BoolVar(&daemonDefault, "daemon-default", false, "let the daemon resolve the target when no explicit serial is supplied")
	run.Flags().DurationVar(&options.shutdownTimeout, "shutdown-timeout", 10*time.Second, "direct-only maximum cleanup wait; cannot interrupt blocked native I/O")
	job := &cobra.Command{Use: "job ID", Short: "Read a Job's status", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidSequenceJobID(args[0]) {
			return fmt.Errorf("invalid job ID")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.SequenceJob(ctx, args[0])
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	var cancelWait bool
	var cancelSerial string
	cancel := &cobra.Command{Use: "cancel ID", Short: "Cancel a Job", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidSequenceJobID(args[0]) {
			return fmt.Errorf("invalid job ID")
		}
		guarded := cmd.Flags().Changed("serial")
		if guarded && !control.ValidDeviceSerial(cancelSerial) {
			return fmt.Errorf("--serial must be a valid device serial")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			var result control.SequenceJob
			var err error
			if guarded {
				result, err = client.CancelSequenceJobOnDevice(ctx, args[0], cancelSerial)
			} else {
				result, err = client.CancelSequenceJob(ctx, args[0])
			}
			if err != nil {
				if daemonclient.SequenceSubmissionRejected(err) {
					return fmt.Errorf("job %s cancellation rejected: %w", args[0], err)
				}
				return sequenceControlUnknown(options.endpoint, args[0], err)
			}
			if cancelWait {
				result, err = waitSequenceControlJob(ctx, client, result)
				if err != nil {
					return sequenceControlUnknown(options.endpoint, args[0], err)
				}
			}
			return printSequenceControlJob(cmd, result, true)
		})
	}}
	cancel.Flags().BoolVar(&cancelWait, "wait", false, "wait for cancellation and hardware cleanup")
	cancel.Flags().StringVar(&cancelSerial, "serial", "", "require the Job's accepted device serial to match")
	devices := &cobra.Command{Use: "devices", Short: "List the daemon's devices", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.Devices(ctx)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	state := &cobra.Command{Use: "state [SERIAL]", Short: "Read a device's cached state", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		explicit := ""
		if len(args) == 1 {
			explicit = args[0]
		}
		if len(args) == 1 && !control.ValidDeviceSerial(explicit) {
			return fmt.Errorf("invalid device serial")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			serial, err := resolveControlSerial(ctx, client, cmd, options.settings, explicit)
			if err != nil {
				return err
			}
			result, err := client.Device(ctx, serial)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	command.AddCommand(preview, run, job, cancel, devices, state, newSequenceColorsCommand(options), newSequencePatternsCommand(options))
	command.AddCommand(newSequenceEmitCommand(options), newSequenceTriggersCommand(options))
	return command
}

func (options *sequenceControlFlags) explicitSerial(cmd *cobra.Command) (string, error) {
	settings := options.settings
	settings.Device = ""
	return preferredSerial(cmd, settings)
}

func newSequenceEmitCommand(options *sequenceControlFlags) *cobra.Command {
	var id string
	var wait bool
	command := &cobra.Command{
		Use: "emit NAME", Short: "Play a saved Pattern through a named daemon trigger", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serial, err := options.explicitSerial(cmd)
			if err != nil {
				return err
			}
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				return options.runDaemonJob(ctx, client, cmd, id, wait, func(jobID string) (control.SequenceJob, error) {
					return client.SubmitTrigger(ctx, args[0], serial, jobID)
				})
			})
		},
	}
	command.Flags().StringVar(&id, "id", "", "Job ID to preserve for queries and repeated submissions")
	command.Flags().BoolVar(&wait, "wait", false, "wait for the Job's terminal state")
	return command
}

func newSequenceTriggersCommand(options *sequenceControlFlags) *cobra.Command {
	var check bool
	command := &cobra.Command{Use: "triggers", Short: "List the daemon's configured triggers", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				if check {
					report, err := client.CheckSequenceTriggers(ctx)
					if err != nil {
						return err
					}
					if err := printControlJSON(cmd.OutOrStdout(), report); err != nil {
						return err
					}
					for _, row := range report.Items {
						if len(row.Issues) != 0 {
							return fmt.Errorf("trigger check found issues; review the JSON report")
						}
					}
					return nil
				}
				result, err := client.SequenceTriggers(ctx)
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), result)
			})
		},
	}
	command.Flags().BoolVar(&check, "check", false, "check saved Pattern references and device bindings")
	return command
}

func (options *sequenceControlFlags) runDaemonJob(ctx context.Context, client *daemonclient.Client, cmd *cobra.Command, id string, wait bool, submit func(string) (control.SequenceJob, error)) error {
	if id == "" {
		var err error
		id, err = control.NewSequenceJobID()
		if err != nil {
			return err
		}
	} else if !control.ValidSequenceJobID(id) {
		return fmt.Errorf("--id must contain 1 to 64 ASCII letters, digits, hyphens or underscores")
	}
	job, err := submit(id)
	if err != nil {
		if daemonclient.SequenceSubmissionRejected(err) {
			return fmt.Errorf("job %s submission rejected: %w", id, err)
		}
		return sequenceControlUnknown(options.endpoint, id, err)
	}
	if wait {
		job, err = waitSequenceControlJob(ctx, client, job)
		if err != nil {
			return sequenceControlUnknown(options.endpoint, id, err)
		}
	}
	return printSequenceControlJob(cmd, job, false)
}

func (options *sequenceControlFlags) configureConnection(cmd *cobra.Command) error {
	if options.timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	settings, _, err := applicationSettings(cmd)
	if err != nil {
		return err
	}
	options.settings = settings
	if !options.direct && !cmd.Flags().Changed("endpoint") {
		address, err := serverAddress(cmd, settings.Server.Listen)
		if err != nil {
			return err
		}
		options.endpoint = "http://" + address
	}
	return nil
}

func (options *sequenceControlFlags) withClient(cmd *cobra.Command, run func(context.Context, *daemonclient.Client) error) error {
	var token string
	var err error
	if cmd.Flags().Changed("token-file") {
		token, err = remoteaccess.LoadToken(options.tokenFile)
	} else if !cmd.Flags().Changed("endpoint") && options.settings.Server.LocalTokenFile != "" {
		token, err = remoteaccess.LoadToken(options.settings.Server.LocalTokenFile)
	}
	if err != nil {
		return fmt.Errorf("load local API token: %w", err)
	}
	client, err := daemonclient.NewWithToken(options.endpoint, token)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(cmd.Context(), options.timeout)
	defer cancel()
	return sequenceControlConnectionError(cmd, options.endpoint, run(ctx, client))
}

func readSequenceControlFile(path string) (daemonclient.SequenceInput, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return daemonclient.SequenceInput{}, err
	}
	if !before.Mode().IsRegular() || before.Size() > sequence.MaxInputBytes {
		return daemonclient.SequenceInput{}, fmt.Errorf("input must be a regular file of at most 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return daemonclient.SequenceInput{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return daemonclient.SequenceInput{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return daemonclient.SequenceInput{}, fmt.Errorf("input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, sequence.MaxInputBytes+1))
	if err != nil {
		return daemonclient.SequenceInput{}, err
	}
	return daemonclient.DecodeSequenceInput(data)
}

func waitSequenceControlJob(ctx context.Context, client *daemonclient.Client, previous control.SequenceJob) (control.SequenceJob, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for previous.Status != "succeeded" && previous.Status != "cancelled" && previous.Status != "failed" {
		select {
		case <-ctx.Done():
			return previous, ctx.Err()
		case <-ticker.C:
		}
		current, err := client.SequenceJob(ctx, previous.ID)
		if err != nil {
			return previous, err
		}
		if current.Serial != previous.Serial || current.TotalSteps != previous.TotalSteps || !current.CreatedAt.Equal(previous.CreatedAt) || current.CompletedSteps < previous.CompletedSteps {
			return previous, fmt.Errorf("daemon changed the Job's identity or progress")
		}
		previous = current
	}
	return previous, nil
}

func printSequenceControlJob(cmd *cobra.Command, job control.SequenceJob, cancelledOK bool) error {
	if err := printControlJSON(cmd.OutOrStdout(), job); err != nil {
		return err
	}
	if job.Status == "failed" {
		return fmt.Errorf("job %s failed: %s", job.ID, job.Error)
	}
	if job.Status == "cancelled" && !cancelledOK {
		return fmt.Errorf("job %s was cancelled", job.ID)
	}
	return nil
}

func sequenceControlUnknown(endpoint, id string, err error) error {
	return fmt.Errorf("job %s outcome unknown; query this daemon with control --endpoint %s job %s: %w", id, endpoint, id, err)
}
