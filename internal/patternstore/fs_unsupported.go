//go:build !darwin && !linux && !windows
// +build !darwin,!linux,!windows

package patternstore

type fsTxn struct{}

func openFS(string, bool) (*fsTxn, error)   { return nil, ErrUnsupported }
func (*fsTxn) read(int) ([]byte, error)     { return nil, ErrUnsupported }
func (*fsTxn) publish([]byte) (bool, error) { return false, ErrUnsupported }
func (*fsTxn) close() error                 { return nil }
