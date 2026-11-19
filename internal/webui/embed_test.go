package webui

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func setNightlabFixtureAsset(t *testing.T, files fstest.MapFS, record *manifest, name string, decoded []byte, encoding string) {
	t.Helper()
	stored := decoded
	if encoding == "gzip" {
		var output bytes.Buffer
		writer := gzip.NewWriter(&output)
		if _, err := writer.Write(decoded); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		stored = output.Bytes()
	}
	storedSize, decodedSize, decodedHash := int64(len(stored)), int64(len(decoded)), digest(decoded)
	file := assetRecord{Path: name, Bytes: &storedSize, SHA256: digest(stored), Encoding: &encoding, DecodedBytes: &decodedSize, DecodedSHA256: &decodedHash}
	found := false
	for i := range record.Files {
		if record.Files[i].Path == name {
			record.Files[i], found = file, true
			break
		}
	}
	if !found {
		record.Files = append(record.Files, file)
	}
	files["dist/"+name] = &fstest.MapFile{Data: stored}
}

func nightlabBundleFixture(t *testing.T) (fstest.MapFS, manifest) {
	t.Helper()
	files, record := bundleFixture(t)
	record.SchemaVersion, record.Build.Tool, record.Files = 2, "static-nightlab/v1", nil
	for _, name := range nightlabAssetNames {
		encoding := "identity"
		if name == "nightlab/compiler/nb1.wasm" {
			encoding = "gzip"
		}
		setNightlabFixtureAsset(t, files, &record, name, []byte("fixture "+name), encoding)
	}
	refreshNightlabFixture(t, files, &record)
	return files, record
}

func refreshNightlabFixture(t *testing.T, files fstest.MapFS, record *manifest) {
	t.Helper()
	contents := map[string][]byte{}
	for _, file := range record.Files {
		if present := files["dist/"+file.Path]; present != nil {
			contents[file.Path] = present.Data
		}
	}
	record.Source.TreeSHA256 = treeDigest(contents)
	setFixtureManifest(t, files, record)
}

func bundleFixture(t *testing.T) (fstest.MapFS, manifest) {
	t.Helper()
	files := fstest.MapFS{}
	clean := false
	record := manifest{SchemaVersion: 1, Product: "nb1-web", AssetBase: "./", Source: sourceRecord{Repository: "https://github.com/b1ug/nb1-debugger", Commit: strings.Repeat("1", 40), Dirty: &clean, TreeSHA256: strings.Repeat("2", 64)}, Build: buildRecord{Tool: "static-editor/v1", Node: "v24.0.0", LockfileSHA256: strings.Repeat("3", 64)}, APIContracts: []string{"nb1-api/v1"}}
	for _, name := range assetNames {
		data := []byte("fixture " + name)
		size := int64(len(data))
		files["dist/"+name] = &fstest.MapFile{Data: data}
		record.Files = append(record.Files, assetRecord{Path: name, Bytes: &size, SHA256: digest(data)})
	}
	contents := make(map[string][]byte, len(assetNames))
	for _, name := range assetNames {
		contents[name] = files["dist/"+name].Data
	}
	record.Source.TreeSHA256 = treeDigest(contents)
	setFixtureManifest(t, files, record)
	return files, record
}

func setFixtureManifest(t *testing.T, files fstest.MapFS, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	setFixtureRawManifest(t, files, data)
}

func setFixtureRawManifest(t *testing.T, files fstest.MapFS, data []byte) {
	t.Helper()
	files["dist/manifest.json"] = &fstest.MapFile{Data: data}
	lock, err := json.Marshal(bundleLock{SchemaVersion: 1, Product: "nb1-web", SourceCommit: strings.Repeat("1", 40), ManifestSHA256: digest(data)})
	if err != nil {
		t.Fatal(err)
	}
	files["manifest.lock.json"] = &fstest.MapFile{Data: lock}
}

