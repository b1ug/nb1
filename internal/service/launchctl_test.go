package service

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestLaunchctlOutputIsBoundedWithoutShortWrites(t *testing.T) {
	var output boundedOutput
	input := strings.Repeat("x", 100<<10)
	if count, err := output.Write([]byte(input)); err != nil || count != len(input) || !output.truncated || len(output.data) != 64<<10 {
		t.Fatalf("capture bound: n=%d err=%v size=%d truncated=%t", count, err, len(output.data), output.truncated)
	}
	if _, err := output.Write([]byte("more")); err != nil || len(output.data) != 64<<10 {
		t.Fatal("overflow affected command execution")
	}
}

func TestNativeUserServiceIsExplicitlyUnsupportedElsewhere(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("constructor rejection applies to other platforms")
	}
	if _, err := NewUserManager(); !errors.Is(err, ErrUserServiceUnsupported) {
		t.Fatalf("unsupported platform reached native manager: %v", err)
	}
}
