package devicebackend

import (
	"errors"
	"fmt"
	"testing"
)

type codedTestError struct {
	platform string
	code     uint64
	cause    error
}

func (e codedTestError) Error() string          { return e.cause.Error() }
func (e codedTestError) Unwrap() error          { return e.cause }
func (e codedTestError) NativePlatform() string { return e.platform }
func (e codedTestError) NativeCode() uint64     { return e.code }

func TestNativeCodeTakesPrecedenceOverErrorWording(t *testing.T) {
	for _, tc := range []struct {
		platform string
		code     uint64
		message  string
		retry    bool
	}{
		{"darwin", 0xe00002bc, "localized native error", true},
		{"darwin", 0xe00002d5, "localized busy", true},
		{"darwin", 0xe00002d6, "localized timeout", true},
		{"darwin", 0xdeadbeef, "general error", false},
		{"windows", 0xe00002bc, "general error", false},
	} {
		err := fmt.Errorf("SDK query: %w", codedTestError{tc.platform, tc.code, errors.New(tc.message)})
		if got := transientMK2Transport(err); got != tc.retry {
			t.Errorf("%s/%x: retry=%v want%v", tc.platform, tc.code, got, tc.retry)
		}
	}
	if !transientMK2Transport(fmt.Errorf("legacy SDK: %w", errors.New("general error"))) {
		t.Fatal("older GID compatibility path disappeared")
	}
}
