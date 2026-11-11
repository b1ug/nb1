// Package userconfig resolves per-user application storage without touching the
// filesystem. User configuration and native OS coordination have explicit,
// separate policies so changing a profile location cannot split ownership locks.
package userconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RootDir returns the configuration parent directory. On macOS and Linux it is
// XDG_CONFIG_HOME, or ~/.config when that variable is empty. Windows retains
// os.UserConfigDir's native location. Relative roots are rejected, never made
// absolute against the current working directory.
func RootDir() (string, error) {
	return resolveRoot(runtime.GOOS, os.Getenv("XDG_CONFIG_HOME"), os.UserHomeDir, os.UserConfigDir)
}

// AppDir returns nb1's shared configuration and library root.
// It creates nothing and does not inspect or migrate older storage locations.
func AppDir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "nb1"), nil
}

// NativeAppDir retains nb1's established native OS coordination root. Device
// locks must keep this location while older controllers may still own a handle.
// On macOS it is distinct from AppDir, even with XDG_CONFIG_HOME configured.
func NativeAppDir() (string, error) {
	root, err := resolveRoot("native", "", nil, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "nb1"), nil
}

func resolveRoot(platform, xdg string, homeDir, nativeDir func() (string, error)) (string, error) {
	var root, source string
	var err error
	if platform == "darwin" || platform == "linux" {
		if xdg != "" {
			root, source = xdg, "XDG_CONFIG_HOME"
		} else {
			var home string
			home, err = homeDir()
			if err != nil {
				return "", fmt.Errorf("find user home directory: %w", err)
			}
			if home == "" || !filepath.IsAbs(home) {
				return "", fmt.Errorf("user home directory must be an absolute path")
			}
			root, source = filepath.Join(home, ".config"), "user configuration directory"
		}
	} else {
		root, err = nativeDir()
		if err != nil {
			return "", fmt.Errorf("find native user configuration directory: %w", err)
		}
		source = "native user configuration directory"
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s must be an absolute path", source)
	}
	if !utf8.ValidString(root) || strings.IndexFunc(root, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s must be valid UTF-8 without control characters", source)
	}
	return filepath.Clean(root), nil
}
