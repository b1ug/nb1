package cmd

import (
	"fmt"
	"reflect"

	"github.com/1set/gut/ystring"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// configSetCmd represents the set command
var configSetCmd = &cobra.Command{
	Use:     "set <key> <value>",
	Aliases: aliasesSet,
	Short:   "Set configuration value",
	Long: hdoc(`
		Set configuration value for a key.
		
		The key must already exist.
	`),
	Example: hdocf(`
		# preview the set result
		$ %[1]s config set port 8080 --dry-run

		# set the port for web server
		$ %[1]s config set port 8080
	`, config.AppName),
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// ensure key exists
		k, nv := args[0], args[1]
		k1, kr := util.SplitConfigKey(k)
		log.Debugw("key for config set", "key_raw", k, "value", nv, "key1", k1, "key_rest", kr)
		if ok := viper.IsSet(k1); !ok {
			log.Errorw("config key does not exist", "key_raw", k, "key1", k1)
			return errConfigKeyNotFound
		}

		// set or insert new value, retrieve the before/after values for logging
		ov := viper.Get(k1)
		switch k1 {
		case "color":
			// color is a map, so we need to split the key and set the sub-key
			if ystring.IsBlank(kr) {
				log.Errorw("config sub-key is blank", "key_raw", k, "key1", k1, "key_rest", kr)
				return errConfigSubKeyBlank
			}
			cm := config.GetColorMap()
			cm[kr] = nv
			viper.Set(k1, cm)
		default:
			// for other keys, just set the value
			viper.Set(k1, nv)
		}
		fv := viper.Get(k1)
		log.Debugw("config key found and set new value", "key", k1, "old_value", ov, "old_type", reflect.TypeOf(ov), "new_raw", nv, "new_value", fv, "new_type", reflect.TypeOf(fv))

		// preview result
		res := configKeyValuePairList{{Key: k1, Value: fv}}
		fmt.Print(res.String())

		// quit if dry-run
		if configSetDryRun {
			log.Debugw("quit because dry-run")
			return nil
		}

		// write back to config file
		if err := viper.WriteConfig(); err != nil {
			// retry if the config file doesn't exist
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				err = viper.WriteConfigAs(config.DefaultConfigFile)
			}
			if err != nil {
				log.Errorw("fail to save config file", zap.Error(err))
				return err
			}
			log.Debugw("save as new config file", "path", config.DefaultConfigFile)
		} else {
			log.Debugw("save as config file", "path", viper.ConfigFileUsed())
		}
		return nil
	},
}

var (
	configSetDryRun bool
)

func init() {
	configCmd.AddCommand(configSetCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// configSetCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	configSetCmd.Flags().BoolVar(&configSetDryRun, "dry-run", false, "preview the result without saving")
}
