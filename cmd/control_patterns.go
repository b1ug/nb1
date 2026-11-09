package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/patterns"
	"github.com/b1ug/nb1/internal/patternstore"
	"github.com/spf13/cobra"
)

func isPatternCommand(cmd *cobra.Command) bool {
	return cmd.Parent() != nil && cmd.Parent().Name() == "patterns"
}

func newControlPatternsCommand(options *controlFlags) *cobra.Command {
	parent := &cobra.Command{Use: "patterns", Short: "Manage the daemon's bounded local Pattern library", Long: "Use the configured daemon library; these commands never open USB or create a direct-mode library. Import does not play a Pattern. Run requires an exact saved revision and preserves its request ticket."}
	for _, name := range []string{"info", "list"} {
		name := name
		parent.AddCommand(&cobra.Command{Use: name, Args: cobra.NoArgs, Short: "Read Pattern library " + name, RunE: func(cmd *cobra.Command, _ []string) error {
			client, ctx, done, err := options.client(cmd.Context())
			if err != nil {
				return err
			}
			defer done()
			value, err := client.ListPatterns(ctx)
			if err != nil {
				return err
			}
			if name == "info" {
				return printControlJSON(cmd.OutOrStdout(), value.Library)
			}
			return printControlJSON(cmd.OutOrStdout(), value)
		}})
	}
	parent.AddCommand(&cobra.Command{Use: "get ID", Args: cobra.ExactArgs(1), Short: "Read one verified frozen Pattern record", RunE: func(cmd *cobra.Command, args []string) error {
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.GetPattern(ctx, args[0])
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	parent.AddCommand(&cobra.Command{Use: "import FILE", Args: cobra.ExactArgs(1), Short: "Import original authoring or snapshot bytes without playing", RunE: func(cmd *cobra.Command, args []string) error {
		data, err := readPatternFile(args[0])
		if err != nil {
			return err
		}
		records, err := patterns.DecodePackage(data)
		if err != nil {
			return err
		}
		out := patternImportOutput{Requested: make([]httpapi.PatternRef, 0, len(records))}
		for _, r := range records {
			out.Requested = append(out.Requested, httpapi.PatternRef{ID: r.ID, Revision: r.Revision})
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.ImportPatterns(ctx, data)
		if err != nil {
			var failure *daemonclient.PatternImportError
			out.PublicationUnknown = errors.As(err, &failure) && failure.PublicationUnknown
			out.Error = err.Error()
		} else {
			out.Result = &value
		}
		return printPatternResult(cmd, out, err)
	}})
	var output string
	export := &cobra.Command{Use: "export [ID]", Args: cobra.MaximumNArgs(1), Short: "Export a verified canonical snapshot; never overwrite output", RunE: func(cmd *cobra.Command, args []string) error {
		id := ""
		if len(args) == 1 {
			if args[0] == "" {
				return fmt.Errorf("explicit Pattern export ID must not be empty")
			}
			id = args[0]
		}
		if cmd.Flags().Changed("output") && output == "" {
			return fmt.Errorf("explicit --output must name a new file")
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		data, err := client.ExportPatterns(ctx, id)
		if err != nil {
			return err
		}
		return writePatternExport(cmd.OutOrStdout(), output, data)
	}}
	export.Flags().StringVar(&output, "output", "", "optional new local file; existing files are never replaced and directories are not created")
	parent.AddCommand(export)
	var previewRevision, serial string
	preview := &cobra.Command{Use: "preview ID", Args: cobra.ExactArgs(1), Short: "Preview an exact saved revision without creating a Job", RunE: func(cmd *cobra.Command, args []string) error {
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		var target *string
		if cmd.Flags().Changed("serial") {
			target = &serial
		}
		value, err := client.PreviewPattern(ctx, httpapi.PatternRef{ID: args[0], Revision: previewRevision}, target)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}}
	preview.Flags().StringVar(&previewRevision, "revision", "", "required exact sha256 revision returned by the daemon")
	preview.Flags().StringVar(&serial, "serial", "", "optional daemon serial for cached capability checks")
	_ = preview.MarkFlagRequired("revision")
	parent.AddCommand(preview)
	var runRevision string
	run := &cobra.Command{Use: "run SERIAL ID", Args: cobra.ExactArgs(2), Short: "Run a fixed Pattern with reject policy and the original request ticket", RunE: func(cmd *cobra.Command, args []string) error {
		return runPatternDaemon(cmd, options, args[0], httpapi.PatternRef{ID: args[1], Revision: runRevision})
	}}
	run.Flags().StringVar(&runRevision, "revision", "", "required exact sha256 revision returned by the daemon")
	_ = run.MarkFlagRequired("revision")
	parent.AddCommand(run)
	return parent
}

type patternImportOutput struct {
	PublicationUnknown bool                       `json:"publication_unknown"`
	Requested          []httpapi.PatternRef       `json:"requested"`
	Result             *patternstore.ImportResult `json:"result,omitempty"`
	Error              string                     `json:"error,omitempty"`
}
type patternRunOutput struct {
	PatternRef     httpapi.PatternRef      `json:"pattern_ref"`
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

func runPatternDaemon(cmd *cobra.Command, options *controlFlags, serial string, ref httpapi.PatternRef) error {
	if !control.ValidDeviceSerial(serial) {
		return fmt.Errorf("invalid pattern run serial")
	}
	// Validate the reference locally before any optional ticket preparation.
	if err := validatePatternCLIRef(ref); err != nil {
		return err
	}
	client, ctx, done, err := options.client(cmd.Context())
	if err != nil {
		return err
	}
	defer done()
	ticket := control.ReceiptRequest{RequestID: options.requestID, ExpectedDaemonInstanceID: options.expectedInstance}
	if ticket.RequestID == "" {
		ticket, err = client.Prepare(ctx)
		if err != nil {
			return err
		}
	}
	out := patternRunOutput{PatternRef: ref, Mode: "daemon", Policy: "reject", RequestTicket: &ticket}
	accepted, err := client.SubmitPatternWithReceipt(ctx, serial, ref, ticket)
	if err != nil {
		var failure *daemonclient.ReceiptError
		out.OutcomeUnknown = errors.As(err, &failure) && failure.OutcomeUnknown
		out.Error = err.Error()
		return printPatternResult(cmd, out, err)
	}
	out.Accepted, out.Job, out.Replayed, out.DaemonInstance = true, &accepted.Job, &accepted.Replayed, accepted.DaemonInstanceID
	if waitComplete {
		last := accepted.Job
		_, err = waitRemoteSnapshot(ctx, func() (control.Job, error) {
			current, err := client.LookupPatternRequest(ctx, serial, ticket, accepted.Job)
			if err == nil {
				last = current
			}
			return current, err
		})
		out.Job = &last
		if err != nil {
			out.OutcomeUnknown = true
			out.Error = err.Error()
			return printPatternResult(cmd, out, err)
		}
		if last.Status == "failed" || last.Status == "cancelled" {
			err = fmt.Errorf("pattern Job %s: %s (%s)", last.ID, last.Status, last.Error)
			out.Error = err.Error()
		}
	}
	return printPatternResult(cmd, out, err)
}

func validatePatternCLIRef(ref httpapi.PatternRef) error {
	if len(ref.ID) < 1 || len(ref.ID) > 64 {
		return fmt.Errorf("invalid pattern ID")
	}
	for i, c := range []byte(ref.ID) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
			return fmt.Errorf("invalid pattern ID")
		}
	}
	if len(ref.Revision) != 71 || ref.Revision[:7] != "sha256:" {
		return fmt.Errorf("--revision requires sha256: plus 64 lowercase hex characters")
	}
	for _, c := range []byte(ref.Revision[7:]) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("invalid pattern revision")
		}
	}
	return nil
}

func readPatternFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, patterns.MaxPackageBytes+1))
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > patterns.MaxPackageBytes {
		return nil, fmt.Errorf("pattern package exceeds 1 MiB")
	}
	return data, nil
}

func writePatternExport(stdout io.Writer, path string, data []byte) (resultErr error) {
	if path == "" {
		n, err := stdout.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			if resultErr == nil {
				resultErr = err
			} else {
				resultErr = fmt.Errorf("%w; close export: %v", resultErr, err)
			}
		}
		if resultErr != nil {
			// Keep a failed output for inspection. Removing by path after an
			// error could delete a replacement; this is not an atomic export.
			resultErr = fmt.Errorf("export may be incomplete at %q: %w", path, resultErr)
		}
	}()
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return f.Sync()
}

func printPatternResult(cmd *cobra.Command, value interface{}, resultErr error) error {
	if err := printControlJSON(cmd.OutOrStdout(), value); err != nil {
		if resultErr != nil {
			return fmt.Errorf("%w; cannot write Pattern result: %v", resultErr, err)
		}
		return err
	}
	return resultErr
}
