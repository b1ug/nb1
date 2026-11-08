package hdwr

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/internal/ownership"
)

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
