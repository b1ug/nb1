package remoteaccess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openTokenFile(path string, create bool) (*os.File, error) {
	// Alternate data streams and the device namespace are not token files.
	if strings.ContainsRune(strings.TrimPrefix(path, filepath.VolumeName(path)), ':') ||
		strings.HasPrefix(path, `\\.\`) {
		return nil, errPrivateTokenFile
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errPrivateTokenFile
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, errPrivateTokenFile
	}
	sid := user.User.Sid
	var attributes *windows.SecurityAttributes
	access, share, disposition := uint32(windows.GENERIC_READ), uint32(windows.FILE_SHARE_READ), uint32(windows.OPEN_EXISTING)
	flags := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if create {
		// An explicit protected DACL avoids inheriting grants from the parent.
		descriptor, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;;FA;;;" + sid.String() + ")")
		if err != nil {
			return nil, errPrivateTokenFile
		}
		attributes = &windows.SecurityAttributes{SecurityDescriptor: descriptor}
		attributes.Length = uint32(unsafe.Sizeof(*attributes))
		access, share, disposition = windows.GENERIC_WRITE|windows.READ_CONTROL, 0, windows.CREATE_NEW
	}
	handle, err := windows.CreateFile(name, access, share, attributes, disposition, flags, 0)
	if err != nil {
		return nil, tokenOpenError(err, create)
	}
	file := os.NewFile(uintptr(handle), path)
	if !privateWindowsToken(handle, sid) {
		info, statErr := file.Stat()
		_ = file.Close()
		if create && statErr == nil {
			removeOwnTokenFile(path, info)
		}
		return nil, errPrivateTokenFile
	}
	return file, nil
}

func privateWindowsToken(handle windows.Handle, sid *windows.SID) bool {
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return false
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil ||
		info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 ||
		info.NumberOfLinks != 1 {
		return false
	}
	var volumeFlags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &volumeFlags, nil, 0); err != nil ||
		volumeFlags&windows.FILE_PERSISTENT_ACLS == 0 {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !sid.Equals(owner) {
		return false
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace == nil ||
			ace.Header.AceSize < 16 || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		principal := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !principal.IsValid() || principal.Len() > int(ace.Header.AceSize)-8 {
			return false
		}
		// Built-in administrators and Local System hold equivalent recovery
		// privileges on the machine even when omitted from a private DACL.
		if !sid.Equals(principal) && !principal.IsWellKnown(windows.WinLocalSystemSid) &&
			!principal.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return false
		}
	}
	runtime.KeepAlive(descriptor)
	return true
}
