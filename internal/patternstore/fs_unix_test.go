//go:build darwin || linux
// +build darwin linux

package patternstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fsDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "library")
}

func fsOpen(t *testing.T, path string) *fsTxn {
	t.Helper()
	txn, err := openFS(path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = txn.close() })
	return txn
}

func fsSeed(t *testing.T, path string, data string) {
	t.Helper()
	txn := fsOpen(t, path)
	if _, err := txn.read(1024); err != nil {
		t.Fatal(err)
	}
	if published, err := txn.publish([]byte(data)); err != nil || !published {
		t.Fatalf("seed: published=%v err=%v", published, err)
	}
	if err := txn.close(); err != nil {
		t.Fatal(err)
	}
}

func fsAssertData(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path, snapshotName))
	if err != nil || string(data) != want {
		t.Fatalf("snapshot=%q want=%q err=%v", data, want, err)
	}
}

func TestFSLifecycleAndMissingRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "library")
	if _, err := openFS(path, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing readonly directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readonly open created parent: %v", err)
	}
	txn := fsOpen(t, path)
	data, err := txn.read(1024)
	if err != nil || data != nil {
		t.Fatalf("empty read: data=%q err=%v", data, err)
	}
	if published, err := txn.publish([]byte("first")); err != nil || !published {
		t.Fatalf("first publish: %v %v", published, err)
	}
	if err := txn.close(); err != nil {
		t.Fatal(err)
	}
	if err := txn.close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	for name, mode := range map[string]os.FileMode{"": 0700, lockName: 0600, snapshotName: 0600} {
		info, err := os.Lstat(filepath.Join(path, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("mode %s: info=%v err=%v", name, info, err)
		}
	}
	again, err := openFS(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer again.close()
	data, err = again.read(1024)
	if err != nil || string(data) != "first" {
		t.Fatalf("reopen: %q %v", data, err)
	}
	data[0] = 'X'
	if published, err := again.publish([]byte("second")); err != nil || !published {
		t.Fatalf("returned bytes aliased transaction snapshot: %v %v", published, err)
	}
	fsAssertData(t, path, "second")
}

func TestFSBusyAndProcessLock(t *testing.T) {
	path := fsDirectory(t)
	fsSeed(t, path, "old")
	txn := fsOpen(t, path)
	if _, err := openFS(path, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second open must report busy: %v", err)
	}
	run := func(want int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFSLockProcessHelper$")
		cmd.Env = append(os.Environ(), "NB1_PATTERNSTORE_LOCK_HELPER="+path)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("helper: %v output=%s", err, out)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("helper exit=%d want=%d output=%s", code, want, out)
		}
	}
	run(29)
	if err := txn.close(); err != nil {
		t.Fatal(err)
	}
	run(0)
}

func TestFSLockProcessHelper(t *testing.T) {
	path := os.Getenv("NB1_PATTERNSTORE_LOCK_HELPER")
	if path == "" {
		return
	}
	txn, err := openFS(path, false)
	if errors.Is(err, ErrBusy) {
		os.Exit(29)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(30)
	}
	if err := txn.close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(31)
	}
	os.Exit(0)
}

const fsInterruptedSnapshot = "new-complete-snapshot-after-process-exit"

func TestFSProcessInterruptionCommitBoundary(t *testing.T) {
	for _, phase := range []string{"before-rename", "after-rename"} {
		t.Run(phase, func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old-complete-snapshot")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFSInterruptionProcessHelper$")
			// Do not inherit either helper's trigger from the invoking shell.
			// Only this one test invocation receives the explicit fixture path.
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "NB1_PATTERNSTORE_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "NB1_PATTERNSTORE_INTERRUPT_HELPER="+path, "NB1_PATTERNSTORE_INTERRUPT_PHASE="+phase)
			out, err := cmd.CombinedOutput()
			wantExit := 42
			if phase == "after-rename" {
				wantExit = 43
			}
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != wantExit {
				t.Fatalf("interrupted helper: want exit=%d err=%v context=%v output=%s", wantExit, err, ctx.Err(), out)
			}
			// Verify kernel lock release independently of openFS's unknown-file
			// rejection. The child never ran deferred Go cleanup or Close.
			lock, err := os.OpenFile(filepath.Join(path, lockName), os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
				_ = lock.Close()
				t.Fatalf("kernel did not release exited process lock: %v", err)
			}
			unlockErr := unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			closeErr := lock.Close()
			if unlockErr != nil || closeErr != nil {
				t.Fatalf("parent lock cleanup: unlock=%v close=%v", unlockErr, closeErr)
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "before-rename" {
				fsAssertData(t, path, "old-complete-snapshot")
				var pending string
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".pending-") {
						if pending != "" {
							t.Fatal("more than one orphan staging file")
						}
						pending = filepath.Join(path, entry.Name())
					}
				}
				if len(entries) != 3 || pending == "" {
					t.Fatalf("expected old snapshot, lock and orphan staging file: %v", entries)
				}
				data, err := os.ReadFile(pending)
				if err != nil || string(data) != fsInterruptedSnapshot {
					t.Fatalf("orphan did not contain complete synced bytes: %q %v", data, err)
				}
				if other, err := openFS(path, false); !errors.Is(err, ErrUnsafe) {
					if other != nil {
						_ = other.close()
					}
					t.Fatalf("orphan was accepted or silently cleared: %v", err)
				}
				data, err = os.ReadFile(pending)
				if err != nil || string(data) != fsInterruptedSnapshot {
					t.Fatalf("rejected open modified orphan: %q %v", data, err)
				}
				fsAssertData(t, path, "old-complete-snapshot")
			} else {
				fsAssertData(t, path, fsInterruptedSnapshot)
				if len(entries) != 2 {
					t.Fatalf("renamed transaction left staging data: %v", entries)
				}
				again, err := openFS(path, false)
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := again.read(1024)
				closeErr := again.close()
				if readErr != nil || closeErr != nil || string(data) != fsInterruptedSnapshot {
					t.Fatalf("reopen after confirmed rename: data=%q read=%v close=%v", data, readErr, closeErr)
				}
			}
		})
	}
}

