// Package appconfig reads the current CLI and daemon's per-user configuration.
// Loading configuration never creates directories, scans USB or starts a service.
package appconfig

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	Filename             = "config.yaml"
	MaxFileBytes         = 1 << 20
	DefaultTerminalLimit = 16384
	DefaultTerminalTTL   = 30 * time.Minute
	DefaultListen        = "127.0.0.1:8931"
)

type Config struct {
	Device     string       `json:"device" yaml:"device"`
	Server     ServerConfig `json:"server" yaml:"server"`
	Jobs       JobConfig    `json:"jobs" yaml:"jobs"`
	LibraryDir string       `json:"library_dir" yaml:"library_dir"`
}

type ServerConfig struct {
	Devices []string `json:"devices" yaml:"devices"`
	Listen  string   `json:"listen" yaml:"listen"`
}

type JobConfig struct {
	TerminalLimit int           `json:"terminal_limit" yaml:"terminal_limit"`
	TerminalTTL   time.Duration `json:"terminal_ttl" yaml:"terminal_ttl"`
}

// Selection separates the stable application storage root from the selected
// configuration file. Choosing another file does not relocate libraries or locks.
type Selection struct {
	BaseDir  string
	Path     string
	Required bool
}

func DefaultBaseDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	return filepath.Abs(filepath.Join(base, "nb1"))
}

// Select implements --config > NB1_CONFIG > the user's nb1/config.yaml.
// Callers pass the flag and environment values explicitly; no environment is
// otherwise imported into typed configuration.
func Select(flagPath, envPath string) (Selection, error) {
	base, err := DefaultBaseDir()
	if err != nil {
		return Selection{}, err
	}
	path := flagPath
	if path == "" {
		path = envPath
	}
	required := path != ""
	if !required {
		path = filepath.Join(base, Filename)
	}
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return Selection{}, fmt.Errorf("configuration path must not be blank or contain NUL")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Selection{}, fmt.Errorf("resolve configuration path: %w", err)
	}
	return Selection{BaseDir: base, Path: path, Required: required}, nil
}

func Defaults(baseDir string) Config {
	return Config{Server: ServerConfig{Devices: []string{}, Listen: DefaultListen},
		Jobs:       JobConfig{TerminalLimit: DefaultTerminalLimit, TerminalTTL: DefaultTerminalTTL},
		LibraryDir: filepath.Join(baseDir, "library")}
}

// Load reads at most MaxFileBytes+1 bytes. Only a missing optional default file
// is ignored. A configured but unreadable, malformed or invalid file is an error.
func Load(selected Selection) (Config, error) {
	if !filepath.IsAbs(selected.BaseDir) || !filepath.IsAbs(selected.Path) {
		return Config{}, fmt.Errorf("configuration selection requires absolute base and file paths")
	}
	config := Defaults(selected.BaseDir)
	before, err := os.Lstat(selected.Path)
	if os.IsNotExist(err) && !selected.Required {
		return config, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read configuration %q: %w", selected.Path, err)
	}
	if !before.Mode().IsRegular() || before.Size() > MaxFileBytes {
		return Config{}, fmt.Errorf("configuration %q must be a regular file of at most 1 MiB", selected.Path)
	}
	f, err := openConfig(selected.Path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration %q: %w", selected.Path, err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("inspect configuration %q: %w", selected.Path, err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > MaxFileBytes {
		return Config{}, fmt.Errorf("configuration %q changed or is not a regular file of at most 1 MiB", selected.Path)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration %q: %w", selected.Path, err)
	}
	if len(data) > MaxFileBytes {
		return Config{}, fmt.Errorf("configuration %q exceeds 1 MiB", selected.Path)
	}
	if err := decode(data, &config); err != nil {
		return Config{}, fmt.Errorf("configuration %q: %w", selected.Path, err)
	}
	if config.LibraryDir == "" {
		config.LibraryDir = filepath.Join(selected.BaseDir, "library")
	}
	if err := Validate(config); err != nil {
		return Config{}, fmt.Errorf("configuration %q: %w", selected.Path, err)
	}
	return config, nil
}

var serialPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)

