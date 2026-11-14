//go:build windows
// +build windows

package patternstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This diagnostic reads only the two named temporary parents. It never changes
// their owner/ACL, imports a Pattern, or opens a device. No raw SID, arbitrary
// environment or security-descriptor string is printed.
func TestWindowsStoreTempParentDiagnostics(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal("cannot query diagnostic process token user")
	}
	userSID := user.User.Sid.String()
	var elevated, length uint32
	elevationErr := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &length)
	var elevation interface{}
	elevationStatus := windowsDiagnosticError(elevationErr)
	if elevationErr == nil && length == uint32(unsafe.Sizeof(elevated)) {
		elevation = elevated != 0
	} else if elevationErr == nil {
		elevationStatus = "unexpected_size"
	}
	actualTestParent := t.TempDir()
	tempRoot := windowsDiagnosticParent(os.TempDir(), userSID)
	testParent := windowsDiagnosticParent(actualTestParent, userSID)
	privateExpected := os.Getenv("NB1_PATTERN_PRIVATE_TEMP")
	privateStatus := "not_requested"
	var privateErr error
	if privateExpected != "" {
		privateErr = windowsDiagnosticPrivateAssertion(privateExpected, os.TempDir(), actualTestParent)
		if privateErr == nil {
			privateErr = windowsDiagnosticPrivateSecurity(tempRoot, testParent)
		}
		privateStatus = "PASS"
		if privateErr != nil {
			privateStatus = "FAIL"
		}
	}
	requestedArch := os.Getenv("GOARCH")
	archStatus := "not_requested"
	if requestedArch != "" {
		archStatus = "PASS"
		if requestedArch != runtime.GOARCH {
			archStatus = "FAIL"
		}
	}
	report := map[string]interface{}{
		"kind":                   "nb1-patternstore-windows-temp-diagnostic/v1",
		"goarch":                 runtime.GOARCH,
		"go_version":             runtime.Version(),
		"pointer_bytes":          unsafe.Sizeof(uintptr(0)),
		"elevated":               elevation,
		"elevation_query":        elevationStatus,
		"os_temp_dir":            windowsDiagnosticPath(os.TempDir()),
		"TEMP":                   windowsDiagnosticPath(os.Getenv("TEMP")),
		"TMP":                    windowsDiagnosticPath(os.Getenv("TMP")),
		"private_temp_expected":  windowsDiagnosticPath(privateExpected),
		"private_temp_assertion": privateStatus,
		"requested_goarch":       windowsDiagnosticPath(requestedArch),
		"goarch_assertion":       archStatus,
		"os_temp_parent":         tempRoot,
		"test_temp_parent":       testParent,
		"runner_temp":            windowsDiagnosticParent(os.Getenv("RUNNER_TEMP"), userSID),
		"scope":                  "Read-only parent ACL diagnostics; not a storage or ordinary-user qualification result.",
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal("cannot encode selected diagnostic fields")
	}
	t.Log(string(data))
	if privateErr != nil {
		t.Error(privateErr)
	}
	if archStatus == "FAIL" {
		t.Error("diagnostic binary architecture does not match the requested GOARCH")
	}
}

// An explicitly requested CI root must pass the same security and filesystem
// guards used by the store. Merely changing TEMP to a matching path is not a
// qualification result; the original, unrequested diagnostic remains read-only.
func windowsDiagnosticPrivateSecurity(parents ...map[string]interface{}) error {
	for _, parent := range parents {
		if parent["query"] != "ok" || parent["parent_guard"] != "ok" || parent["ntfs_guard"] != "ok" {
			return errors.New("requested private temporary root or test child failed its Windows security/filesystem guard")
		}
	}
	return nil
}

func TestWindowsDiagnosticPrivateSecurityRequiresActualGuards(t *testing.T) {
	valid := map[string]interface{}{"query": "ok", "parent_guard": "ok", "ntfs_guard": "ok"}
	if err := windowsDiagnosticPrivateSecurity(valid, valid); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"query", "parent_guard", "ntfs_guard"} {
		invalid := map[string]interface{}{"query": "ok", "parent_guard": "ok", "ntfs_guard": "ok"}
		invalid[field] = "unsafe"
		if err := windowsDiagnosticPrivateSecurity(valid, invalid); err == nil {
			t.Errorf("accepted failed %s assertion", field)
		}
	}
}

