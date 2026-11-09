package taskquery

import (
	"strings"
	"testing"
)

func TestSystemPathComesFromNativeResultNotEnvironment(t *testing.T) {
	for _, directory := range []string{`C:\Windows\System32`, `c:\Windows\System32`, `D:\系统 目录\System32`} {
		binary, windows, paths, ok := systemPaths(directory)
		if !ok || binary != directory+`\WindowsPowerShell\v1.0\powershell.exe` || !strings.HasSuffix(windows, strings.Split(directory, `\`)[1]) || len(paths) != 6 {
			t.Fatal(binary, windows, paths, ok)
		}
	}
	for _, directory := range []string{"", `%SystemRoot%\System32`, `\\server\Windows\System32`, `\\?\C:\Windows\System32`, `C:Windows\System32`, `C:\Windows\..\System32`, `C:\Windows\\System32`, `C:\Windows.\System32`, `C:\Windows\System32\`, "C:\\Windows\\System32\n", `C:/Windows/System32`} {
		if _, _, _, ok := systemPaths(directory); ok {
			t.Fatal("untrusted path accepted", directory)
		}
	}
}

func TestConservativeSystemFileACL(t *testing.T) {
	const system = "S-1-5-18"
	const users = "S-1-5-32-545"
	tests := []struct {
		name, owner string
		directory   bool
		entries     []accessEntry
		want        bool
	}{
		{"system", system, false, []accessEntry{{sid: system, mask: 0x10000000}}, true},
		{"trustedinstaller", "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464", false, nil, true},
		{"user_owner", fixtureSID, false, nil, false},
		{"read_execute", system, false, []accessEntry{{sid: users, mask: 0x1200A9}}, true},
		{"file_write", system, false, []accessEntry{{sid: users, mask: 2}}, false},
		{"file_append", system, false, []accessEntry{{sid: users, mask: 4}}, false},
		{"file_write_attr", system, false, []accessEntry{{sid: users, mask: 0x100}}, false},
		{"directory_create_only", system, true, []accessEntry{{sid: users, mask: 6}}, true},
		{"directory_delete_child", system, true, []accessEntry{{sid: users, mask: 0x40}}, false},
		{"directory_write_dacl", system, true, []accessEntry{{sid: users, mask: 0x40000}}, false},
		{"generic_write", system, true, []accessEntry{{sid: users, mask: 0x40000000}}, false},
		{"inherit_only", system, false, []accessEntry{{sid: "S-1-3-0", mask: 0x10000000, flags: 8}}, true},
		{"unknown_ace", system, false, []accessEntry{{sid: users, kind: 5}}, false},
		{"unknown_inherit_only_ace", system, false, []accessEntry{{sid: users, kind: 5, flags: 8}}, false},
		{"deny_does_not_excuse_allow", system, false, []accessEntry{{sid: users, mask: 2, kind: 1}, {sid: users, mask: 2}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if protectedACL(test.owner, test.entries, test.directory) != test.want {
				t.Fatal("ACL decision differs")
			}
		})
	}
}
