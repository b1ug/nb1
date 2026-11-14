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
	if err := trustedExecutablePath(binary, uid); err != nil {
		return "", err
	}
	entry, err := os.Lstat(binary)
	if err != nil || (!ownedBy(entry, uid) && !ownedBy(entry, 0)) {
		return "", fmt.Errorf("package executable entry has an untrusted owner")
	}
	if err := trustedExecutablePath(target, uid); err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
		return "", fmt.Errorf("package executable must be an owned, protected executable file")
	}
	return target, nil
}

func trustedExecutablePath(path string, uid int) error {
	links := 40
	_, err := trustedPackagePath(path, uid, &links)
	return err
}

// Check each link's traversal, not just its final target. A link out of a
// private directory cannot carry its access protection to a public directory.
func trustedPackagePath(path string, uid int, links *int) (bool, error) {
	private := false
	directory := "/"
	parts := strings.Split(path, "/")
	for i, part := range parts {
		directory = filepath.Join(directory, part)
		info, err := os.Lstat(directory)
		if err != nil || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
			return false, fmt.Errorf("package executable has an untrusted path: %s", directory)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if *links <= 0 {
				return false, fmt.Errorf("package executable has too many symbolic links")
			}
			*links = *links - 1
			target, err := os.Readlink(directory)
			if err != nil {
				return false, err
			}
			// Leading ../ is used by Homebrew's opt links. A parent step
			// after a named component could traverse an unchecked link before
			// returning; lexical Clean would erase that traversal.
			named := false
			for _, component := range strings.Split(target, "/") {
				if component == ".." && named {
					return false, fmt.Errorf("package executable link contains ambiguous parent traversal")
				}
				if component != "" && component != "." && component != ".." {
					named = true
				}
			}
			if !filepath.IsAbs(target) {
				parent, err := filepath.EvalSymlinks(filepath.Dir(directory))
				if err != nil {
					return false, err
				}
				target = filepath.Join(parent, target)
			}
			private, err = trustedPackagePath(filepath.Clean(target), uid, links)
			if err != nil {
				return false, err
			}
			continue
		}
		if !info.IsDir() {
			if i != len(parts)-1 || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
				return false, fmt.Errorf("package executable must be an owned, protected executable file")
			}
			continue
		}
		// macOS Homebrew normally grants directory writes to admin (gid 80).
		// Below a caller-owned 0700 boundary, other UIDs cannot traverse to
		// group-writable directories created by an ordinary build's umask.
		administratorGroup := false
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			administratorGroup = runtime.GOOS == "darwin" && stat.Gid == 80
		}
		stickyRoot := ownedBy(info, 0) && info.Mode()&os.ModeSticky != 0
		if !stickyRoot && (info.Mode().Perm()&0002 != 0 || (info.Mode().Perm()&0020 != 0 && !administratorGroup && !private)) {
			return false, fmt.Errorf("package executable directory is writable by untrusted users: %s", directory)
		}
		if ownedBy(info, uid) && info.Mode().Perm()&0077 == 0 {
			private = true
		}
	}
	return private, nil
}
