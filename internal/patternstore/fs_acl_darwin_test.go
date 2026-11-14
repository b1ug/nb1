//go:build darwin
// +build darwin

package patternstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func darwinGrant(t *testing.T, path, grant string) {
	t.Helper()
	if out, err := exec.Command("/bin/chmod", "+a", grant, path).CombinedOutput(); err != nil {
		t.Fatalf("grant temporary fixture ACL: %v %s", err, out)
	}
}

func darwinACLBytes(t *testing.T, path string) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	attrs := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY}
	data, err := darwinAttributes(file, &attrs)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), data...)
}

func TestFSDarwinACLRejectsGrantAndPreserves(t *testing.T) {
	for _, name := range []string{"directory", snapshotName, lockName} {
		t.Run(name, func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old")
			target := filepath.Join(path, name)
			grant, mode := "everyone allow read,write", os.FileMode(0600)
			if name == "directory" {
				target, grant, mode = path, "everyone allow list,search", 0700
			}
			darwinGrant(t, target, grant)
			info, err := os.Lstat(target)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("fixture must retain private mode despite ACL: %v %v", info, err)
			}
			before := darwinACLBytes(t, target)
			if binary.LittleEndian.Uint32(before[4:8])&unix.ATTR_CMN_EXTENDED_SECURITY == 0 {
				t.Fatal("fixture did not gain an extended ACL")
			}
			if txn, err := openFS(path, false); !errors.Is(err, ErrUnsafe) {
				if txn != nil {
					_ = txn.close()
				}
				t.Fatalf("extended ACL accepted: %v", err)
			}
			if !bytes.Equal(before, darwinACLBytes(t, target)) {
				t.Fatal("rejection modified ACL")
			}
			fsAssertData(t, path, "old")
		})
	}
}

func TestFSDarwinACLRejectsInheritedNewRoot(t *testing.T) {
	parent := t.TempDir()
	darwinGrant(t, parent, "everyone allow list,search,file_inherit,directory_inherit")
	path := filepath.Join(parent, "library")
	if txn, err := openFS(path, true); !errors.Is(err, ErrUnsafe) {
		if txn != nil {
			_ = txn.close()
		}
		t.Fatalf("new root inherited an ACL but was accepted: %v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsafe new root gained managed files: %v %v", entries, err)
	}
	if err := parseDarwinNoACL(darwinACLBytes(t, path)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("inherited ACL was silently removed: %v", err)
	}
}

func TestFSDarwinACLDetectsChangesAfterRead(t *testing.T) {
	for _, name := range []string{"directory", snapshotName, lockName} {
		t.Run(name, func(t *testing.T) {
			path := fsDirectory(t)
			fsSeed(t, path, "old")
			txn := fsOpen(t, path)
			if _, err := txn.read(1024); err != nil {
				t.Fatal(err)
			}
			target, grant := filepath.Join(path, name), "everyone allow read"
			if name == "directory" {
				target, grant = path, "everyone allow list,search"
			}
			darwinGrant(t, target, grant)
			before := darwinACLBytes(t, target)
			if published, err := txn.publish([]byte("new")); published || !errors.Is(err, ErrUnsafe) {
				t.Fatalf("ACL change accepted: published=%v err=%v", published, err)
			}
			if !bytes.Equal(before, darwinACLBytes(t, target)) {
				t.Fatal("failed publication modified ACL")
			}
			fsAssertData(t, path, "old")
		})
	}
}

func darwinNoACLFixture() []byte {
	data := make([]byte, 32)
	binary.LittleEndian.PutUint32(data[:4], 32)
	binary.LittleEndian.PutUint32(data[4:8], unix.ATTR_CMN_RETURNED_ATTRS)
	return data
}

func darwinACLFixture(count uint32, payloadBytes int) []byte {
	data := make([]byte, 32+payloadBytes)
	binary.LittleEndian.PutUint32(data[:4], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[4:8], unix.ATTR_CMN_RETURNED_ATTRS|unix.ATTR_CMN_EXTENDED_SECURITY)
	binary.LittleEndian.PutUint32(data[24:28], 8)
	binary.LittleEndian.PutUint32(data[28:32], uint32(payloadBytes))
	if payloadBytes >= 44 {
		binary.LittleEndian.PutUint32(data[32:36], 0x012cc16d)
		binary.LittleEndian.PutUint32(data[68:72], count)
	}
	return data
}

