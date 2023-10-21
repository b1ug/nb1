package cmd

import (
	"fmt"
	"os"

	"bitbucket.org/ai69/amoy"
	"github.com/b1ug/nb1/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:     "server",
	Aliases: aliasesServer,
	Short:   "Start a web server",
	Long: hdoc(`
		Start a web server to serve the web application.
	`),
	RunE: func(cmd *cobra.Command, args []string) error {
		pwd, _ := os.Getwd()
		fmt.Println("CWD:", pwd)

		fmt.Println("Port:", config.GetPort())
		fmt.Println("Base:", config.GetBaseURL())
		fmt.Println(amoy.Quote())

		return errNotImplemented
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// serverCmd.PersistentFlags().String("foo", "", "A help for foo")
	serverCmd.PersistentFlags().Uint32("port", config.DefaultPort, "port to run web server on")
	viper.BindPFlag("port", serverCmd.PersistentFlags().Lookup("port"))

	serverCmd.PersistentFlags().String("base", "", "base URL of the web server")
	viper.BindPFlag("base_url", serverCmd.PersistentFlags().Lookup("base"))

	// Local flags which will only run when this command is called directly
	// serverCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
