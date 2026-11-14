package appconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSelectionPrecedenceDoesNotCreateStorage(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", filepath.Join(root, "roaming"))
	} else {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	}
	base, err := DefaultBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		flag, env, want string
		required        bool
	}{
		{"", "", filepath.Join(base, Filename), false},
		{"", "environment.yaml", "environment.yaml", true},
		{"flag.yaml", "environment.yaml", "flag.yaml", true},
	} {
		selected, err := Select(test.flag, test.env)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.Abs(test.want)
		if selected.Path != want || selected.BaseDir != base || selected.Required != test.required {
			t.Fatalf("selection=%+v, want path=%s required=%t", selected, want, test.required)
		}
	}
	if _, err := Select(" ", ""); err == nil {
		t.Fatal("blank explicit path accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("selection wrote storage: %v, %v", entries, err)
	}
}

func configSelection(t *testing.T, text string) Selection {
	t.Helper()
	base := filepath.Join(t.TempDir(), "application-root")
	path := filepath.Join(t.TempDir(), Filename)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return Selection{BaseDir: base, Path: path, Required: true}
}

func TestLoadDefaultsAndOpaqueLegacyFields(t *testing.T) {
	selected := configSelection(t, "title: Original CLI\nbase_url: /old\nport: 8080\ncontent_dir: ./site\ncolor:\n  amber: '#ffbf00'\n")
	config, err := Load(selected)
	if err != nil {
		t.Fatal(err)
	}
	if config.Device != "" || config.Server.Listen != DefaultListen || len(config.Server.Devices) != 0 || config.Jobs.TerminalLimit != DefaultTerminalLimit || config.Jobs.TerminalTTL != DefaultTerminalTTL || config.LibraryDir != filepath.Join(selected.BaseDir, "library") {
		t.Fatalf("wrong defaults: %+v", config)
	}
	data, _ := json.Marshal(config)
	for _, key := range []string{"title", "base_url", "content_dir", "amber"} {
		if bytes.Contains(data, []byte(key)) {
			t.Fatalf("legacy field leaked into typed output: %s", data)
		}
	}
	if _, err := os.Stat(selected.BaseDir); !os.IsNotExist(err) {
		t.Fatalf("load created application storage: %v", err)
	}
}

func TestLoadOverridesAndDoesNotRelocateStorage(t *testing.T) {
	selected := configSelection(t, "device: '20005601'\nserver:\n  devices: ['20005601', 'ABCD1234']\n  listen: '[::1]:8932'\njobs:\n  terminal_limit: 7\n  terminal_ttl: 1h30m\nlibrary_dir: ''\n")
	config, err := Load(selected)
	if err != nil {
		t.Fatal(err)
	}
	if config.Device != "20005601" || len(config.Server.Devices) != 2 || config.Server.Listen != "[::1]:8932" || config.Jobs.TerminalLimit != 7 || config.Jobs.TerminalTTL != 90*time.Minute || config.LibraryDir != filepath.Join(selected.BaseDir, "library") {
		t.Fatalf("wrong config: %+v", config)
	}
	custom := filepath.Join(t.TempDir(), "custom-library")
	selected = configSelection(t, "library_dir: "+strconvQuote(custom)+"\n")
	config, err = Load(selected)
	if err != nil || config.LibraryDir != custom {
		t.Fatalf("custom library rejected: %+v %v", config, err)
	}
}