func TestFSDarwinACLParserBoundaries(t *testing.T) {
	for _, valid := range [][]byte{darwinNoACLFixture(), darwinACLFixture(0xffffffff, 44)} {
		if err := parseDarwinNoACL(valid); err != nil {
			t.Fatalf("known no-ACL representation rejected: %v", err)
		}
	}
	for _, name := range []string{"short", "reported-length", "unknown-mask", "unexpected-group", "noacl-nonzero-reference", "negative-offset", "large-offset", "large-length", "short-payload", "magic", "too-many-entries", "entry-length", "empty-acl", "grant-acl", "noacl-flags", "trailing"} {
		t.Run(name, func(t *testing.T) {
			data := darwinACLFixture(0xffffffff, 44)
			switch name {
			case "short":
				data = data[:31]
			case "reported-length":
				binary.LittleEndian.PutUint32(data[:4], 8193)
			case "unknown-mask":
				binary.LittleEndian.PutUint32(data[4:8], 0xffffffff)
			case "unexpected-group":
				binary.LittleEndian.PutUint32(data[8:12], 1)
			case "noacl-nonzero-reference":
				data = darwinNoACLFixture()
				binary.LittleEndian.PutUint32(data[24:28], 8)
			case "negative-offset":
				binary.LittleEndian.PutUint32(data[24:28], 0xffffffff)
			case "large-offset":
				binary.LittleEndian.PutUint32(data[24:28], 0x7fffffff)
			case "large-length":
				binary.LittleEndian.PutUint32(data[28:32], 0xffffffff)
			case "short-payload":
				data = darwinACLFixture(0, 43)
			case "magic":
				binary.LittleEndian.PutUint32(data[32:36], 0)
			case "too-many-entries":
				binary.LittleEndian.PutUint32(data[68:72], 129)
			case "entry-length":
				binary.LittleEndian.PutUint32(data[68:72], 1)
			case "empty-acl":
				data = darwinACLFixture(0, 44)
			case "grant-acl":
				data = darwinACLFixture(1, 68)
			case "noacl-flags":
				binary.LittleEndian.PutUint32(data[72:76], 0x20000)
			case "trailing":
				data = append(data, 0)
				binary.LittleEndian.PutUint32(data[:4], uint32(len(data)))
			}
			if err := parseDarwinNoACL(data); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("malformed or extended ACL accepted: %v", err)
			}
		})
	}
}

func TestFSDarwinVolumeParserBoundaries(t *testing.T) {
	fixture := func() []byte {
		data := make([]byte, 56)
		binary.LittleEndian.PutUint32(data[:4], 56)
		binary.LittleEndian.PutUint32(data[4:8], unix.ATTR_CMN_RETURNED_ATTRS)
		binary.LittleEndian.PutUint32(data[8:12], unix.ATTR_VOL_CAPABILITIES)
		binary.LittleEndian.PutUint32(data[28:32], 0x600)
		binary.LittleEndian.PutUint32(data[44:48], 0x600)
		return data
	}
	if err := parseDarwinVolume(fixture()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"short", "length", "common-mask", "volume-mask", "group", "acl-unsupported", "acl-validity-unknown", "flock-unsupported", "flock-validity-unknown"} {
		t.Run(name, func(t *testing.T) {
			data := fixture()
			switch name {
			case "short":
				data = data[:55]
			case "length":
				binary.LittleEndian.PutUint32(data[:4], 57)
			case "common-mask":
				binary.LittleEndian.PutUint32(data[4:8], 0)
			case "volume-mask":
				binary.LittleEndian.PutUint32(data[8:12], unix.ATTR_VOL_CAPABILITIES|unix.ATTR_VOL_INFO)
			case "group":
				binary.LittleEndian.PutUint32(data[16:20], 1)
			case "acl-unsupported":
				binary.LittleEndian.PutUint32(data[28:32], 0x200)
			case "acl-validity-unknown":
				binary.LittleEndian.PutUint32(data[44:48], 0x200)
			case "flock-unsupported":
				binary.LittleEndian.PutUint32(data[28:32], 0x400)
			case "flock-validity-unknown":
				binary.LittleEndian.PutUint32(data[44:48], 0x400)
			}
			if err := parseDarwinVolume(data); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unverified volume accepted: %v", err)
			}
		})
	}
}
