//go:build !windows
// +build !windows

package taskuser

import (
	"errors"
	"testing"
)

func TestCurrentUnsupported(t *testing.T) {
	c, err := Current()
	if !errors.Is(err, ErrUnsupported) || c != (Context{}) {
		t.Fatalf("context=%+v err=%v", c, err)
	}
}