func TestFSInterruptionProcessHelper(t *testing.T) {
	path := os.Getenv("NB1_PATTERNSTORE_INTERRUPT_HELPER")
	if path == "" {
		return
	}
	phase := os.Getenv("NB1_PATTERNSTORE_INTERRUPT_PHASE")
	txn, err := openFS(path, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(44)
	}
	if _, err := txn.read(1024); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(45)
	}
	switch phase {
	case "before-rename":
		txn.rename = func(string, string) error {
			// The real staging Write and Sync have completed. Exit without
			// running deferred cleanup or performing the rename syscall.
			os.Exit(42)
			return nil
		}
	case "after-rename":
		txn.syncDir = func() error {
			// The real rename syscall has succeeded, but directory Sync and
			// all Go cleanup have yet to run.
			os.Exit(43)
			return nil
		}
	default:
		os.Exit(46)
	}
	_, err = txn.publish([]byte(fsInterruptedSnapshot))
	fmt.Fprintln(os.Stderr, "helper unexpectedly returned from publication:", err)
	os.Exit(47)
}

func TestFSRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"unknown", "pending", "snapshot-symlink", "lock-symlink", "snapshot-hardlink", "snapshot-fifo", "snapshot-directory", "snapshot-mode", "lock-mode", "directory-mode"} {
		t.Run(name, func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old")
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot := filepath.Join(path, snapshotName)
			lock := filepath.Join(path, lockName)
			var err error
			switch name {
			case "unknown":
				err = os.WriteFile(filepath.Join(path, "do-not-delete.txt"), []byte("unknown"), 0600)
			case "pending":
				err = os.WriteFile(filepath.Join(path, ".pending-orphan"), []byte("unfinished"), 0600)
			case "snapshot-symlink":
				_ = os.Remove(snapshot)
				err = os.Symlink(outside, snapshot)
			case "lock-symlink":
				_ = os.Remove(lock)
				err = os.Symlink(outside, lock)
			case "snapshot-hardlink":
				_ = os.Remove(snapshot)
				err = os.Link(outside, snapshot)
			case "snapshot-fifo":
				_ = os.Remove(snapshot)
				err = unix.Mkfifo(snapshot, 0600)
			case "snapshot-directory":
				_ = os.Remove(snapshot)
				err = os.Mkdir(snapshot, 0700)
			case "snapshot-mode":
				err = os.Chmod(snapshot, 0644)
			case "lock-mode":
				err = os.Chmod(lock, 0644)
			case "directory-mode":
				err = os.Chmod(path, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadDir(path)
			if err != nil {
				t.Fatal(err)
			}
			if txn, err := openFS(path, false); err == nil {
				_ = txn.close()
				t.Fatal("unsafe entry accepted")
			}
			after, _ := os.ReadDir(path)
			if len(before) != len(after) {
				t.Fatalf("unsafe directory modified: before=%v after=%v", before, after)
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "outside" {
				t.Fatalf("outside file changed: %q", data)
			}
		})
	}
	t.Run("directory-symlink", func(t *testing.T) {
		path := fsDirectory(t)
		actual := t.TempDir()
		if err := os.Symlink(actual, path); err != nil {
			t.Fatal(err)
		}
		if _, err := openFS(path, true); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("directory symlink: %v", err)
		}
		entries, _ := os.ReadDir(actual)
		if len(entries) != 0 {
			t.Fatal("directory symlink target was modified")
		}
	})
}

