package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
)

const (
	Budget            = 3 * time.Second
	ObservationMaxAge = 5 * time.Second
)

type Options struct{ Endpoint, Serial string }

type Issue struct {
	Code    string `json:"code"`
	Serial  string `json:"serial"`
	Message string `json:"message"`
	Advice  string `json:"advice"`
}

type Daemon struct {
	Endpoint       string `json:"endpoint"`
	State          string `json:"state"`
	HTTPResponse   string `json:"http_response"`
	HealthStatus   string `json:"health_status"`
	APIVersion     string `json:"api_version"`
	DeviceListRead bool   `json:"device_list_read"`
	Error          string `json:"error"`
}

type Snapshot struct {
	State     string     `json:"state"`
	SampledAt *time.Time `json:"sampled_at"`
	AgeMS     *int64     `json:"age_ms"`
	LED1      string     `json:"led1"`
	LED2      string     `json:"led2"`
}

type Device struct {
	Serial     string   `json:"serial"`
	Connection string   `json:"connection"`
	ActiveJob  string   `json:"active_job"`
	QueueDepth int      `json:"queue_depth"`
	Firmware   int      `json:"firmware"`
	Observed   Snapshot `json:"observed"`
	LastError  string   `json:"last_error"`
}

// Report is versioned separately from the daemon API. Arrays are always JSON
// arrays, and unknown snapshot timestamps/ages are null rather than zero dates.
type Report struct {
	SchemaVersion       int       `json:"schema_version"`
	CapturedAt          time.Time `json:"captured_at"`
	Status              string    `json:"status"`
	Build               Build     `json:"build"`
	Daemon              Daemon    `json:"daemon"`
	Selection           string    `json:"selection"`
	RequestedSerial     string    `json:"requested_serial"`
	ObservationMaxAgeMS int64     `json:"observation_max_age_ms"`
	Devices             []Device  `json:"devices"`
	Issues              []Issue   `json:"issues"`
	Notes               []string  `json:"notes"`
}

func Collect(ctx context.Context, options Options, build Build) (Report, error) {
	return collect(ctx, options, build, time.Now)
}

