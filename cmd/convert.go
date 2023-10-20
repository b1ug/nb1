package cmd

import (
	"errors"

	"bitbucket.org/ai69/amoy"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// convertCmd represents the turn command
var convertCmd = &cobra.Command{
	Use:     "convert",
	Aliases: aliasesConvert,
	Short:   "Convert pattern formats",
	Long: hdoc(`
		Convert a pattern from one format to another.
		
		Supported formats:
		  - Play Text (e.g. "red blink 3 times")
		  - Pattern JSON (e.g. '{"repeat":1,"seq":"#FF0000L0T1500;#FF0000L0T3500"...}')
		  - Starlark Script (e.g. 'play(red, blue, green)')
	`),
}

var (
	convertPreviewPattern bool
)

func init() {
	rootCmd.AddCommand(convertCmd)

	// Here you will define your flags and configuration settings.
	// Cobra supports Persistent Flags which will work for this command and all subcommands, e.g.:
	// convertCmd.PersistentFlags().String("foo", "", "A help for foo")
	convertCmd.PersistentFlags().BoolVarP(&convertPreviewPattern, "preview", "p", false, "Preview the converted pattern")

	// Cobra supports local flags which will only run when this command is called directly, e.g.:
	// convertCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	// Subcommands
	convertCmd.AddCommand(convertText2JSONCmd)
	convertCmd.AddCommand(convertJSON2TextCmd)
	convertCmd.AddCommand(convertText2ScriptCmd)
	convertCmd.AddCommand(convertJSON2ScriptCmd)
}

var (
	inputPath  string
	outputPath string
)

// getInOutPathArgs returns a PersistentPreRunE function that sets inputPath and outputPath from args.
func getInOutPathArgs(msg, extName string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		// input and output path
		inputPath = args[0]
		if len(args) >= 2 {
			outputPath = args[1]
		} else {
			outputPath = util.ChangeFileExt(args[0], extName)
		}
		log.Infow(msg, "input_path", inputPath, "output_path", outputPath)
		return nil
	}
}

// convertText2JSONCmd represents the text2json command
var convertText2JSONCmd = &cobra.Command{
	Use:     "text2json",
	Aliases: []string{"textjson", "t2j", "tj"},
	Short:   "Convert play.txt to pattern.json",
	Long: hdoc(`
		Convert a Play Text file to a Pattern JSON file.
	`),
	Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: getInOutPathArgs("converting text to json", ".json"),
	RunE: func(cmd *cobra.Command, args []string) error {
		// read & parse
		lines, err := exchange.LoadFromLine(inputPath)
		if err != nil {
			return err
		}
		ps, err := exchange.ParsePlayText(lines)
		if err != nil {
			return err
		}

		// output
		if convertPreviewPattern {
			amoy.PrintOneLineJSON(ps)
		}
		return exchange.SaveAsJSON(ps, outputPath)
	},
}

// convertJSON2TextCmd represents the json2text command
var convertJSON2TextCmd = &cobra.Command{
	Use:     "json2text",
	Aliases: []string{"json2txt", "j2t", "jt"},
	Short:   "Convert pattern.json to play.txt",
	Long: hdoc(`
		Convert a Pattern JSON file to a Play Text file.
	`),
	Args:              cobra.MinimumNArgs(1),
	PersistentPreRunE: getInOutPathArgs("converting json to text", ".txt"),
	RunE: func(cmd *cobra.Command, args []string) error {
		// load from file
		var ps schema.PatternSet
		if err := exchange.LoadFromJSON(&ps, inputPath); err != nil {
			return err
		}
		ps.Length = uint(len(ps.Sequence)) // TODO: may auto calculate length with helper methods

		// output
		if convertPreviewPattern {
			amoy.PrintJSON(ps)
		}
		ls := exchange.EncodePlayText(&ps)
		return exchange.SaveAsLine(ls, outputPath)
	},
}

// convertText2ScriptCmd represents the text2script command
var convertText2ScriptCmd = &cobra.Command{
	Use:     "text2script",
	Aliases: []string{"txt2script", "t2s", "ts"},
	Short:   "Convert play.txt to script.star",
	Long: hdoc(`
		Convert a Play Text file to a Starlark Script.
	`),
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		//
		return errors.New("not implemented")
	},
}

// convertJSON2ScriptCmd represents the json2script command
var convertJSON2ScriptCmd = &cobra.Command{
	Use:     "json2script",
	Aliases: []string{"json2s", "j2s", "js"},
	Short:   "Convert pattern.json to script.star",
	Long: hdoc(`
		Convert a Pattern JSON file to a Starlark Script.
	`),
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		//
		return errors.New("not implemented")
	},
}
