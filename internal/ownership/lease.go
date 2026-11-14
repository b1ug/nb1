// Package ownership coordinates device ownership between cooperating local
// controllers. A lease must outlive all native I/O and the device handle itself.
package ownership

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/b1ug/nb1/internal/userconfig"
)

// ErrBusy means another lease already owns the selected device.
var ErrBusy = errors.New("nb1: device already owned")

// Lease holds an operating-system lock until Close or process termination.
// Do not copy a Lease after first use. Lock files intentionally remain on disk:
// removing one would allow a competing process to lock a different inode.
type Lease struct {
	mu   sync.Mutex
	file *os.File
}

// DefaultDir returns the per-user ownership directory. The function does not
// create it. Direct and daemon controllers must use the same directory.
func DefaultDir() (string, error) {
	// A cache location may be purged while a process still holds the old lock
	// inode. This native coordination root deliberately stays stable when the
	// user configuration or library moves to XDG storage. Existing controllers
	// may still own handles protected by these same lock files.
	base, err := userconfig.NativeAppDir()
	if err != nil {
		return "", fmt.Errorf("find ownership directory: %w", err)
	}
	return filepath.Join(base, "device-locks"), nil
}

// Acquire immediately acquires ownership of serial, or returns ErrBusy. An
// empty dir selects DefaultDir. Serial values are hashed rather than used as
// filesystem names. Only nonblank serials are accepted; callers must resolve
// aliases and enumeration indexes to a real serial before acquiring a lease.
func Acquire(dir, serial string) (*Lease, error) {
	if strings.TrimSpace(serial) == "" {
		return nil, errors.New("ownership requires a device serial number")
	}
	if dir == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create ownership directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect ownership directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("ownership directory must be a directory, not a symlink or file")
	}
	if err := checkPrivateDirectory(info); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(serial))))
	file, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("open ownership lock: %w", err)
	}
	info, err = file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect ownership lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("ownership lock must be a regular file")
	}
	if err := lock(file); err != nil {
		file.Close()
		if errors.Is(err, ErrBusy) {
			return nil, fmt.Errorf("%w: %q", ErrBusy, serial)
		}
		return nil, fmt.Errorf("acquire ownership lock: %w", err)
	}
	return &Lease{file: file}, nil
}

// Close releases ownership. It is safe to call repeatedly or concurrently.
// Callers must first stop native I/O and close the device: releasing a lease
// earlier would allow a new owner to overlap with the previous native handle.
func (lease *Lease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.file == nil {
		return nil
	}
	file := lease.file
	lease.file = nil
	unlockErr := unlock(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release ownership lock: %w", unlockErr)
	}
	return closeErr
}
