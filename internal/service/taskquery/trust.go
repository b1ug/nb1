package taskquery

import "strings"

type accessEntry struct {
	kind  byte
	flags byte
	mask  uint32
	sid   string
}

func trustedOwner(sid string) bool {
	switch sid {
	case "S-1-5-18", // LocalSystem
		"S-1-5-32-544", // Administrators
		"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464": // TrustedInstaller
		return true
	}
	return false
}

// This is deliberately conservative, not a general Windows effective-access
// evaluator. Deny ACEs cannot make a suspicious allow ACE acceptable here.
func protectedACL(owner string, entries []accessEntry, directory bool) bool {
	if !trustedOwner(owner) {
		return false
	}
	const replace = uint32(0x00010000 | 0x00040000 | 0x00080000 | 0x00000040 | 0x10000000 | 0x40000000 | 0x02000000)
	mask := replace
	if !directory {
		mask |= 0x00000002 | 0x00000004 | 0x00000010 | 0x00000100
	}
	for _, entry := range entries {
		if entry.kind != 0 && entry.kind != 1 {
			return false
		}
		if entry.flags&0x08 != 0 {
			continue
		} // INHERIT_ONLY does not apply to this object.
		if entry.kind == 0 && entry.mask&mask != 0 && !trustedOwner(entry.sid) {
			return false
		}
	}
	return true
}

func systemPaths(systemDirectory string) (binary, windowsDirectory string, paths []string, ok bool) {
	if len(systemDirectory) < 6 || systemDirectory[1:3] != `:\` ||
		!((systemDirectory[0] >= 'A' && systemDirectory[0] <= 'Z') || (systemDirectory[0] >= 'a' && systemDirectory[0] <= 'z')) ||
		strings.ContainsAny(systemDirectory[3:], "/:*?\"<>|\x00\r\n") {
		return "", "", nil, false
	}
	parts := strings.Split(systemDirectory[3:], `\`)
	if len(parts) < 2 {
		return "", "", nil, false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, ". ") != part {
			return "", "", nil, false
		}
	}
	current := systemDirectory[:3]
	paths = append(paths, current)
	for _, part := range parts {
		if !strings.HasSuffix(current, `\`) {
			current += `\`
		}
		current += part
		paths = append(paths, current)
	}
	windowsDirectory = paths[len(paths)-2]
	for _, part := range []string{"WindowsPowerShell", "v1.0", "powershell.exe"} {
		current += `\` + part
		paths = append(paths, current)
	}
	return current, windowsDirectory, paths, true
}
