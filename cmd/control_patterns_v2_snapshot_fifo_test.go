//go:build linux || darwin
// +build linux darwin

package cmd

import (
	"io"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestControlPatternsV2SnapshotExistingFIFOIsNeverOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot-fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readPatternSnapshotFileV2(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO input accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO input blocked")
	}
	if err := writePatternExport(io.Discard, path, []byte("x")); err == nil {
		t.Fatal("FIFO output opened")
	}
}
