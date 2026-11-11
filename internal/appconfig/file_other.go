//go:build !darwin && !linux && !windows
// +build !darwin,!linux,!windows

package appconfig

import "os"

func openConfig(path string) (*os.File, error) { return os.Open(path) }
func privateDirectory(info os.FileInfo) bool   { return info.Mode().Perm()&0077 == 0 }
