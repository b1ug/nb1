//go:build windows
// +build windows

package ownership

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func checkPrivateDirectory(info os.FileInfo) error {
	// Windows privacy is controlled by ACLs, not Unix permission bits. The
	// default directory inherits the current user's application-directory ACL.
	return nil
}

func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
}

func lock(file *os.File) error {
	var overlap windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

func unlock(file *os.File) error {
	var overlap windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlap)
}
