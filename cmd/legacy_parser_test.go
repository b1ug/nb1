package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// The sentinel stops every valid command at the open boundary. These tests
// never enumerate USB devices or construct a controller.
func captureLegacyOpen(t *testing.T) (*int, error) {
	t.Helper()
	previousOpen, previousConfig, previousAbsent, previousLog := openBasicDevice, configurationInitError, allowAbsent, log
	calls := new(int)
	stopped := errors.New("test stopped before USB access")
	openBasicDevice = func(string) error { *calls++; return stopped }
	configurationInitError, allowAbsent, log = nil, false, zap.NewNop().Sugar()
	t.Cleanup(func() {
		openBasicDevice, configurationInitError, allowAbsent, log = previousOpen, previousConfig, previousAbsent, previousLog
	})
	return calls, stopped
}

func TestLegacyStateCommandsParseBeforeOpeningUSB(t *testing.T) {
	oldFade, oldFadeLED := fadeTimeDur, fadeLedNum
	oldBlink, oldBlinkLED, oldTimes := blinkTimeDur, blinkLedNum, blinkTimes
	t.Cleanup(func() {
		fadeTimeDur, fadeLedNum = oldFade, oldFadeLED
		blinkTimeDur, blinkLedNum, blinkTimes = oldBlink, oldBlinkLED, oldTimes
	})
	fadeTimeDur, fadeLedNum = 20*time.Millisecond, 0
	blinkTimeDur, blinkLedNum, blinkTimes = 20*time.Millisecond, 0, 1
	for _, tc := range []struct {
		name    string
		command *cobra.Command
		args    []string
		valid   bool
	}{
		{"act trailing input", actCmd, []string{"set", "red", "nonsense"}, false},
		{"act hold not fade", actCmd, []string{"set red for 2s"}, false},
		{"act invalid precision", actCmd, []string{"#FF0000L1T15"}, false},
		{"act valid original case", actCmd, []string{"FaDe to HsV(210,75,90) over 20ms"}, true},
		{"fade invalid color", fadeCmd, []string{"red extra"}, false},
		{"fade off is action", fadeCmd, []string{"off"}, false},
		{"fade full RGB", fadeCmd, []string{"RGB(1, 2, 3)"}, true},
		{"fade random keyword", fadeCmd, []string{"RANDOM"}, true},
		{"blink bad second color", blinkCmd, []string{"red", "not-a-color"}, false},
		{"blink explicit black", blinkCmd, []string{"black"}, false},
		{"blink valid full colors", blinkCmd, []string{"red", "HSV(210,75,90)"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, stopped := captureLegacyOpen(t)
			if tc.command.PersistentPreRunE != nil {
				t.Fatal("command retains an early device-open hook")
			}
			err := tc.command.RunE(tc.command, tc.args)
			if tc.valid {
				if *calls != 1 || !errors.Is(err, stopped) {
					t.Fatalf("valid input missed open boundary: calls=%d err=%v", *calls, err)
				}
			} else if *calls != 0 || err == nil || errors.Is(err, stopped) {
				t.Fatalf("invalid input touched USB: calls=%d err=%v", *calls, err)
			}
		})
	}
	t.Run("fade invalid typed duration", func(t *testing.T) {
		calls, stopped := captureLegacyOpen(t)
		fadeTimeDur = time.Millisecond
		err := fadeCmd.RunE(fadeCmd, []string{"red"})
		if *calls != 0 || err == nil || errors.Is(err, stopped) {
			t.Fatalf("duration reached USB: calls=%d err=%v", *calls, err)
		}
	})
}

func TestLegacyFileCommandsValidateEntireFileBeforeUSB(t *testing.T) {
	oldReset, oldFlash, oldPreview := writeResetAll, writeSaveFlash, writePreviewPattern
	oldChanged := executeCmd.Flag("times").Changed
	t.Cleanup(func() {
		writeResetAll, writeSaveFlash, writePreviewPattern = oldReset, oldFlash, oldPreview
		executeCmd.Flag("times").Changed = oldChanged
	})
	writeResetAll, writeSaveFlash, writePreviewPattern = false, false, false
	executeCmd.Flag("times").Changed = false
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"valid.txt", "title: Desk 夜灯\nrepeat: 2\nfade #abc over 20ms on led 1\noff", true},
		{"invalid.txt", "set red\nunknown words", false},
		{"precision.json", `{"repeat":1,"seq":"#FF0000L1T15"}`, false},
		{"repeat.json", `{"repeat":256,"seq":"#FF0000L1T20"}`, false},
	} {
		path := filepath.Join(t.TempDir(), tc.name)
		if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []*cobra.Command{executeCmd, writeCmd} {
			t.Run(command.Name()+"/"+tc.name, func(t *testing.T) {
				calls, stopped := captureLegacyOpen(t)
				err := command.RunE(command, []string{path})
				if tc.valid {
					if *calls != 1 || !errors.Is(err, stopped) {
						t.Fatalf("valid file missed open boundary: calls=%d err=%v", *calls, err)
					}
				} else if *calls != 0 || err == nil || errors.Is(err, stopped) {
					t.Fatalf("bad file touched USB: calls=%d err=%v", *calls, err)
				}
			})
		}
	}
	t.Run("write no action", func(t *testing.T) {
		calls, _ := captureLegacyOpen(t)
		if err := writeCmd.RunE(writeCmd, nil); !errors.Is(err, errNoAction) || *calls != 0 {
			t.Fatalf("no-action opened USB: calls=%d err=%v", *calls, err)
		}
	})
}

func TestLegacyConverterValidatesBeforeOpeningOutput(t *testing.T) {
	oldInput, oldOutput, oldPreview := inputPath, outputPath, convertPreviewPattern
	t.Cleanup(func() { inputPath, outputPath, convertPreviewPattern = oldInput, oldOutput, oldPreview })
	convertPreviewPattern = false
	for _, content := range []string{
		`{"repeat":1,"seq":"#FF0000L1T15"}`,
		`{"repeat":256,"seq":"#FF0000L1T20"}`,
		`{"name":"bad\nname","repeat":1,"seq":"#FF0000L1T20"}`,
	} {
		dir := t.TempDir()
		inputPath = filepath.Join(dir, "pattern.json")
		if err := os.WriteFile(inputPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		for _, exists := range []bool{false, true} {
			outputPath = filepath.Join(dir, "output.txt")
			if exists {
				if err := os.WriteFile(outputPath, []byte("keep this file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := convertJSON2TextCmd.RunE(convertJSON2TextCmd, nil); err == nil {
				t.Fatal("invalid data exported")
			}
			got, err := os.ReadFile(outputPath)
			if exists {
				if err != nil || string(got) != "keep this file" {
					t.Fatalf("existing output changed: %q %v", got, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid export created output: %q %v", got, err)
			}
		}
	}
}
