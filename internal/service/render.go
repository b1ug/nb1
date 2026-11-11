// Package service renders reviewable per-user startup configurations and
// manages explicitly requested macOS user agents. Rendering alone never
// registers, enables, starts or stops an operating-system service.
package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const Label = "org.b1ug.nb1"

type Config struct {
	Platform   string   `json:"platform"`
	Binary     string   `json:"binary"`
	BinaryRoot string   `json:"binary_root,omitempty"`
	Devices    []string `json:"devices"`
	Listen     string   `json:"listen"`
	LockDir    string   `json:"lock_dir"`
}

type Artifact struct {
	Filename string
	Content  []byte
}

var serialPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)

func validate(c Config) error {
	if c.Platform != "darwin" && c.Platform != "linux" && c.Platform != "windows" {
		return fmt.Errorf("platform must be darwin, linux or windows")
	}
	if c.BinaryRoot != "" {
		if c.Platform == "windows" || !strings.HasPrefix(c.BinaryRoot, "/") || !utf8.ValidString(c.BinaryRoot) || strings.IndexFunc(c.BinaryRoot, unicode.IsControl) >= 0 {
			return fmt.Errorf("binary_root requires an absolute Unix package directory")
		}
	}
	for name, value := range map[string]string{"binary": c.Binary, "lock directory": c.LockDir} {
		if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("%s must be valid UTF-8 without control characters", name)
		}
		if name == "lock directory" && value == "" {
			continue
		}
		if !absoluteTargetPath(c.Platform, value) {
			return fmt.Errorf("%s must be an absolute %s path", name, c.Platform)
		}
		if c.Platform == "windows" && strings.ContainsAny(value, `"<>|*?`) {
			return fmt.Errorf("%s contains characters invalid in a Windows path", name)
		}
	}
	if strings.HasSuffix(c.Binary, "/") || (c.Platform == "windows" && strings.HasSuffix(c.Binary, `\`)) {
		return fmt.Errorf("binary must name a file, not a directory")
	}
	if len(c.Devices) == 0 || len(c.Devices) > 64 {
		return fmt.Errorf("select between 1 and 64 explicit device serials")
	}
	seen := make(map[string]bool)
	for _, serial := range c.Devices {
		if !serialPattern.MatchString(serial) {
			return fmt.Errorf("invalid serial %q: use exactly eight hex digits, not a device ordinal", serial)
		}
		key := strings.ToLower(serial)
		if seen[key] {
			return fmt.Errorf("duplicate device serial %q", serial)
		}
		seen[key] = true
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be a loopback host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("listen must use a loopback IP or localhost")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("service listen port must be between 1 and 65535")
	}
	return nil
}

func absoluteTargetPath(platform, value string) bool {
	if platform != "windows" {
		return strings.HasPrefix(value, "/")
	}
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	if strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		parts := strings.FieldsFunc(value[2:], func(r rune) bool { return r == '\\' || r == '/' })
		return len(parts) >= 2
	}
	return false
}

func arguments(c Config) []string {
	args := []string{"server", "--devices", strings.Join(c.Devices, ","), "--listen", c.Listen}
	if c.LockDir != "" {
		args = append(args, "--lock-dir", c.LockDir)
	}
	return args
}

// Render treats paths as target-platform values. Cross-platform rendering does
// not stat a remote executable or prove that it will run on the target system.
func Render(c Config) (Artifact, error) {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8931"
	}
	if err := validate(c); err != nil {
		return Artifact{}, err
	}
	switch c.Platform {
	case "darwin":
		return Artifact{"org.b1ug.nb1.plist", renderLaunchd(c)}, nil
	case "linux":
		return Artifact{"nb1.service", renderSystemd(c)}, nil
	default:
		return Artifact{"nb1-task.ps1", renderWindows(c)}, nil
	}
}

// WriteFile validates and renders before opening the requested staging file.
// It refuses to replace an existing file and does not create parent directories.
func WriteFile(c Config, output string) (Artifact, error) {
	artifact, err := Render(c)
	if err != nil {
		return Artifact{}, err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Artifact{}, err
	}
	_, writeErr := f.Write(artifact.Content)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(output)
		if writeErr != nil {
			return Artifact{}, writeErr
		}
		return Artifact{}, closeErr
	}
	return artifact, nil
}

func renderLaunchd(c Config) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">\n<dict>\n  <key>Label</key>\n  <string>" + Label + "</string>\n  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range append([]string{c.Binary}, arguments(c)...) {
		b.WriteString("    <string>")
		_ = xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n  <key>RunAtLoad</key><true/>\n  <key>KeepAlive</key><true/>\n  <key>ThrottleInterval</key><integer>10</integer>\n  <key>ExitTimeOut</key><integer>20</integer>\n</dict>\n</plist>\n")
	return b.Bytes()
}

func systemdArgument(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%").Replace(value)
	return "\"" + value + "\""
}

func renderSystemd(c Config) []byte {
	args := append([]string{c.Binary}, arguments(c)...)
	for i := range args {
		args[i] = systemdArgument(args[i])
	}
	// The ':' executable prefix disables environment expansion for argv;
	// percent specifiers still need escaping independently.
	return []byte("[Unit]\nDescription=nb1 local blink(1) control daemon\n\n[Service]\nType=simple\nExecStart=:" + strings.Join(args, " ") + "\nRestart=on-failure\nRestartSec=5s\nKillSignal=SIGTERM\nTimeoutStopSec=20s\nUMask=0077\nNoNewPrivileges=yes\n\n[Install]\nWantedBy=default.target\n")
}

func powershellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// windowsArgument follows native Windows backslash/quote parsing, including a
// doubled trailing backslash before the closing quote. No cmd.exe is involved.
func windowsArgument(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range value {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		b.WriteRune(r)
		slashes = 0
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}

func encodedPowerShell(source string) string {
	units := utf16.Encode([]rune(source))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func renderWindows(c Config) []byte {
	args := arguments(c)
	for i := range args {
		args[i] = windowsArgument(args[i])
	}
	// Task Scheduler expands environment variables in action arguments. Keep
	// user paths inside an encoded payload, then create the process without a
	// shell; literal percent signs and PowerShell syntax remain ordinary data.
	payload := "$ErrorActionPreference = 'Stop'\n$startInfo = New-Object System.Diagnostics.ProcessStartInfo\n$startInfo.FileName = " + powershellLiteral(c.Binary) + "\n$startInfo.Arguments = " + powershellLiteral(strings.Join(args, " ")) + "\n$startInfo.UseShellExecute = $false\n$startInfo.CreateNoWindow = $true\n$process = [System.Diagnostics.Process]::Start($startInfo)\n$process.WaitForExit()\nexit $process.ExitCode\n"
	return []byte("# Review this script before running it. Running it registers a current-user logon task.\n# Rendering this file does not install or start anything. This is not an SCM service.\n$ErrorActionPreference = 'Stop'\n$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()\n$taskName = 'B1UG-nb1-' + $identity.User.Value\nif (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) { throw 'The task already exists; inspect it before replacing it.' }\n$encoded = '" + encodedPowerShell(payload) + "'\n$powerShell = Join-Path ([Environment]::GetFolderPath('System')) 'WindowsPowerShell\\v1.0\\powershell.exe'\n$action = New-ScheduledTaskAction -Execute $powerShell -Argument ('-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand ' + $encoded)\n$trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity.Name\n$principal = New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType Interactive -RunLevel Limited\n$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)\nRegister-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description 'Local blink(1) device daemon for the current user'\nWrite-Output ('Registered ' + $taskName + '. It will start at logon; use Start-ScheduledTask for an immediate manual start.')\n")
}
