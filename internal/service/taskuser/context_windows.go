//go:build windows
// +build windows

package taskuser

import (
	"syscall"
	"unsafe"
)

// Current opens only this process's token with TOKEN_QUERY via the Go 1.18
// standard library. It never obtains a linked token or changes token rights.
func Current() (Context, error) {
	return readContext(func() (tokenReader, error) {
		token, err := syscall.OpenCurrentProcessToken()
		if err != nil {
			return nil, err
		}
		return nativeToken{token}, nil
	})
}

type nativeToken struct{ token syscall.Token }

func (n nativeToken) close() error { return n.token.Close() }

func (n nativeToken) info(class uint32) ([]byte, error) {
	return queryInfo(func(buffer []byte) (uint32, error) {
		var size uint32
		err := syscall.GetTokenInformation(n.token, class, &buffer[0], uint32(len(buffer)), &size)
		if err == syscall.ERROR_INSUFFICIENT_BUFFER {
			err = errBufferSmall
		}
		return size, err
	})
}

func nativeLayout() tokenLayout {
	var groups struct {
		Count uint32
		Items [1]syscall.SIDAndAttributes
	}
	return tokenLayout{int(unsafe.Sizeof(uintptr(0))), int(unsafe.Sizeof(syscall.SIDAndAttributes{})), int(unsafe.Offsetof(groups.Items))}
}

func (n nativeToken) label(class uint32, integrity bool) (string, error) {
	buffer, err := n.info(class)
	if err != nil {
		return "", err
	}
	return decodeLabel(buffer, uint64(uintptr(unsafe.Pointer(&buffer[0]))), nativeLayout(), integrity)
}

func (n nativeToken) userSID() (string, error) { return n.label(syscall.TokenUser, false) }
func (n nativeToken) integritySID() (string, error) {
	return n.label(syscall.TokenIntegrityLevel, true)
}

func (n nativeToken) dword(class uint32) (uint32, error) {
	buffer, err := n.info(class)
	if err != nil {
		return 0, err
	}
	return decodeDWORD(buffer)
}

func (n nativeToken) elevation() (uint32, error)     { return n.dword(syscall.TokenElevation) }
func (n nativeToken) elevationType() (uint32, error) { return n.dword(syscall.TokenElevationType) }
func (n nativeToken) sessionID() (uint32, error)     { return n.dword(syscall.TokenSessionId) }
func (n nativeToken) groups() ([]tokenGroup, error) {
	buffer, err := n.info(syscall.TokenGroups)
	if err != nil {
		return nil, err
	}
	return decodeGroups(buffer, uint64(uintptr(unsafe.Pointer(&buffer[0]))), nativeLayout())
}
