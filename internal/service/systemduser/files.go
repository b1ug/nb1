package systemduser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/service"
)

const marker = "# nb1-managed-systemd-user-v1:"

type definition struct {
	Version int            `json:"version"`
	Unit    string         `json:"unit"`
	UID     int            `json:"uid"`
	Config  service.Config `json:"config"`
}
type unitFile struct {
	definition definition
	content    []byte
	info       os.FileInfo
}
type enableLink struct {
	target string
	info   os.FileInfo
}
type snapshot struct {
	unit *unitFile
	link *enableLink
}

func unitContent(d definition) ([]byte, error) {
	artifact, err := service.Render(d.Config)
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return append([]byte(marker+base64.StdEncoding.EncodeToString(metadata)+"\n"), artifact.Content...), nil
}

func (m *Manager) unitDir() string  { return filepath.Join(m.configRoot, "systemd", "user") }
func (m *Manager) unitPath() string { return filepath.Join(m.unitDir(), UnitName) }
func (m *Manager) linkPath() string {
	return filepath.Join(m.unitDir(), "default.target.wants", UnitName)
}

func (m *Manager) inspect() (*snapshot, error) {
	s := &snapshot{}
	if err := m.directory(m.unitDir(), false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if _, err := os.Lstat(m.unitPath() + ".d"); err == nil || !errors.Is(err, os.ErrNotExist) {
		return s, fmt.Errorf("an unmanaged unit drop-in directory exists or could not be inspected; takeover is refused")
	}
	f, err := openRegular(m.unitPath())
	if err == nil {
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return s, err
		}
		if !info.Mode().IsRegular() || !ownedBy(info, m.uid) || info.Mode().Perm()&0022 != 0 {
			return s, fmt.Errorf("the unit is not a safe current-user regular file")
		}
		content, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
		if err != nil || len(content) > 128<<10 {
			return s, fmt.Errorf("the complete managed unit could not be read, or the file is too large")
		}
		parts := bytes.SplitN(content, []byte("\n"), 2)
		if len(parts) != 2 || !bytes.HasPrefix(parts[0], []byte(marker)) {
			return s, fmt.Errorf("the existing unit is not this tool's complete managed file; it is not overwritten or deleted")
		}
		metadata, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(parts[0]), marker))
		if err != nil {
			return s, fmt.Errorf("the unit managed marker is damaged")
		}
		var d definition
		decoder := json.NewDecoder(bytes.NewReader(metadata))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&d); err != nil || d.Version != 1 || d.Unit != UnitName || d.UID != m.uid || d.Config.Platform != "linux" {
			return s, fmt.Errorf("the unit managed identity or version does not match")
		}
		canonical, err := unitContent(d)
		if err != nil || !bytes.Equal(canonical, content) {
			return s, fmt.Errorf("the unit content was modified or its managed marker is inconsistent; it is not overwritten or deleted")
		}
		s.unit = &unitFile{definition: d, content: content, info: info}
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, fmt.Errorf("the unit could not be read safely; it is not overwritten or deleted: %w", err)
	}
	if err := m.directory(filepath.Dir(m.linkPath()), false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	info, err := os.Lstat(m.linkPath())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if info.Mode()&os.ModeSymlink == 0 || !ownedBy(info, m.uid) {
		return s, fmt.Errorf("the login-enablement path is not a managed symbolic link owned by the current user; it is not overwritten or deleted")
	}
	target, err := os.Readlink(m.linkPath())
	if err != nil || target != m.unitPath() || s.unit == nil {
		return s, fmt.Errorf("the login-enablement link target or unit ownership is unknown; no takeover is performed")
	}
	s.link = &enableLink{target: target, info: info}
	return s, nil
}

func (m *Manager) unchanged(original *snapshot) error {
	current, err := m.inspect()
	if err != nil {
		return err
	}
	if !sameSnapshot(original, current) {
		return fmt.Errorf("the unit or login link changed during inspection; start, stop and deletion are refused")
	}
	return nil
}

func sameSnapshot(a, b *snapshot) bool { return sameUnit(a.unit, b.unit) && sameLink(a.link, b.link) }

func sameUnit(a, b *unitFile) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a.info, b.info) && bytes.Equal(a.content, b.content)
}
func sameLink(a, b *enableLink) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a.info, b.info) && a.target == b.target
}

func (m *Manager) managementLease() (*ownership.Lease, error) {
	path := filepath.Join(m.configRoot, "nb1", "systemd-user-management")
	if err := m.directory(path, true); err != nil {
		return nil, err
	}
	return ownership.Acquire(path, "systemd-user/"+UnitName)
}

func (m *Manager) directory(path string, create bool) error {
	rel, err := filepath.Rel(m.configRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("the unit directory is outside the user configuration root")
	}
	if create {
		if err := os.MkdirAll(m.configRoot, 0700); err != nil {
			return err
		}
	}
	paths := []string{m.configRoot}
	current := m.configRoot
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			paths = append(paths, current)
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || !ownedBy(info, m.uid) || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("the user configuration directory is not a safe current-user directory; symbolic links and directories writable by others are refused")
		}
	}
	return nil
}

// Resolve only existing ancestors, leaving creation to explicit operations.
func canonicalPath(path string) (string, error) {
	missing := []string{}
	current := filepath.Clean(path)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	canonical, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		canonical = filepath.Join(canonical, missing[i])
	}
	return canonical, nil
}

// Root-owned sticky temporary ancestors are allowed for isolated tests and
// user config roots; the configuration root itself must belong to this UID.
func safeAncestors(path string, uid int) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
			return fmt.Errorf("ownership of a configuration path ancestor is unsafe")
		}
		if info.Mode().Perm()&0022 != 0 && !(ownedBy(info, 0) && info.Mode()&os.ModeSticky != 0) {
			return fmt.Errorf("a configuration path ancestor is writable by other users")
		}
	}
	return nil
}