func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestLoadRejectsAmbiguousOrInvalidConfiguration(t *testing.T) {
	for _, text := range []string{
		"", "null\n", "[]\n", "---\n{}\n---\n{}\n", "device: [\n",
		"default_device: '20005601'\n", "server:\n  port: 8931\n", "jobs:\n  dedup_window: 1m\n",
		"device: ''\ndevice: '20005601'\n", "server:\n  listen: '127.0.0.1:8931'\n  listen: '127.0.0.1:8932'\n",
		"color:\n  amber: red\n  amber: blue\n", "title: &x old\ncontent_dir: *x\n", "<<: {device: '20005601'}\n",
		"device: 20005601\n", "device: true\n", "device: null\n", "device: '1'\n", "device: '2000560g'\n",
		"server: null\n", "server:\n  devices: '20005601'\n", "server:\n  devices: [20005601]\n",
		"server:\n  devices: ['abcd1234', 'ABCD1234']\n", "server:\n  devices: ['../bad']\n",
		"server:\n  listen: '0.0.0.0:8931'\n", "server:\n  listen: 'example.com:8931'\n", "server:\n  listen: '127.0.0.1:65536'\n", "server:\n  listen: '127.0.0.1:+80'\n",
		"jobs:\n  terminal_limit: 0\n", "jobs:\n  terminal_limit: -1\n", "jobs:\n  terminal_limit: 1.5\n", "jobs:\n  terminal_limit: '3'\n", "jobs:\n  terminal_limit: 999999999999999999999999999999999999\n",
		"jobs:\n  terminal_ttl: 0s\n", "jobs:\n  terminal_ttl: -1m\n", "jobs:\n  terminal_ttl: 30\n", "jobs:\n  terminal_ttl: forever\n",
		"library_dir: relative/path\n", "library_dir: false\n", "library_dir: null\n",
	} {
		t.Run(strings.ReplaceAll(text, "\n", "_"), func(t *testing.T) {
			if _, err := Load(configSelection(t, text)); err == nil {
				t.Fatalf("accepted invalid configuration %q", text)
			}
		})
	}
}

func TestMissingFileRequiresExplicitSelection(t *testing.T) {
	base := t.TempDir()
	selected := Selection{BaseDir: base, Path: filepath.Join(base, "missing.yaml")}
	config, err := Load(selected)
	if err != nil || config.Jobs.TerminalTTL != DefaultTerminalTTL {
		t.Fatalf("missing default: %+v %v", config, err)
	}
	selected.Required = true
	if _, err := Load(selected); err == nil {
		t.Fatal("missing explicit file ignored")
	}
}

func TestLoadRejectsDirectorySymlinkAndOversizedFile(t *testing.T) {
	selected := configSelection(t, "{}\n")
	directory := selected
	directory.Path = filepath.Dir(selected.Path)
	if _, err := Load(directory); err == nil {
		t.Fatal("directory accepted")
	}
	if runtime.GOOS != "windows" {
		link := selected
		link.Path += ".link"
		if err := os.Symlink(selected.Path, link.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(link); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	if err := os.WriteFile(selected.Path, bytes.Repeat([]byte(" "), MaxFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(selected); err == nil {
		t.Fatal("oversized configuration accepted")
	}
	// A boundary-sized valid document is accepted.
	if err := os.WriteFile(selected.Path, append([]byte("{}\n#"), bytes.Repeat([]byte("x"), MaxFileBytes-4)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(selected); err != nil {
		t.Fatalf("boundary-sized document rejected: %v", err)
	}
}

func TestInitCreatesOnlyPrivateExampleAndDoesNotOverwrite(t *testing.T) {
	base := filepath.Join(t.TempDir(), "nb1")
	path := filepath.Join(base, Filename)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || entries[0].Name() != Filename {
		t.Fatalf("unexpected initialized entries: %v %v", entries, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config permission = %o", info.Mode().Perm())
	}
	info, err = os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("directory permission = %o", info.Mode().Perm())
	}
	config, err := Load(Selection{BaseDir: base, Path: path, Required: true})
	if err != nil || config.Device != "" || len(config.Server.Devices) != 0 {
		t.Fatalf("example selects hardware or is invalid: %+v %v", config, err)
	}
	if err := os.WriteFile(path, []byte("# user content\n{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Init(path); err == nil {
		t.Fatal("existing file replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "# user content\n{}\n" {
		t.Fatalf("existing content changed: %q %v", data, err)
	}
}
