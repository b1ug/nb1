//go:build linux || darwin
// +build linux darwin

package cmd

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSequenceControlV1RejectsExistingFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input-fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readSequenceControlFile(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO input accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO input blocked")
	}
}
