package remoteaccess

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin extended ACLs can grant access even when group and other mode bits
// are clear. Read security attributes from the open descriptor.
func privateTokenACL(file *os.File) bool {
	var volume unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &volume); err != nil || volume.Flags&unix.MNT_LOCAL == 0 ||
		volume.Flags&unix.MNT_IGNORE_OWNERSHIP != 0 {
		return false
	}
	attrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	var buffer [8192]byte
	_, _, errno := syscall.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), unix.FSOPT_REPORT_FULLSIZE, 0)
	runtime.KeepAlive(file)
	runtime.KeepAlive(&attrs)
	if errno != 0 {
		return false
	}
	size := binary.LittleEndian.Uint32(buffer[:4])
	common := binary.LittleEndian.Uint32(buffer[4:8])
	offset := binary.LittleEndian.Uint32(buffer[24:28])
	length := binary.LittleEndian.Uint32(buffer[28:32])
	switch common {
	case unix.ATTR_CMN_RETURNED_ATTRS:
		return size == 32 && offset == 0 && length == 0
	case unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY:
		const noACL = 0xffffffff
		return size == 76 && offset == 8 && length == 44 &&
			binary.LittleEndian.Uint32(buffer[32:36]) == 0x012cc16d &&
			binary.LittleEndian.Uint32(buffer[68:72]) == noACL &&
			binary.LittleEndian.Uint32(buffer[72:76]) == 0
	default:
		return false
	}
}
