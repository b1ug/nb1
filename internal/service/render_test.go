package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLaunchdArgumentsRoundTrip(t *testing.T) {
	c := Config{Platform: "darwin", Binary: `/Applications/灯 & 家/<nb1> "quoted"/bin\nb1`, Devices: []string{"20005601", "30000001"}, Listen: "[::1]:8931", LockDir: `/Users/用户/100%/<locks> & "one"\tail`}
	artifact, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(artifact.Content))
	var key string
	var args []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			if err := decoder.DecodeElement(&key, &start); err != nil {
				t.Fatal(err)
			}
		}
		if start.Name.Local == "array" && key == "ProgramArguments" {
			var array struct {
				Strings []string `xml:"string"`
			}
			if err := decoder.DecodeElement(&array, &start); err != nil {
				t.Fatal(err)
			}
			args = array.Strings
		}
	}
	want := []string{c.Binary, "server", "--devices", "20005601,30000001", "--listen", "[::1]:8931", "--lock-dir", c.LockDir}
	if artifact.Filename != "org.b1ug.nb1.plist" || !reflect.DeepEqual(args, want) {
		t.Fatalf("decoded argv = %#v, want %#v", args, want)
	}
}

func TestSystemdLiteralArguments(t *testing.T) {
	c := Config{Platform: "linux", Binary: `/opt/灯 100%/$USER/"q"\nb1`, Devices: []string{"20005601"}, LockDir: `/tmp/locks/%u/$HOME/\leaf`}
	artifact, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `ExecStart=:"/opt/灯 100%%/$USER/\"q\"\\nb1" "server" "--devices" "20005601" "--listen" "127.0.0.1:8931" "--lock-dir" "/tmp/locks/%%u/$HOME/\\leaf"`
	if artifact.Filename != "nb1.service" || !strings.Contains(string(artifact.Content), want+"\n") {
		t.Fatalf("missing literal ExecStart:\n%s\nwant:\n%s", artifact.Content, want)
	}
	if !bytes.Contains(artifact.Content, []byte("WantedBy=default.target")) {
		t.Fatal("unit is not intended for the user manager")
	}
}

func decodePowerShell(t *testing.T, content []byte) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^\$encoded = '([A-Za-z0-9+/=]+)'$`).FindSubmatch(content)
	if len(match) != 2 {
		t.Fatal("encoded command not found")
	}
	data, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil || len(data)%2 != 0 {
		t.Fatalf("invalid encoded command: %v", err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units))
}

func TestWindowsTaskLiteralLauncher(t *testing.T) {
	c := Config{Platform: "windows", Binary: `C:\Program Files\灯's 100%TEMP%\nb1.exe`, Devices: []string{"20005601"}, LockDir: `C:\Users\灯's\locks 100%USERNAME%\`}
	artifact, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range artifact.Content {
		if value > 127 {
			t.Fatal("outer script must remain ASCII for Windows PowerShell 5.1")
		}
	}
	payload := decodePowerShell(t, artifact.Content)
	for _, required := range []string{
		`$startInfo.FileName = 'C:\Program Files\灯''s 100%TEMP%\nb1.exe'`,
		`$startInfo.Arguments = '"server" "--devices" "20005601" "--listen" "127.0.0.1:8931" "--lock-dir" "C:\Users\灯''s\locks 100%USERNAME%\\"'`,
		`$startInfo.UseShellExecute = $false`, `$process.WaitForExit()`, `exit $process.ExitCode`,
	} {
		if !strings.Contains(payload, required) {
			t.Fatalf("decoded launcher missing %q:\n%s", required, payload)
		}
	}
	for _, required := range []string{"-AtLogOn -User $identity.Name", "-LogonType Interactive -RunLevel Limited", "-ExecutionTimeLimit ([TimeSpan]::Zero)", "-MultipleInstances IgnoreNew"} {
		if !bytes.Contains(artifact.Content, []byte(required)) {
			t.Fatalf("missing per-user task setting %q", required)
		}
	}
	if bytes.Contains(artifact.Content, []byte("%TEMP%")) || bytes.Contains(artifact.Content, []byte("\nStart-ScheduledTask")) || bytes.Contains(artifact.Content, []byte("-Force")) {
		t.Fatal("task would expand a literal path, start immediately, or replace an existing task")
	}
}

func TestWindowsArgumentFixtures(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"", `""`}, {"server", `"server"`},
		{`C:\sp ace\`, `"C:\sp ace\\"`},
		{`a\"b`, `"a\\\"b"`},
		{`灯 100% $x`, `"灯 100% $x"`},
	} {
		if got := windowsArgument(test.input); got != test.want {
			t.Errorf("quote(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestInvalidConfigurationDoesNotCreateOutput(t *testing.T) {
	base := Config{Platform: "linux", Binary: "/opt/nb1", Devices: []string{"20005601"}, Listen: "127.0.0.1:8931"}
	for _, test := range []struct {
		name   string
		modify func(*Config)
	}{
		{"platform", func(c *Config) { c.Platform = "plan9" }},
		{"relative binary", func(c *Config) { c.Binary = "./nb1" }},
		{"binary directory", func(c *Config) { c.Binary = "/opt/" }},
		{"control in binary", func(c *Config) { c.Binary = "/opt/nb1\nRestart=always" }},
		{"no devices", func(c *Config) { c.Devices = nil }},
		{"ordinal", func(c *Config) { c.Devices = []string{"1"} }},
		{"duplicate serial", func(c *Config) { c.Devices = []string{"2abcdef0", "2ABCDEF0"} }},
		{"all interfaces", func(c *Config) { c.Listen = "0.0.0.0:8931" }},
		{"unscoped listen", func(c *Config) { c.Listen = ":8931" }},
		{"ephemeral port", func(c *Config) { c.Listen = "127.0.0.1:0" }},
		{"invalid port", func(c *Config) { c.Listen = "localhost:70000" }},
		{"relative lock directory", func(c *Config) { c.LockDir = "locks" }},
		{"Windows drive relative", func(c *Config) { c.Platform = "windows"; c.Binary = `C:nb1.exe` }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.modify(&config)
			output := filepath.Join(t.TempDir(), "rendered")
			if _, err := WriteFile(config, output); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid configuration created a file: %v", err)
			}
		})
	}
}

func TestStagingFileIsNotReplaced(t *testing.T) {
	output := filepath.Join(t.TempDir(), "nb1.service")
	if err := os.WriteFile(output, []byte("existing content"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Platform: "linux", Binary: "/opt/nb1", Devices: []string{"20005601"}}
	if _, err := WriteFile(c, output); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing file error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "existing content" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}
