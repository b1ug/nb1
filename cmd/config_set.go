package cmd

import (
	"fmt"
	"reflect"

	"github.com/b1ug/nb1/config"
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
		if ok := viper.IsSet(k); !ok {
			log.Errorw("config key does not exist", "key", k)
			return errConfigKeyNotFound
		}

		// set new value
		ov := viper.Get(k)
		viper.Set(k, nv)
		fv := viper.Get(k)
		log.Debugw("config key found and set new value", "key", k, "old_value", ov, "old_type", reflect.TypeOf(ov), "new_raw", nv, "new_value", fv, "new_type", reflect.TypeOf(fv))

		// preview result
		res := configKeyValuePairList{{Key: k, Value: fv}}
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
