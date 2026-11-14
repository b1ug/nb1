//go:build darwin
// +build darwin

package patternstore

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin ACLs can grant access beyond mode 0700/0600. Query only opened fds;
// no path-based shell command, cgo, ACL rewriting or administrative right is
// used. The ABI is defined by the installed Apple sys/attr.h and sys/kauth.h:
// attrlist is 24 bytes; returned attribute_set_t is five uint32 fields;
// attrreference_t is int32 offset/uint32 length, relative to the reference.
// See getattrlist(2), ATTR_CMN_EXTENDED_SECURITY, FSOPT_REPORT_FULLSIZE.
var (
	_ [24 - int(unsafe.Sizeof(unix.Attrlist{}))]byte
	_ [int(unsafe.Sizeof(unix.Attrlist{})) - 24]byte
)

func checkNoExtendedACL(file *os.File) error {
	if err := checkDarwinVolume(file); err != nil {
		return err
	}
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	data, err := darwinAttributes(file, &attrs)
	if err != nil {
		return err
	}
	return parseDarwinNoACL(data)
}

// Without a volume capability check, an unsupported security attribute can be
// indistinguishable from a supported attribute on an object without an ACL.
func checkDarwinVolume(file *os.File) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &fs); err != nil {
		return fmt.Errorf("%w: inspect library volume: %v", ErrUnsafe, err)
	}
	if fs.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Errorf("%w: library requires a verified local filesystem", ErrUnsafe)
	}
	name := make([]byte, 0, len(fs.Mntonname))
	terminated := false
	for _, value := range fs.Mntonname {
		if value == 0 {
			terminated = true
			break
		}
		name = append(name, byte(value))
	}
	if !terminated || len(name) == 0 || !filepath.IsAbs(string(name)) {
		return fmt.Errorf("%w: invalid library volume mount point", ErrUnsafe)
	}
	fd, err := unix.Open(string(name), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%w: open library volume: %v", ErrUnsafe, err)
	}
	volume := os.NewFile(uintptr(fd), string(name))
	defer volume.Close()
	var current unix.Statfs_t
	if err := unix.Fstatfs(fd, &current); err != nil || current.Fsid != fs.Fsid {
		return fmt.Errorf("%w: library volume binding cannot be verified", ErrUnsafe)
	}
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS, Volattr: unix.ATTR_VOL_INFO | unix.ATTR_VOL_CAPABILITIES}
	data, err := darwinAttributes(volume, &attrs)
	if err != nil {
		return err
	}
	return parseDarwinVolume(data)
}

func darwinAttributes(file *os.File, attrs *unix.Attrlist) ([]byte, error) {
	// Apple ATTR_MAX_BUFFER is 8192. Report-fullsize detects truncation rather
	// than trusting potentially out-of-range attrreference offsets.
	var buffer [8192]byte
	_, _, errno := syscall.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), unix.FSOPT_REPORT_FULLSIZE, 0)
	runtime.KeepAlive(attrs)
	runtime.KeepAlive(&buffer)
	runtime.KeepAlive(file)
	if errno != 0 {
		return nil, fmt.Errorf("%w: read Darwin security attributes: %v", ErrUnsafe, errno)
	}
	length := binary.LittleEndian.Uint32(buffer[:4])
	if length < 24 || length > uint32(len(buffer)) {
		return nil, fmt.Errorf("%w: invalid or truncated Darwin attributes", ErrUnsafe)
	}
	return buffer[:int(length)], nil
}

func parseDarwinVolume(data []byte) error {
	// length + attribute_set_t + vol_capabilities_attr_t = 4 + 20 + 32.
	if len(data) != 56 || binary.LittleEndian.Uint32(data[:4]) != 56 ||
		binary.LittleEndian.Uint32(data[4:8]) != unix.ATTR_CMN_RETURNED_ATTRS ||
		binary.LittleEndian.Uint32(data[8:12]) != unix.ATTR_VOL_CAPABILITIES ||
		binary.LittleEndian.Uint32(data[12:16]) != 0 || binary.LittleEndian.Uint32(data[16:20]) != 0 || binary.LittleEndian.Uint32(data[20:24]) != 0 {
		return fmt.Errorf("%w: unrecognized Darwin volume attributes", ErrUnsafe)
	}
	// sys/attr.h: VOL_CAPABILITIES_INTERFACES=1; FLOCK=0x200,
	// EXTENDED_SECURITY=0x400. Both support and validity must be affirmative.
	const required = 0x00000600
	capabilities := binary.LittleEndian.Uint32(data[28:32])
	valid := binary.LittleEndian.Uint32(data[44:48])
	if capabilities&required != required || valid&required != required {
		return fmt.Errorf("%w: library volume does not confirm ACL inspection and flock support", ErrUnsafe)
	}
	return nil
}

func parseDarwinNoACL(data []byte) error {
	if len(data) < 32 || uint64(binary.LittleEndian.Uint32(data[:4])) != uint64(len(data)) {
		return fmt.Errorf("%w: invalid Darwin security attribute length", ErrUnsafe)
	}
	common := binary.LittleEndian.Uint32(data[4:8])
	if common != unix.ATTR_CMN_RETURNED_ATTRS && common != unix.ATTR_CMN_RETURNED_ATTRS|unix.ATTR_CMN_EXTENDED_SECURITY {
		return fmt.Errorf("%w: unrecognized Darwin security attribute mask", ErrUnsafe)
	}
	for offset := 8; offset < 24; offset += 4 {
		if binary.LittleEndian.Uint32(data[offset:offset+4]) != 0 {
			return fmt.Errorf("%w: unexpected Darwin security attribute groups", ErrUnsafe)
		}
	}
	offset := int32(binary.LittleEndian.Uint32(data[24:28]))
	length := binary.LittleEndian.Uint32(data[28:32])
	if common == unix.ATTR_CMN_RETURNED_ATTRS {
		// Actual no-ACL objects omit EXTENDED_SECURITY from the returned mask
		// and return a zero reference, despite this attribute being requested.
		if len(data) == 32 && offset == 0 && length == 0 {
			return nil
		}
		return fmt.Errorf("%w: inconsistent Darwin no-ACL reference", ErrUnsafe)
	}
	// There is one variable attribute. Its fixed eight-byte reference starts
	// at offset24; the payload immediately follows it. Widen before addition.
	if offset != 8 || length < 44 || uint64(24)+uint64(offset)+uint64(length) != uint64(len(data)) {
		return fmt.Errorf("%w: invalid or truncated Darwin ACL reference", ErrUnsafe)
	}
	payload := data[32:]
	if binary.LittleEndian.Uint32(payload[:4]) != 0x012cc16d {
		return fmt.Errorf("%w: unrecognized Darwin ACL structure", ErrUnsafe)
	}
	count := binary.LittleEndian.Uint32(payload[36:40])
	flags := binary.LittleEndian.Uint32(payload[40:44])
	if count == 0xffffffff && length == 44 && flags == 0 {
		// KAUTH_FILESEC_NOACL is explicitly different from an empty ACL.
		return nil
	}
	if count > 128 || uint64(length) != uint64(44)+uint64(count)*24 {
		return fmt.Errorf("%w: invalid Darwin ACL entry count", ErrUnsafe)
	}
	return fmt.Errorf("%w: extended ACLs are not accepted in the private library", ErrUnsafe)
}
