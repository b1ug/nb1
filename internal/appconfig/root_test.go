package appconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/userconfig"
)

func TestConfigurationLibraryShareXDGWhileOwnershipKeepsNativeRoot(t *testing.T) {
	isolated := t.TempDir()
	root := filepath.Join(isolated, "user-config")
	t.Setenv("HOME", filepath.Join(isolated, "home"))
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", root)
	} else {
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	app, err := userconfig.AppDir()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Select("", "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := Load(selected)
	if err != nil {
		t.Fatal(err)
	}
	locks, err := ownership.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	native, err := userconfig.NativeAppDir()
	if err != nil {
		t.Fatal(err)
	}
	if app != filepath.Join(root, "nb1") || selected.BaseDir != app || selected.Path != filepath.Join(app, Filename) || settings.LibraryDir != filepath.Join(app, "library") || locks != filepath.Join(native, "device-locks") {
		t.Fatalf("split roots: app=%q selection=%+v library=%q locks=%q", app, selected, settings.LibraryDir, locks)
	}
	if runtime.GOOS == "darwin" && native != filepath.Join(isolated, "home", "Library", "Application Support", "nb1") {
		t.Fatalf("macOS coordination root moved: %q", native)
	}
	// An explicitly selected file elsewhere does not alter the storage root.
	selected, err = Select(filepath.Join(isolated, "separate", "other.yaml"), "")
	if err != nil || selected.BaseDir != app {
		t.Fatalf("custom file relocated storage: %+v %v", selected, err)
	}
	entries, err := os.ReadDir(isolated)
	if err != nil || len(entries) != 0 {
		t.Fatalf("path resolution created storage: %v %v", entries, err)
	}
}

func TestInvalidXDGStopsSelectionBeforeAnyUSB(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows retains its native configuration root")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative-root")
	t.Setenv("HOME", filepath.Join(t.TempDir(), "isolated-home"))
	if _, err := Select("", ""); err == nil {
		t.Fatal("relative XDG accepted for default configuration")
	}
	if _, err := Select(filepath.Join(t.TempDir(), "explicit.yaml"), ""); err == nil {
		t.Fatal("explicit config file hid invalid XDG storage root")
	}
	if runtime.GOOS == "linux" {
		if _, err := ownership.DefaultDir(); err == nil {
			t.Fatal("relative native Linux root accepted for ownership")
		}
		if lease, err := ownership.Acquire("", "isolated-test-device"); err == nil || lease != nil {
			if lease != nil {
				lease.Close()
			}
			t.Fatalf("invalid native root acquired a lock: %v", err)
		}
	} else if _, err := ownership.DefaultDir(); err != nil {
		t.Fatalf("macOS native coordination incorrectly followed XDG: %v", err)
	}
}
