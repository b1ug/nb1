package cmd

import (
	"errors"
	"fmt"
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
	errInvalidColor      = errors.New("color is invalid")
)

// openBlink1Device works as a PersistentPreRunE function that opens a blink(1) device for use.
func openBlink1Device(cmd *cobra.Command, args []string) error {
	if err := validateLEDSelection(cmd); err != nil {
		return err
	}
	err := hdwr.OpenBlink1Device(config.GetPreferredDevice())
	if allowAbsent {
		return nil
	}
	return err
}

// validateLEDSelection checks LED flags before a device is opened or a uint is
// converted to the SDK's byte-sized LEDIndex.
func validateLEDSelection(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("led") == nil {
		return nil
	}
	led, err := cmd.Flags().GetUint("led")
	if err != nil {
		return err
	}
	if led > 2 {
		return fmt.Errorf("invalid LED index %d: --led must be 0 (all), 1 (top), or 2 (bottom)", led)
	}
	return nil
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
		if err := exchange.SaveAsJSON(saveJSONData, outputJSONPath); err != nil {
			return err
		}
	}
	if len(saveTextLine) > 0 && ystring.IsNotEmpty(outputTextPath) {
		return exchange.SaveAsLine(saveTextLine, outputTextPath)
	}
	return nil
}
