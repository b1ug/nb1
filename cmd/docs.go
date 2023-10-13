package cmd

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/1set/gut/yos"
	"github.com/b1ug/nb1/config"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"go.uber.org/zap"
)

// docsCmd represents the docs command
var docsCmd = &cobra.Command{
	Use:     "docs",
	Aliases: aliasesDocs,
	Short:   "Generate documentation",
	Long: hdoc(`
		Generate documentation for all commands in Markdown format.

		If no output directory is given, the docs will be generated to a temporary directory.
		If the given output directory does not exist, it will be created.
	`),
	Example: hdocf(`
	# generate to default output dir, i.e. temp dir
	$ %[1]s docs

	# generate to given output dir
	$ %[1]s docs -o manual
	`, config.AppName),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// use temp dir if no output dir given
		var err error
		if docsOutputDir == "" {
			log.Debugw("no given output dir, use temp dir")
			if docsOutputDir, err = ioutil.TempDir("", config.AppName+"-docs"); err != nil {
				log.Errorw("failed to create temp dir", zap.Error(err))
				return err
			}
		}

		// make sure output dir exists
		if err := yos.MakeDir(docsOutputDir); err != nil {
			log.Errorw("failed to create output dir", "output_path", docsOutputDir, zap.Error(err))
			return err
		}

		// generate markdown docs
		if err = doc.GenMarkdownTree(rootCmd, docsOutputDir); err != nil {
			log.Errorw("failed to generate docs", "output_path", docsOutputDir, zap.Error(err))
			return err
		}

		// print success message
		if path, err := filepath.Abs(docsOutputDir); err == nil {
			docsOutputDir = path
		}
		fmt.Println("Documentation was successfully generated at", docsOutputDir)
		return nil
	},
}

var (
	docsOutputDir string
)

func init() {
	rootCmd.AddCommand(docsCmd)

	// Configuration settings

	// Persistent flags which will work for this command and all subcommands
	// docsCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Local flags which will only run when this command is called directly
	docsCmd.Flags().StringVarP(&docsOutputDir, "output-dir", "o", "", "output directory for generated docs")
}
