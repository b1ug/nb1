package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/spf13/cobra"
)

func newSequenceColorsCommand(options *sequenceControlFlags) *cobra.Command {
	command := &cobra.Command{Use: "colors", Short: "Manage local named colors"}
	list := &cobra.Command{Use: "list", Short: "List local named colors", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			colors, err := client.SequenceColors(ctx)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), colors)
		})
	}}
	set := &cobra.Command{Use: "set NAME INPUT", Short: "Save a local named color", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if !daemonclient.ValidSequenceColorName(args[0]) {
			return fmt.Errorf("color name must start with a lowercase letter and contain 1 to 32 lowercase letters, digits or hyphens")
		}
		color, err := daemonclient.ParseSequenceColorInput(args[1])
		if err != nil {
			return err
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			result, err := client.SaveSequenceColor(ctx, args[0], color)
			if err != nil {
				return sequenceLibraryWriteError(err, "color "+args[0], "control --endpoint "+options.endpoint+" colors list")
			}
			return printControlJSON(cmd.OutOrStdout(), result)
		})
	}}
	set.Example = "  nb1 control colors set status-ok lime\n  nb1 control colors set status-ok '#00ff00'\n  nb1 control colors set sunset '{\"space\":\"hsv\",\"h_deg\":30,\"s_pct\":100,\"v_pct\":100}'"
	command.AddCommand(list, set)
	return command
}

func newSequencePatternsCommand(options *sequenceControlFlags) *cobra.Command {
	command := &cobra.Command{Use: "patterns", Short: "Manage local saved patterns"}
	list := &cobra.Command{Use: "list", Short: "List local saved patterns", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			patterns, err := client.SequencePatterns(ctx)
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), patterns)
		})
	}}
	get := &cobra.Command{Use: "get ID", Short: "Read a saved pattern", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			pattern, err := client.SequencePattern(ctx, args[0])
			if err != nil {
				return err
			}
			return printControlJSON(cmd.OutOrStdout(), pattern)
		})
	}}
	var title string
	save := &cobra.Command{Use: "save ID FILE", Short: "Save an action or sequence as a local pattern", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if !control.ValidSequenceJobID(args[0]) {
			return fmt.Errorf("invalid pattern ID")
		}
		input, err := readSequenceControlFile(args[1])
		if err != nil {
			return err
		}
		if input.PatternID != nil {
			return fmt.Errorf("saving a pattern requires an action or sequence")
		}
		return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
			pattern, err := client.SaveSequencePattern(ctx, args[0], input, title)
			if err != nil {
				return sequenceLibraryWriteError(err, "pattern "+args[0], "control --endpoint "+options.endpoint+" patterns get "+args[0])
			}
			return printControlJSON(cmd.OutOrStdout(), pattern)
		})
	}}
	save.Flags().StringVar(&title, "title", "", "optional pattern title")
	command.AddCommand(list, get, save)
	return command
}

func sequenceLibraryWriteError(err error, item, lookup string) error {
	var rejection *daemonclient.Error
	var malformed *daemonclient.DecodeError
	var transport *url.Error
	unknown := errors.As(err, &malformed) || errors.As(err, &transport) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if errors.As(err, &rejection) {
		unknown = rejection.Status >= 500
	}
	if !unknown {
		return err
	}
	return fmt.Errorf("%s save outcome unknown; check with %s: %w", item, lookup, err)
}
