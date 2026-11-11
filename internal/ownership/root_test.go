//go:build darwin || linux || windows
// +build darwin linux windows

package ownership

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/b1ug/nb1/internal/userconfig"
)

func TestDefaultRootLockCoordinatesWithExplicitSameDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "isolated-user-root")
	t.Setenv("HOME", filepath.Join(root, "home"))
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", root)
	} else {
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	native, err := userconfig.NativeAppDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(native, "device-locks") {
		t.Fatalf("wrong default ownership root: %q", dir)
	}
	if runtime.GOOS == "darwin" && dir != filepath.Join(root, "home", "Library", "Application Support", "nb1", "device-locks") {
		t.Fatalf("macOS existing lock root moved: %q", dir)
	}
	first, err := Acquire("", "isolated-test-device")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Acquire(dir, "isolated-test-device")
	if !errors.Is(err, ErrBusy) || second != nil {
		if second != nil {
			second.Close()
		}
		t.Fatalf("default and explicit locks split: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir, "isolated-test-device")
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("lock inode disappeared or duplicated: %v %v", files, err)
	}
}
