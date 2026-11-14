//go:build windows
// +build windows

package patternstore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	winSnapshotName = "patterns.json"
	winLockName     = "writer.lock"
	winFullAccess   = uint32(0x001f01ff)
)

var winGetAce = windows.NewLazySystemDLL("advapi32.dll").NewProc("GetAce")

// All subordinate opens and publication are relative to dirHandle. The root
// and persistent lock omit FILE_SHARE_DELETE. This coordinates cooperating
// writers; it is not a sandbox against another process with the same user SID.
type fsTxn struct {
	dir        string
	dirHandle  windows.Handle
	dirInfo    windows.ByHandleFileInformation
	lockHandle windows.Handle
	lockInfo   windows.ByHandleFileInformation
	userSID    string
	privateSD  *windows.SECURITY_DESCRIPTOR
	closed     bool
	locked     bool
	readDone   bool
	readLimit  int
	readBytes  []byte
	readInfo   *windows.ByHandleFileInformation
}

func openFS(dir string, create bool) (_ *fsTxn, resultErr error) {
	if err := winLocalPath(dir); err != nil {
		return nil, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	userSID := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + userSID + "D:P(A;OICI;FA;;;" + userSID + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return nil, err
	}
	if err := winPrepareDirectory(dir, create, sd, userSID); err != nil {
		return nil, err
	}
	parent, err := winResolveParent(filepath.Dir(dir), filepath.EvalSymlinks)
	if err != nil {
		return nil, err
	}
	parentHandle, err := winOpenDirectory(parent)
	if err != nil {
		return nil, err
	}
	parentGuard := fsTxn{userSID: userSID}
	_, parentErr := parentGuard.checkHandle(parentHandle, true, true)
	_ = windows.CloseHandle(parentHandle)
	if parentErr != nil {
		return nil, parentErr
	}
	// Resolve an existing parent (e.g. a Windows profile junction), but never
	// resolve the final managed directory itself through a reparse point.
	dir = filepath.Join(parent, filepath.Base(dir))
	handle, err := winOpenDirectory(dir)
	if err != nil {
		return nil, err
	}
	txn := &fsTxn{dir: dir, dirHandle: handle, userSID: userSID, privateSD: sd}
	defer func() {
		if resultErr != nil {
			_ = txn.close()
		}
	}()
	if txn.dirInfo, err = txn.checkHandle(handle, true, false); err != nil {
		return nil, err
	}
	if err := winCheckVolume(handle); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(dir, winLockName)); errors.Is(err, os.ErrNotExist) {
		// Do not add our first lock file to an unrelated nonempty directory.
		// An existing writer's temporary file is checked only after its lock
		// has been acquired, so the normal concurrent result is ErrBusy.
		if err := txn.checkEntries(""); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	txn.lockHandle, err = txn.openRelative(winLockName, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_OPEN_IF, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sd)
	if err != nil {
		return nil, err
	}
	if txn.lockInfo, err = txn.checkHandle(txn.lockHandle, false, false); err != nil {
		return nil, err
	}
	var overlap windows.Overlapped
	if err := windows.LockFileEx(txn.lockHandle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrBusy
		}
		return nil, err
	}
	txn.locked = true
	if err := txn.checkBinding(""); err != nil {
		return nil, err
	}
	return txn, nil
}

func (t *fsTxn) read(maxBytes int) ([]byte, error) {
	t.readDone = false
	if maxBytes < 1 {
		return nil, fmt.Errorf("%w: snapshot read limit must be positive", ErrUnsafe)
	}
	if err := t.checkBinding(""); err != nil {
		return nil, err
	}
	data, info, err := t.readSnapshot(maxBytes)
	if err != nil {
		return nil, err
	}
	if err := t.checkBinding(""); err != nil {
		return nil, err
	}
	t.readDone, t.readInfo = true, info
	t.readLimit = maxBytes
	t.readBytes = append([]byte(nil), data...)
	return data, nil
}

