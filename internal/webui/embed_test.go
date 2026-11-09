package webui

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func bundleFixture(t *testing.T) (fstest.MapFS, manifest) {
	t.Helper()
	files := fstest.MapFS{}
	clean := false
	record := manifest{SchemaVersion: 1, Product: "nb1-web", AssetBase: "./", Source: sourceRecord{Repository: "local:nb1-web", Commit: strings.Repeat("1", 40), Dirty: &clean, TreeSHA256: strings.Repeat("2", 64)}, Build: buildRecord{Tool: "copy-static/v1", Node: "v24.0.0", LockfileSHA256: strings.Repeat("3", 64)}, APIContracts: []string{"nb1-light-action/v1", "nb1-light-action/experimental-v2"}}
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
		{"duplicate-contract", func(_ fstest.MapFS, r *manifest) { r.APIContracts[0] = r.APIContracts[1] }},
		{"changed-asset", func(f fstest.MapFS, _ *manifest) { f["dist/demo.js"].Data = []byte("changed") }},
		{"extra", func(f fstest.MapFS, _ *manifest) { f["dist/.env"] = &fstest.MapFile{Data: []byte("fixture")} }},
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
