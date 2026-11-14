//go:build !darwin && !linux && !windows
// +build !darwin,!linux,!windows

package ownership

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("device ownership locks are unsupported on this platform")

func checkPrivateDirectory(info os.FileInfo) error { return errUnsupported }
func openLockFile(path string) (*os.File, error)   { return nil, errUnsupported }
func lock(file *os.File) error                     { return errUnsupported }
func unlock(file *os.File) error                   { return errUnsupported }