func (t *fsTxn) publish(data []byte) (published bool, resultErr error) {
	if !t.readDone {
		return false, fmt.Errorf("%w: publication requires a successful snapshot read", ErrUnsafe)
	}
	if len(data) > t.readLimit {
		return false, fmt.Errorf("%w: publication exceeds validated snapshot limit", ErrUnsafe)
	}
	if err := t.checkBinding(""); err != nil {
		return false, err
	}
	if err := t.unchangedSnapshot(); err != nil {
		return false, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, err
	}
	name := ".pattern-" + hex.EncodeToString(random[:]) + ".tmp"
	handle, err := t.openRelative(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE|windows.READ_CONTROL, windows.FILE_CREATE, windows.FILE_SHARE_READ, t.privateSD)
	if err != nil {
		return false, err
	}
	file := os.NewFile(uintptr(handle), filepath.Join(t.dir, name))
	defer func() {
		if !published {
			// This is the still-held handle returned by FILE_CREATE. Mark only
			// that object for deletion before closing it: never reopen a name
			// after releasing the identity, and never delete a published file.
			remove := [4]byte{1} // FILE_DISPOSITION_INFO contains a Win32 BOOL.
			if cleanupErr := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &remove[0], uint32(len(remove))); cleanupErr != nil {
				if resultErr == nil {
					resultErr = cleanupErr
				} else {
					resultErr = fmt.Errorf("%w; temporary cleanup: %v", resultErr, cleanupErr)
				}
			}
		}
		if closeErr := file.Close(); closeErr != nil && resultErr == nil {
			resultErr = closeErr
		}
	}()
	info, err := t.checkHandle(handle, false, false)
	if err != nil {
		return false, err
	}
	if _, err := file.Write(data); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := t.checkBinding(name); err != nil {
		return false, err
	}
	if err := t.unchangedSnapshot(); err != nil {
		return false, err
	}
	// Rename the still-open staging handle, with a relative destination under
	// the anchored directory. No copy fallback or delete-then-move is used.
	if err := winRename(handle, t.dirHandle, winSnapshotName, t.readInfo != nil); err != nil {
		return false, err
	}
	published = true
	t.readDone = false
	if err := t.checkBinding(""); err != nil {
		return true, err
	}
	actual, afterInfo, err := t.readSnapshot(len(data))
	if err != nil {
		return true, err
	}
	if afterInfo == nil || !winSameFile(info, *afterInfo) || !bytes.Equal(actual, data) {
		return true, fmt.Errorf("%w: published snapshot identity or bytes changed", ErrUnsafe)
	}
	// File.Sync requested FlushFileBuffers before the rename. Windows does not
	// provide a portable directory fsync here; this is not a power-loss proof.
	return true, nil
}

func (t *fsTxn) close() error {
	if t == nil || t.closed {
		return nil
	}
	t.closed = true
	var first error
	if t.lockHandle != 0 && t.lockHandle != windows.InvalidHandle {
		if t.locked {
			var overlap windows.Overlapped
			first = windows.UnlockFileEx(t.lockHandle, 0, 1, 0, &overlap)
		}
		if err := windows.CloseHandle(t.lockHandle); first == nil {
			first = err
		}
	}
	if t.dirHandle != 0 && t.dirHandle != windows.InvalidHandle {
		if err := windows.CloseHandle(t.dirHandle); first == nil {
			first = err
		}
	}
	return first
}

