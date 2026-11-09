package cmd

import (
	"errors"
	"fmt"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/lightactionv2"
	"github.com/spf13/cobra"
)

type lightV2CancelOutput struct {
	Contract        string                  `json:"contract"`
	Mode            string                  `json:"mode"`
	RequestTicket   *control.ReceiptRequest `json:"request_ticket"`
	CancelAttempted bool                    `json:"cancel_attempted"`
	CancelConfirmed bool                    `json:"cancel_confirmed"`
	AlreadyTerminal bool                    `json:"already_terminal"`
	OutcomeUnknown  bool                    `json:"outcome_unknown"`
	DaemonInstance  string                  `json:"daemon_instance_id,omitempty"`
	JobID           string                  `json:"job_id,omitempty"`
	Job             *control.FrameJob       `json:"job,omitempty"`
	CancelError     string                  `json:"cancel_error,omitempty"`
	Error           string                  `json:"error,omitempty"`
}

func newLightV2CancelCommand(options *controlFlags) *cobra.Command {
	var jobID string
	command := &cobra.Command{Use: "cancel SERIAL FILE", Args: cobra.ExactArgs(2), Short: "Cancel the original experimental-v2 light Job using its action and receipt", RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidDeviceSerial(args[0]) || cmd.Flags().Changed("job-id") && !validLightV2CLIJobID(jobID) {
			return fmt.Errorf("light cancel requires a valid serial and optional framejob_ ID")
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		data, err := readControlLight(args[1])
		if err != nil {
			return err
		}
		if !isLightV2File(data) {
			return fmt.Errorf("light cancel requires the original experimental-v2 action file")
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
		out := lightV2CancelOutput{Contract: lightactionv2.Contract, Mode: "daemon", RequestTicket: &receipt, JobID: jobID}
		result, err := client.CancelLightV2(ctx, args[0], data, receipt, jobID)
		out.CancelAttempted, out.AlreadyTerminal, out.DaemonInstance = result.CancelAttempted, result.AlreadyTerminal, result.DaemonInstanceID
		if result.Job.ID != "" {
			out.Job = &result.Job
			out.JobID = result.Job.ID
		}
		cancelErr := err
		if cancelErr != nil && result.CancelAttempted {
			out.CancelError = cancelErr.Error()
		}
		canWait := err == nil || result.QueryRecoveryAllowed
		if canWait && waitComplete && out.Job != nil && !lightV2Terminal(result.Job.Status) {
			last := result.Job
			_, err = waitLightV2Receipt(ctx, func() (control.FrameJob, error) {
				current, queryErr := client.LookupLightV2Request(ctx, args[0], data, receipt, result.Job.ID)
				if queryErr != nil {
					return control.FrameJob{}, queryErr
				}
				if progressErr := daemonclient.ValidateLightV2Progress(last, current); progressErr != nil {
					return control.FrameJob{}, &daemonclient.ReceiptError{Err: progressErr, Receipt: receipt, OutcomeUnknown: true}
				}
				if result.RecoveryAnchor != nil {
					if progressErr := daemonclient.ValidateLightV2Progress(*result.RecoveryAnchor, current); progressErr != nil {
						return control.FrameJob{}, &daemonclient.ReceiptError{Err: progressErr, Receipt: receipt, OutcomeUnknown: true}
					}
				}
				last = current
				return current, nil
			})
			out.Job = &last
			if err == nil && cancelErr != nil && last.Status != "cancelled" {
				// The final task state is known. Retain the earlier uncertain
				// cancellation as a diagnostic, not as this snapshot's status.
				err = fmt.Errorf("job reached %s without confirming cancellation (%s); original cancellation error: %v", last.Status, last.Error, cancelErr)
			}
		}
		out.CancelConfirmed = out.Job != nil && out.Job.Status == "cancelled"
		if err != nil {
			var failure *daemonclient.ReceiptError
			out.OutcomeUnknown = errors.As(err, &failure) && failure.OutcomeUnknown || out.Job != nil && !lightV2Terminal(out.Job.Status)
			out.Error = err.Error()
		} else if !out.AlreadyTerminal && out.Job != nil && out.Job.Status == "failed" {
			err = fmt.Errorf("light FrameJob %s failed during cancellation: %s", out.Job.ID, out.Job.Error)
			out.Error = err.Error()
		}
		if writeErr := printControlJSON(cmd.OutOrStdout(), out); writeErr != nil {
			if err != nil {
				return fmt.Errorf("%w; cannot write cancellation result: %v", err, writeErr)
			}
			return writeErr
		}
		return err
	}}
	command.Flags().StringVar(&jobID, "job-id", "", "optional previously confirmed frame Job ID")
	command.Long = "Daemon-only cancellation of an original experimental-v2 action. Supply its unchanged FILE, --request-id and --expected-daemon-instance-id; --job-id is optional. First verifies the original receipt, then sends at most one instance-conditional cancel POST and confirms through that same receipt. Already terminal or cancelling jobs are only queried. --wait may recover transport uncertainty using only original-receipt GETs; integrity errors remain failures. Cancel errors stay in cancel_error. Cancelling is not completed cleanup; cancelling a completed hold Job does not turn the light off."
	return command
}

func lightV2Terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}
