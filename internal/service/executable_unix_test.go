//go:build darwin || linux
// +build darwin linux

package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func boundFixture(t *testing.T, prefix string) (string, string, string, string) {
	t.Helper()
	prefix, err := filepath.EvalSymlinks(prefix)
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
	return filepath.Join(link, "bin", "nb1"), root, one, two
}

func TestBoundExecutableFollowsUpgradeAndOldKegRemoval(t *testing.T) {
	binary, root, one, two := boundFixture(t, t.TempDir())
	if got, err := ResolveBoundExecutable(binary, root, os.Geteuid()); err != nil || got != filepath.Join(one, "bin", "nb1") {
		t.Fatalf("initial package binding = %s, %v", got, err)
	}
	link := filepath.Dir(filepath.Dir(binary))
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(two, link); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(one); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveBoundExecutable(binary, root, os.Geteuid()); err != nil || got != filepath.Join(two, "bin", "nb1") {
		t.Fatalf("upgraded package binding = %s, %v", got, err)
	}
}

func TestBoundExecutableRejectsEscapeAndUnsafeTarget(t *testing.T) {
	for _, variant := range []string{"escape", "sibling-rack", "missing", "not-executable", "writable", "unsafe-directory"} {
		t.Run(variant, func(t *testing.T) {
			prefix := t.TempDir()
			binary, root, one, _ := boundFixture(t, prefix)
			file := filepath.Join(one, "bin", "nb1")
			switch variant {
			case "escape", "sibling-rack":
				outside := filepath.Join(filepath.Dir(root), "nb1-other", "one")
				if err := os.MkdirAll(filepath.Join(outside, "bin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outside, "bin", "nb1"), []byte("outside"), 0700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Dir(filepath.Dir(binary))
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				if variant == "escape" {
					root = one
				}
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "not-executable":
				if err := os.Chmod(file, 0600); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(file, 0777); err != nil {
					t.Fatal(err)
				}
			case "unsafe-directory":
				if err := os.Chmod(filepath.Dir(file), 0777); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ResolveBoundExecutable(binary, root, os.Geteuid()); err == nil {
				t.Fatal("unsafe package executable accepted")
			}
		})
	}
}

func TestUserBoundInstallAndStartKeepStableConfiguration(t *testing.T) {
	m, runner, config := fixtureManager(t)
	binary, root, one, two := boundFixture(t, m.home)
	config.Binary, config.BinaryRoot = binary, root
	runner.steps = absentCalls(m)
	status, err := m.Install(context.Background(), config, false)
	if err != nil || status.Config == nil || status.Config.Binary != binary || status.Config.BinaryRoot != root {
		t.Fatalf("stable install = %+v %v", status, err)
	}
	original, err := os.ReadFile(m.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Dir(filepath.Dir(binary))
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(two, link); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(one); err != nil {
		t.Fatal(err)
	}
	runner.steps = append(absentCalls(m), fakeCall{args: []string{"bootstrap", m.domain(), m.plistPath()}}, registeredCall(m, config))
	status, err = m.Start(context.Background())
	if err != nil || !status.RegistrationOwned {
		t.Fatalf("start upgraded binding = %+v %v", status, err)
	}
	assertContent(t, m.plistPath(), original)
}

func TestBoundExecutableGroupWriteRequiresAnIndependentPrivateBoundary(t *testing.T) {
	private, root, one, _ := boundFixture(t, t.TempDir())
	if err := os.Chmod(filepath.Dir(filepath.Join(one, "bin", "nb1")), 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBoundExecutable(private, root, os.Geteuid()); err != nil {
		t.Fatalf("group-writable directory below a private boundary rejected: %v", err)
	}
	public, err := os.MkdirTemp("/tmp", "nb1-binding-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(public) })
	publicEntry, publicRoot, publicOne, _ := boundFixture(t, public)
	for _, directory := range []string{public, filepath.Dir(publicRoot), publicRoot, publicOne, filepath.Join(public, "opt")} {
		if err := os.Chmod(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(publicOne, "bin")
	if err := os.Chmod(bin, 0775); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" && info.Sys().(*syscall.Stat_t).Gid == 80 {
		t.Skip("the public fixture inherits the separately trusted macOS administrator group")
	}
	if _, err := ResolveBoundExecutable(publicEntry, publicRoot, os.Geteuid()); err == nil {
		t.Fatal("public group-writable directory accepted")
	}
	link := filepath.Dir(filepath.Dir(private))
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(publicOne, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBoundExecutable(private, publicRoot, os.Geteuid()); err == nil {
		t.Fatal("private source link transferred trust to its public target")
	}
	if err := os.Remove(filepath.Join(publicOne, "bin", "nb1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(one, "bin", "nb1"), filepath.Join(publicOne, "bin", "nb1")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBoundExecutable(private, root, os.Geteuid()); err == nil {
		t.Fatal("private-to-public-to-private link chain hid a writable bridge")
	}
}

func TestBoundExecutableRejectsParentTraversalAfterALinkComponent(t *testing.T) {
	prefix := t.TempDir()
	binary, root, one, _ := boundFixture(t, prefix)
	safe, bridge := filepath.Join(prefix, "safe"), filepath.Join(prefix, "bridge")
	for _, directory := range []string{safe, filepath.Join(bridge, "child")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(safe, "jump"):      filepath.Join(bridge, "child"),
		filepath.Join(safe, "version"):   one,
		filepath.Join(bridge, "version"): one,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Dir(filepath.Dir(binary))
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../safe/jump/../version", link); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(binary); err != nil || resolved != filepath.Join(one, "bin", "nb1") {
		t.Fatalf("fixture did not return to its safe rack: %s %v", resolved, err)
	}
	if _, err := ResolveBoundExecutable(binary, root, os.Geteuid()); err == nil {
		t.Fatal("lexical cleaning hid a link traversal")
	}
}
