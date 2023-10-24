package cmd

import (
	"errors"
	"strconv"
	"strings"

	"github.com/1set/gut/ystring"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/exchange"
	"github.com/b1ug/nb1/hdwr"
	"github.com/b1ug/nb1/util"
	"github.com/spf13/cobra"
)

// Errors for commands and subcommands
var (
	errNotImplemented    = errors.New("not implemented")
	errNoAction          = errors.New("no action specified")
	errConfigKeyNotFound = errors.New("config key not found")
	errConfigSubKeyBlank = errors.New("config sub-key is blank")
)

// openBlink1Device works as a PersistentPreRunE function that opens a blink(1) device for use.
func openBlink1Device(cmd *cobra.Command, args []string) error {
	err := hdwr.OpenBlink1Device(config.GetPreferredDevice())
	if allowAbsent {
		return nil
	}
	return err
}

var (
	patternStartPos int
	patternEndPos   int
)

// getPatternPosArgs works as a PersistentPreRunE function that sets patternStartPos and patternEndPos from args.
func getPatternPosArgs(cmd *cobra.Command, args []string) error {
	// default start-end is 0-0
	if len(args) == 0 {
		patternStartPos = 0
		patternEndPos = 0
		return nil
	}

	// split by "-"
	var err error
	as := strings.SplitN(args[0], "-", 2)
	if len(as) == 1 {
		// only start
		if patternStartPos, err = strconv.Atoi(as[0]); err != nil {
			return err
		}
		patternEndPos = 0
		return nil
	} else if len(as) == 2 {
		if patternStartPos, err = strconv.Atoi(as[0]); err != nil {
			return err
		}
		if patternEndPos, err = strconv.Atoi(as[1]); err != nil {
			return err
		}
	}
	return nil
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

var (
	outputJSONPath string
	outputTextPath string
	saveJSONData   interface{}
	saveTextLine   []string
)

func saveResultData(cmd *cobra.Command, args []string) error {
	if saveJSONData != nil && ystring.IsNotEmpty(outputJSONPath) {
		return exchange.SaveAsJSON(saveJSONData, outputPath)
	}
	if len(saveTextLine) > 0 && ystring.IsNotEmpty(outputTextPath) {
		return exchange.SaveAsLine(saveTextLine, outputPath)
	}
	return nil
}
