// Package webui serves a verified static build of the independent nb1 Web UI.
// Building nb1 never invokes frontend tools or reads the frontend source tree.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/jsoninput"
)

const maxBundleBytes = 256 * 1024
const maxManifestBytes = 16 * 1024

var assetNames = []string{
	"LICENSE.txt", "demo.css", "demo.js", "index.html", "orbit.css", "orbit.html", "orbit.js",
	"patterns.css", "patterns.html", "patterns.js", "i18n.js", "locales.js",
	"action-icons.js",
	"locale-menu.css",
	"typography.css",
	"assets/nb1-icon.svg",
	"assets/nb1-mascot.webp",
	"fonts/fredoka-700-latin.woff2",
	"fonts/fredoka-700-latin-ext.woff2",
	"fonts/mplusround-500-latin.woff2",
	"fonts/mplusround-700-latin.woff2",
	"fonts/Fredoka-OFL.txt",
	"fonts/MPLUSRounded1c-OFL.txt",
}

//go:embed all:dist manifest.lock.json
var bundled embed.FS

type sourceRecord struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Dirty      *bool  `json:"dirty"`
	TreeSHA256 string `json:"tree_sha256"`
}
type buildRecord struct {
	Tool           string `json:"tool"`
	Node           string `json:"node"`
	LockfileSHA256 string `json:"lockfile_sha256"`
}
type assetRecord struct {
	Path   string `json:"path"`
	Bytes  *int64 `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	SchemaVersion int           `json:"schema_version"`
	Product       string        `json:"product"`
	AssetBase     string        `json:"asset_base"`
	Source        sourceRecord  `json:"source"`
	Build         buildRecord   `json:"build"`
	APIContracts  []string      `json:"api_contracts"`
	Files         []assetRecord `json:"files"`
}
type bundleLock struct {
	SchemaVersion  int    `json:"schema_version"`
	Product        string `json:"product"`
	SourceCommit   string `json:"source_commit"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

var validateOnce sync.Once
var validationError error

// Validate checks the complete embedded file set and its source-bound lock.
func Validate() error {
	validateOnce.Do(func() { validationError = validateBundle(bundled) })
	return validationError
}

// ReadFile returns only a known UI asset after validating the entire bundle.
// The HTTP layer retains responsibility for routes, methods and security headers.
func ReadFile(name string) ([]byte, error) {
	if !knownAsset(name) {
		return nil, fs.ErrNotExist
	}
	if err := Validate(); err != nil {
		return nil, err
	}
	return bundled.ReadFile("dist/" + name)
}

func knownAsset(name string) bool {
	for _, allowed := range assetNames {
		if name == allowed {
			return true
		}
	}
	return false
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func treeDigest(contents map[string][]byte) string {
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	var data strings.Builder
	for _, name := range names {
		data.WriteString(name)
		data.WriteByte(0)
		data.WriteString(digest(contents[name]))
		data.WriteByte('\n')
	}
	return digest([]byte(data.String()))
}

func readBounded(files fs.FS, name string, limit int64) ([]byte, error) {
	info, err := fs.Stat(files, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("invalid web bundle file size or type: %s", name)
	}
	data, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("web bundle file changed: %s", name)
	}
	return data, nil
}

func decodeRecord(data []byte, target interface{}, limit int) error {
	if len(data) == 0 || len(data) > limit || !validUnicode(data) {
		return fmt.Errorf("invalid web bundle JSON bytes")
	}
	return jsoninput.DecodeObject(data, target)
}

// encoding/json replaces unpaired surrogate escapes; reject that lossy input.
func validUnicode(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) || data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		v, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func validateBundle(files fs.FS) error {
	lockData, err := readBounded(files, "manifest.lock.json", 4096)
	if err != nil {
		return err
	}
	var lock bundleLock
	if err = decodeRecord(lockData, &lock, 4096); err != nil {
		return err
	}
	if lock.SchemaVersion != 1 || lock.Product != "nb1-web" || !validHex(lock.SourceCommit, 40) || !validHex(lock.ManifestSHA256, 64) {
		return fmt.Errorf("invalid web bundle lock")
	}
	data, err := readBounded(files, "dist/manifest.json", maxManifestBytes)
	if err != nil {
		return err
	}
	if digest(data) != lock.ManifestSHA256 {
		return fmt.Errorf("web manifest differs from its lock")
	}
	var record manifest
	if err = decodeRecord(data, &record, maxManifestBytes); err != nil {
		return err
	}
	if record.SchemaVersion != 1 || record.Product != "nb1-web" || record.AssetBase != "./" || record.Source.Repository != "local:nb1-web" || record.Source.Commit != lock.SourceCommit || record.Source.Dirty == nil || *record.Source.Dirty || !validHex(record.Source.TreeSHA256, 64) || record.Build.Tool != "copy-static/v1" || !validHex(record.Build.LockfileSHA256, 64) || len(record.Build.Node) == 0 || len(record.Build.Node) > 128 || strings.TrimSpace(record.Build.Node) != record.Build.Node {
		return fmt.Errorf("invalid web manifest provenance")
	}
	for _, char := range record.Build.Node {
		if char < 32 || char >= 127 {
			return fmt.Errorf("invalid web build tool version")
		}
	}
	if len(record.APIContracts) != 1 || record.APIContracts[0] != "nb1-api/v1" {
		return fmt.Errorf("web bundle requires the nb1-api/v1 contract")
	}
	if len(record.Files) != len(assetNames) {
		return fmt.Errorf("incomplete web asset set")
	}
	expected := map[string]bool{"manifest.json": true}
	contents := make(map[string][]byte, len(record.Files))
	total := int64(len(data))
	for _, file := range record.Files {
		if !knownAsset(file.Path) || expected[file.Path] || file.Bytes == nil || *file.Bytes <= 0 || *file.Bytes > maxBundleBytes || !validHex(file.SHA256, 64) {
			return fmt.Errorf("invalid web asset declaration")
		}
		expected[file.Path] = true
		content, err := readBounded(files, "dist/"+file.Path, *file.Bytes)
		if err != nil {
			return err
		}
		if int64(len(content)) != *file.Bytes || digest(content) != file.SHA256 {
			return fmt.Errorf("web asset %s differs from its manifest", file.Path)
		}
		contents[file.Path] = content
		total += *file.Bytes
		if total > maxBundleBytes {
			return fmt.Errorf("web bundle exceeds %d bytes", maxBundleBytes)
		}
	}
	for _, name := range assetNames {
		if !expected[name] {
			return fmt.Errorf("incomplete web asset set")
		}
	}
	if treeDigest(contents) != record.Source.TreeSHA256 {
		return fmt.Errorf("web source tree differs from the verified assets")
	}
	parents := map[string]bool{"": true}
	for name := range expected {
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			parents[strings.Join(parts[:i], "/")] = true
		}
	}
	count := 0
	err = fs.WalkDir(files, "dist", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.TrimPrefix(strings.TrimPrefix(path, "dist"), "/")
		if entry.IsDir() {
			if parents[name] {
				return nil
			}
			return fmt.Errorf("unexpected web asset directory %s", name)
		}
		if !entry.Type().IsRegular() || !expected[name] {
			return fmt.Errorf("unexpected web asset %s", name)
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(expected) {
		return fmt.Errorf("unexpected web asset set")
	}

	return nil
}