func TestBundleValidation(t *testing.T) {
	files, _ := bundleFixture(t)
	if err := validateBundle(files); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(fstest.MapFS, *manifest)
	}{
		{"dirty", func(_ fstest.MapFS, r *manifest) { value := true; r.Source.Dirty = &value }},
		{"missing-dirty", func(_ fstest.MapFS, r *manifest) { r.Source.Dirty = nil }},
		{"wrong-commit", func(_ fstest.MapFS, r *manifest) { r.Source.Commit = strings.Repeat("4", 40) }},
		{"bad-tree", func(_ fstest.MapFS, r *manifest) { r.Source.TreeSHA256 = "bad" }},
		{"false-tree-with-matching-lock", func(_ fstest.MapFS, r *manifest) { r.Source.TreeSHA256 = strings.Repeat("4", 64) }},
		{"wrong-product", func(_ fstest.MapFS, r *manifest) { r.Product = "other" }},
		{"base", func(_ fstest.MapFS, r *manifest) { r.AssetBase = "/" }},
		{"node-control", func(_ fstest.MapFS, r *manifest) { r.Build.Node = "node\n24" }},
		{"missing-file", func(_ fstest.MapFS, r *manifest) { r.Files = r.Files[1:] }},
		{"duplicate-file", func(_ fstest.MapFS, r *manifest) { r.Files[0] = r.Files[1] }},
		{"traversal", func(_ fstest.MapFS, r *manifest) { r.Files[0].Path = "../LICENSE.txt" }},
		{"absolute", func(_ fstest.MapFS, r *manifest) { r.Files[0].Path = "/LICENSE.txt" }},
		{"missing-bytes", func(_ fstest.MapFS, r *manifest) { r.Files[0].Bytes = nil }},
		{"unknown-contract", func(_ fstest.MapFS, r *manifest) { r.APIContracts[0] = "other" }},
		{"duplicate-contract", func(_ fstest.MapFS, r *manifest) { r.APIContracts = append(r.APIContracts, r.APIContracts[0]) }},
		{"changed-asset", func(f fstest.MapFS, _ *manifest) { f["dist/demo.js"].Data = []byte("changed") }},
		{"extra", func(f fstest.MapFS, _ *manifest) { f["dist/.env"] = &fstest.MapFile{Data: []byte("fixture")} }},
		{"extra-font", func(f fstest.MapFS, _ *manifest) {
			f["dist/fonts/unreviewed.woff2"] = &fstest.MapFile{Data: []byte("fixture")}
		}},
		{"extra-directory", func(f fstest.MapFS, _ *manifest) { f["dist/unreviewed"] = &fstest.MapFile{Mode: fs.ModeDir} }},
		{"linked-font-directory", func(f fstest.MapFS, _ *manifest) { f["dist/fonts"] = &fstest.MapFile{Mode: fs.ModeSymlink} }},
		{"directory", func(f fstest.MapFS, _ *manifest) { f["dist/demo.js"].Mode = fs.ModeDir }},
		{"link", func(f fstest.MapFS, _ *manifest) { f["dist/demo.js"].Mode = fs.ModeSymlink }},
		{"total", func(f fstest.MapFS, r *manifest) {
			for i := range r.Files {
				if r.Files[i].Path == "demo.js" {
					data := []byte(strings.Repeat("x", maxBundleBytes))
					size := int64(len(data))
					f["dist/demo.js"].Data = data
					r.Files[i].Bytes, r.Files[i].SHA256 = &size, digest(data)
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files, record := bundleFixture(t)
			test.mutate(files, &record)
			setFixtureManifest(t, files, record)
			if err := validateBundle(files); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

func TestBundleRawJSONBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string) string
	}{
		{"unknown", func(s string) string {
			return strings.Replace(s, `"product":"nb1-web"`, `"product":"nb1-web","other":true`, 1)
		}},
		{"duplicate", func(s string) string {
			return strings.Replace(s, `"product":"nb1-web"`, `"product":"nb1-web","product":"nb1-web"`, 1)
		}},
		{"trailing", func(s string) string { return s + `{}` }},
		{"surrogate", func(s string) string { return strings.Replace(s, `"v24.0.0"`, `"\ud800"`, 1) }},
		{"invalid-utf8", func(s string) string { return strings.Replace(s, `"v24.0.0"`, "\"\xff\"", 1) }},
		{"null", func(s string) string { return strings.Replace(s, `"dirty":false`, `"dirty":null`, 1) }},
		{"absent-dirty", func(s string) string { return strings.Replace(s, `"dirty":false,`, ``, 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			files, _ := bundleFixture(t)
			data := []byte(test.mutate(string(files["dist/manifest.json"].Data)))
			setFixtureRawManifest(t, files, data)
			if err := validateBundle(files); err == nil {
				t.Fatal("invalid JSON bundle accepted")
			}
		})
	}
	files, _ := bundleFixture(t)
	files["dist/manifest.json"].Data = append(files["dist/manifest.json"].Data, ' ')
	if err := validateBundle(files); err == nil {
		t.Fatal("unlocked manifest bytes accepted")
	}
}

func TestBundleLockBinding(t *testing.T) {
	for _, content := range []string{
		`{}`,
		`{"schema_version":1,"product":"other","source_commit":"` + strings.Repeat("1", 40) + `","manifest_sha256":"` + strings.Repeat("0", 64) + `"}`,
		`{"schema_version":1,"product":"nb1-web","source_commit":"wrong","manifest_sha256":"` + strings.Repeat("0", 64) + `"}`,
		`{"schema_version":1,"product":"nb1-web","source_commit":"` + strings.Repeat("1", 40) + `","manifest_sha256":"` + strings.Repeat("0", 64) + `","extra":true}`,
	} {
		files, _ := bundleFixture(t)
		files["manifest.lock.json"].Data = []byte(content)
		if err := validateBundle(files); err == nil {
			t.Fatal("invalid lock accepted", content)
		}
	}
	files, _ := bundleFixture(t)
	lock := string(files["manifest.lock.json"].Data)
	files["manifest.lock.json"].Data = []byte(strings.Replace(lock, `"product":"nb1-web"`, `"product":"nb1-web","product":"nb1-web"`, 1))
	if err := validateBundle(files); err == nil {
		t.Fatal("duplicate lock field accepted")
	}
}

func TestEmbeddedBundle(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range assetNames {
		data, err := ReadFile(name)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded %s: %v", name, err)
		}
	}
	if _, err := ReadFile("../manifest.lock.json"); err != fs.ErrNotExist {
		t.Fatal("nonasset lookup accepted", err)
	}
}

func TestEmbeddedBundlePublication(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	workstationPath := regexp.MustCompile(`(?i)(/Users/[^\s/"']+/|[A-Za-z]:[\\/]+Users[\\/]+[^\s\\/"']+[\\/]|/home/[^\s/"']+/(Codebase|projects|workspace|\.codex)/)`)
	for _, file := range bundledRecord.Files {
		t.Run(file.Path, func(t *testing.T) {
			data, err := ReadFile(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if workstationPath.Match(data) {
				t.Fatal("embedded asset contains a workstation-specific path")
			}
		})
	}
}

func TestBundleRequiresEveryRuntimeAsset(t *testing.T) {
	for _, name := range assetNames {
		t.Run(name, func(t *testing.T) {
			files, _ := bundleFixture(t)
			delete(files, "dist/"+name)
			if err := validateBundle(files); err == nil {
				t.Fatal("missing required asset accepted")
			}
		})
	}
}

func TestNightlabBundleValidationAndCompression(t *testing.T) {
	files, _ := nightlabBundleFixture(t)
	if err := validateBundle(files); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(fstest.MapFS, *manifest)
	}{
		{"missing-encoding", func(_ fstest.MapFS, r *manifest) { r.Files[0].Encoding = nil }},
		{"unsupported-encoding", func(_ fstest.MapFS, r *manifest) { value := "br"; r.Files[0].Encoding = &value }},
		{"missing-decoded-size", func(_ fstest.MapFS, r *manifest) { r.Files[0].DecodedBytes = nil }},
		{"missing-decoded-hash", func(_ fstest.MapFS, r *manifest) { r.Files[0].DecodedSHA256 = nil }},
		{"decoded-size-limit", func(_ fstest.MapFS, r *manifest) {
			size := int64(maxNightlabDecodedBytes + 1)
			r.Files[0].DecodedBytes = &size
		}},
		{"identity-hash-mismatch", func(_ fstest.MapFS, r *manifest) { value := strings.Repeat("4", 64); r.Files[0].DecodedSHA256 = &value }},
		{"identity-size-mismatch", func(_ fstest.MapFS, r *manifest) { *r.Files[0].DecodedBytes++ }},
		{"legacy-tool", func(_ fstest.MapFS, r *manifest) { r.Build.Tool = "static-editor/v1" }},
		{"unknown-file", func(f fstest.MapFS, _ *manifest) {
			f["dist/nightlab/private.env"] = &fstest.MapFile{Data: []byte("private")}
		}},
		{"missing-file", func(f fstest.MapFS, _ *manifest) { delete(f, "dist/nightlab/studio-3d.js") }},
		{"traversal", func(_ fstest.MapFS, r *manifest) { r.Files[0].Path = "nightlab/../demo.js" }},
		{"linked-model-directory", func(f fstest.MapFS, _ *manifest) {
			f["dist/nightlab/model-assets"] = &fstest.MapFile{Mode: fs.ModeSymlink}
		}},
		{"base-limit", func(f fstest.MapFS, r *manifest) {
			setNightlabFixtureAsset(t, f, r, "demo.js", bytes.Repeat([]byte("x"), maxNightlabBaseBytes), "gzip")
		}},
		{"decoded-total-limit", func(f fstest.MapFS, r *manifest) {
			setNightlabFixtureAsset(t, f, r, "nightlab/compiler/nb1.wasm", bytes.Repeat([]byte("x"), maxNightlabDecodedBytes), "gzip")
		}},
		{"stored-total-limit", func(f fstest.MapFS, r *manifest) {
			setNightlabFixtureAsset(t, f, r, "nightlab/compiler/nb1.wasm", bytes.Repeat([]byte("x"), maxNightlabStoredBytes), "identity")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files, record := nightlabBundleFixture(t)
			test.mutate(files, &record)
			refreshNightlabFixture(t, files, &record)
			if err := validateBundle(files); err == nil {
				t.Fatal("invalid Nightlab bundle accepted")
			}
		})
	}
	for _, name := range nightlabAssetNames {
		t.Run("required-"+name, func(t *testing.T) {
			files, _ := nightlabBundleFixture(t)
			delete(files, "dist/"+name)
			if err := validateBundle(files); err == nil {
				t.Fatal("missing Nightlab asset accepted")
			}
		})
	}
}

func TestNightlabCompressedAssetBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
		size   int64
	}{
		{"valid", func(b []byte) []byte { return b }, 16},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }, 16},
		{"checksum", func(b []byte) []byte { b[len(b)-5] ^= 1; return b }, 16},
		{"trailing", func(b []byte) []byte { return append(b, 0) }, 16},
		{"multiple-members", func(b []byte) []byte { return append(b, b...) }, 16},
		{"expands-past-declaration", func(b []byte) []byte { return b }, 15},
		{"shorter-than-declaration", func(b []byte) []byte { return b }, 17},
	} {
		t.Run(test.name, func(t *testing.T) {
			files, record := nightlabBundleFixture(t)
			setNightlabFixtureAsset(t, files, &record, "nightlab/compiler/nb1.wasm", []byte("0123456789abcdef"), "gzip")
			for i := range record.Files {
				file := &record.Files[i]
				if file.Path == "nightlab/compiler/nb1.wasm" {
					data := test.mutate(files["dist/"+file.Path].Data)
					files["dist/"+file.Path].Data = data
					size := int64(len(data))
					file.Bytes, file.SHA256, file.DecodedBytes = &size, digest(data), &test.size
				}
			}
			refreshNightlabFixture(t, files, &record)
			err := validateBundle(files)
			if (err == nil) != (test.name == "valid") {
				t.Fatalf("compression boundary %s: %v", test.name, err)
			}
		})
	}
}
