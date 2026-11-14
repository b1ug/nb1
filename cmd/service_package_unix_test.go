//go:build darwin || linux
// +build darwin linux

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceCLIUsesActivePackagedBindingAndRejectsStaleBinary(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			fixture := serviceCLITestFixture(t, goos)
			prefix, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(prefix, "Cellar", "nb1")
			one, two := filepath.Join(root, "one"), filepath.Join(root, "two")
			for _, version := range []string{one, two} {
				if err := os.MkdirAll(filepath.Join(version, "bin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(version, "bin", "nb1"), []byte(version), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(prefix, "opt"), 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(prefix, "opt", "nb1")
			if err := os.Symlink(one, link); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(link, "bin", "nb1")
			fixture.management.packagedBinary, fixture.management.packagedRoot = entry, root
			fixture.management.executable = func() (string, error) { return filepath.Join(one, "bin", "nb1"), nil }
			var output bytes.Buffer
			if err := executeServiceCLI(fixture.management, context.Background(), &output, "install", "--devices", "20000001", "--json"); err != nil {
				t.Fatal(err)
			}
			calls := fixture.mac.calls
			if goos == "linux" {
				calls = fixture.linux.calls
			}
			if len(calls) != 1 || calls[0].config.Binary != entry || calls[0].config.BinaryRoot != root {
				t.Fatalf("package entry was pinned to its current version: %+v", calls)
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(two, link); err != nil {
				t.Fatal(err)
			}
			fixture.created = map[string]int{}
			output.Reset()
			err = executeServiceCLI(fixture.management, context.Background(), &output, "install", "--devices", "20000001", "--json")
			if err == nil || !strings.Contains(err.Error(), "active package executable differs") || len(fixture.created) != 0 {
				t.Fatalf("stale package binary silently selected the new version: %v factories=%v", err, fixture.created)
			}
			if err := executeServiceCLI(fixture.management, context.Background(), &output, "install", "--binary", filepath.Join(one, "bin", "nb1"), "--devices", "20000001", "--json"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
