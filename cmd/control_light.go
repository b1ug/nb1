package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/lightaction"
	"github.com/spf13/cobra"
)

type lightRunOutput struct {
	Contract       string                  `json:"contract"`
	Mode           string                  `json:"mode"`
	Policy         string                  `json:"policy"`
	Accepted       bool                    `json:"accepted"`
	OutcomeUnknown bool                    `json:"outcome_unknown"`
	RequestTicket  *control.ReceiptRequest `json:"request_ticket,omitempty"`
	DaemonInstance string                  `json:"daemon_instance_id,omitempty"`
	Replayed       *bool                   `json:"replayed,omitempty"`
	Job            *control.Job            `json:"job,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

func isLightCommand(cmd *cobra.Command) bool {
	return cmd.Parent() != nil && cmd.Parent().Name() == "light"
}

func newControlLightCommand(options *controlFlags) *cobra.Command {
	parent := &cobra.Command{Use: "light", Short: "Preview or run versioned light actions through the shared executor"}
	var serial string
	preview := &cobra.Command{Use: "preview FILE", Args: cobra.ExactArgs(1), Short: "Compile a contract/action file without a Job or device write", RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("serial") && (!control.ValidDeviceSerial(serial) || options.mode == "direct") {
			return fmt.Errorf("--serial requires a valid daemon device; direct light preview is software-only")
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		data, err := readControlLight(args[0])
		if err != nil {
			return err
		}
		compiled, err := lightaction.CompileRequest(data)
		if err != nil {
			return err
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if options.mode == "direct" {
			program, err := control.Preview(&compiled.Program, nil)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), daemonclient.LightPreview{Result: compiled, Preview: program})
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		var target *string
		if cmd.Flags().Changed("serial") {
			target = &serial
		}
		result, err := client.PreviewLight(ctx, data, target)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), result)
	}}
	preview.Long = "FILE must contain exactly contract nb1-light-action/v1 and action. Default daemon mode sends the original action to the authoritative compiler. --mode direct uses that same pure Go compiler locally, without HTTP, discovery, ownership locks or USB. Optional --serial reads only a configured daemon snapshot. No receipt or policy flags are accepted."
	preview.Flags().StringVar(&serial, "serial", "", "optional daemon serial for cached capability validation")
	parent.AddCommand(preview)
	run := &cobra.Command{Use: "run SERIAL FILE", Args: cobra.ExactArgs(2), Short: "Run one light action with reject policy and a preserved receipt", RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidDeviceSerial(args[0]) {
			return fmt.Errorf("light run requires a valid serial")
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		data, err := readControlLight(args[1])
		if err != nil {
			return err
		}
		compiled, err := lightaction.CompileRequest(data)
		if err != nil {
			return err
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if options.mode == "direct" {
			return runLightDirect(cmd, options, args[0], compiled)
		}
		return runLightDaemon(cmd, options, args[0], data)
	}}
	run.Long = "Run accepts a strict contract/action file. Its effective policy is always reject, independent of the legacy commands' replace default; an explicit non-reject policy is refused. Daemon mode prepares one complete request ticket if none was supplied, submits once, and prints the ticket even when an outcome is unknown. --wait polls that original receipt and never cancels or resubmits on timeout. Direct mode waits within this process and reuses the existing ownership and shutdown cleanup."
	parent.AddCommand(run)
	return parent
}

func readControlLight(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, lightaction.MaxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > lightaction.MaxRequestBytes {
		return nil, fmt.Errorf("light action file exceeds %d bytes", lightaction.MaxRequestBytes)
	}
	return data, nil
}

func runLightDaemon(cmd *cobra.Command, options *controlFlags, serial string, data []byte) error {
	client, ctx, done, err := options.client(cmd.Context())
	if err != nil {
		return err
	}
	defer done()
	receipt := control.ReceiptRequest{RequestID: options.requestID, ExpectedDaemonInstanceID: options.expectedInstance}
	if receipt.RequestID == "" {
		receipt, err = client.Prepare(ctx)
		if err != nil {
			return err
		}
	}
	out := lightRunOutput{Contract: lightaction.Contract, Mode: "daemon", Policy: "reject", RequestTicket: &receipt}
	accepted, err := client.SubmitLightWithReceipt(ctx, serial, data, receipt)
	if err != nil {
		var failure *daemonclient.ReceiptError
		out.OutcomeUnknown = errors.As(err, &failure) && failure.OutcomeUnknown
		out.Error = err.Error()
		return printLightResult(cmd, out, err)
	}
	out.Accepted, out.Job, out.Replayed, out.DaemonInstance = true, &accepted.Job, &accepted.Replayed, accepted.DaemonInstanceID
	if waitComplete {
		last := accepted.Job
		_, err = waitRemoteSnapshot(ctx, func() (control.Job, error) {
			current, err := client.LookupLightRequest(ctx, serial, data, receipt, accepted.Job.ID)
			if err == nil {
				last = current
			}
			return current, err
		})
		out.Job = &last
		if err != nil {
			out.OutcomeUnknown = true
			out.Error = err.Error()
			return printLightResult(cmd, out, err)
		}
		if last.Status == "failed" || last.Status == "cancelled" {
			err = fmt.Errorf("light Job %s: %s (%s)", last.ID, last.Status, last.Error)
			out.Error = err.Error()
		}
	}
	return printLightResult(cmd, out, err)
}

func runLightDirect(cmd *cobra.Command, options *controlFlags, serial string, compiled lightaction.Result) error {
	directOptions := *options
	directOptions.policy = "reject"
	out := lightRunOutput{Contract: lightaction.Contract, Mode: "direct", Policy: "reject"}
	var owned *control.Engine
	err := directOptions.direct(cmd, serial, false, func(ctx context.Context, engine *control.Engine) error {
		owned = engine
		if _, err := waitDirectDevice(ctx, engine, serial); err != nil {
			return err
		}
		job, err := engine.Submit(ctx, serial, compiled.Program, "reject")
		if err != nil {
			return err
		}
		out.Accepted, out.Job = true, &job
		_, err = waitRemoteSnapshot(ctx, func() (control.Job, error) {
			current, err := engine.Job(job.ID)
			if err == nil {
				out.Job = &current
			}
			return current, err
		})
		return err
	})
	// The common direct helper has now completed shutdown/ownership cleanup.
	if owned != nil && out.Job != nil {
		if final, lookupErr := owned.Job(out.Job.ID); lookupErr == nil {
			out.Job = &final
		}
	}
	if err == nil && out.Job != nil && (out.Job.Status == "failed" || out.Job.Status == "cancelled") {
		err = fmt.Errorf("light Job %s: %s (%s)", out.Job.ID, out.Job.Status, out.Job.Error)
	}
	if err != nil {
		out.Error = err.Error()
		out.OutcomeUnknown = out.Accepted && (out.Job == nil || out.Job.Status != "succeeded" && out.Job.Status != "failed" && out.Job.Status != "cancelled")
	}
	return printLightResult(cmd, out, err)
}

func printLightResult(cmd *cobra.Command, out lightRunOutput, resultErr error) error {
	if err := printControlJSON(cmd.OutOrStdout(), out); err != nil {
		if resultErr != nil {
			return fmt.Errorf("%w; cannot write light result: %v", resultErr, err)
		}
		return err
	}
	return resultErr
}
