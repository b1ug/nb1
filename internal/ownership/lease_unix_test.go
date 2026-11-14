//go:build darwin || linux
// +build darwin linux

package ownership

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateDirectoryAndLockPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("lock directory permissions = %v, %v", info, err)
	}
	info, err = lease.file.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("lock file permissions = %v, %v", info, err)
	}
}

func TestSharedDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if lease, err := Acquire(dir, "20005601"); err == nil || lease != nil {
		t.Fatalf("shared ownership directory accepted: %v, %v", lease, err)
	}
}

func TestLockFileSymlinkRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	name := lease.file.Name()
	lease.Close()
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
	if lease, err := Acquire(dir, "20005601"); err == nil || lease != nil {
		t.Fatalf("symlink lock file accepted: %v, %v", lease, err)
	}
}

func TestOwnershipDirectorySymlinkRejected(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if lease, err := Acquire(link, "20005601"); err == nil || lease != nil {
		t.Fatalf("symlink ownership directory accepted: %v, %v", lease, err)
	}
}

func TestNonRegularLockFileRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	name := lease.file.Name()
	lease.Close()
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	if lease, err := Acquire(dir, "20005601"); err == nil || lease != nil {
		t.Fatalf("FIFO lock file accepted: %v, %v", lease, err)
	}
}
