package cmd

import (
	"fmt"
	"strings"

	cl "bitbucket.org/ai69/colorlogo"
	"github.com/1set/gut/yos"
	"github.com/1set/gut/ystring"
	"github.com/b1ug/nb1/config"
	"github.com/spf13/cobra"
)

var (
	colorLogo = cl.RoseWaterByLine(config.AppLogoArt)
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: aliasesVersion,
	Short:   "Show version information",
	Long: hdoc(`
		Print version with build information and quit.
	`),
	Example: hdocf(`
		# show version
		$ %[1]s version
	`, config.AppName),
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// write logo
		var sb strings.Builder
		sb.WriteString(colorLogo)
		sb.WriteString(ystring.NewLine)

		// inline helpers
		arrow := "➣ "
		if yos.IsOnWindows() {
			arrow = "> "
		}
		addNonBlankField := func(name, value string) {
			if ystring.IsNotBlank(value) {
				fmt.Fprintln(&sb, arrow+name+":", value)
			}
		}

		// concatenate fields
		addNonBlankField("App Name  ", config.AppName)
		addNonBlankField("Build Num ", config.CIBuildNum)
		addNonBlankField("Build Date", config.BuildDate)
		addNonBlankField("Build Host", config.BuildHost)
		addNonBlankField("Go Version", config.GoVersion)
		addNonBlankField("Git Branch", config.GitBranch)
		addNonBlankField("Git Commit", config.GitCommit)
		addNonBlankField("GitSummary", config.GitSummary)

		// device info
		addNonBlankField("Device", config.GetPreferredDevice())

		// output to stdout
		fmt.Println(sb.String())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// versionCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	// versionCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
