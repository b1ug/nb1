//go:build linux || darwin
// +build linux darwin

package systemduser

import (
	"os"
	"path/filepath"
	"syscall"
)

func ownedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func publish(path string, content []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".nb1-systemd-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	info, statErr := f.Stat()
	defer func() {
		if now, err := os.Lstat(name); err == nil && statErr == nil && os.SameFile(info, now) {
			_ = os.Remove(name)
		}
	}()
	if statErr != nil {
		f.Close()
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
	return os.Link(name, path) // atomic publication; refuses any existing target
}
