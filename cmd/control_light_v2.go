package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/spf13/cobra"
)

// Only dispatches; the chosen compiler still validates the entire original
// file, including duplicate keys and the exact envelope. No v1 upgrade occurs.
func isLightV2File(data []byte) bool {
	var header struct {
		Contract string `json:"contract"`
	}
	return json.Unmarshal(data, &header) == nil && header.Contract == lightactionv2.Contract
}

func previewLightV2(cmd *cobra.Command, options *controlFlags, data []byte, serial *string) error {
	local, err := daemonclient.LocalLightV2Preview(data)
	if err != nil {
		return err
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	if options.mode == "direct" {
		return printControlJSON(cmd.OutOrStdout(), local)
	}
	client, ctx, done, err := options.client(cmd.Context())
	if err != nil {
		return err
	}
	defer done()
	result, err := client.PreviewLightV2(ctx, data, serial)
	if err != nil {
		return err
	}
	return printControlJSON(cmd.OutOrStdout(), result)
}

type lightV2RunOutput struct {
	Contract       string                  `json:"contract"`
	Mode           string                  `json:"mode"`
	Policy         string                  `json:"policy"`
	Accepted       bool                    `json:"accepted"`
	OutcomeUnknown bool                    `json:"outcome_unknown"`
	RequestTicket  *control.ReceiptRequest `json:"request_ticket,omitempty"`
	DaemonInstance string                  `json:"daemon_instance_id,omitempty"`
	Replayed       *bool                   `json:"replayed,omitempty"`
	Job            *control.FrameJob       `json:"job,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

func runLightV2Daemon(cmd *cobra.Command, options *controlFlags, serial string, data []byte) error {
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
	out := lightV2RunOutput{Contract: lightactionv2.Contract, Mode: "daemon", Policy: "reject", RequestTicket: &receipt}
	accepted, err := client.SubmitLightV2WithReceipt(ctx, serial, data, receipt)
	if err != nil {
		var failure *daemonclient.ReceiptError
		out.OutcomeUnknown = errors.As(err, &failure) && failure.OutcomeUnknown
		out.Error = err.Error()
		return printLightV2Result(cmd, out, err)
	}
	out.Accepted, out.Job, out.Replayed, out.DaemonInstance = true, &accepted.Job, &accepted.Replayed, accepted.DaemonInstanceID
	if waitComplete {
		last := accepted.Job
		_, err = waitLightV2Receipt(ctx, func() (control.FrameJob, error) {
			current, err := client.LookupLightV2Request(ctx, serial, data, receipt, accepted.Job.ID)
			if err == nil {
				if progressErr := daemonclient.ValidateLightV2Progress(last, current); progressErr != nil {
					return control.FrameJob{}, &daemonclient.ReceiptError{Err: progressErr, Receipt: receipt, OutcomeUnknown: true}
				}
				last = current
			}
			return current, err
		})
		out.Job = &last
		if err != nil {
			out.OutcomeUnknown, out.Error = true, err.Error()
			return printLightV2Result(cmd, out, err)
		}
		if last.Status == "failed" || last.Status == "cancelled" {
			err = fmt.Errorf("light FrameJob %s: %s (%s)", last.ID, last.Status, last.Error)
			out.Error = err.Error()
		}
	}
	return printLightV2Result(cmd, out, err)
}

func newLightV2RequestCommand(options *controlFlags) *cobra.Command {
	var jobID string
	command := &cobra.Command{Use: "request SERIAL FILE", Args: cobra.ExactArgs(2), Short: "Recover one experimental-v2 action using its original request ticket", RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidDeviceSerial(args[0]) || cmd.Flags().Changed("job-id") && !validLightV2CLIJobID(jobID) {
			return fmt.Errorf("light request requires a valid serial and optional framejob_ ID")
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		data, err := readControlLight(args[1])
		if err != nil {
			return err
		}
		if !isLightV2File(data) {
			return fmt.Errorf("light request currently requires an explicit experimental-v2 action file")
		}
		if _, err := daemonclient.LocalLightV2Preview(data); err != nil {
			return err
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		receipt := control.ReceiptRequest{RequestID: options.requestID, ExpectedDaemonInstanceID: options.expectedInstance}
		out := lightV2RunOutput{Contract: lightactionv2.Contract, Mode: "daemon", Policy: "reject", RequestTicket: &receipt}
		var trusted *control.FrameJob
		query := func() (control.FrameJob, error) {
			job, err := client.LookupLightV2Request(ctx, args[0], data, receipt, jobID)
			if err == nil {
				if trusted != nil {
					if progressErr := daemonclient.ValidateLightV2Progress(*trusted, job); progressErr != nil {
						return control.FrameJob{}, &daemonclient.ReceiptError{Err: progressErr, Receipt: receipt, OutcomeUnknown: true}
					}
				}
				if jobID == "" {
					jobID = job.ID
				}
				trusted = &job
				out.Accepted, out.Job, out.DaemonInstance = true, &job, receipt.ExpectedDaemonInstanceID
			}
			return job, err
		}
		if waitComplete {
			_, err = waitLightV2Receipt(ctx, query)
		} else {
			_, err = query()
		}
		if err != nil {
			out.OutcomeUnknown, out.Error = true, err.Error()
		} else if out.Job != nil && (out.Job.Status == "failed" || out.Job.Status == "cancelled") {
			err = fmt.Errorf("light FrameJob %s: %s (%s)", out.Job.ID, out.Job.Status, out.Job.Error)
			out.Error = err.Error()
		}
		return printLightV2Result(cmd, out, err)
	}}
	command.Long = "Read only the original experimental-v2 action receipt. Supply the unchanged action file, --request-id and --expected-daemon-instance-id. --job-id is optional when the acceptance response was lost; after the first successful lookup any --wait polling binds that returned ID. Never prepares a new ticket, submits, cancels, or opens USB."
	command.Flags().StringVar(&jobID, "job-id", "", "optional previously confirmed frame Job ID")
	return command
}

func validLightV2CLIJobID(id string) bool {
	const prefix = "framejob_"
	if len(id) != len(prefix)+32 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, ch := range id[len(prefix):] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func waitLightV2Receipt(ctx context.Context, snapshot func() (control.FrameJob, error)) (control.FrameJob, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return control.FrameJob{}, err
		}
		job, err := snapshot()
		if err != nil {
			return job, err
		}
		if job.Status == "succeeded" || job.Status == "failed" || job.Status == "cancelled" {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

func printLightV2Result(cmd *cobra.Command, out lightV2RunOutput, resultErr error) error {
	if err := printControlJSON(cmd.OutOrStdout(), out); err != nil {
		if resultErr != nil {
			return fmt.Errorf("%w; cannot write v2 light result: %v", resultErr, err)
		}
		return err
	}
	return resultErr
}

func runLightV2Direct(cmd *cobra.Command, options *controlFlags, serial string, compiled lightactionv2.Result) error {
	directOptions := *options
	directOptions.policy = "reject"
	out := lightV2RunOutput{Contract: lightactionv2.Contract, Mode: "direct", Policy: "reject"}
	var owned *control.Engine
	err := directOptions.direct(cmd, serial, false, func(ctx context.Context, engine *control.Engine) error {
		owned = engine
		return runLightV2OnEngine(ctx, engine, serial, compiled, &out)
	})
	// The common direct helper has completed its shutdown/ownership handling.
	if owned != nil && out.Job != nil {
		if final, lookupErr := owned.FrameJob(out.Job.ID); lookupErr == nil {
			out.Job = &final
		}
	}
	if err == nil && out.Job != nil && (out.Job.Status == "failed" || out.Job.Status == "cancelled") {
		err = fmt.Errorf("light FrameJob %s: %s (%s)", out.Job.ID, out.Job.Status, out.Job.Error)
	}
	if err != nil {
		out.Error = err.Error()
		out.OutcomeUnknown = out.Accepted && (out.Job == nil || out.Job.Status != "succeeded" && out.Job.Status != "failed" && out.Job.Status != "cancelled")
	}
	return printLightV2Result(cmd, out, err)
}

// The caller owns this already-started Engine and performs shutdown. The
// ephemeral ticket only deduplicates this direct process; it is not a daemon
// recovery ticket and is deliberately absent from the CLI result envelope.
func runLightV2OnEngine(ctx context.Context, engine *control.Engine, serial string, compiled lightactionv2.Result, out *lightV2RunOutput) error {
	if _, err := waitDirectDevice(ctx, engine, serial); err != nil {
		return err
	}
	info := engine.Instance()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	receipt := control.ReceiptRequest{RequestID: info.Idempotency.RequestIDPrefix + hex.EncodeToString(nonce[:]), ExpectedDaemonInstanceID: info.DaemonInstanceID}
	accepted, err := engine.SubmitFrameWithReceipt(ctx, serial, compiled.Program, "reject", receipt)
	if err != nil {
		return err
	}
	out.Accepted, out.Job = true, &accepted.Job
	_, err = waitLightV2Receipt(ctx, func() (control.FrameJob, error) {
		current, lookupErr := engine.FrameJob(accepted.Job.ID)
		if lookupErr == nil {
			out.Job = &current
		}
		return current, lookupErr
	})
	return err
}
