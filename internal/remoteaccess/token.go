package remoteaccess

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
)

const maxTokenFileSize = 4096

var errPrivateTokenFile = errors.New("remote token file must be a private regular file owned by the current user")

// LoadToken reads a private bearer token file with an optional final LF or CRLF.
func LoadToken(path string) (string, error) {
	file, err := openTokenFile(path, false)
	if err != nil {
		return "", err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxTokenFileSize+1))
	if err != nil || len(data) > maxTokenFileSize {
		return "", errors.New("could not read remote token file within 4096 bytes")
	}
	token := strings.TrimSuffix(string(data), "\r\n")
	if len(token) == len(data) {
		token = strings.TrimSuffix(token, "\n")
	}
	if !validBearerToken(token) {
		return "", errors.New("remote token file must contain one bearer token of at least 32 characters")
	}
	return token, nil
}

// GenerateTokenFile creates a private file without replacing an existing entry.
func GenerateTokenFile(path string) error {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return errors.New("could not generate remote token")
	}
	token := base64.RawURLEncoding.EncodeToString(bytes[:]) + "\n"

	file, err := openTokenFile(path, true)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	_, writeErr := io.WriteString(file, token)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr == nil && syncErr == nil && closeErr == nil {
		return nil
	}
	if statErr == nil {
		removeOwnTokenFile(path, info)
	}
	return errors.New("could not save remote token file")
}

func removeOwnTokenFile(path string, original fs.FileInfo) {
	if current, err := os.Lstat(path); err == nil && os.SameFile(original, current) {
		_ = os.Remove(path)
	}
}

func tokenOpenError(err error, create bool) error {
	if !create {
		return errPrivateTokenFile
	}
	if errors.Is(err, fs.ErrExist) {
		return errors.New("remote token file already exists")
	}
	return errors.New("could not create remote token file")
}

// RFC 6750 b64token has one or more data characters followed by zero or more
// padding characters. It is opaque and need not decode as canonical base64.
func validBearerToken(token string) bool {
	if len(token) < 32 || len(token) > maxTokenFileSize || token[0] == '=' {
		return false
	}
	padding := false
	for i := 0; i < len(token); i++ {
		c := token[i]
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", rune(c))) {
			return false
		}
	}
	return true
}
