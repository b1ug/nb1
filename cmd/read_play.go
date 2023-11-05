package cmd

import (
	"fmt"
	"time"

	"github.com/b1ug/nb1/hdwr"
	"github.com/spf13/cobra"
)

// readPlayCmd represents the play command
var readPlayCmd = &cobra.Command{
	Use:     "play",
	Aliases: aliasesPlay,
	Short:   "Read the playing state of pattern",
	Long: hdoc(`
		Read the pattern playing state from a blink(1) device.
	`),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ts := time.Now()
		pls, err := hdwr.ReadPlayingState()
		if err != nil {
			return err
		}
		log.Infow("read device playing state", "play_state", pls, "time_cost", time.Since(ts))

		// preview
		if readPreviewResult {
			fmt.Println("Playing:", pls.IsPlaying)
			fmt.Println("Start:", pls.StartPosition)
			fmt.Println("End:", pls.EndPosition)
			fmt.Println("Repeat:", pls.RepeatTimes)
		}

		// save result
		saveJSONData = pls
		saveTextLine = []string{
			fmt.Sprintf("Playing: %v", pls.IsPlaying),
			fmt.Sprintf("Start: %d", pls.StartPosition),
			fmt.Sprintf("End: %d", pls.EndPosition),
			fmt.Sprintf("Repeat: %d", pls.RepeatTimes),
		}

		return nil
	},
}

func init() {
	readCmd.AddCommand(readPlayCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// readPlayCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// readPlayCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
