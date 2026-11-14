package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
	"github.com/spf13/cobra"
)

type patternImportOutputV2 struct {
	PublicationConfirmed bool                               `json:"publication_confirmed"`
	PublicationUnknown   bool                               `json:"publication_unknown"`
	Requested            []patternidentity.Ref              `json:"requested"`
	SnapshotSHA256       string                             `json:"snapshot_sha256"`
	OriginalSnapshotJSON string                             `json:"original_snapshot_json,omitempty"`
	Result               *daemonclient.PatternWriteResultV2 `json:"result,omitempty"`
	Error                string                             `json:"error,omitempty"`
}

func addPatternSnapshotCommandsV2(parent *cobra.Command, options *controlFlags) {
	parent.AddCommand(&cobra.Command{Use: "import FILE", Args: cobra.ExactArgs(1), Short: "Import a complete verified advanced snapshot without playing or overwriting", RunE: func(cmd *cobra.Command, args []string) error {
		data, err := readPatternSnapshotFileV2(args[0])
		if err != nil {
			return err
		}
		records, err := patternsv2.DecodeSnapshot(data)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out := patternImportOutputV2{Requested: make([]patternidentity.Ref, 0, len(records)), SnapshotSHA256: "sha256:" + hex.EncodeToString(sum[:])}
		for _, record := range records {
			out.Requested = append(out.Requested, patternidentity.Ref{RecordContract: record.Contract, ID: record.ID, Revision: record.Revision})
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.ImportPatternsV2(ctx, data)
		if err != nil {
			var failure *daemonclient.PatternImportError
			out.PublicationUnknown = errors.As(err, &failure) && failure.PublicationUnknown
			if out.PublicationUnknown {
				out.OriginalSnapshotJSON = string(data)
			}
			out.Error = err.Error()
		} else {
			out.PublicationConfirmed, out.Result = true, &value
		}
		return printPatternResult(cmd, out, err)
	}})
	var output string
	export := &cobra.Command{Use: "export [ID]", Args: cobra.MaximumNArgs(1), Short: "Export a canonical advanced snapshot, including read-only future recipes", RunE: func(cmd *cobra.Command, args []string) error {
		id := ""
		if len(args) == 1 {
			if args[0] == "" {
				return fmt.Errorf("explicit Pattern v2 export ID must not be empty")
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
		data, err := client.ExportPatternsV2(ctx, id)
		if err != nil {
			return err
		}
		return writePatternExport(cmd.OutOrStdout(), output, data)
	}}
	export.Flags().StringVar(&output, "output", "", "optional new local file; never replace existing files or create parent directories")
	parent.AddCommand(export)
}

func readPatternSnapshotFileV2(path string) (data []byte, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > patternsv2.MaxBytes {
		return nil, fmt.Errorf("Pattern v2 snapshot input must be a regular file of at most 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			data, err = nil, closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("Pattern v2 snapshot input changed while opening")
	}
	data, err = io.ReadAll(io.LimitReader(file, patternsv2.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > patternsv2.MaxBytes {
		return nil, fmt.Errorf("Pattern v2 snapshot exceeds 1 MiB")
	}
	return data, nil
}
