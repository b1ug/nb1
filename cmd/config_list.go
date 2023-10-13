package cmd

import (
	"fmt"

	"github.com/b1ug/nb1/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// configListCmd represents the list command
var configListCmd = &cobra.Command{
	Use:     "list",
	Aliases: aliasesList,
	Short:   "Print configuration",
	Long: hdoc(`
		Print all configuration keys and values, both default and custom settings.
	`),
	Example: hdocf(`
	# print all configuration
	$ %[1]s config list
	`, config.AppName),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		all := viper.AllSettings()
		log.Debugw("config list all", "count", len(all))

		// convert for print
		var result configKeyValuePairList
		for k, v := range all {
			result = append(result, configKeyValuePair{Key: k, Value: v})
		}

		// print the result
		result.Sort()
		fmt.Print(result.String())
		return nil
	},
}

func init() {
	configCmd.AddCommand(configListCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// configListCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	// configListCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
