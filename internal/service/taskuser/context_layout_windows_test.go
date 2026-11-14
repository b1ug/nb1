//go:build windows
// +build windows

package taskuser

import "testing"

// This checks native Go struct alignment only; it does not open a token.
func TestNativeContextLayout(t *testing.T) {
	if !nativeLayout().valid() {
		t.Fatalf("unsupported native ABI: %+v", nativeLayout())
	}
}
