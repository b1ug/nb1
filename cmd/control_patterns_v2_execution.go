package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/spf13/cobra"
)

func patternV2Flags(cmd *cobra.Command, o *controlFlags) error {
	if o.mode != "daemon" {
		return fmt.Errorf("Pattern v2 commands require --mode daemon; no direct fallback")
	}
	receipt := cmd.Flags().Changed("request-id")
	instance := cmd.Flags().Changed("expected-daemon-instance-id")
	switch cmd.Name() {
	case "run":
		if receipt != instance || receipt && (o.requestID == "" || o.expectedInstance == "") {
			return fmt.Errorf("Pattern v2 run requires both nonempty original receipt flags or neither")
		}
	case "request", "cancel":
		if !receipt || !instance || o.requestID == "" || o.expectedInstance == "" {
			return fmt.Errorf("Pattern v2 %s requires both nonempty original receipt flags", cmd.Name())
		}
		if cmd.Flags().Changed("policy") {
			return fmt.Errorf("Pattern v2 %s does not accept --policy", cmd.Name())
		}
	default:
		if receipt || instance || cmd.Flags().Changed("policy") || cmd.Flags().Changed("wait") {
			return fmt.Errorf("Pattern v2 authoring/preview commands do not accept policy, wait or execution receipt flags")
		}
	}
	return nil
}

type patternRunOutputV2 struct {
	Mode            string                      `json:"mode"`
	PatternRef      patternidentity.Ref         `json:"pattern_ref"`
	Policy          string                      `json:"policy,omitempty"`
	Accepted        bool                        `json:"accepted"`
	OutcomeUnknown  bool                        `json:"outcome_unknown"`
	RequestTicket   control.ReceiptRequest      `json:"request_ticket"`
	DaemonInstance  string                      `json:"daemon_instance_id,omitempty"`
	Replayed        *bool                       `json:"replayed,omitempty"`
	Job             *control.PatternJobEnvelope `json:"job,omitempty"`
	CancelAttempted *bool                       `json:"cancel_attempted,omitempty"`
	Error           string                      `json:"error,omitempty"`
}

