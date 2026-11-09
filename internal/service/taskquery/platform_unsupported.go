//go:build !windows
// +build !windows

package taskquery

func newPlatformReader() (*Reader, error) { return nil, ErrUnsupported }
