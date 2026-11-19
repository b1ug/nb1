//go:build darwin || linux

package remoteaccess

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnixTokenFilePrivacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := GenerateTokenFile(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("generated Unix token does not have mode 0600: %v", err)
	}

	symlink := filepath.Join(dir, "symlink")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(symlink); err == nil {
		t.Fatal("symlink accepted for reading")
	}
	if err := GenerateTokenFile(symlink); err == nil {
		t.Fatal("symlink accepted for generation")
	}

	hardlink := filepath.Join(dir, "hardlink")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("token with an extra hardlink accepted for reading")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("group-readable token accepted for reading")
	}

	pipe := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(pipe); err == nil {
		t.Fatal("FIFO accepted for reading")
	}
}
