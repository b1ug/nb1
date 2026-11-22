package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/spf13/cobra"
)

func newPlaybackCommand() *cobra.Command {
	options := &sequenceControlFlags{endpoint: daemonclient.DefaultEndpoint, timeout: 10 * time.Second}
	command := &cobra.Command{Use: "playback", Short: "Inspect daemon Playback policy and temporary overrides"}
	command.PersistentFlags().StringVar(&options.endpoint, "endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.PersistentFlags().StringVar(&options.tokenFile, "token-file", "", "private Bearer token file for the loopback API")
	command.PersistentFlags().DurationVar(&options.timeout, "timeout", 10*time.Second, "daemon request limit")
	command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return options.configureConnection(cmd) }

	status := &cobra.Command{Use: "status", Short: "Read the daemon's effective Playback status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				result, err := client.PlaybackStatus(ctx)
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), result)
			})
		}}
	requests := &cobra.Command{Use: "requests", Short: "Read recent Playback policy decisions", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				result, err := client.PlaybackRequests(ctx, limit)
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), result)
			})
		}}
	requests.Flags().Int("limit", 0, "maximum rows to return (default: configured history limit)")

	override := &cobra.Command{Use: "override", Short: "Temporarily override the configured Playback policy", Args: cobra.NoArgs}
	for _, mode := range []string{"suppress", "permit"} {
		mode := mode
		cmd := &cobra.Command{Use: mode, Short: mode + " Playback temporarily", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				duration, _ := cmd.Flags().GetDuration("for")
				if !cmd.Flags().Changed("for") || duration <= 0 || duration%time.Millisecond != 0 {
					return fmt.Errorf("--for must be a positive duration, such as 30m or 2h")
				}
				return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
					result, err := client.SetPlaybackOverride(ctx, mode, duration)
					if err != nil {
						return err
					}
					return printControlJSON(cmd.OutOrStdout(), result)
				})
			}}
		cmd.Flags().Duration("for", 0, "override duration (maximum 720h)")
		_ = cmd.MarkFlagRequired("for")
		override.AddCommand(cmd)
	}
	clear := &cobra.Command{Use: "clear", Short: "Remove the temporary Playback override", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return options.withClient(cmd, func(ctx context.Context, client *daemonclient.Client) error {
				result, err := client.ClearPlaybackOverride(ctx)
				if err != nil {
					return err
				}
				return printControlJSON(cmd.OutOrStdout(), result)
			})
		}}
	override.AddCommand(clear)
	command.AddCommand(status, requests, override)
	return command
}

func init() { rootCmd.AddCommand(newPlaybackCommand()) }
