//go:build windows
// +build windows

package patternstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func windowsTxn(t *testing.T, dir string) *fsTxn {
	t.Helper()
	tx, err := openFS(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tx.close(); err != nil {
			t.Errorf("close transaction: %v", err)
		}
	})
	return tx
}

func windowsPublish(t *testing.T, tx *fsTxn, data []byte) {
	t.Helper()
	if _, err := tx.read(1 << 20); err != nil {
		t.Fatal(err)
	}
	if published, err := tx.publish(data); err != nil || !published {
		t.Fatalf("publish = %v, %v", published, err)
	}
}

func TestWindowsStoreMissingReadDoesNotCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent", "library")
	if tx, err := openFS(dir, false); !errors.Is(err, os.ErrNotExist) || tx != nil {
		t.Fatalf("missing read = %v, %v", tx, err)
	}
	if _, err := os.Lstat(filepath.Dir(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing read created its parent: %v", err)
	}
}

func TestWindowsStoreCurrentUserPrivatePublication(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "one", "two", "library")
	tx := windowsTxn(t, dir)
	windowsPublish(t, tx, []byte("first"))
	windowsPublish(t, tx, []byte("second"))
	actual, err := tx.read(32)
	if err != nil || string(actual) != "second" {
		t.Fatalf("read = %q, %v", actual, err)
	}
	for _, name := range []string{winLockName, winSnapshotName} {
		handle, err := tx.openRelative(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, checkErr := tx.checkHandle(handle, false, false)
		_ = windows.CloseHandle(handle)
		if checkErr != nil {
			t.Fatalf("%s security: %v", name, checkErr)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 || entries[0].Name() != winSnapshotName || entries[1].Name() != winLockName {
		t.Fatalf("unexpected residual entries: %v, %v", entries, err)
	}
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
	other, err := openFS(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	actual, err = other.read(32)
	if err != nil || string(actual) != "second" {
		t.Fatalf("reopen = %q, %v", actual, err)
	}
}

func TestWindowsStoreBusyDuringStagingAcrossProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	stage := filepath.Join(dir, ".pattern-held-for-lock-test.tmp")
	if err := os.WriteFile(stage, []byte("staged but unpublished"), 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := openFS(dir, false); !errors.Is(err, ErrBusy) || other != nil {
		t.Fatalf("concurrent transaction during staging = %v, %v", other, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestWindowsStoreLockProcessHelper$")
	command.Env = append(os.Environ(), "NB1_PATTERN_LOCK_TEST="+dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("second process did not observe ErrBusy: %v\n%s", err, output)
	}
	if err := os.Remove(stage); err != nil {
		t.Fatal(err)
	}
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
	other, err := openFS(dir, false)
	if err != nil {
		t.Fatalf("lock remained after close: %v", err)
	}
	if err := other.close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsStoreLockProcessHelper(t *testing.T) {
	dir := os.Getenv("NB1_PATTERN_LOCK_TEST")
	if dir == "" {
		return
	}
	other, err := openFS(dir, false)
	if other != nil {
		_ = other.close()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected library busy, got %v", err)
	}
}

func TestWindowsStoreUnknownEntriesPreservedBeforeFirstLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, winLockName)); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, "user-file.txt")
	if err := os.WriteFile(unknown, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if tx, err := openFS(dir, true); err == nil || tx != nil {
		t.Fatalf("adopted an unknown directory: %v, %v", tx, err)
	}
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "preserve" {
		t.Fatalf("unknown entry changed: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, winLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected directory acquired a lock file: %v", err)
	}
}

func TestWindowsStoreRejectsBroadACL(t *testing.T) {
	for _, target := range []string{"directory", winLockName, winSnapshotName} {
		t.Run(target, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "library")
			tx := windowsTxn(t, dir)
			windowsPublish(t, tx, []byte("unchanged"))
			if err := tx.close(); err != nil {
				t.Fatal(err)
			}
			path := dir
			if target != "directory" {
				path = filepath.Join(dir, target)
			}
			sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + tx.userSID + ")(A;;FA;;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			other, err := openFS(dir, false)
			if err == nil {
				_, err = other.read(32)
				_ = other.close()
			}
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("broad %s ACL was accepted: %v", target, err)
			}
			if data, err := os.ReadFile(filepath.Join(dir, winSnapshotName)); err != nil || string(data) != "unchanged" {
				t.Fatalf("rejected ACL changed snapshot: %q, %v", data, err)
			}
		})
	}
}