func TestFSReadAndPublishDetectReplacement(t *testing.T) {
	for _, name := range []string{"snapshot-replace", "snapshot-rewrite", "lock-replace", "directory-replace", "snapshot-mode", "lock-mode", "directory-mode", "unknown-after-read"} {
		t.Run(name, func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old")
			txn := fsOpen(t, path)
			if _, err := txn.read(1024); err != nil {
				t.Fatal(err)
			}
			snapshot, lock := filepath.Join(path, snapshotName), filepath.Join(path, lockName)
			var err error
			switch name {
			case "snapshot-replace":
				_ = os.Remove(snapshot)
				err = os.WriteFile(snapshot, []byte("old"), 0600)
			case "snapshot-rewrite":
				err = os.WriteFile(snapshot, []byte("changed"), 0600)
			case "lock-replace":
				_ = os.Remove(lock)
				err = os.WriteFile(lock, nil, 0600)
			case "directory-replace":
				err = os.Rename(path, path+"-previous")
				if err == nil {
					err = os.Mkdir(path, 0700)
				}
			case "snapshot-mode":
				err = os.Chmod(snapshot, 0644)
			case "lock-mode":
				err = os.Chmod(lock, 0644)
			case "directory-mode":
				err = os.Chmod(path, 0755)
			case "unknown-after-read":
				err = os.WriteFile(filepath.Join(path, "foreign"), []byte("keep"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if published, err := txn.publish([]byte("new")); err == nil || published {
				t.Fatalf("replacement accepted: published=%v err=%v", published, err)
			}
			if name == "snapshot-rewrite" {
				fsAssertData(t, path, "changed")
			} else if name != "directory-replace" {
				fsAssertData(t, path, "old")
			} else {
				fsAssertData(t, path+"-previous", "old")
			}
		})
	}
}

func TestFSPublicationCommitBoundary(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_rename_%v", afterRename), func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old")
			txn := fsOpen(t, path)
			if _, err := txn.read(1024); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected filesystem failure")
			if afterRename {
				txn.syncDir = func() error { return injected }
			} else {
				txn.rename = func(string, string) error { return injected }
			}
			published, err := txn.publish([]byte("new"))
			if !errors.Is(err, injected) || published != afterRename {
				t.Fatalf("commit boundary lost: published=%v err=%v", published, err)
			}
			want := "old"
			if afterRename {
				want = "new"
			}
			fsAssertData(t, path, want)
			entries, _ := os.ReadDir(path)
			if len(entries) != 2 {
				t.Fatalf("own staging file leaked: %v", entries)
			}
		})
	}
}

func TestFSConcurrentOpenSeesBusyDuringStaging(t *testing.T) {
	path := fsDirectory(t)
	fsSeed(t, path, "old")
	txn := fsOpen(t, path)
	if _, err := txn.read(1024); err != nil {
		t.Fatal(err)
	}
	rename := txn.rename
	txn.rename = func(from, to string) error {
		// This is inside the writer's staging interval: the private pending file
		// exists, but a second cooperating opener must first observe the lock.
		if other, err := openFS(path, false); !errors.Is(err, ErrBusy) {
			if other != nil {
				_ = other.close()
			}
			return fmt.Errorf("concurrent open must see Busy during staging, got %v", err)
		}
		return rename(from, to)
	}
	if published, err := txn.publish([]byte("new")); !published || err != nil {
		t.Fatalf("staged writer: %v %v", published, err)
	}
}

func TestFSStagingCleanupPreservesForeignReplacement(t *testing.T) {
	path := fsDirectory(t)
	fsSeed(t, path, "old")
	txn := fsOpen(t, path)
	if _, err := txn.read(1024); err != nil {
		t.Fatal(err)
	}
	var replacement string
	txn.rename = func(from, to string) error {
		replacement = filepath.Join(path, from)
		if err := os.Remove(replacement); err != nil {
			return err
		}
		if err := os.WriteFile(replacement, []byte("foreign replacement"), 0600); err != nil {
			return err
		}
		return syscall.EIO
	}
	if published, err := txn.publish([]byte("new")); published || err == nil {
		t.Fatalf("expected failed publication: %v %v", published, err)
	}
	data, err := os.ReadFile(replacement)
	if err != nil || string(data) != "foreign replacement" {
		t.Fatalf("foreign staging replacement removed: %q %v", data, err)
	}
	fsAssertData(t, path, "old")
}

func TestFSReadLimitsAndInvalidState(t *testing.T) {
	path := fsDirectory(t)
	fsSeed(t, path, "1234")
	txn := fsOpen(t, path)
	if published, err := txn.publish([]byte("new")); published || err == nil {
		t.Fatalf("publish before read: %v %v", published, err)
	}
	if _, err := txn.read(3); err == nil {
		t.Fatal("oversize old snapshot accepted")
	}
	if published, err := txn.publish([]byte("new")); published || err == nil {
		t.Fatalf("publish after failed read: %v %v", published, err)
	}
	if _, err := txn.read(4); err != nil {
		t.Fatal(err)
	}
	if published, err := txn.publish([]byte("12345")); published || err == nil {
		t.Fatalf("oversize new snapshot: %v %v", published, err)
	}
	if err := txn.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := txn.read(4); err == nil {
		t.Fatal("read after close accepted")
	}
	fsAssertData(t, path, "1234")
	for _, path := range []string{"", "relative/path", string(filepath.Separator)} {
		if _, err := openFS(path, true); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("unsafe path %q: %v", path, err)
		}
	}
}

func TestFSCanonicalParentAlias(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	txn := fsOpen(t, filepath.Join(alias, "library"))
	if strings.Contains(txn.path, "/alias/") {
		t.Fatalf("parent alias was not canonicalized: %s", txn.path)
	}
	if _, err := txn.read(1024); err != nil {
		t.Fatal(err)
	}
}
