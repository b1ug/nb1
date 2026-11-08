package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateLEDSelection(t *testing.T) {
	for _, led := range []uint{0, 1, 2} {
		t.Run(strconv.FormatUint(uint64(led), 10), func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Uint("led", led, "")
			if err := validateLEDSelection(cmd); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := validateLEDSelection(&cobra.Command{Use: "without-led"}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidLEDSelectionCommands(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "setting.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"fade", "red"}, {"blink", "red"}, {"read", "state"}} {
		for _, led := range []string{"3", "256", "-1"} {
			t.Run(strings.Join(command, " ")+"/"+led, func(t *testing.T) {
				args := []string{"-test.run=^TestLEDSelectionCommandHelper$", "--", "--config", configPath,
					// A regression must never select an attached device, even on a developer's machine.
					"--device", "nb1-test-nonexistent-device", "--allow-absent"}
				args = append(args, command...)
				args = append(args, "--led", led)
				proc := exec.Command(bin, args...)
				proc.Env = append(os.Environ(), "NB1_LED_TEST_HELPER=1")
				out, err := proc.CombinedOutput()
				if err == nil {
					t.Fatalf("command accepted --led %s: %s", led, out)
				}
				want := "invalid LED index " + led
				if led == "-1" {
					want = "invalid argument \"-1\" for \"-l, --led\" flag"
				}
				if !strings.Contains(string(out), want) {
					t.Fatalf("output = %s, want %q", out, want)
				}
			})
		}
	}
}

// Execute each command in a separate process to isolate Cobra's global flags and
// initializers. Invalid selections must fail even when --allow-absent is set.
func TestLEDSelectionCommandHelper(t *testing.T) {
	if os.Getenv("NB1_LED_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			rootCmd.SetArgs(os.Args[i+1:])
			if err := rootCmd.Execute(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	t.Fatal("missing command arguments")
}