func collect(parent context.Context, options Options, build Build, now func() time.Time) (Report, error) {
	if options.Serial != "" && !validSerial(options.Serial) {
		return Report{}, fmt.Errorf("--serial must contain 1-128 letters, digits, underscores or hyphens")
	}
	if options.Endpoint == "" {
		options.Endpoint = daemonclient.DefaultEndpoint
	}
	client, err := daemonclient.New(options.Endpoint)
	if err != nil {
		// URL parsing errors may quote the original input, including accidental
		// credentials. Report the constraint without echoing rejected input.
		return Report{}, fmt.Errorf("--endpoint must be a local loopback HTTP address, such as %s", daemonclient.DefaultEndpoint)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(parent, Budget)
	defer cancel()
	report := Report{SchemaVersion: 1, Status: "ok", Build: build,
		Daemon:    Daemon{Endpoint: strings.TrimRight(options.Endpoint, "/"), State: "unavailable", HTTPResponse: "unconfirmed"},
		Selection: "daemon_configured", RequestedSerial: options.Serial, ObservationMaxAgeMS: ObservationMaxAge.Milliseconds(),
		Devices: []Device{}, Issues: []Issue{}, Notes: []string{
			"An HTTP response verifies only the service; device online state comes from the daemon, and observed is an existing snapshot, not a new sample triggered by this command.",
			"This command only reads build metadata and local HTTP; it does not enumerate or open USB, submit Jobs or automatically fall back to direct control.",
			"native_hid_supported means only that this binary includes a native backend; it does not establish physical validation of the current OS version or device model.",
		}}
	if options.Serial != "" {
		report.Selection = "explicit_serial"
	}
	finish := func() Report {
		report.CapturedAt = now().UTC()
		if len(report.Issues) != 0 {
			report.Status = "attention"
		}
		return report
	}
	health, err := client.Health(ctx)
	if err != nil {
		report.Daemon.Error = err.Error()
		var responseError *daemonclient.Error
		var decodeError *daemonclient.DecodeError
		if errors.As(err, &responseError) || errors.As(err, &decodeError) {
			report.Daemon.HTTPResponse = "received"
		}
		start := "nb1 server --devices DEVICE_SERIAL"
		if options.Serial != "" {
			start = "nb1 server --devices " + options.Serial
		}
		report.issue("daemon_unavailable", "", "A compatible daemon could not be confirmed; this does not establish that the hardware is broken or offline.", "Check that --endpoint matches the existing service; if it has not been started, run "+start+". Do not start a second process to take over the same device.")
		return finish(), nil
	}
	report.Daemon.HTTPResponse, report.Daemon.HealthStatus, report.Daemon.APIVersion = "received", health.Status, health.APIVersion
	if health.APIVersion != "v1" {
		report.Daemon.State = "incompatible"
		report.issue("api_version_mismatch", "", "HTTP responded, but the API version is not v1.", "Confirm that the process on this port is the current nb1 daemon; update the matching CLI and daemon, then diagnose again.")
		return finish(), nil
	}
	if health.Status != "ok" {
		report.Daemon.State = "unhealthy"
		report.issue("daemon_unhealthy", "", "The daemon health status is not ok.", "Check the daemon logs and service status; this command does not restart it or change the device.")
		return finish(), nil
	}
	states, err := client.Devices(ctx)
	if err != nil || states == nil {
		report.Daemon.State = "devices_error"
		if err != nil {
			report.Daemon.Error = err.Error()
		} else {
			report.Daemon.Error = "device response must be an array, not null"
		}
		report.issue("device_list_unavailable", "", "HTTP and the v1 health check passed, but the device list is unavailable.", "Check the daemon logs and matching versions; an HTTP response does not replace checking device connectivity.")
		return finish(), nil
	}
	report.Daemon.State, report.Daemon.DeviceListRead = "ready", true
	sort.Slice(states, func(i, j int) bool { return states[i].Serial < states[j].Serial })
	seen := make(map[string]bool)
	for _, state := range states {
		if !validSerial(state.Serial) || seen[state.Serial] || state.QueueDepth < 0 {
			report.Daemon.State = "devices_error"
			report.issue("invalid_device_state", "", "The device list contains invalid or duplicate serial numbers, or a negative queue depth.", "Check the daemon API implementation and matching versions; do not perform automatic control based on this response.")
			return finish(), nil
		}
		seen[state.Serial] = true
	}
	checkedAt := now().UTC()
	for _, state := range states {
		if options.Serial != "" && state.Serial != options.Serial {
			continue
		}
		device := Device{Serial: state.Serial, Connection: state.Connection, ActiveJob: state.ActiveJob, QueueDepth: state.QueueDepth,
			Firmware: state.Firmware, LastError: state.LastError, Observed: snapshot(state.Observed, checkedAt)}
		report.Devices = append(report.Devices, device)
		switch state.Connection {
		case "online":
			if device.Observed.State != "fresh" {
				report.issue("observation_"+device.Observed.State, state.Serial, "The device is marked online, but its readback snapshot is missing, older than five seconds or has an inconsistent timestamp.", "Compare sampled_at with the local clock and check the daemon logs; diagnose again to see whether sampling continues to update.")
			}
		case "busy":
			report.issue("device_busy", state.Serial, "The device is held by a cooperative ownership lock.", "Control it through the owning daemon, or normally stop the owning process before starting the intended service; keep the lock file and do not delete it to take over the device.")
		case "offline":
			report.issue("device_offline", state.Serial, "The daemon is configured for this device, but it is currently offline.", "Check the serial number, USB connection and access permissions, and inspect last_error; the daemon will continue connection attempts without silently falling back to direct control.")
		case "connecting":
			report.issue("device_connecting", state.Serial, "The daemon is still trying to connect to the device.", "Diagnose again later; if failures persist, check USB, the serial number, permissions and daemon logs.")
		case "error":
			report.issue("device_error", state.Serial, "A device operation or cleanup failed.", "Check last_error and daemon logs to confirm cleanup and subsequent connection state; do not treat an old snapshot as the current physical state.")
		case "closing", "closed":
			report.issue("device_"+state.Connection, state.Serial, "The device worker is closing or has closed.", "Check whether the daemon is exiting normally; wait for it to release the device before starting the service.")
		default:
			report.issue("device_state_unknown", state.Serial, "The daemon returned an unknown device connection state.", "Check that the CLI and daemon versions match, and inspect the daemon logs.")
		}
	}
	if len(report.Devices) == 0 {
		if options.Serial != "" {
			report.issue("target_not_configured", options.Serial, "The selected serial number is not among the daemon's configured targets.", "Check the nb1 server --devices configuration; if needed, normally restart the service and explicitly include this serial number. doctor does not select the first USB device.")
		} else {
			report.issue("no_configured_devices", "", "The daemon returned no configured targets.", "Check the nb1 server --devices configuration and daemon logs.")
		}
	}
	return finish(), nil
}

func (r *Report) issue(code, serial, message, advice string) {
	r.Issues = append(r.Issues, Issue{Code: code, Serial: serial, Message: message, Advice: advice})
}

func validSerial(serial string) bool {
	return serial != "" && len(serial) <= 128 && strings.IndexFunc(serial, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_')
	}) < 0
}

