package remoteaccess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDarwinTokenFileRejectsACLGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := GenerateTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if result, err := exec.Command("/bin/chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("could not set test ACL: %s (%v)", result, err)
	}
	if token, err := LoadToken(path); err == nil || token != "" {
		t.Fatal("token with an extended ACL grant accepted")
	}
}
