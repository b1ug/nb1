//go:build !darwin && !linux && !windows

package remoteaccess

import "os"

func openTokenFile(string, bool) (*os.File, error) {
	return nil, errPrivateTokenFile
}
