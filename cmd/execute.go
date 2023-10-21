package cmd

import (
	"fmt"

	"bitbucket.org/ai69/amoy"
	"github.com/b1ug/nb1/exchange"
	"github.com/spf13/cobra"
)

// executeCmd represents the execute command
var executeCmd = &cobra.Command{
	Use:     "execute",
	Aliases: aliasesExecute,
	Short:   "Execute blink(1) into a given color",
	Long: hdoc(`
		Perform a specific color changing action on a blink(1) device.
		// TODO:
	`),
	Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// read file content
		fp := args[0]

		// TODO: check file types, and THEN read

		// read
		ls, err := amoy.ReadFileLines(fp)
		if err != nil {
			return err
		}

		// parsed
		ps, err := exchange.ParsePlayText(ls)
		if err != nil {
			return err
		}
		fmt.Println("Play", ps)
		amoy.PrintOneLineJSON(ps)
		amoy.PrintJSON(ps)

		// TODO:
		return fmt.Errorf("not implemented")
	},
}

func init() {
	rootCmd.AddCommand(executeCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// executeCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// executeCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
