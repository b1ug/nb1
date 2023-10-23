package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/1set/gut/yos"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// executeCmd represents the execute command
var executeCmd = &cobra.Command{
	Use:     "execute",
	Aliases: aliasesExecute,
	Short:   "Execute pattern files",
	Long: hdoc(`
		Load pattern files and execute them by playing the patterns.

		Supported formats:
		  - Play Text (e.g. "red blink 3 times")
		  - Pattern JSON (e.g. '{"repeat":1,"seq":"#FF0000L0T1500;#FF0000L0T3500"...}')
	`),
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: openBlink1Device,
	RunE: func(cmd *cobra.Command, args []string) error {
		// check input file
		fp := args[0]
		if !yos.ExistFile(fp) {
			return fmt.Errorf("file not exist: %s", fp)
		}
		ext := strings.ToLower(filepath.Ext(fp))

		// read input file
		var ps schema.PatternSet
		switch ext {
		case ".txt":
			// read & parse
			lines, err := exchange.LoadFromLine(fp)
			if err != nil {
				return err
			}
			if ts, err := exchange.ParsePlayText(lines); err != nil {
				return err
			} else if ts != nil {
				ps = *ts
			}
		case ".json":
			if err := exchange.LoadFromJSON(&ps, fp); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported file type: %s", ext)
		}

		// check pattern
		if err := ps.Validate(); err != nil {
			return err
		}

		// preview
		if execPreviewPattern {
			_ = util.PrintPatternSet(&ps)
		}

		// TODO:
		return fmt.Errorf("not implemented")
	},
}

var (
	execPreviewPattern bool
)

func init() {
	rootCmd.AddCommand(executeCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// executeCmd.PersistentFlags().String("foo", "", "A help for foo")
	executeCmd.PersistentFlags().BoolVarP(&execPreviewPattern, "preview", "p", false, "Preview the pattern to be executed")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// executeCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
