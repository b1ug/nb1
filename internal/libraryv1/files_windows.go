//go:build windows
// +build windows

package libraryv1

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Windows enforces inherited directory ACLs rather than POSIX permission bits.
func privateMode(os.FileInfo) bool { return true }

func openRecord(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("%w: record must be a regular file", ErrInvalid)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func replaceRecord(from, to string) error {
	old, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(old, name, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
