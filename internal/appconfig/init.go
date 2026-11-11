package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// Template returns a documented configuration with no selected hardware.
func Template() []byte {
	return []byte(`# nb1 user configuration. No daemon is started by reading this file.
# Set an exact eight-digit USB serial, or leave device empty.
device: ""
server:
  devices: []
  listen: "127.0.0.1:8931"
jobs:
  # Terminal Jobs can expire sooner when the count limit is reached.
  # Eviction or daemon restart releases the ID; submitting it can run again.
  terminal_limit: 16384
  terminal_ttl: 30m
# Empty uses the stable user configuration directory's nb1/library.
library_dir: ""
`)
}

// Init explicitly creates a private configuration directory and a new 0600
// example file. Existing files are never replaced; library directories are not
// created. Existing parent directory permissions are checked, never changed.
func Init(path string) error {
	if path == "" {
		return fmt.Errorf("configuration path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve configuration path: %w", err)
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || !privateDirectory(info) {
		return fmt.Errorf("configuration parent must be a private directory")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create new configuration %q: %w", path, err)
	}
	_, writeErr := f.Write(Template())
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("write configuration %q: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close configuration %q: %w", path, closeErr)
	}
	return nil
}
