//go:build darwin || linux
// +build darwin linux

package patternstore

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	lockName     = "writer.lock"
	snapshotName = "patterns.json"
)

// fsTxn is one short filesystem transaction, anchored to an open directory.
// Only cooperating processes are coordinated by flock. There is no claim of
// isolation from a malicious process already running as the same user.
type fsTxn struct {
	path       string
	dir        *os.File
	lock       *os.File
	dirInfo    os.FileInfo
	lockInfo   os.FileInfo
	readOK     bool
	beforeInfo os.FileInfo
	beforeData []byte
	// Keep the inode referenced for as long as beforeInfo is an identity token.
	// FileInfo alone cannot distinguish unlink/recreate when an inode is reused.
	snapshotAnchor *os.File
	readLimit      int
	// Per-transaction syscall seams allow deterministic commit-boundary tests.
	rename  func(string, string) error
	syncDir func() error
}

func openFS(path string, create bool) (_ *fsTxn, resultErr error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: library directory must be an absolute non-root path", ErrUnsafe)
	}
	path, err := prepareDirectory(filepath.Clean(path), create)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := privateInfo(before, true); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open library directory: %w", err)
	}
	txn := &fsTxn{path: path, dir: os.NewFile(uintptr(fd), path)}
	defer func() {
		if resultErr != nil {
			_ = txn.close()
		}
	}()
	txn.dirInfo, err = txn.dir.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, txn.dirInfo) {
		return nil, fmt.Errorf("%w: library directory changed while opening", ErrUnsafe)
	}
	if err := privateInfo(txn.dirInfo, true); err != nil {
		return nil, err
	}
	if err := checkNoExtendedACL(txn.dir); err != nil {
		return nil, err
	}
	// A held writer may legitimately have a staging file. When a lock already
	// exists, acquire it before scanning so other cooperating callers see Busy.
	// A fresh directory is inspected before creating its persistent lock.
	if _, err := txn.statAt(lockName); errors.Is(err, os.ErrNotExist) {
		if err := txn.scan(""); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	lockFD, err := unix.Openat(fd, lockName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open library writer lock: %w", err)
	}
	txn.lock = os.NewFile(uintptr(lockFD), filepath.Join(path, lockName))
	txn.lockInfo, err = txn.lock.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateInfo(txn.lockInfo, false); err != nil {
		return nil, err
	}
	if err := checkNoExtendedACL(txn.lock); err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, fmt.Errorf("lock library: %w", err)
	}
	if err := txn.checkBindings(); err != nil {
		return nil, err
	}
	if err := txn.scan(""); err != nil {
		return nil, err
	}
	txn.rename = func(from, to string) error { return unix.Renameat(fd, from, fd, to) }
	txn.syncDir = txn.dir.Sync
	return txn, nil
}

// Existing parent aliases (such as macOS /var -> /private/var) are resolved
// before anchoring. The managed directory itself must never be a symlink.
// Only missing directories are created; existing permissions are not repaired.
func prepareDirectory(path string, create bool) (string, error) {
	info, err := os.Lstat(path)
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("%w: library directory is not a real directory", ErrUnsafe)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if errors.Is(err, os.ErrNotExist) && !create {
		return "", os.ErrNotExist
	}
	parent := filepath.Dir(path)
	canonicalParent, parentErr := filepath.EvalSymlinks(parent)
	if errors.Is(parentErr, os.ErrNotExist) && create {
		if _, parentErr = prepareDirectory(parent, true); parentErr == nil {
			canonicalParent, parentErr = filepath.EvalSymlinks(parent)
		}
	}
	if parentErr != nil {
		return "", parentErr
	}
	canonical := filepath.Join(canonicalParent, filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := os.Mkdir(canonical, 0700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return "", mkdirErr
		}
	}
	current, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if err := privateInfo(current, true); err != nil {
		return "", err
	}
	if info != nil && !os.SameFile(info, current) {
		return "", fmt.Errorf("%w: library directory changed during canonicalization", ErrUnsafe)
	}
	return canonical, nil
}

func privateInfo(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: library entry is not owned by the current user", ErrUnsafe)
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("%w: library directory must be a private 0700 directory", ErrUnsafe)
		}
		return nil
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Nlink != 1 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("%w: library file must be regular, private 0600 and singly linked", ErrUnsafe)
	}
	return nil
}

