//go:build windows
// +build windows

package appconfig

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func openConfig(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(handle, &info)
	if err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	return os.NewFile(uintptr(handle), path), nil
}

// Windows uses inherited directory ACLs rather than POSIX permission bits.
func privateDirectory(os.FileInfo) bool { return true }
