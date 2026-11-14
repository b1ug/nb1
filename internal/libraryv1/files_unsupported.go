//go:build !darwin && !linux && !windows
// +build !darwin,!linux,!windows

package libraryv1

import "os"

func privateMode(os.FileInfo) bool        { return false }
func openRecord(string) (*os.File, error) { return nil, ErrInvalid }
func replaceRecord(string, string) error  { return ErrInvalid }
