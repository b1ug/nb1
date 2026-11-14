package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// On native Windows, verify the actual Go process argv parser rather than only
// comparing our generated command-line string with another implementation.
func TestNativeWindowsArgumentRoundTrip(t *testing.T) {
	args := []string{"", `C:\Users\灯's\100%TEMP%\tail space\`, `quote"and\"slashes`, "server", "20005601"}
	command := exec.Command(os.Args[0])
	parts := []string{windowsArgument(os.Args[0]), "-test.run=^TestWindowsArgumentHelper$", "--"}
	for _, arg := range args {
		parts = append(parts, windowsArgument(arg))
	}
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: strings.Join(parts, " ")}
	command.Env = append(os.Environ(), "NB1_SERVICE_ARGV_HELPER=1")
	data, err := command.Output()
	if err != nil {
		t.Fatalf("native argv helper: %v", err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("native argv = %#v, want %#v, decode error %v", got, args, err)
	}
}

func TestWindowsArgumentHelper(t *testing.T) {
	if os.Getenv("NB1_SERVICE_ARGV_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := json.NewEncoder(os.Stdout).Encode(os.Args[i+1:]); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}
