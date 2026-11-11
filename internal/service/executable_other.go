//go:build !darwin && !linux
// +build !darwin,!linux

package service

import "fmt"

func ResolveBoundExecutable(binary, root string, uid int) (string, error) {
	return "", fmt.Errorf("package executable bindings require macOS or Linux")
}