func (t *fsTxn) readSnapshot(maxBytes int) (result []byte, resultInfo *windows.ByHandleFileInformation, resultErr error) {
	handle, err := t.openRelative(winSnapshotName, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(handle), filepath.Join(t.dir, winSnapshotName))
	defer func() {
		if err := file.Close(); resultErr == nil && err != nil {
			result, resultInfo, resultErr = nil, nil, err
		}
	}()
	info, err := t.checkHandle(handle, false, false)
	if err != nil {
		return nil, nil, err
	}
	if uint64(info.FileSizeHigh)<<32|uint64(info.FileSizeLow) > uint64(maxBytes) {
		return nil, nil, fmt.Errorf("%w: snapshot exceeds read limit", ErrUnsafe)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxBytes {
		return nil, nil, fmt.Errorf("%w: snapshot exceeds read limit", ErrUnsafe)
	}
	after, err := t.checkHandle(handle, false, false)
	if err != nil {
		return nil, nil, err
	}
	if !winSameFile(info, after) || info.FileSizeHigh != after.FileSizeHigh || info.FileSizeLow != after.FileSizeLow || info.LastWriteTime != after.LastWriteTime {
		return nil, nil, fmt.Errorf("%w: snapshot changed while reading", ErrUnsafe)
	}
	return data, &after, nil
}

func (t *fsTxn) unchangedSnapshot() error {
	data, info, err := t.readSnapshot(len(t.readBytes))
	if err != nil {
		return err
	}
	if (info == nil) != (t.readInfo == nil) || info != nil && !winSameFile(*info, *t.readInfo) || !bytes.Equal(data, t.readBytes) {
		return fmt.Errorf("%w: snapshot changed after validation", ErrUnsafe)
	}
	return nil
}

func (t *fsTxn) checkBinding(temporary string) error {
	if t.closed {
		return os.ErrClosed
	}
	handle, err := winOpenDirectory(t.dir)
	if err != nil {
		return err
	}
	info, checkErr := t.checkHandle(handle, true, false)
	_ = windows.CloseHandle(handle)
	if checkErr != nil {
		return checkErr
	}
	if !winSameFile(info, t.dirInfo) {
		return fmt.Errorf("%w: managed directory was replaced", ErrUnsafe)
	}
	handle, err = t.openRelative(winLockName, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil)
	if err != nil {
		return err
	}
	info, checkErr = t.checkHandle(handle, false, false)
	_ = windows.CloseHandle(handle)
	if checkErr != nil {
		return checkErr
	}
	if !winSameFile(info, t.lockInfo) {
		return fmt.Errorf("%w: writer lock was replaced", ErrUnsafe)
	}
	return t.checkEntries(temporary)
}

func (t *fsTxn) checkEntries(temporary string) (resultErr error) {
	directory, err := os.Open(t.dir)
	if err != nil {
		return err
	}
	defer func() {
		if err := directory.Close(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	info, err := t.checkHandle(windows.Handle(directory.Fd()), true, false)
	if err != nil {
		return err
	}
	if !winSameFile(info, t.dirInfo) {
		return fmt.Errorf("%w: directory changed before entry scan", ErrUnsafe)
	}
	for {
		// Never allocate the complete contents of an unrelated directory.
		names, err := directory.Readdirnames(4)
		for _, name := range names {
			if name != winLockName && name != winSnapshotName && (temporary == "" || name != temporary) {
				return fmt.Errorf("%w: unknown entry %q in managed pattern directory", ErrUnsafe, name)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t *fsTxn) openRelative(name string, access, disposition, share uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: t.dirHandle, ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: sd}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &oa, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	runtime.KeepAlive(sd)
	if ntStatus, ok := err.(windows.NTStatus); ok {
		err = ntStatus.Errno()
	}
	return handle, err
}

func (t *fsTxn) checkHandle(handle windows.Handle, directory, parent bool) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	kind, err := windows.GetFileType(handle)
	if err != nil {
		return info, err
	}
	if kind != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DEVICE) != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || !directory && info.NumberOfLinks != 1 {
		return info, fmt.Errorf("%w: expected an ordinary, non-reparse, single-link managed file or directory", ErrUnsafe)
	}
	if err := winCheckSecurity(handle, t.userSID, parent); err != nil {
		return info, err
	}
	return info, nil
}

func winSameFile(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}

func winOpenDirectory(dir string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	const fileTraverse = 0x20
	return windows.CreateFile(name, windows.GENERIC_READ|windows.READ_CONTROL|fileTraverse, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func winPrepareDirectory(dir string, create bool, sd *windows.SECURITY_DESCRIPTOR, sid string) error {
	return winPrepareDirectoryWithResolver(dir, create, sd, sid, filepath.EvalSymlinks)
}

// The resolver is passed per call, never stored in mutable global state. Native
// tests can model a parent alias to a remote destination without contacting it.
func winPrepareDirectoryWithResolver(dir string, create bool, sd *windows.SECURITY_DESCRIPTOR, sid string, resolve func(string) (string, error)) error {
	parent := filepath.Dir(dir)
	if parent == dir {
		return fmt.Errorf("%w: cannot create a volume root as a pattern store", ErrUnsafe)
	}
	canonical, err := winResolveParent(parent, resolve)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = winPrepareDirectoryWithResolver(parent, true, sd, sid, resolve); err != nil {
			return err
		}
		canonical, err = winResolveParent(parent, resolve)
	}
	if err != nil {
		return err
	}
	child := filepath.Join(canonical, filepath.Base(dir))
	info, err := os.Lstat(child)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: managed directory must not be a link or special file", ErrUnsafe)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) || !create {
		return err
	}
	parentHandle, err := winOpenDirectory(canonical)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parentHandle)
	guard := fsTxn{userSID: sid}
	if _, err := guard.checkHandle(parentHandle, true, true); err != nil {
		return err
	}
	if err := winCheckVolume(parentHandle); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(child)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	err = windows.CreateDirectory(name, &attributes)
	runtime.KeepAlive(sd)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil // open and validate the resulting object; never change its ACL.
	}
	return err
}

func winLocalPath(dir string) error {
	return winLocalPathRole(dir, false)
}

func winResolveParent(parent string, resolve func(string) (string, error)) (string, error) {
	canonical, err := resolve(parent)
	if err != nil {
		return "", err
	}
	if err := winLocalPathRole(canonical, true); err != nil {
		return "", err
	}
	return canonical, nil
}

func winLocalPathRole(dir string, allowVolumeRoot bool) error {
	if !utf8.ValidString(dir) {
		return fmt.Errorf("%w: Windows pattern path must be valid UTF-8", ErrUnsafe)
	}
	dir = filepath.FromSlash(dir)
	volume := filepath.VolumeName(dir)
	if !filepath.IsAbs(dir) || len(volume) != 2 || volume[1] != ':' {
		return fmt.Errorf("%w: Windows pattern directory must be an absolute local drive path", ErrUnsupported)
	}
	components := strings.Split(strings.TrimPrefix(dir[len(volume):], `\`), `\`)
	if allowVolumeRoot && filepath.Clean(dir) == volume+`\` {
		components = nil
	}
	for _, component := range components {
		if component == "" || strings.TrimRight(component, " .") != component || strings.ContainsAny(component, `:<>"|?*`) {
			return fmt.Errorf("%w: ambiguous Windows path component", ErrUnsafe)
		}
		for _, c := range component {
			if c < 0x20 {
				return fmt.Errorf("%w: Windows path component contains a control character", ErrUnsafe)
			}
		}
		// Windows also recognizes device names with spaces immediately before
		// an extension, e.g. NUL .txt. Match the Go/Win32 reserved-name rule.
		base := strings.TrimRight(strings.ToUpper(strings.SplitN(component, ".", 2)[0]), " ")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' || base == "COM¹" || base == "COM²" || base == "COM³" || base == "LPT¹" || base == "LPT²" || base == "LPT³" {
			return fmt.Errorf("%w: reserved Windows path component", ErrUnsafe)
		}
	}
	name, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return err
	}
	if windows.GetDriveType(name) != windows.DRIVE_FIXED {
		return fmt.Errorf("%w: Windows pattern storage requires a fixed local drive", ErrUnsupported)
	}
	return nil
}

