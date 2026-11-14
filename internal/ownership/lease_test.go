//go:build darwin || linux || windows
// +build darwin linux windows

package ownership

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAcquireContendsAndReleases(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	first, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Acquire(dir, "20005601"); !errors.Is(err, ErrBusy) || second != nil {
		if second != nil {
			second.Close()
		}
		t.Fatalf("duplicate acquire = %v, %v; want nil, ErrBusy", second, err)
	}
	other, err := Acquire(dir, "20005602")
	if err != nil {
		t.Fatalf("another serial should be independent: %v", err)
	}
	other.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestLeaseCloseConcurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	again, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestSerialDoesNotBecomeAPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "../other/device/设备\x00")
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("lock directory = %v, %v", entries, err)
	}
	if name := entries[0].Name(); !strings.HasSuffix(name, ".lock") || len(name) != 69 || entries[0].IsDir() {
		t.Fatalf("expected a SHA-256 lock filename, got %q", name)
	}
	// Close must keep this same file available to all future contenders.
	if _, err := os.Stat(filepath.Join(dir, entries[0].Name())); err != nil {
		t.Fatalf("Close removed lock file: %v", err)
	}
}

func TestBlankSerialRejected(t *testing.T) {
	for _, serial := range []string{"", " ", "\n\t"} {
		if lease, err := Acquire(t.TempDir(), serial); err == nil || lease != nil {
			t.Fatalf("Acquire(%q) = %v, %v", serial, lease, err)
		}
	}
}

func TestCrossProcessContention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	lease, err := Acquire(dir, "20005601")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	runHelper(t, dir, "expect-busy")
	lease.Close()
	runHelper(t, dir, "acquire")
}

func TestProcessExitReleasesOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := helperCommand(ctx, dir, "hold-and-exit")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() || lines.Text() != "locked" {
		cancel()
		waitErr := cmd.Wait()
		t.Fatalf("helper did not acquire lock: %v, %v, %s", lines.Err(), waitErr, stderr.String())
	}
	if lease, err := Acquire(dir, "20005601"); !errors.Is(err, ErrBusy) || lease != nil {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("parent acquired live child's lock: %v", err)
	}
	if _, err := stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v, %s", err, stderr.String())
	}
	// Windows may release terminated-process locks asynchronously. No PID-file
	// deletion or stale-file cleanup should be needed on any platform.
	deadline := time.Now().Add(2 * time.Second)
	for {
		lease, err := Acquire(dir, "20005601")
		if err == nil {
			lease.Close()
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("process exit did not release lock: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runHelper(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := helperCommand(ctx, dir, mode).CombinedOutput(); err != nil {
		t.Fatalf("helper %s: %v\n%s", mode, err, output)
	}
}

func helperCommand(ctx context.Context, dir, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnershipHelper$")
	cmd.Env = append(os.Environ(), "NB1_OWNERSHIP_HELPER="+mode, "NB1_OWNERSHIP_DIR="+dir)
	return cmd
}

func TestOwnershipHelper(t *testing.T) {
	mode := os.Getenv("NB1_OWNERSHIP_HELPER")
	if mode == "" {
		return
	}
	lease, err := Acquire(os.Getenv("NB1_OWNERSHIP_DIR"), "20005601")
	if mode == "expect-busy" {
		if lease != nil {
			lease.Close()
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("expected ownership conflict, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "hold-and-exit" {
		fmt.Fprintln(os.Stdout, "locked")
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(lease)
		os.Exit(0) // Deliberately bypass Lease.Close to test kernel cleanup.
	}
	if mode != "acquire" {
		lease.Close()
		t.Fatalf("unknown helper mode %q", mode)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
