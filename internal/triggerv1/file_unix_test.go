//go:build darwin || linux

package triggerv1

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTriggerFileRejectsSpecialFilesAndSymlinks(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, Filename)
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				target := filepath.Join(dir, "real.json")
				if err = os.WriteFile(target, []byte(validFile), 0600); err == nil {
					err = os.Symlink(target, path)
				}
			case "dangling-symlink":
				err = os.Symlink(filepath.Join(dir, "missing.json"), path)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := New(dir).List(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s was followed, opened or treated as a missing optional file: %v", kind, err)
			}
		})
	}
}
