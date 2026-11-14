//go:build darwin || linux
// +build darwin linux

package appconfig

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFIFOConfigurationFailsWithoutBlocking(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "pipe")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := Load(Selection{BaseDir: base, Path: path, Required: true}); result <- err }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("configuration FIFO blocked")
	}
}

func TestInitRejectsNonprivateParentWithoutChangingIt(t *testing.T) {
	base := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(filepath.Join(base, Filename)); err == nil {
		t.Fatal("shared configuration directory accepted")
	}
	info, err := os.Stat(base)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("existing parent changed: %v %v", info, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("shared parent written: %v %v", entries, err)
	}
}
