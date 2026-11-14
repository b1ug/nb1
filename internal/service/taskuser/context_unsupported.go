//go:build !windows
// +build !windows

package taskuser

func Current() (Context, error) { return Context{}, ErrUnsupported }
