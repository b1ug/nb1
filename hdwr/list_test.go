package hdwr

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/ownership"
)

func TestDeviceInfoListingUsesOnlyTheExplicitDiscoveryScope(t *testing.T) {
	for _, all := range []bool{false, true} {
		blinkCalls, allCalls := 0, 0
		first := &gid.DeviceInfo{VendorID: 0x27b8, ProductID: 0x01ed, SerialNumber: "20000002", Path: "first"}
		second := &gid.DeviceInfo{VendorID: 0x27b8, ProductID: 0x01ed, SerialNumber: "20000002", Path: "second"}
		lower := &gid.DeviceInfo{VendorID: 0x27b8, ProductID: 0x01ed, SerialNumber: "10000001"}
		other := &gid.DeviceInfo{VendorID: 0x1234, ProductID: 0x5678, SerialNumber: "00000000"}
		got := listDeviceInfoFrom(all, func() []*gid.DeviceInfo {
			blinkCalls++
			return []*gid.DeviceInfo{first, lower, second}
		}, func() []*gid.DeviceInfo {
			allCalls++
			return []*gid.DeviceInfo{first, lower, second, other}
		})
		want := []*gid.DeviceInfo{lower, first, second}
		if all {
			want = []*gid.DeviceInfo{other, lower, first, second}
			if blinkCalls != 0 || allCalls != 1 {
				t.Fatalf("--all scope: blink=%d all=%d", blinkCalls, allCalls)
			}
		} else if blinkCalls != 1 || allCalls != 0 {
			t.Fatalf("default enumerated all HID devices: blink=%d all=%d", blinkCalls, allCalls)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stable listing for all=%t: got=%v want=%v", all, got, want)
		}
	}
}

func TestDeviceInfoListingPreservesNilWhenEmpty(t *testing.T) {
	for _, all := range []bool{false, true} {
		if got := listDeviceInfoFrom(all, func() []*gid.DeviceInfo { return nil }, func() []*gid.DeviceInfo { return nil }); got != nil {
			t.Fatalf("empty discovery changed nil semantics for all=%t: %#v", all, got)
		}
	}
}

func TestFirmwareListingChecksOwnershipBeforeNativeOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	t.Setenv("NB1_LOCK_DIR", dir)
	lease, err := ownership.Acquire(dir, "not-a-real-device")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	_, err = readDeviceFirmware(&gid.DeviceInfo{SerialNumber: "not-a-real-device", Path: "never-an-actual-hid-path"})
	if !errors.Is(err, ownership.ErrBusy) {
		t.Fatalf("firmware query bypassed ownership: %v", err)
	}
}
