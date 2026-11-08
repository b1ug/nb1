//go:build darwin || linux
// +build darwin linux

package ownership

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func checkPrivateDirectory(info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("ownership directory must be private (0700), got %04o", info.Mode().Perm())
	}
	return nil
}

func openLockFile(path string) (*os.File, error) {
	// NONBLOCK also prevents an unexpected FIFO at this path from stalling
	// Acquire before the common regular-file check can reject it.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func lock(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrBusy
		}
		return err
	}
}

func unlock(file *os.File) error {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}
