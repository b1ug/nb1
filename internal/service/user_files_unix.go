//go:build darwin || linux
// +build darwin linux

package service

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func ownedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}

func openManaged(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func createManaged(path string, content []byte) error {
	// Write and sync the complete private staging file before publishing a
	// hard link. Link fails if *anything* already occupies path and never
	// replaces it; launchd cannot observe a partially written final plist.
	f, err := os.CreateTemp(filepath.Dir(path), ".nb1-user-service-*.tmp")
	if err != nil {
		return err
	}
	temporary := f.Name()
	info, statErr := f.Stat()
	defer func() {
		if current, err := os.Lstat(temporary); err == nil && statErr == nil && os.SameFile(info, current) {
			_ = os.Remove(temporary)
		}
	}()
	if statErr != nil {
		_ = f.Close()
		return statErr
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Link(temporary, path)
}
