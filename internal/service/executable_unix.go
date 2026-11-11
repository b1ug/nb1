//go:build darwin || linux
// +build darwin linux

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// ResolveBoundExecutable validates a deliberately updatable package entry.
// Fixed-path installations do not use this policy. The package rack remains
// canonical and a target must stay inside it after every link resolution.
func ResolveBoundExecutable(binary, root string, uid int) (string, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(root) || filepath.Clean(binary) != binary || filepath.Clean(root) != root || root == "/" {
		return "", fmt.Errorf("invalid package executable binding")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || canonicalRoot != root {
		return "", fmt.Errorf("package executable root is missing or no longer canonical")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm()&0002 != 0 {
		return "", fmt.Errorf("package executable root must be a protected directory")
	}
	target, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", fmt.Errorf("package executable is unavailable: %w", err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("package executable escaped its managed installation directory")
	}
	if err := trustedExecutableAncestors(binary, uid); err != nil {
		return "", err
	}
	entry, err := os.Lstat(binary)
	if err != nil || (!ownedBy(entry, uid) && !ownedBy(entry, 0)) {
		return "", fmt.Errorf("package executable entry has an untrusted owner")
	}
	if err := trustedExecutableAncestors(target, uid); err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
		return "", fmt.Errorf("package executable must be an owned, protected executable file")
	}
	return target, nil
}

func trustedExecutableAncestors(path string, uid int) error {
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil || (!info.IsDir() && info.Mode()&os.ModeSymlink == 0) || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
			return fmt.Errorf("package executable has an untrusted directory or link: %s", directory)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			// macOS Homebrew's Cellar and opt directories are normally writable
			// by the system admin group. This trusts that existing administrator
			// authority; it does not grant new permissions or allow shared files.
			administratorGroup := false
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				administratorGroup = runtime.GOOS == "darwin" && stat.Gid == 80
			}
			stickyRoot := ownedBy(info, 0) && info.Mode()&os.ModeSticky != 0
			if !stickyRoot && (info.Mode().Perm()&0002 != 0 || (info.Mode().Perm()&0020 != 0 && !administratorGroup)) {
				return fmt.Errorf("package executable directory is writable by untrusted users: %s", directory)
			}
		}
		if directory == filepath.Dir(directory) {
			return nil
		}
	}
}