func addPatternExecutionCommandsV2(parent *cobra.Command, o *controlFlags) {
	var previewRevision, previewSerial string
	preview := &cobra.Command{Use: "preview ID", Args: cobra.ExactArgs(1), Short: "Preview an exact saved advanced Pattern; optional device evidence is cached only", RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := patternCLIRefV2(args[0], previewRevision)
		if err != nil {
			return err
		}
		var serial *string
		if cmd.Flags().Changed("serial") {
			if !control.ValidDeviceSerial(previewSerial) {
				return fmt.Errorf("Pattern preview requires a valid --serial")
			}
			serial = &previewSerial
		}
		c, ctx, done, err := o.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := c.PreviewPatternRefV2(ctx, ref, serial)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}}
	preview.Flags().StringVar(&previewRevision, "revision", "", "required exact sha256 revision from the complete saved record")
	preview.Flags().StringVar(&previewSerial, "serial", "", "optional selected daemon device for cached evidence; never reserves it")
	parent.AddCommand(preview)
	var runRevision string
	run := &cobra.Command{Use: "run SERIAL ID", Args: cobra.ExactArgs(2), Short: "Run one exact advanced Pattern with its original receipt", RunE: func(cmd *cobra.Command, args []string) error {
		ref, err := patternCLIRefV2(args[1], runRevision)
		if err != nil {
			return err
		}
		if !control.ValidDeviceSerial(args[0]) {
			return fmt.Errorf("Pattern run requires an explicit valid serial")
		}
		c, ctx, done, err := o.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		key := control.ReceiptRequest{RequestID: o.requestID, ExpectedDaemonInstanceID: o.expectedInstance}
		if key.RequestID == "" {
			key, err = c.Prepare(ctx)
			if err != nil {
				return err
			}
		}
		policy := "reject"
		if cmd.Flags().Changed("policy") {
			policy = o.policy
		}
		out := patternRunOutputV2{Mode: "daemon", PatternRef: ref, Policy: policy, RequestTicket: key}
		result, err := c.SubmitPatternV2WithReceipt(ctx, args[0], ref, policy, key)
		if err != nil {
			return printPatternExecutionV2(cmd, &out, err)
		}
		out.Accepted, out.Job, out.Replayed, out.DaemonInstance = true, &result.Job, &result.Replayed, result.DaemonInstanceID
		if waitComplete {
			err = followPatternReceiptV2(ctx, c, args[0], &out, "", "")
			if err == nil {
				err = patternCompletionErrorV2(out.Job, false)
			}
		}
		return printPatternExecutionV2(cmd, &out, err)
	}}
	run.Long = "Submit one exact Ref, never an implicit library preview or Get. Effective default policy is reject; explicitly choose replace/queue/reject. A missing ticket prepares once; a supplied complete original ticket is never replaced. --wait reads only that original receipt and never cancels or resubmits on timeout. Ref, ticket and last trusted typed Job remain in output on uncertainty."
	run.Flags().StringVar(&runRevision, "revision", "", "required exact saved revision; no latest inference")
	parent.AddCommand(run)
	for _, cancel := range []bool{false, true} {
		var revision, knownID, knownDigest string
		name, short := "request", "Read an original advanced Pattern receipt without accessing its library"
		if cancel {
			name, short = "cancel", "Bind an advanced Pattern cancellation to its original receipt before one POST"
		}
		isCancel := cancel
		cmd := &cobra.Command{Use: name + " SERIAL ID", Args: cobra.ExactArgs(2), Short: short, RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := patternCLIRefV2(args[1], revision)
			if err != nil {
				return err
			}
			if !control.ValidDeviceSerial(args[0]) || cmd.Flags().Changed("job-id") && !patternCLIJobIDV2(knownID) || cmd.Flags().Changed("execution-digest") && patternidentity.ValidateDigest(knownDigest) != nil {
				return fmt.Errorf("Pattern receipt requires valid serial and optional exact known Job/digest")
			}
			key := control.ReceiptRequest{RequestID: o.requestID, ExpectedDaemonInstanceID: o.expectedInstance}
			out := patternRunOutputV2{Mode: "daemon", PatternRef: ref, RequestTicket: key}
			c, ctx, done, err := o.client(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			if isCancel {
				result, cancelErr := c.CancelPatternV2Request(ctx, args[0], ref, key, knownID, knownDigest)
				out.Job, out.DaemonInstance, out.CancelAttempted = result.Job, result.DaemonInstanceID, &result.CancelAttempted
				out.Accepted = out.Job != nil
				if cancelErr != nil {
					return printPatternExecutionV2(cmd, &out, cancelErr)
				}
				if waitComplete {
					err = followPatternReceiptV2(ctx, c, args[0], &out, knownID, knownDigest)
					if err == nil {
						err = patternCompletionErrorV2(out.Job, true)
					}
				}
			} else if waitComplete {
				err = followPatternReceiptV2(ctx, c, args[0], &out, knownID, knownDigest)
				if err == nil {
					err = patternCompletionErrorV2(out.Job, false)
				}
			} else {
				value, lookupErr := c.LookupPatternV2Request(ctx, args[0], ref, key, knownID, knownDigest)
				err = lookupErr
				if err == nil {
					out.Job, out.Accepted, out.DaemonInstance = &value, true, key.ExpectedDaemonInstanceID
				}
			}
			return printPatternExecutionV2(cmd, &out, err)
		}}
		cmd.Flags().StringVar(&revision, "revision", "", "required exact original Pattern revision")
		cmd.Flags().StringVar(&knownID, "job-id", "", "optional previously confirmed job_ or framejob_ identity")
		cmd.Flags().StringVar(&knownDigest, "execution-digest", "", "optional previously confirmed frozen execution digest")
		cmd.Long = "Supply the complete original Ref and both receipt flags. No Store lookup, new ticket, retry, history lookup or USB fallback. Optional known Job/digest add binding. Cancel first queries that receipt, skips POST for terminal/cancelling, otherwise sends at most one confirmed_job_id POST. On any error preserve ticket and last trusted state; no later good response hides a bad one. --wait only reads original receipts and does not cancel on timeout."
		parent.AddCommand(cmd)
	}
}

