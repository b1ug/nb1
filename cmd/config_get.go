package cmd

import (
	"fmt"

	"github.com/b1ug/nb1/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// configGetCmd represents the get command
var configGetCmd = &cobra.Command{
	Use:     "get <key>...",
	Aliases: aliasesGet,
	Short:   "Get configuration value",
	Long: hdoc(`
		Get configuration values for one or more keys.
		
		Non-existing keys will be ignored.
	`),
	Example: hdocf(`
	# get the port for web server
	$ %[1]s config get port

	# get the port and the base url for web server
	$ %[1]s config get port base_url
	`, config.AppName),
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		log.Debugw("config get", "count", len(args), "args", args)

		// lookup each key and save the value
		var result configKeyValuePairList
		for _, k := range args {
			if ok := viper.IsSet(k); !ok {
				log.Warnw("config key not found", "key", k)
			} else {
				v := viper.Get(k)
				log.Debugw("config key found", "key", k, "value", v)
				result = append(result, configKeyValuePair{Key: k, Value: v})
			}
		}

		// print the result
		fmt.Print(result.String())
		return nil
	},
}

func init() {
	configCmd.AddCommand(configGetCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// configGetCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	// configGetCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