func snapshot(observed *control.Observation, now time.Time) Snapshot {
	result := Snapshot{State: "missing"}
	if observed == nil || observed.SampledAt.IsZero() {
		return result
	}
	sampledAt := observed.SampledAt.UTC()
	result.SampledAt, result.LED1, result.LED2 = &sampledAt, observed.LED1, observed.LED2
	age := now.Sub(sampledAt)
	if age < -time.Second {
		result.State = "clock_skew"
		return result
	}
	if age < 0 {
		age = 0 // tolerate sub-second clock adjustment/rounding
	}
	ageMS := age.Milliseconds()
	result.AgeMS, result.State = &ageMS, "fresh"
	if age > ObservationMaxAge {
		result.State = "stale"
	}
	return result
}

func WriteText(output io.Writer, report Report) error {
	var text strings.Builder
	fmt.Fprintf(&text, "nb1 doctor · read-only diagnostics · %s\n", report.Status)
	fmt.Fprintf(&text, "checked at: %s\nbuild: %s %s/%s · CGO=%t · native HID backend=%t\n", report.CapturedAt.Format(time.RFC3339Nano), report.Build.GoVersion, report.Build.OS, report.Build.Arch, report.Build.CGOEnabled, report.Build.NativeHIDSupported)
	fmt.Fprintf(&text, "nb1: %s · revision=%s · modified=%s\n", moduleLabel(report.Build.Main), report.Build.Revision, report.Build.Modified)
	for _, module := range report.Build.Dependencies {
		fmt.Fprintf(&text, "%s: %s\n", module.Path, moduleLabel(module))
	}
	fmt.Fprintf(&text, "daemon: %s · %s · HTTP=%s · API=%q · health=%q\n", report.Daemon.Endpoint, report.Daemon.State, report.Daemon.HTTPResponse, report.Daemon.APIVersion, report.Daemon.HealthStatus)
	if report.Daemon.Error != "" {
		fmt.Fprintf(&text, "diagnostic error: %q\n", report.Daemon.Error)
	}
	for _, device := range report.Devices {
		fmt.Fprintf(&text, "device %q: %s · queue=%d · active_job=%q · firmware=%d\n", device.Serial, device.Connection, device.QueueDepth, device.ActiveJob, device.Firmware)
		fmt.Fprintf(&text, "  observed: %s", device.Observed.State)
		if device.Observed.SampledAt != nil {
			fmt.Fprintf(&text, " · sampled_at=%s · LED1=%q · LED2=%q", device.Observed.SampledAt.Format(time.RFC3339Nano), device.Observed.LED1, device.Observed.LED2)
		}
		if device.Observed.AgeMS != nil {
			fmt.Fprintf(&text, " · age=%dms", *device.Observed.AgeMS)
		}
		fmt.Fprintln(&text)
		if device.LastError != "" {
			fmt.Fprintf(&text, "  last_error: %q\n", device.LastError)
		}
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(&text, "attention [%s] %q: %s\n  advice: %s\n", issue.Code, issue.Serial, issue.Message, issue.Advice)
	}
	for _, note := range report.Notes {
		fmt.Fprintln(&text, note)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

func moduleLabel(module Module) string {
	switch module.Source {
	case "unavailable":
		return "build information unavailable"
	case "local_unversioned":
		return "local workspace/replacement, actual version unversioned (requested=" + module.RequestedVersion + ")"
	case "replacement":
		return module.Version + " (replacement, requested=" + module.RequestedVersion + ")"
	default:
		return module.Version
	}
}
