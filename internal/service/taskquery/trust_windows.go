package taskquery

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel               = syscall.NewLazyDLL("kernel32.dll")
	security             = syscall.NewLazyDLL("advapi32.dll")
	getSystemDirectory   = kernel.NewProc("GetSystemDirectoryW")
	localFree            = kernel.NewProc("LocalFree")
	getNamedSecurityInfo = security.NewProc("GetNamedSecurityInfoW")
	getAce               = security.NewProc("GetAce")
	convertSID           = security.NewProc("ConvertSidToStringSidW")
	isValidSID           = security.NewProc("IsValidSid")
)

func systemDirectory() (string, error) {
	buffer := make([]uint16, 512)
	count, _, _ := getSystemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if count == 0 || count > 32768 {
		return "", ErrTrust
	}
	if count >= uintptr(len(buffer)) {
		buffer = make([]uint16, count+1)
		count, _, _ = getSystemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if count == 0 || count >= uintptr(len(buffer)) {
			return "", ErrTrust
		}
	}
	return syscall.UTF16ToString(buffer[:count]), nil
}

func checkSystemFiles(paths []string) error {
	if len(paths) < 4 {
		return ErrTrust
	}
	for index, path := range paths {
		name, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			return ErrTrust
		}
		attributes, err := syscall.GetFileAttributes(name)
		if err != nil || attributes&0x400 != 0 {
			return ErrTrust
		} // FILE_ATTRIBUTE_REPARSE_POINT
		directory := index != len(paths)-1
		if (attributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
			return ErrTrust
		}
		owner, entries, err := fileACL(name)
		if err != nil || !protectedACL(owner, entries, directory) {
			return ErrTrust
		}
	}
	return nil
}

func sidString(sid unsafe.Pointer) (string, error) {
	if sid == nil {
		return "", ErrTrust
	}
	valid, _, _ := isValidSID.Call(uintptr(sid))
	if valid == 0 {
		return "", ErrTrust
	}
	var text *uint16
	ok, _, _ := convertSID.Call(uintptr(sid), uintptr(unsafe.Pointer(&text)))
	if ok == 0 || text == nil {
		return "", ErrTrust
	}
	defer localFree.Call(uintptr(unsafe.Pointer(text)))
	// SIDs are bounded by SID_MAX_SUB_AUTHORITIES (15); 256 UTF-16 units
	// conservatively exceed their documented textual representation.
	var units []uint16
	for index := uintptr(0); index < 256; index++ {
		unit := *(*uint16)(unsafe.Add(unsafe.Pointer(text), index*2))
		if unit == 0 {
			return syscall.UTF16ToString(units), nil
		}
		units = append(units, unit)
	}
	return "", ErrTrust
}

func fileACL(name *uint16) (string, []accessEntry, error) {
	var owner, dacl, descriptor unsafe.Pointer
	// SE_FILE_OBJECT=1, OWNER_SECURITY_INFORMATION|DACL_SECURITY_INFORMATION=5.
	code, _, _ := getNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), 1, 5,
		uintptr(unsafe.Pointer(&owner)), 0, uintptr(unsafe.Pointer(&dacl)), 0,
		uintptr(unsafe.Pointer(&descriptor)))
	runtime.KeepAlive(name)
	if descriptor != nil {
		defer localFree.Call(uintptr(descriptor))
	}
	if code != 0 || descriptor == nil || owner == nil || dacl == nil {
		return "", nil, ErrTrust
	}
	ownerText, err := sidString(owner)
	if err != nil {
		return "", nil, err
	}
	// ACL header: revision/unused, total byte size, ACE count, unused.
	size := *(*uint16)(unsafe.Add(dacl, 2))
	count := *(*uint16)(unsafe.Add(dacl, 4))
	if size < 8 || count > 4096 {
		return "", nil, ErrTrust
	}
	entries := make([]accessEntry, 0, count)
	for index := uint16(0); index < count; index++ {
		var ace unsafe.Pointer
		ok, _, _ := getAce.Call(uintptr(dacl), uintptr(index), uintptr(unsafe.Pointer(&ace)))
		if ok == 0 || uintptr(ace) < uintptr(dacl)+8 || uintptr(ace) > uintptr(dacl)+uintptr(size)-4 {
			return "", nil, ErrTrust
		}
		header := (*[4]byte)(ace)
		length := uint16(header[2]) | uint16(header[3])<<8
		if length < 16 || uintptr(ace)+uintptr(length) > uintptr(dacl)+uintptr(size) {
			return "", nil, ErrTrust
		}
		entry := accessEntry{kind: header[0], flags: header[1]}
		if entry.kind != 0 && entry.kind != 1 {
			return "", nil, ErrTrust
		}
		if entry.kind == 0 || entry.kind == 1 {
			entry.mask = *(*uint32)(unsafe.Add(ace, 4))
			// Check variable-length SID bounds before any native SID function.
			subAuthorities := *(*byte)(unsafe.Add(ace, 9))
			if subAuthorities > 15 || 16+int(subAuthorities)*4 > int(length) {
				return "", nil, ErrTrust
			}
			entry.sid, err = sidString(unsafe.Add(ace, 8))
			if err != nil {
				return "", nil, err
			}
		}
		entries = append(entries, entry)
	}
	return ownerText, entries, nil
}
