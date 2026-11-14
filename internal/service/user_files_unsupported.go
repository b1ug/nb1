//go:build !darwin && !linux
// +build !darwin,!linux

package service

import "os"

func ownedBy(os.FileInfo, int) bool        { return false }
func openManaged(string) (*os.File, error) { return nil, ErrUserServiceUnsupported }
func createManaged(string, []byte) error   { return ErrUserServiceUnsupported }