func (txn *fsTxn) checkBindings() error {
	if txn == nil || txn.dir == nil || txn.lock == nil {
		return fmt.Errorf("%w: closed library transaction", ErrUnsafe)
	}
	info, err := os.Lstat(txn.path)
	if err != nil {
		return fmt.Errorf("%w: library directory path changed: %v", ErrUnsafe, err)
	}
	if err := privateInfo(info, true); err != nil {
		return err
	}
	if !os.SameFile(info, txn.dirInfo) {
		return fmt.Errorf("%w: library directory was replaced", ErrUnsafe)
	}
	if err := checkNoExtendedACL(txn.dir); err != nil {
		return err
	}
	current, err := txn.statAt(lockName)
	if err != nil {
		return err
	}
	if !os.SameFile(current, txn.lockInfo) {
		return fmt.Errorf("%w: library writer lock was replaced", ErrUnsafe)
	}
	return nil
}

func (txn *fsTxn) statAt(name string) (os.FileInfo, error) {
	// Inspect with fstatat before open so a static FIFO/device/directory at a
	// managed name is rejected without opening it. O_NOFOLLOW and the later
	// inode comparison still protect the actual open against path replacement.
	var before unix.Stat_t
	if err := unix.Fstatat(int(txn.dir.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if before.Uid != uint32(os.Geteuid()) || uint32(before.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(before.Mode)&07777 != 0600 || before.Nlink != 1 {
		return nil, fmt.Errorf("%w: managed library entry is not a current-user private singly-linked regular file", ErrUnsafe)
	}
	fd, err := unix.Openat(int(txn.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateInfo(info, false); err != nil {
		return nil, err
	}
	after, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(before.Dev) != uint64(after.Dev) || uint64(before.Ino) != uint64(after.Ino) {
		return nil, fmt.Errorf("%w: managed library entry changed while opening", ErrUnsafe)
	}
	if err := checkNoExtendedACL(file); err != nil {
		return nil, err
	}
	return info, nil
}

func (txn *fsTxn) scan(temporary string) error {
	fd, err := unix.Openat(int(txn.dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), txn.path)
	defer file.Close()
	for {
		// At most three names are valid, including this transaction's staging
		// file. A polluted directory must not cause an unbounded allocation.
		names, err := file.Readdirnames(4)
		for _, name := range names {
			if name != lockName && name != snapshotName && (temporary == "" || name != temporary) {
				return fmt.Errorf("%w: unknown library entry %q; preserved for inspection", ErrUnsafe, name)
			}
			if _, err := txn.statAt(name); err != nil {
				return fmt.Errorf("%w: inspect library entry %q: %v", ErrUnsafe, name, err)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (txn *fsTxn) read(maxBytes int) ([]byte, error) {
	txn.readOK = false
	if txn.snapshotAnchor != nil {
		previous := txn.snapshotAnchor
		txn.snapshotAnchor = nil
		if err := previous.Close(); err != nil {
			return nil, fmt.Errorf("close previous library snapshot anchor: %w", err)
		}
	}
	if maxBytes < 1 {
		return nil, fmt.Errorf("%w: invalid library read limit", ErrUnsafe)
	}
	if err := txn.checkBindings(); err != nil {
		return nil, err
	}
	if err := txn.scan(""); err != nil {
		return nil, err
	}
	data, info, err := txn.readSnapshot(maxBytes, true)
	if err != nil {
		return nil, err
	}
	txn.beforeInfo, txn.beforeData, txn.readLimit, txn.readOK = info, append([]byte(nil), data...), maxBytes, true
	return data, nil
}

func (txn *fsTxn) readSnapshot(maxBytes int, retain bool) (output []byte, outputInfo os.FileInfo, resultErr error) {
	pathInfo, err := txn.statAt(snapshotName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Openat(int(txn.dir.Fd()), snapshotName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("read library snapshot: %w", err)
	}
	file := os.NewFile(uintptr(fd), snapshotName)
	retained := false
	defer func() {
		if !retained {
			if err := file.Close(); resultErr == nil && err != nil {
				output, outputInfo = nil, nil
				resultErr = fmt.Errorf("close library snapshot: %w", err)
			}
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if err := privateInfo(before, false); err != nil {
		return nil, nil, err
	}
	if err := checkNoExtendedACL(file); err != nil {
		return nil, nil, err
	}
	if before.Size() > int64(maxBytes) {
		return nil, nil, fmt.Errorf("%w: library snapshot exceeds limit", ErrUnsafe)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxBytes {
		return nil, nil, fmt.Errorf("%w: library snapshot exceeds limit", ErrUnsafe)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if err := privateInfo(after, false); err != nil {
		return nil, nil, err
	}
	current, err := txn.statAt(snapshotName)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(pathInfo, before) || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != after.Size() {
		return nil, nil, fmt.Errorf("%w: library snapshot changed while reading", ErrUnsafe)
	}
	if retain {
		txn.snapshotAnchor, retained = file, true
	}
	return data, after, nil
}

func (txn *fsTxn) publish(data []byte) (published bool, resultErr error) {
	if !txn.readOK || len(data) > txn.readLimit {
		return false, fmt.Errorf("%w: publication requires a bounded successful read", ErrUnsafe)
	}
	if err := txn.checkBindings(); err != nil {
		return false, err
	}
	if err := txn.scan(""); err != nil {
		return false, err
	}
	current, currentInfo, err := txn.readSnapshot(txn.readLimit, false)
	if err != nil {
		return false, err
	}
	if (currentInfo == nil) != (txn.beforeInfo == nil) || currentInfo != nil && !os.SameFile(currentInfo, txn.beforeInfo) || !bytes.Equal(current, txn.beforeData) {
		return false, fmt.Errorf("%w: library snapshot changed since read", ErrUnsafe)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, err
	}
	name := fmt.Sprintf(".pending-%x", random)
	fd, err := unix.Openat(int(txn.dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return false, fmt.Errorf("create library staging file: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	stagedInfo, statErr := file.Stat()
	defer func() {
		// Cleanup is inode-bound and never removes an unexpected replacement.
		// Keep the descriptor open until after cleanup so an unlinked staging
		// inode cannot be recycled and mistaken for a newly created foreign file.
		if stagedInfo != nil {
			if current, err := txn.statAt(name); err == nil && os.SameFile(current, stagedInfo) {
				_ = unix.Unlinkat(int(txn.dir.Fd()), name, 0)
			}
		}
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close library staging file: %w", closeErr)
		}
	}()
	if statErr != nil {
		return false, statErr
	}
	if err := privateInfo(stagedInfo, false); err != nil {
		return false, err
	}
	if err := checkNoExtendedACL(file); err != nil {
		return false, err
	}
	n, err := file.Write(data)
	if err != nil {
		return false, err
	}
	if n != len(data) {
		return false, io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := txn.checkBindings(); err != nil {
		return false, err
	}
	if err := txn.scan(name); err != nil {
		return false, err
	}
	current, currentInfo, err = txn.readSnapshot(txn.readLimit, false)
	if err != nil {
		return false, err
	}
	if (currentInfo == nil) != (txn.beforeInfo == nil) || currentInfo != nil && !os.SameFile(currentInfo, txn.beforeInfo) || !bytes.Equal(current, txn.beforeData) {
		return false, fmt.Errorf("%w: library snapshot changed before publication", ErrUnsafe)
	}
	currentStage, err := txn.statAt(name)
	if err != nil || !os.SameFile(currentStage, stagedInfo) {
		return false, fmt.Errorf("%w: library staging file changed", ErrUnsafe)
	}
	if err := txn.rename(name, snapshotName); err != nil {
		return false, fmt.Errorf("publish library snapshot: %w", err)
	}
	published, txn.readOK = true, false
	// Anything after this line must retain published=true. The complete new
	// bytes are visible even if directory synchronization or binding checks fail.
	if err := txn.syncDir(); err != nil {
		return true, fmt.Errorf("sync published library directory: %w", err)
	}
	if err := txn.checkBindings(); err != nil {
		return true, err
	}
	if err := txn.scan(""); err != nil {
		return true, err
	}
	info, err := txn.statAt(snapshotName)
	if err != nil || !os.SameFile(info, stagedInfo) {
		return true, fmt.Errorf("%w: published snapshot binding changed", ErrUnsafe)
	}
	return true, nil
}

func (txn *fsTxn) close() error {
	if txn == nil {
		return nil
	}
	var first error
	if txn.snapshotAnchor != nil {
		first = txn.snapshotAnchor.Close()
		txn.snapshotAnchor = nil
	}
	if txn.lock != nil {
		if err := unix.Flock(int(txn.lock.Fd()), unix.LOCK_UN); err != nil && first == nil {
			first = err
		}
		if err := txn.lock.Close(); first == nil {
			first = err
		}
		txn.lock = nil
	}
	if txn.dir != nil {
		if err := txn.dir.Close(); first == nil {
			first = err
		}
		txn.dir = nil
	}
	return first
}
