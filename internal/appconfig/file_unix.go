//go:build darwin || linux
// +build darwin linux

package appconfig

import (
	"golang.org/x/sys/unix"
	"os"
)

func openConfig(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func privateDirectory(info os.FileInfo) bool { return info.Mode().Perm()&0077 == 0 }
