//go:build !linux && !darwin
// +build !linux,!darwin

package systemduser

import "os"

func ownedBy(os.FileInfo, int) bool        { return false }
func openRegular(string) (*os.File, error) { return nil, ErrUnsupported }
func publish(string, []byte) error         { return ErrUnsupported }
