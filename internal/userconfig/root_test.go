package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacAndLinuxUseTheSameXDGPolicy(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	custom := filepath.Join(t.TempDir(), "custom with space", "100%", "$literal")
	for _, platform := range []string{"darwin", "linux"} {
		for _, xdg := range []string{"", custom} {
			homeCalls := 0
			root, err := resolveRoot(platform, xdg, func() (string, error) {
				homeCalls++
				return home, nil
			}, func() (string, error) {
				t.Fatal("Unix resolution used the native macOS/Windows application root")
				return "", nil
			})
			want, calls := filepath.Join(home, ".config"), 1
			if xdg != "" {
				want, calls = custom, 0
			}
			if err != nil || root != want || homeCalls != calls {
				t.Fatalf("%s %q: root=%q homeCalls=%d error=%v", platform, xdg, root, homeCalls, err)
			}
		}
	}
}

func TestRelativeOrMalformedXDGNeverFallsBackToHomeOrCWD(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		for _, xdg := range []string{"relative", "../relative", "~/.config", " ", filepath.Join(t.TempDir(), "bad") + "\x00", filepath.Join(t.TempDir(), "bad") + "\n", filepath.Join(t.TempDir(), "bad") + string([]byte{0xff})} {
			_, err := resolveRoot(platform, xdg, func() (string, error) { t.Fatal("invalid explicit XDG fell back to home"); return "", nil }, func() (string, error) { t.Fatal("invalid explicit XDG fell back to native storage"); return "", nil })
			if err == nil || !strings.Contains(err.Error(), "XDG_CONFIG_HOME") {
				t.Fatalf("%s accepted %q: %v", platform, xdg, err)
			}
		}
	}
}

func TestWindowsRetainsNativeLocationAndIgnoresXDG(t *testing.T) {
	native := filepath.Join(t.TempDir(), "native-roaming")
	root, err := resolveRoot("windows", "relative-XDG-is-irrelevant", func() (string, error) { t.Fatal("Windows used Unix home fallback"); return "", nil }, func() (string, error) { return native, nil })
	if err != nil || root != native {
		t.Fatalf("root=%q error=%v", root, err)
	}
}

func TestHomeAndNativeLookupErrorsAreNotHidden(t *testing.T) {
	want := errors.New("missing user location")
	for _, platform := range []string{"darwin", "linux", "windows"} {
		if _, err := resolveRoot(platform, "", func() (string, error) { return "", want }, func() (string, error) { return "", want }); !errors.Is(err, want) {
			t.Fatalf("%s hid lookup failure: %v", platform, err)
		}
	}
	if _, err := resolveRoot("darwin", "", func() (string, error) { return "relative-home", nil }, nil); err == nil {
		t.Fatal("relative HOME became relative storage")
	}
}

func TestPublicResolverCreatesNothing(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", filepath.Join(root, "roaming"))
	} else {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	}
	base, err := RootDir()
	if err != nil {
		t.Fatal(err)
	}
	app, err := AppDir()
	if err != nil || app != filepath.Join(base, "nb1") {
		t.Fatalf("app=%q base=%q error=%v", app, base, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolution created files: %v %v", entries, err)
	}
}
