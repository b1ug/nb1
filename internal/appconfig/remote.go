package appconfig

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RemoteServerConfig contains listener settings and paths to credentials.
// Load resolves relative credential paths beside the selected configuration file.
type RemoteServerConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Listen      string `json:"listen" yaml:"listen"`
	Host        string `json:"host,omitempty" yaml:"host,omitempty"`
	TokenFile   string `json:"token_file" yaml:"token_file"`
	TLSCertFile string `json:"tls_cert_file" yaml:"tls_cert_file"`
	TLSKeyFile  string `json:"tls_key_file" yaml:"tls_key_file"`
}

func (remote *RemoteServerConfig) resolvePaths(configDir string) {
	remote.TokenFile = resolveCredentialPath(configDir, remote.TokenFile)
	remote.TLSCertFile = resolveCredentialPath(configDir, remote.TLSCertFile)
	remote.TLSKeyFile = resolveCredentialPath(configDir, remote.TLSKeyFile)
}

func resolveCredentialPath(configDir, path string) string {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return path
	}
	return filepath.Join(configDir, path)
}

func validCredentialPath(path string) bool {
	return filepath.IsAbs(path) && !strings.ContainsRune(path, 0)
}

// Validate checks enabled listener settings without opening files or sockets.
func (remote RemoteServerConfig) Validate() error {
	if !remote.Enabled {
		return nil
	}
	host, portText, err := net.SplitHostPort(remote.Listen)
	port, validPort := remotePort(portText)
	ip := net.ParseIP(host)
	if err != nil || !validPort ||
		(!strings.EqualFold(host, "localhost") && !concreteRemoteIP(ip)) {
		return fmt.Errorf("server.remote.listen must use a concrete IP or localhost and a decimal port")
	}
	if remote.Host != "" && !validRemoteAuthority(remote.Host, port) {
		return fmt.Errorf("server.remote.host must be an HTTP authority with the listener port (optional for port 443)")
	}
	for _, field := range []struct{ name, value string }{
		{"token_file", remote.TokenFile},
		{"tls_cert_file", remote.TLSCertFile},
		{"tls_key_file", remote.TLSKeyFile},
	} {
		if !validCredentialPath(field.value) {
			return fmt.Errorf("server.remote.%s must specify a file path", field.name)
		}
	}
	return nil
}

func concreteRemoteIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func remotePort(text string) (uint64, bool) {
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	port, err := strconv.ParseUint(text, 10, 16)
	return port, err == nil
}

var remoteDNSLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func validRemoteAuthority(authority string, listenPort uint64) bool {
	if listenPort == 0 {
		return false
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		if listenPort != 443 {
			return false
		}
		host = authority
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	} else if port, ok := remotePort(portText); !ok || port != listenPort {
		return false
	}
	if strings.HasPrefix(authority, "[") {
		return strings.ContainsRune(host, ':') && concreteRemoteIP(net.ParseIP(host))
	}
	if ip := net.ParseIP(host); ip != nil {
		return !strings.ContainsRune(host, ':') && concreteRemoteIP(ip)
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !remoteDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func decodeRemoteServer(node *yaml.Node) (*RemoteServerConfig, error) {
	var remote RemoteServerConfig
	if err := mapping(node, "server.remote", func(key string, value *yaml.Node) error {
		var field *string
		switch key {
		case "enabled":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" || value.Decode(&remote.Enabled) != nil {
				return fmt.Errorf("server.remote.enabled must be a boolean")
			}
			return nil
		case "listen":
			field = &remote.Listen
		case "host":
			field = &remote.Host
		case "token_file":
			field = &remote.TokenFile
		case "tls_cert_file":
			field = &remote.TLSCertFile
		case "tls_key_file":
			field = &remote.TLSKeyFile
		default:
			return fmt.Errorf("unknown configuration field server.remote.%s", key)
		}
		return stringValue(value, "server.remote."+key, field)
	}); err != nil {
		return nil, err
	}
	return &remote, nil
}
