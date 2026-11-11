package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/sequence"
	"github.com/spf13/cobra"
)

type sequenceControlFlags struct {
	endpoint string
	timeout  time.Duration
}

func newSequenceControlCommand() *cobra.Command {
	options := &sequenceControlFlags{}
	command := &cobra.Command{Use: "control", Short: "Preview and run light actions and sequences"}
	command.PersistentFlags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.PersistentFlags().DurationVar(&options.timeout, "timeout", 30*time.Second, "maximum time to wait for the daemon")
	command.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if options.timeout <= 0 {
			return fmt.Errorf("--timeout must be positive")
		}
		return nil
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
	run := &cobra.Command{Use: "run SERIAL FILE", Short: "Run one action, sequence or saved pattern", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidDeviceSerial(args[0]) {
			return fmt.Errorf("invalid device serial")
		}
		input, err := readSequenceControlFile(args[1])
		if err != nil {
			return err
		}
		if id == "" {
			id, err = control.NewSequenceJobID()
			if err != nil {
				return err
			}
		}
		if !control.ValidSequenceJobID(id) {
			return fmt.Errorf("--id must contain 1 to 64 ASCII letters, digits, hyphens or underscores")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			job, err := client.SubmitSequence(ctx, input, args[0], id)
			if err != nil {
				var rejection *daemonclient.Error
				if errors.As(err, &rejection) && rejection.Status >= 400 && rejection.Status < 500 {
					return fmt.Errorf("job %s submission rejected: %w", id, err)
				}
				return sequenceControlUnknown(options.endpoint, id, err)
			}
			if runWait {
				job, err = waitSequenceControlJob(ctx, client, job)
				if err != nil {
					return sequenceControlUnknown(options.endpoint, id, err)
				}
			}
			return printSequenceControlJob(cmd, job, false)
		})
	}}
	run.Flags().StringVar(&id, "id", "", "job ID to preserve for queries and repeated submissions")
	run.Flags().BoolVar(&runWait, "wait", false, "wait for the Job to reach a terminal state")
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
	cancel := &cobra.Command{Use: "cancel ID", Short: "Cancel a Job", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidSequenceJobID(args[0]) {
			return fmt.Errorf("invalid job ID")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.CancelSequenceJob(ctx, args[0])
			if err != nil {
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
	devices := &cobra.Command{Use: "devices", Short: "List the daemon's devices", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.Devices(ctx)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	state := &cobra.Command{Use: "state SERIAL", Short: "Read a device's cached state", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidDeviceSerial(args[0]) {
			return fmt.Errorf("invalid device serial")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.Device(ctx, args[0])
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	command.AddCommand(preview, run, job, cancel, devices, state, newSequenceColorsCommand(options), newSequencePatternsCommand(options))
	return command
}

func (options *sequenceControlFlags) withClient(cmd *cobra.Command, run func(context.Context, *daemonclient.Client) error) error {
	client, err := daemonclient.New(options.endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(cmd.Context(), options.timeout)
	defer cancel()
	return run(ctx, client)
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