func patternCLIRefV2(id, revision string) (patternidentity.Ref, error) {
	ref := patternidentity.Ref{RecordContract: patternidentity.RecordContract, ID: id, Revision: revision}
	if err := patternidentity.ValidateRef(ref); err != nil {
		return patternidentity.Ref{}, fmt.Errorf("Pattern v2 requires ID and exact --revision: %w", err)
	}
	return ref, nil
}
func patternCLIJobIDV2(id string) bool {
	prefix := "job_"
	if strings.HasPrefix(id, "framejob_") {
		prefix = "framejob_"
	}
	if len(id) != len(prefix)+32 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, c := range id[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func patternJobStateV2(e *control.PatternJobEnvelope) (id, status, problem string) {
	if e == nil {
		return
	}
	if e.ProgramJob != nil {
		return e.ProgramJob.ID, e.ProgramJob.Status, e.ProgramJob.Error
	}
	if e.FrameJob != nil {
		return e.FrameJob.ID, e.FrameJob.Status, e.FrameJob.Error
	}
	return
}
func patternCompletionErrorV2(e *control.PatternJobEnvelope, cancel bool) error {
	id, status, problem := patternJobStateV2(e)
	if status == "failed" || status == "cancelled" && !cancel {
		return fmt.Errorf("Pattern Job %s: %s (%s)", id, status, problem)
	}
	return nil
}
func followPatternReceiptV2(ctx context.Context, c *daemonclient.Client, serial string, out *patternRunOutputV2, knownID, knownDigest string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		// A previously validated terminal snapshot remains a known outcome even
		// if the caller ends its context immediately after that response.
		if out.Job != nil {
			_, status, _ := patternJobStateV2(out.Job)
			if status == "succeeded" || status == "failed" || status == "cancelled" {
				return nil
			}
			knownID, _, _ = patternJobStateV2(out.Job)
			knownDigest = out.Job.ExecutionDigest
		}
		if err := ctx.Err(); err != nil {
			return &daemonclient.ReceiptError{Err: err, Receipt: out.RequestTicket, OutcomeUnknown: true}
		}
		current, err := c.LookupPatternV2Request(ctx, serial, out.PatternRef, out.RequestTicket, knownID, knownDigest)
		if err != nil {
			return err
		}
		if out.Job != nil {
			if err = daemonclient.ValidatePatternV2Progress(*out.Job, current); err != nil {
				return &daemonclient.ReceiptError{Err: err, Receipt: out.RequestTicket, OutcomeUnknown: true}
			}
		}
		out.Job, out.Accepted, out.DaemonInstance = &current, true, out.RequestTicket.ExpectedDaemonInstanceID
		_, status, _ := patternJobStateV2(out.Job)
		if status == "succeeded" || status == "failed" || status == "cancelled" {
			return nil
		}
		select {
		case <-ctx.Done():
			return &daemonclient.ReceiptError{Err: ctx.Err(), Receipt: out.RequestTicket, OutcomeUnknown: true}
		case <-ticker.C:
		}
	}
}
func printPatternExecutionV2(cmd *cobra.Command, out *patternRunOutputV2, err error) error {
	if err != nil {
		out.Error = err.Error()
		var receipt *daemonclient.ReceiptError
		if errors.As(err, &receipt) {
			out.OutcomeUnknown = receipt.OutcomeUnknown
		} else {
			// A reported failed/cancelled terminal state is a known outcome,
			// even when this command returns nonzero for that completion.
			_, status, _ := patternJobStateV2(out.Job)
			terminal := status == "succeeded" || status == "failed" || status == "cancelled"
			out.OutcomeUnknown = out.Accepted && !terminal
		}
	}
	return printPatternResult(cmd, *out, err)
}