func TestWindowsStoreManyUnknownEntriesArePreserved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1024; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("foreign-%04d", i)), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if other, err := openFS(dir, false); !errors.Is(err, ErrUnsafe) || other != nil {
		t.Fatalf("large unrelated directory accepted: %v, %v", other, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1025 {
		t.Fatalf("unknown entries were changed: count=%d, err=%v", len(entries), err)
	}
}

func TestWindowsStoreRejectsSnapshotHardLink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	windowsPublish(t, tx, []byte("unchanged"))
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(dir), "hardlink")
	if err := os.Link(filepath.Join(dir, winSnapshotName), outside); err != nil {
		t.Fatal(err)
	}
	other, err := openFS(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	if _, err := other.read(32); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("snapshot hard link accepted: %v", err)
	}
}

func TestWindowsStoreRejectsUnprotectedRestrictedACL(t *testing.T) {
	for _, target := range []string{"directory", winLockName, winSnapshotName} {
		t.Run(target, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "library")
			tx := windowsTxn(t, dir)
			windowsPublish(t, tx, []byte("unchanged"))
			if err := tx.close(); err != nil {
				t.Fatal(err)
			}
			path := dir
			if target != "directory" {
				path = filepath.Join(dir, target)
			}
			// Retain the same private explicit ACEs but enable inheritance. This
			// remains unacceptable even before a parent has broadened the ACL.
			acl, _, err := tx.privateSD.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			other, err := openFS(dir, false)
			if err == nil {
				_, err = other.read(32)
				_ = other.close()
			}
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unprotected %s DACL accepted: %v", target, err)
			}
		})
	}
}

func TestWindowsStoreParentInheritOnlyTemplateAndActiveWrite(t *testing.T) {
	for _, test := range []struct {
		name, extra string
		wantUnsafe  bool
	}{
		{"creator-owner-inherit-only", "(A;OICIIO;FA;;;CO)", false},
		{"everyone-active-write", "(A;;FA;;;WD)", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "parent")
			tx := windowsTxn(t, parent)
			if err := tx.close(); err != nil {
				t.Fatal(err)
			}
			sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + tx.userSID + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" + test.extra)
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			childPath := filepath.Join(parent, "library")
			child, err := openFS(childPath, true)
			if test.wantUnsafe {
				if !errors.Is(err, ErrUnsafe) || child != nil {
					t.Fatalf("active parent write accepted: %v, %v", child, err)
				}
				if _, err := os.Lstat(childPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unsafe parent received managed child: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("inactive creator-owner template rejected: %v", err)
			}
			if err := child.close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsStoreRejectsReparseRootAndSnapshot(t *testing.T) {
	for _, target := range []string{"directory", winSnapshotName} {
		t.Run(target, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "library")
			tx := windowsTxn(t, dir)
			windowsPublish(t, tx, []byte("unchanged"))
			if err := tx.close(); err != nil {
				t.Fatal(err)
			}
			path := dir
			if target != "directory" {
				path = filepath.Join(dir, target)
			}
			moved := path + "-original"
			if target != "directory" {
				moved = filepath.Join(filepath.Dir(dir), "snapshot-original")
			}
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, path); err != nil {
				if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
					t.Skip("Windows account cannot create symlinks; reparse test not exercised")
				}
				t.Fatal(err)
			}
			other, err := openFS(dir, false)
			if err == nil {
				_, err = other.read(32)
				_ = other.close()
			}
			if err == nil {
				t.Fatal("managed reparse point was accepted")
			}
			if data, err := os.ReadFile(filepath.Join(dir, winSnapshotName)); err != nil || string(data) != "unchanged" {
				t.Fatalf("rejected reparse target changed: %q, %v", data, err)
			}
		})
	}
}

func TestWindowsStoreRejectsReadAndPublishBudgetViolations(t *testing.T) {
	tx := windowsTxn(t, filepath.Join(t.TempDir(), "library"))
	if published, err := tx.publish([]byte("x")); published || !errors.Is(err, ErrUnsafe) {
		t.Fatalf("publish before read = %v, %v", published, err)
	}
	for _, limit := range []int{0, -1} {
		if _, err := tx.read(limit); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("read(%d) = %v", limit, err)
		}
	}
	if _, err := tx.read(2); err != nil {
		t.Fatal(err)
	}
	if published, err := tx.publish([]byte("xxx")); published || !errors.Is(err, ErrUnsafe) {
		t.Fatalf("over-budget publish = %v, %v", published, err)
	}
	windowsPublish(t, tx, []byte("1234"))
	if _, err := tx.read(3); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("over-budget read = %v", err)
	}
}

