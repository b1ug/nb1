//go:build !darwin && !linux && !windows

package triggerv1

import "os"

func openFile(path string) (*os.File, error) { return os.Open(path) }
