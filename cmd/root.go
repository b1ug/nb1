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
	"github.com/b1ug/nb1/exchange"
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
	Short: "Control blink(1) lights and share a local device API",
	Long: colorLogo + ystring.NewLine + hdoc(`
		nb1 controls blink(1) lights and can run a local daemon that shares
		configured devices with the CLI and other clients through versioned HTTP APIs.

		Start the daemon with server --devices, then use control to send requests.
		control previews Actions and Sequences, submits Jobs and manages the local
		color and Pattern library through HTTP. doctor inspects build information,
		daemon status and cached device snapshots without scanning or opening USB.

		Standalone commands on, off, blink, fade, play, read and write access the
		device directly and require it to be available. Use nb1 <command> --help.

		Run as a regular user with the required device access configured.
		server listens on loopback. Starting it does not install a startup service.
`),
	Example: `  nb1 server --devices DEVICE_SERIAL
  nb1 control devices
  nb1 control preview action.json
  nb1 control run DEVICE_SERIAL action.json --wait
  nb1 control job j_7K3XJNY9ZM
  nb1 doctor
  nb1 --device DEVICE_SERIAL on`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// fix for Windows terminal output
	winornot.EnableANSIControl()

	err := rootCmd.Execute()
	if closeErr := hdwr.CloseBlink1Device(); err == nil {
		err = closeErr
	}
	if err != nil {
		amoy.Eprintln("🚨Error:", err)
		os.Exit(1)
	}
}

// for flags
var (
	cfgFile      string
	logFile      string
	logLevel     string
	debugMode    bool
	waitComplete bool
	allowAbsent  bool
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
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error, panic, fatal)")
	rootCmd.PersistentFlags().BoolVarP(&debugMode, "debug", "D", false, "enable debug mode (if true, also use debug log level)")
	rootCmd.PersistentFlags().StringP("device", "d", "", "preferred blink(1) device (if non-empty, use this device)")
	rootCmd.PersistentFlags().BoolVarP(&waitComplete, "wait", "w", false, "wait for completion")
	rootCmd.PersistentFlags().BoolVarP(&allowAbsent, "allow-absent", "a", false, "ignore opening error for absent blink(1) devices")
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
	exchange.SetLog(log)

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