func windowsDiagnosticPrivateAssertion(expected, actualTemp, actualChild string) error {
	if !filepath.IsAbs(expected) || !strings.EqualFold(filepath.Clean(expected), filepath.Clean(actualTemp)) {
		return errors.New("private temporary root does not match the actual os.TempDir selection")
	}
	relative, err := filepath.Rel(filepath.Clean(expected), filepath.Clean(actualChild))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("test temporary directory is not a strict child of the selected private root")
	}
	return nil
}

func windowsDiagnosticPath(value string) string {
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return value
}

func windowsDiagnosticError(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, ErrUnsafe) {
		return "unsafe"
	}
	if errors.Is(err, ErrUnsupported) {
		return "unsupported"
	}
	return "query_failed"
}

func windowsDiagnosticClass(sid, current string) string {
	switch sid {
	case current:
		return "current-user"
	case "S-1-5-18":
		return "system"
	case "S-1-5-32-544":
		return "administrators"
	default:
		return "other"
	}
}

func windowsDiagnosticParent(path, userSID string) map[string]interface{} {
	result := map[string]interface{}{"path": windowsDiagnosticPath(path)}
	if path == "" {
		result["query"] = "not_configured"
		return result
	}
	// This is a diagnostic of two known local fixtures, not an arbitrary path
	// inspection tool. Refuse nonlocal/ambiguous inputs before opening them.
	if err := winLocalPathRole(path, true); err != nil {
		result["query"] = windowsDiagnosticError(err)
		return result
	}
	handle, err := winOpenDirectory(path)
	if err != nil {
		result["query"] = "open_failed"
		return result
	}
	defer windows.CloseHandle(handle)
	result["parent_guard"] = windowsDiagnosticError(winCheckSecurity(handle, userSID, true))
	result["ntfs_guard"] = windowsDiagnosticError(winCheckVolume(handle))
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		result["query"] = "security_query_failed"
		return result
	}
	control, _, err := sd.Control()
	if err != nil {
		result["query"] = "control_query_failed"
		return result
	}
	result["protected"] = control&windows.SE_DACL_PROTECTED != 0
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		result["query"] = "owner_query_failed"
		return result
	}
	result["owner_class"] = windowsDiagnosticClass(owner.String(), userSID)
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		result["query"] = "dacl_missing"
		return result
	}
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
	if header.size < 8 {
		result["query"] = "invalid_acl_header"
		return result
	}
	result["ace_count"] = header.count
	entries := make([]map[string]interface{}, 0)
	for i := uint16(0); i < header.count && i < 32; i++ {
		var raw unsafe.Pointer
		ok, _, _ := winGetAce.Call(uintptr(unsafe.Pointer(acl)), uintptr(i), uintptr(unsafe.Pointer(&raw)))
		begin, end := uintptr(unsafe.Pointer(acl)), uintptr(unsafe.Pointer(acl))+uintptr(header.size)
		if ok == 0 || uintptr(raw) < begin+8 || uintptr(raw) > end-8 {
			result["query"] = "ace_query_failed"
			return result
		}
		ace := (*aceHeader)(raw)
		if ace.size < 8 || uintptr(raw)+uintptr(ace.size) > end {
			result["query"] = "invalid_ace_size"
			return result
		}
		class := "unparsed"
		if (ace.kind == 0 || ace.kind == 1) && ace.size >= 16 {
			sid := (*windows.SID)(unsafe.Add(raw, 8))
			if sid.IsValid() && sid.Len() <= int(ace.size)-8 {
				class = windowsDiagnosticClass(sid.String(), userSID)
			}
		}
		entries = append(entries, map[string]interface{}{"type": ace.kind, "flags": fmt.Sprintf("0x%02x", ace.flags), "mask": fmt.Sprintf("0x%08x", ace.mask), "sid_class": class, "inherit_only": ace.flags&0x08 != 0})
	}
	runtime.KeepAlive(sd)
	result["aces"], result["truncated"] = entries, header.count > 32
	result["query"] = "ok"
	return result
}