func winCheckVolume(handle windows.Handle) error {
	var fileSystem [32]uint16
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, &fileSystem[0], uint32(len(fileSystem))); err != nil {
		return err
	}
	// Enforce this before creating a managed child as well as when opening an
	// existing library. A filesystem with ACLs is not automatically NTFS.
	if windows.UTF16ToString(fileSystem[:]) != "NTFS" || flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return fmt.Errorf("%w: Windows pattern storage requires local NTFS with persistent ACLs", ErrUnsupported)
	}
	return nil
}

func winCheckSecurity(handle windows.Handle, userSID string, parent bool) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	if !parent && control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%w: managed Windows DACL must be protected from inheritance", ErrUnsafe)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return fmt.Errorf("%w: managed object has no valid Windows owner", ErrUnsafe)
	}
	ownerSID := owner.String()
	if ownerSID != userSID && (!parent || ownerSID != "S-1-5-18" && ownerSID != "S-1-5-32-544") {
		return fmt.Errorf("%w: managed object is not owned by the current Windows user", ErrUnsafe)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return fmt.Errorf("%w: managed object requires an explicit restricted DACL", ErrUnsafe)
	}
	// ACL and ACCESS_ALLOWED_ACE headers follow the documented Win32 layout.
	// GetSecurityInfo/GetAce supply validated OS-owned buffers; bounded checks
	// precede the SID pointer and this descriptor stays alive throughout.
	type aclHeader struct {
		revision, reserved byte
		size, count, rest  uint16
	}
	type aceHeader struct {
		kind, flags byte
		size        uint16
		mask        uint32
	}
	header := (*aclHeader)(unsafe.Pointer(acl))
	if header.size < 8 || header.count == 0 {
		return fmt.Errorf("%w: managed object has an empty or malformed DACL", ErrUnsafe)
	}
	hasUser := false
	for i := uint16(0); i < header.count; i++ {
		var raw unsafe.Pointer
		result, _, callErr := winGetAce.Call(uintptr(unsafe.Pointer(acl)), uintptr(i), uintptr(unsafe.Pointer(&raw)))
		if result == 0 {
			return fmt.Errorf("read DACL ACE: %w", callErr)
		}
		begin, end := uintptr(unsafe.Pointer(acl)), uintptr(unsafe.Pointer(acl))+uintptr(header.size)
		if uintptr(raw) < begin+8 || uintptr(raw) > end-8 {
			return fmt.Errorf("%w: malformed Windows DACL ACE pointer", ErrUnsafe)
		}
		ace := (*aceHeader)(raw)
		if uintptr(raw)+uintptr(ace.size) > end || ace.size < 16 || ace.kind != 0 {
			return fmt.Errorf("%w: unsupported or malformed Windows DACL ACE", ErrUnsafe)
		}
		sid := (*windows.SID)(unsafe.Add(raw, 8))
		if !sid.IsValid() || sid.Len() > int(ace.size)-8 {
			return fmt.Errorf("%w: malformed Windows DACL SID", ErrUnsafe)
		}
		principal := sid.String()
		trusted := principal == userSID || principal == "S-1-5-18" || principal == "S-1-5-32-544"
		if parent && ace.flags&0x08 != 0 {
			// An inherit-only parent ACE (e.g. CREATOR_OWNER) grants no access
			// to the parent itself. New managed children use explicit protected
			// DACLs, so those inherited templates cannot broaden their access.
			continue
		}
		// A managed directory/file is private, analogous to 0700/0600. For an
		// existing parent only, other principals may read/traverse but cannot
		// modify children, permissions, ownership or attributes.
		if !trusted {
			const mutations = windows.GENERIC_WRITE | windows.GENERIC_ALL | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | 0x40
			if !parent || ace.mask&uint32(mutations) != 0 {
				return fmt.Errorf("%w: Windows DACL grants access to an unapproved principal", ErrUnsafe)
			}
		}
		if principal == userSID && ace.flags&0x08 == 0 && (ace.mask&winFullAccess == winFullAccess || ace.mask&windows.GENERIC_ALL != 0) {
			hasUser = true
		}
	}
	runtime.KeepAlive(sd)
	if !parent && !hasUser {
		return fmt.Errorf("%w: Windows DACL lacks current-user full access", ErrUnsafe)
	}
	return nil
}

func winRename(file, directory windows.Handle, name string, replace bool) error {
	text, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	type renameInfo struct {
		replace uint32
		root    windows.Handle
		length  uint32
		name    [1]uint16
	}
	var layout renameInfo
	offset := int(unsafe.Offsetof(layout.name))
	length := (len(text) - 1) * 2
	buffer := make([]byte, int(unsafe.Sizeof(layout))+length)
	info := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	if replace {
		info.replace = windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	info.root, info.length = directory, uint32(length)
	copy(unsafe.Slice((*uint16)(unsafe.Add(unsafe.Pointer(info), offset)), len(text)-1), text[:len(text)-1])
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(file, &status, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	if ntStatus, ok := err.(windows.NTStatus); ok {
		return ntStatus.Errno()
	}
	return err
}
