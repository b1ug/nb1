//go:build darwin || linux

package remoteaccess

import (
	"os"

	"golang.org/x/sys/unix"
)

func openTokenFile(path string, create bool) (*os.File, error) {
	flags := unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_RDONLY
	if create {
		flags = unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Open(path, flags, 0o600)
	if err != nil {
		return nil, tokenOpenError(err, create)
	}
	file := os.NewFile(uintptr(fd), path)
	var permissionErr error
	if create {
		permissionErr = unix.Fchmod(fd, 0o600)
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || permissionErr != nil || info.Mode&unix.S_IFMT != unix.S_IFREG ||
		info.Uid != uint32(os.Geteuid()) || info.Mode&0o077 != 0 || info.Nlink != 1 || !privateTokenACL(file) {
		createdInfo, statErr := file.Stat()
		_ = file.Close()
		if create && statErr == nil {
			removeOwnTokenFile(path, createdInfo)
		}
		return nil, errPrivateTokenFile
	}
	return file, nil
}
