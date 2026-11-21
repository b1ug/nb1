package httpapi

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/control"
)

type rootEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type rootDevice struct {
	Serial       string               `json:"serial"`
	Product      string               `json:"product,omitempty"`
	Firmware     int                  `json:"firmware,omitempty"`
	Connection   string               `json:"connection"`
	Capabilities []string             `json:"capabilities,omitempty"`
	ActiveJob    string               `json:"active_job,omitempty"`
	QueueDepth   int                  `json:"queue_depth"`
	LastError    string               `json:"last_error,omitempty"`
	Observed     *control.Observation `json:"observed"`
}

type rootDeviceSummary struct {
	Configured int `json:"configured"`
	Online     int `json:"online"`
	NotOnline  int `json:"not_online"`
}

type rootBuild struct {
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	Modified string `json:"modified"`
}

type rootWelcome struct {
	Service           string            `json:"service"`
	Welcome           string            `json:"welcome"`
	Status            string            `json:"status"`
	Build             rootBuild         `json:"build"`
	APIProfile        string            `json:"api_profile"`
	APIVersion        string            `json:"api_version"`
	ConsoleName       string            `json:"console_name"`
	ConsoleURL        string            `json:"console_url"`
	HealthURL         string            `json:"health_url"`
	DeviceStateSource string            `json:"device_state_source"`
	DeviceSummary     rootDeviceSummary `json:"device_summary"`
	DefaultDevice     string            `json:"default_device,omitempty"`
	DeviceFallback    string            `json:"device_fallback"`
	Devices           []rootDevice      `json:"devices"`
	Endpoints         []rootEndpoint    `json:"endpoints"`
	GeneratedAt       time.Time         `json:"generated_at"`
}

var sequenceAPIEndpoints = []rootEndpoint{
	{http.MethodGet, "/", "Service welcome, build version, API directory, default device and cached device status."},
	{http.MethodGet, "/healthz", "Confirm the local HTTP service is responding."},
	{http.MethodGet, "/v1/presence", "Identify the service and API version; this does not query devices."},
	{http.MethodGet, "/v1/service-status", "Read service status and configured/online device counts."},
	{http.MethodGet, "/v1/transport-metrics", "Read accumulated host-side device I/O call and retry counts."},
	{http.MethodGet, "/v1/devices", "List configured blink(1) devices and their cached status."},
	{http.MethodGet, "/v1/devices/{serial}", "Read one configured blink(1) device and its cached status."},
	{http.MethodPost, "/v1/preview", "Validate and compile an Action, Sequence, or saved Pattern without playing it."},
	{http.MethodPost, "/v1/jobs", "Submit a Sequence Job. Omit serial to use the configured default; provide it to target a specific device."},
	{http.MethodGet, "/v1/jobs/{job_id}", "Read the current in-memory status of a Sequence Job."},
	{http.MethodPost, "/v1/jobs/{job_id}/cancel", "Request cancellation of a Sequence Job."},
	{http.MethodGet, "/v1/colors", "List named colors saved in the local library."},
	{http.MethodPut, "/v1/colors/{name}", "Save or replace a named color in the local library."},
	{http.MethodPost, "/v1/colors/resolve", "Resolve a CSS name, hex value, RGB, or HSV color input."},
	{http.MethodGet, "/v1/patterns", "List saved Patterns available to the Sequence API."},
	{http.MethodGet, "/v1/patterns/{id}", "Read one saved Pattern."},
	{http.MethodPut, "/v1/patterns/{id}", "Save or replace a Pattern in the local library."},
	{http.MethodGet, "/v1/triggers", "List local trigger names and their Pattern/device bindings."},
	{http.MethodGet, "/v1/triggers/check", "Check saved trigger bindings against the Pattern library and managed devices."},
}

func (h *handler) serveWelcome(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !frameNoBodyOrQuery(w, r) {
		return
	}

	devices := []control.DeviceState{}
	if h.service != nil {
		if snapshot := h.service.Devices(); snapshot != nil {
			devices = snapshot
		}
	}
	response := rootWelcome{
		Service:           "nb1",
		Welcome:           "Welcome to nb1, the local blink(1) control service.",
		Status:            "running",
		Build:             readRootBuild(),
		APIProfile:        "sequence-v1",
		APIVersion:        "v1",
		ConsoleName:       "nb1 Workbench",
		ConsoleURL:        "/demo/",
		HealthURL:         "/healthz",
		DeviceStateSource: "cached",
		DeviceFallback:    "error",
		DeviceSummary: rootDeviceSummary{
			Configured: len(devices),
		},
		Devices:     make([]rootDevice, 0, len(devices)),
		Endpoints:   append([]rootEndpoint(nil), sequenceAPIEndpoints...),
		GeneratedAt: time.Now().UTC(),
	}
	if h.routing != nil {
		response.DefaultDevice = h.routing.defaultSerial
		if h.routing.useSingleOnline {
			response.DeviceFallback = "single_online"
		}
	}
	for _, device := range devices {
		if device.Connection == "online" {
			response.DeviceSummary.Online++
		} else {
			response.DeviceSummary.NotOnline++
		}
		response.Devices = append(response.Devices, rootDevice{
			Serial: device.Serial, Product: device.Product, Firmware: device.Firmware,
			Connection: device.Connection, Capabilities: publicSequenceCapabilities(device.Capabilities), ActiveJob: device.ActiveJob,
			QueueDepth: device.QueueDepth, LastError: device.LastError, Observed: device.Observed,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func readRootBuild() rootBuild {
	info, ok := debug.ReadBuildInfo()
	version := ""
	if ok && info != nil {
		version = info.Main.Version
	}
	if (version == "" || version == "(devel)") && strings.TrimSpace(config.GitSummary) != "" {
		version = strings.TrimSpace(config.GitSummary)
	}
	if version == "" || version == "(devel)" {
		version = "development"
	}
	build := rootBuild{Version: version, Revision: strings.TrimSpace(config.GitCommit), Modified: "unknown"}
	if ok && info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				build.Revision = setting.Value
			case "vcs.modified":
				build.Modified = setting.Value
			}
		}
	}
	return build
}
