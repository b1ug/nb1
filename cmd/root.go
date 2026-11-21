// Package cmd contains all the commands and subcommands of the application.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bitbucket.org/neiku/winornot"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/textfmt"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// for package scope shared variables
var (
	log   *zap.SugaredLogger
	hdoc  = textfmt.HereDoc
	hdocf = textfmt.HereDocf
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   config.AppName,
	Short: "Control blink(1) lights and share a local device API",
	Long: hdoc(`
		nb1 controls blink(1) lights and can run a local daemon that shares
		configured devices with the CLI and other clients through versioned HTTP APIs.

		Start the daemon with server --devices, then use control to send requests.
		control previews Actions and Sequences, submits Jobs and manages the local
		color and Pattern library through HTTP. doctor inspects build information,
		daemon status and cached device snapshots without scanning or opening USB.
		Use control --direct run for one foreground Action, Sequence or local
		Pattern without a daemon. Its Job is local to that invocation.

		Use device reset SERIAL to replace the selected device's persistent Flash preset through the daemon.
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
  nb1 control --direct run DEVICE_SERIAL action.json
  nb1 doctor
  nb1 --device DEVICE_SERIAL on`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		if loggerInitError != nil {
			return loggerInitError
		}
		// Recovery and service stop/status must remain available even if a
		// configuration file was removed or edited incorrectly after install.
		if cmd.Parent() != nil && ((cmd.Parent().Name() == "config" && (cmd.Name() == "path" || cmd.Name() == "init")) || (cmd.Parent().Name() == "service" && cmd.Name() != "install")) {
			return nil
		}
		return configurationInitError
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// fix for Windows terminal output
	winornot.EnableANSIControl()
	restore := guardLoggerInitialization(rootCmd)
	defer restore()

	if err := executePresentedCommand(rootCmd, os.Args[1:], hdwr.CloseBlink1Device); err != nil {
		os.Exit(1)
	}
}

// Guard existing overrides too: Cobra runs only the nearest persistent hook.
// Restoring both hooks leaves successful and subsequent executions unchanged.
func guardLoggerInitialization(root *cobra.Command) func() {
	var restore []func()
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		beforeE, before := command.PersistentPreRunE, command.PersistentPreRun
		if beforeE != nil || before != nil {
			command.PersistentPreRun = nil
			command.PersistentPreRunE = func(current *cobra.Command, args []string) error {
				if loggerInitError != nil {
					return loggerInitError
				}
				if beforeE != nil {
					return beforeE(current, args)
				}
				before(current, args)
				return nil
			}
			restore = append(restore, func() {
				command.PersistentPreRunE, command.PersistentPreRun = beforeE, before
			})
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
	return func() {
		for _, undo := range restore {
			undo()
		}
	}
}

// for flags
var (
	cfgFile                string
	logFile                string
	logLevel               string
	debugMode              bool
	waitComplete           bool
	allowAbsent            bool
	configurationInitError error
	loggerInitError        error
)

func init() {
	cobra.OnInitialize(initConfig)

	// Set root command options
	rootCmd.DisableAutoGenTag = true
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SuggestionsMinimumDistance = 2

	// Persistent flags defined here will be global for your application.
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "configuration file (NB1_CONFIG or per-user nb1/config.yaml; established setting.yaml is a fallback)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "append application diagnostics to a regular JSON log file")
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
	loggerInitError = nil
	// version inspects only the executable. Cobra runs initializers before
	// PersistentPreRunE, so its pre-run override alone cannot prevent runtime
	// configuration logs from contaminating --json output. Resolve the command
	// through Cobra so aliases and persistent flag values remain unambiguous.
	if command, _, err := rootCmd.Find(os.Args[1:]); err == nil && command == versionCmd {
		return
	}
	configurationInitError = nil
	// init logger
	// Keep diagnostics on stderr and command results on stdout. Retain the
	// persistent logger's existing file format, size and retention policy.
	lg, err := newApplicationLogger(os.Stderr, logFile, debugMode)
	if err != nil {
		loggerInitError = err
		return
	}
	if debugMode {
		logLevel = "debug"
	}
	if err := lg.SetLevelString(logLevel); err != nil {
		loggerInitError = err
		return
	}
	log = lg.SugaredLogger

	// pass logger to packages
	util.SetLog(log)
	hdwr.SetLog(log)
	exchange.SetLog(log)

	if rootCmd.PersistentFlags().Changed("config") && cfgFile == "" {
		configurationInitError = fmt.Errorf("--config must name a file")
		return
	}

	// Prefer the per-user profile; retain the established setting.yaml lookup
	// only when no profile was selected or created. Validate before Viper can
	// open a special file such as a FIFO.
	selected, err := appconfig.Select(cfgFile, os.Getenv("NB1_CONFIG"))
	if err != nil {
		configurationInitError = err
		return
	}
	if _, err := os.Lstat(selected.Path); os.IsNotExist(err) && !selected.Required {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			configurationInitError = homeErr
			return
		}
	search:
		for _, directory := range []string{".", filepath.Join(home, ".config", config.AppName)} {
			for _, name := range []string{"setting.yaml", "setting.yml"} {
				path, _ := filepath.Abs(filepath.Join(directory, name))
				if _, err := os.Lstat(path); err == nil {
					selected.Path, selected.Required = path, true
					break search
				}
			}
		}
	}
	viper.SetConfigFile(selected.Path)
	viper.SetConfigType("yaml")
	if _, err := appconfig.Load(selected); err != nil {
		configurationInitError = err
		return
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
		if _, ok := err.(viper.ConfigFileNotFoundError); !selected.Required && (ok || os.IsNotExist(err)) {
			// Config file not found; ignore error if desired
			log.Debugw("config file not found")
		} else {
			// Config file was found but another error was produced
			log.Debugw("fail to read config file", zap.Error(err))
			configurationInitError = fmt.Errorf("read configuration: %w", err)
		}
	} else {
		// Config file found and successfully parsed
		cfp := viper.ConfigFileUsed()
		log.Debugw("using config file", "path", cfp)
		selected.Path, selected.Required = cfp, true
		_, configurationInitError = appconfig.Load(selected)
	}
}
