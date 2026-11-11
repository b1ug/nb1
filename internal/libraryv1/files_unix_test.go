//go:build darwin || linux
// +build darwin linux

package libraryv1

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFIFORecordReturnsWithoutBlocking(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "library"))
	dir, err := s.directory("colors", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, recordFilename("pipe")), 0600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := s.ResolveColor("pipe")
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO read blocked")
	}
}
