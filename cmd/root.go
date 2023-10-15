// Package cmd contains all the commands and subcommands of the application.
package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"bitbucket.org/ai69/amoy"
	"bitbucket.org/neiku/hlog"
	"bitbucket.org/neiku/winornot"
	"github.com/1set/gut/ystring"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// for package scope shared variables
var (
	log   *zap.SugaredLogger
	hdoc  = amoy.HereDoc
	hdocf = amoy.HereDocf
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   config.AppName,
	Short: "new blink(1) command-line tool for geeks",
	Long: colorLogo + ystring.NewLine + hdoc(`
		// TODO:
		This is a standard Go CLI application template.
		It is based on Cobra and Viper. Easy to use, easy to extend.
`),
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// fix for Windows terminal output
	winornot.EnableANSIControl()

	if err := rootCmd.Execute(); err != nil {
		util.StderrPrintln("Error:", err)
		os.Exit(1)
	}
}

// for flags
var (
	cfgFile   string
	logFile   string
	logLevel  string
	debugMode bool
)

func init() {
	cobra.OnInitialize(initConfig)

	// Set root command options
	rootCmd.DisableAutoGenTag = true
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SuggestionsMinimumDistance = 2

	// Persistent flags defined here will be global for your application.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is setting.yaml)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "log file path (if non-empty, use this log file)")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "log level (debug, info, warn, error, panic, fatal)")
	rootCmd.PersistentFlags().BoolVarP(&debugMode, "debug", "D", false, "enable debug mode (if true, also use debug log level)")
	rootCmd.PersistentFlags().StringP("device", "d", "", "preferred blink(1) device (if non-empty, use this device)")
	// _ = rootCmd.MarkPersistentFlagRequired("config")

	viper.BindPFlag("device", rootCmd.PersistentFlags().Lookup("device"))

	// Local flags will only run when this action is called directly.
	// rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	// _ = rootCmd.MarkFlagRequired("toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	// init logger
	lg := hlog.NewPersistentLogger(logFile, debugMode)
	if debugMode {
		logLevel = "debug"
	}
	err := lg.SetLevelString(logLevel)
	cobra.CheckErr(err)
	log = lg.SugaredLogger.With(zap.Int("pid", os.Getpid()))

	// pass logger to packages
	util.SetLog(log)
	hdwr.SetLog(log)

	// init config
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home and config directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)
		configDir := filepath.Join(home, ".config", config.AppName)

		// Search config in current and config directory with name app name (without extension).
		viper.AddConfigPath(".")
		viper.AddConfigPath(configDir)
		viper.SetConfigType("yaml")
		viper.SetConfigName("setting")
	}

	// set default values
	viper.SetTypeByDefaultValue(true)
	config.SetDefaults()

	// read in environment variables that match
	viper.AutomaticEnv()
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.SetEnvPrefix(config.AppName)

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; ignore error if desired
			log.Debugw("config file not found")
		} else {
			// Config file was found but another error was produced
			log.Debugw("fail to read config file", zap.Error(err))
		}
	} else {
		// Config file found and successfully parsed
		cfp := viper.ConfigFileUsed()
		log.Debugw("using config file", "path", cfp)
	}
}
