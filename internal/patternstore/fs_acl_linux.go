//go:build linux
// +build linux

package patternstore

import "os"

// Linux POSIX ACL effective group/named-user permissions are limited by the
// group-class mask, which is reflected by stat's group permission bits. The
// caller already requires exactly0700/0600, so no additional grants survive
// that mask. Darwin's extended ACL model requires a different fd check.
func checkNoExtendedACL(*os.File) error { return nil }
