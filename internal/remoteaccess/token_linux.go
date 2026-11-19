package remoteaccess

import "os"

// The group mode bits already include the effective POSIX ACL mask.
func privateTokenACL(*os.File) bool { return true }
