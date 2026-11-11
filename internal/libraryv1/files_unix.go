//go:build darwin || linux
// +build darwin linux

package libraryv1

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func privateMode(info os.FileInfo) bool {
	if info.IsDir() {
		return info.Mode().Perm() == 0700
	}
	return info.Mode().Perm() == 0600
}

func openRecord(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func replaceRecord(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
