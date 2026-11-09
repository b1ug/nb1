package cmd

import (
	"errors"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/patternidentity"
	"github.com/b1ug/nb1/internal/patternsv2"
	"github.com/spf13/cobra"
)

func isPatternV2Command(cmd *cobra.Command) bool {
	return cmd.Parent() != nil && cmd.Parent().Name() == "patterns-v2"
}

type patternSaveOutputV2 struct {
	PatternRef           patternidentity.Ref                `json:"pattern_ref"`
	PublicationConfirmed bool                               `json:"publication_confirmed"`
	PublicationUnknown   bool                               `json:"publication_unknown"`
	Result               *daemonclient.PatternWriteResultV2 `json:"result,omitempty"`
	Error                string                             `json:"error,omitempty"`
}

func newControlPatternsV2Command(options *controlFlags) *cobra.Command {
	parent := &cobra.Command{
		Use: "patterns-v2", Short: "Read, preview and save experimental high-level Pattern records",
		Long: "Use the daemon's independent experimental v2 library. Source actions and frozen execution stay together. These authoring commands never play a Pattern, prepare a ticket or open USB. Existing patterns commands keep their v1 format.",
	}
	parent.AddCommand(&cobra.Command{Use: "get ID", Args: cobra.ExactArgs(1), Short: "Read a complete high-level record; unknown recipes stay read-only", RunE: func(cmd *cobra.Command, args []string) error {
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.GetPatternV2(ctx, args[0])
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	parent.AddCommand(&cobra.Command{Use: "preview-draft FILE", Args: cobra.ExactArgs(1), Short: "Preview {id,title,source} without saving or device work", RunE: func(cmd *cobra.Command, args []string) error {
		data, err := readPatternFile(args[0])
		if err != nil {
			return err
		}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.PreviewPatternDraftV2(ctx, data)
		if err != nil {
			return err
		}
		return printControlJSON(cmd.OutOrStdout(), value)
	}})
	parent.AddCommand(&cobra.Command{Use: "save FILE", Args: cobra.ExactArgs(1), Short: "Save a draft with its exact expected_revision; never overwrite or play", RunE: func(cmd *cobra.Command, args []string) error {
		data, err := readPatternFile(args[0])
		if err != nil {
			return err
		}
		candidate, err := patternsv2.PrepareSave(data)
		if err != nil {
			return err
		}
		record := candidate.Candidate().Record()
		out := patternSaveOutputV2{PatternRef: patternidentity.Ref{RecordContract: record.Contract, ID: record.ID, Revision: record.Revision}}
		client, ctx, done, err := options.client(cmd.Context())
		if err != nil {
			return err
		}
		defer done()
		value, err := client.SavePatternV2(ctx, data)
		if err != nil {
			var uncertain *daemonclient.PatternImportError
			out.PublicationUnknown = errors.As(err, &uncertain) && uncertain.PublicationUnknown
			out.Error = err.Error()
		} else {
			out.PublicationConfirmed, out.Result = true, &value
		}
		return printPatternResult(cmd, out, err)
	}})
	return parent
}
