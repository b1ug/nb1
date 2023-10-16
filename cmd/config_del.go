package cmd

import (
	"bytes"
	"encoding/json"

	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// configDeleteCmd represents the delete command
var configDeleteCmd = &cobra.Command{
	Use:     "delete <key>...",
	Aliases: aliasesDelete,
	Short:   "Delete configuration value",
	Long: hdoc(`
		Delete configuration values for one or more keys.
		
		Non-existing keys will be ignored.
	`),
	Example: hdocf(`
	# delete the port for web server
	$ %[1]s config delete port

	# delete the pink from color
	$ %[1]s config delete color.pink
	`, config.AppName),
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		log.Debugw("config delete", "count", len(args), "args", args)
		all := viper.AllSettings()

		// lookup each key and remove the target key
		var (
			touched = false
			cm      = config.GetColorMap()
		)
		for _, k := range args {
			// split key into sections, e.g. "web.port" -> ["web", "port"], and only use the first section
			k1, kr := util.SplitConfigKey(k)
			if ok := viper.IsSet(k1); !ok {
				log.Warnw("config key not found", "key", k1)
			} else {
				switch k1 {
				case "color":
					delete(cm, kr)
					all[k1] = cm
				default:
					delete(all, k1)
				}
				touched = true
			}
		}

		// save the result via viper
		if touched {
			if cf, err := json.MarshalIndent(all, "", "  "); err != nil {
				return err
			} else if err := viper.ReadConfig(bytes.NewReader(cf)); err != nil {
				return err
			}
			return viper.WriteConfig()
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(configDeleteCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// configDeleteCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	// configDeleteCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