func Validate(config Config) error {
	if config.Device != "" && !serialPattern.MatchString(config.Device) {
		return fmt.Errorf("device must be empty or exactly eight hexadecimal digits")
	}
	if len(config.Server.Devices) > 64 {
		return fmt.Errorf("server.devices must contain at most 64 serials")
	}
	seen := make(map[string]bool)
	for _, serial := range config.Server.Devices {
		if !serialPattern.MatchString(serial) {
			return fmt.Errorf("server.devices must contain eight-digit hexadecimal serials")
		}
		key := strings.ToLower(serial)
		if seen[key] {
			return fmt.Errorf("server.devices contains duplicate serial %q", serial)
		}
		seen[key] = true
	}
	host, port, err := net.SplitHostPort(config.Server.Listen)
	if err != nil {
		return fmt.Errorf("server.listen must be a loopback host:port")
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("server.listen must use a loopback IP or localhost")
	}
	if port == "" || strings.IndexFunc(port, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
		return fmt.Errorf("server.listen port must be an integer between 0 and 65535")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n > 65535 {
		return fmt.Errorf("server.listen port must be an integer between 0 and 65535")
	}
	if config.Jobs.TerminalLimit <= 0 {
		return fmt.Errorf("jobs.terminal_limit must be a positive integer")
	}
	if config.Jobs.TerminalTTL <= 0 {
		return fmt.Errorf("jobs.terminal_ttl must be a positive duration")
	}
	if !filepath.IsAbs(config.LibraryDir) || strings.ContainsRune(config.LibraryDir, 0) {
		return fmt.Errorf("library_dir must be an absolute path")
	}
	return nil
}

func decode(data []byte, config *Config) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if err == io.EOF {
			return fmt.Errorf("configuration must contain a YAML mapping")
		}
		return fmt.Errorf("invalid YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("configuration must contain exactly one YAML document")
	}
	if len(doc.Content) != 1 {
		return fmt.Errorf("configuration must contain a YAML mapping")
	}
	root := doc.Content[0]
	if err := validateNodes(root); err != nil {
		return err
	}
	return mapping(root, "configuration", func(key string, value *yaml.Node) error {
		switch key {
		case "device":
			return stringValue(value, key, &config.Device)
		case "library_dir":
			return stringValue(value, key, &config.LibraryDir)
		case "server":
			return mapping(value, key, func(key string, value *yaml.Node) error {
				switch key {
				case "listen":
					return stringValue(value, "server.listen", &config.Server.Listen)
				case "devices":
					if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
						return fmt.Errorf("server.devices must be a list of strings")
					}
					config.Server.Devices = []string{}
					for _, node := range value.Content {
						var serial string
						if err := stringValue(node, "server.devices entry", &serial); err != nil {
							return err
						}
						config.Server.Devices = append(config.Server.Devices, serial)
					}
					return nil
				default:
					return fmt.Errorf("unknown configuration field server.%s", key)
				}
			})
		case "jobs":
			return mapping(value, key, func(key string, value *yaml.Node) error {
				switch key {
				case "terminal_limit":
					if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
						return fmt.Errorf("jobs.terminal_limit must be a positive integer")
					}
					if err := value.Decode(&config.Jobs.TerminalLimit); err != nil {
						return fmt.Errorf("jobs.terminal_limit must be a positive integer")
					}
					return nil
				case "terminal_ttl":
					var text string
					if err := stringValue(value, "jobs.terminal_ttl", &text); err != nil {
						return err
					}
					duration, err := time.ParseDuration(text)
					if err != nil {
						return fmt.Errorf("jobs.terminal_ttl must be a duration such as 30m: %w", err)
					}
					config.Jobs.TerminalTTL = duration
					return nil
				default:
					return fmt.Errorf("unknown configuration field jobs.%s", key)
				}
			})
		case "title", "base_url", "port", "content_dir", "color":
			// These original setting.yaml fields belong to the existing Viper
			// CLI. Accept their YAML nodes without adding them to current output.
			return nil
		default:
			return fmt.Errorf("unknown configuration field %s", key)
		}
	})
}

func mapping(node *yaml.Node, path string, visit func(string, *yaml.Node) error) error {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return fmt.Errorf("%s must be a mapping", path)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if err := visit(node.Content[i].Value, node.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func stringValue(node *yaml.Node, path string, out *string) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("%s must be a string", path)
	}
	*out = node.Value
	return nil
}

// Reject duplicate keys even in legacy opaque fields. Aliases and YAML merges
// cannot silently override the typed settings or hide recursive structures.
func validateNodes(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("YAML aliases are not supported in configuration")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("configuration mapping keys must be strings")
			}
			if seen[key.Value] {
				return fmt.Errorf("duplicate configuration key %q", key.Value)
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateNodes(child); err != nil {
			return err
		}
	}
	return nil
}