func TestWindowsStoreChangedSnapshotNotOverwritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	windowsPublish(t, tx, []byte("original"))
	if _, err := tx.read(32); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, winSnapshotName)
	if err := os.WriteFile(path, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if published, err := tx.publish([]byte("new")); published || !errors.Is(err, ErrUnsafe) {
		t.Fatalf("changed snapshot publish = %v, %v", published, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "external" {
		t.Fatalf("external snapshot overwritten: %q, %v", data, err)
	}
}

func TestWindowsStoreBlockedRenamePreservesSnapshotAndCleansOwnStage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	windowsPublish(t, tx, []byte("original"))
	if _, err := tx.read(32); err != nil {
		t.Fatal(err)
	}
	// This ordinary read handle deliberately omits FILE_SHARE_DELETE, so
	// Windows must reject publication rather than delete/copy around it.
	name, _ := windows.UTF16PtrFromString(filepath.Join(dir, winSnapshotName))
	blocker, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	published, publishErr := tx.publish([]byte("replacement"))
	_ = windows.CloseHandle(blocker)
	if published || publishErr == nil {
		t.Fatalf("blocked publication = %v, %v", published, publishErr)
	}
	if data, err := os.ReadFile(filepath.Join(dir, winSnapshotName)); err != nil || !bytes.Equal(data, []byte("original")) {
		t.Fatalf("failed publication changed snapshot: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("failed publication left staging files: %v, %v", entries, err)
	}
}

func TestWindowsStoreOpenDirectoryAndLockCannotBeReplaced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	tx := windowsTxn(t, dir)
	for _, path := range []string{dir, filepath.Join(dir, winLockName)} {
		if err := os.Rename(path, path+"-renamed"); err == nil {
			t.Fatalf("open managed object could be renamed: %s", path)
		}
	}
	if err := tx.close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsStoreRejectsAmbiguousAndRemotePaths(t *testing.T) {
	for _, path := range []string{`\\server\share\library`, `\\?\C:\library`, `C:\library `, `C:\library.`, `C:\library:stream`, `C:\CON\library`, `C:/CON/library`, `C:\AUX.txt\library`, `C:\CONIN$\library`, `C:\CONOUT$.txt\library`, `C:\COM¹\library`, `C:\COM².txt\library`, `C:\COM³\library`, `C:\LPT¹\library`, `C:\LPT².txt\library`, `C:\LPT³\library`, `C:\NUL .txt\library`, `C:\COM1 .txt\library`, `C:\COM¹ .txt\library`, `C:\CONIN$ .txt\library`, "C:\\bad\x00path", "C:\\bad\x1fpath", "C:\\bad\xffpath"} {
		if err := winLocalPath(path); err == nil {
			t.Errorf("ambiguous or remote path accepted: %q", path)
		}
	}
	if err := winLocalPath(filepath.Join(t.TempDir(), strings.Repeat("a", 16))); err != nil {
		t.Fatalf("ordinary local path rejected: %v", err)
	}
}

func TestWindowsStoreResolvedParentMustStayLocal(t *testing.T) {
	parent := t.TempDir()
	for _, destination := range []string{`\\uncontacted-host\share\parent`, `\\?\UNC\uncontacted-host\share\parent`} {
		calls := 0
		resolved, err := winResolveParent(parent, func(request string) (string, error) {
			calls++
			if request != parent {
				t.Fatalf("unexpected parent resolution: %q", request)
			}
			return destination, nil
		})
		// This function is only a resolve-and-validate operation. There is no
		// directory open/create after it in this test, even if validation were
		// to regress. No remote alias, mapping or network access is created.
		if !errors.Is(err, ErrUnsupported) || resolved != "" || calls != 1 {
			t.Fatalf("nonlocal resolved parent %q = %q, %v, calls=%d", destination, resolved, err, calls)
		}
	}
	volumeRoot := filepath.VolumeName(parent) + `\`
	resolved, err := winResolveParent(parent, func(string) (string, error) { return volumeRoot, nil })
	if err != nil || resolved != volumeRoot {
		t.Fatalf("fixed local volume-root parent rejected: %q, %v", resolved, err)
	}
	if err := winLocalPath(volumeRoot); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("volume root accepted as the library itself: %v", err)
	}
}
