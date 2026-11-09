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
		return Report{}, fmt.Errorf("--serial 必须是1–128个字母、数字、下划线或连字符")
	}
	if options.Endpoint == "" {
		options.Endpoint = daemonclient.DefaultEndpoint
	}
	client, err := daemonclient.New(options.Endpoint)
	if err != nil {
		// URL parsing errors may quote the original input, including accidental
		// credentials. Report the constraint without echoing rejected input.
		return Report{}, fmt.Errorf("--endpoint 必须是本地loopback HTTP地址，例如 %s", daemonclient.DefaultEndpoint)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(parent, Budget)
	defer cancel()
	report := Report{SchemaVersion: 1, Status: "ok", Build: build,
		Daemon:    Daemon{Endpoint: strings.TrimRight(options.Endpoint, "/"), State: "unavailable", HTTPResponse: "unconfirmed"},
		Selection: "daemon_configured", RequestedSerial: options.Serial, ObservationMaxAgeMS: ObservationMaxAge.Milliseconds(),
		Devices: []Device{}, Issues: []Issue{}, Notes: []string{
			"HTTP响应只验证服务；设备online来自daemon，observed是已有快照，不是此命令触发的新采样。",
			"此命令只读取构建元数据和本地HTTP，不扫描或打开USB、不提交任务、不自动回退直控。",
			"native_hid_supported只表示此二进制编译了原生后端，不代表当前系统版本或设备型号已经通过实物验证。",
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
		start := "nb1 server --devices <明确序列号>"
		if options.Serial != "" {
			start = "nb1 server --devices " + options.Serial
		}
		report.issue("daemon_unavailable", "", "未能确认兼容的daemon；这不能说明硬件损坏或离线。", "检查 --endpoint 是否匹配已有服务；尚未启动时可运行 "+start+"。不要启动第二个进程抢占同一设备。")
		return finish(), nil
	}
	report.Daemon.HTTPResponse, report.Daemon.HealthStatus, report.Daemon.APIVersion = "received", health.Status, health.APIVersion
	if health.APIVersion != "v1" {
		report.Daemon.State = "incompatible"
		report.issue("api_version_mismatch", "", "HTTP有响应，但API版本不是v1。", "确认端口上的进程是当前nb1 daemon；更新配套CLI与daemon后重新诊断。")
		return finish(), nil
	}
	if health.Status != "ok" {
		report.Daemon.State = "unhealthy"
		report.issue("daemon_unhealthy", "", "daemon的health状态不是ok。", "查看daemon运行日志并确认服务状态；此命令不会重启或改变设备。")
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
		report.issue("device_list_unavailable", "", "HTTP和v1健康检查通过，但设备列表不可用。", "查看daemon日志及配套版本；HTTP存活不能替代设备在线检查。")
		return finish(), nil
	}
	report.Daemon.State, report.Daemon.DeviceListRead = "ready", true
	sort.Slice(states, func(i, j int) bool { return states[i].Serial < states[j].Serial })
	seen := make(map[string]bool)
	for _, state := range states {
		if !validSerial(state.Serial) || seen[state.Serial] || state.QueueDepth < 0 {
			report.Daemon.State = "devices_error"
			report.issue("invalid_device_state", "", "设备列表包含非法或重复序列号，或负数排队深度。", "检查daemon的API实现与配套版本；不要根据这个响应执行自动控制。")
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
				report.issue("observation_"+device.Observed.State, state.Serial, "设备标记在线，但读回快照缺失、超过5秒或时间异常。", "比较sampled_at与本机时间，查看daemon日志；再次诊断确认采样是否继续更新。")
			}
		case "busy":
			report.issue("device_busy", state.Serial, "设备被合作式所有权锁占用。", "通过持有它的daemon控制，或正常停止占用进程后再启动目标服务；保留锁文件，不要删除锁来抢占。")
		case "offline":
			report.issue("device_offline", state.Serial, "daemon已配置该设备，但当前未在线。", "核对序列号、USB连接与访问权限，并查看last_error；daemon会继续尝试连接，不需要静默回退直控。")
		case "connecting":
			report.issue("device_connecting", state.Serial, "daemon仍在尝试连接设备。", "稍后重新诊断；持续失败时核对USB、序列号、权限和daemon日志。")
		case "error":
			report.issue("device_error", state.Serial, "设备执行或清理操作发生错误。", "查看last_error和daemon日志，确认清理及后续连接状态；不要把旧快照当作当前实物状态。")
		case "closing", "closed":
			report.issue("device_"+state.Connection, state.Serial, "设备worker正在关闭或已经关闭。", "检查daemon是否正在正常退出；等待其释放设备后再启动服务。")
		default:
			report.issue("device_state_unknown", state.Serial, "daemon返回了未知设备连接状态。", "检查CLI与daemon是否配套，并查看daemon日志。")
		}
	}
	if len(report.Devices) == 0 {
		if options.Serial != "" {
			report.issue("target_not_configured", options.Serial, "所选序列号不在daemon配置目标中。", "检查 nb1 server --devices 配置；需要时正常重启服务并明确加入该序列号。doctor不会选取第一个USB设备。")
		} else {
			report.issue("no_configured_devices", "", "daemon没有返回配置目标。", "检查 nb1 server --devices 配置与运行日志。")
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
	fmt.Fprintf(&text, "nb1 doctor · 只读诊断 · %s\n", report.Status)
	fmt.Fprintf(&text, "检查时间：%s\n构建：%s %s/%s · CGO=%t · 原生HID后端=%t\n", report.CapturedAt.Format(time.RFC3339Nano), report.Build.GoVersion, report.Build.OS, report.Build.Arch, report.Build.CGOEnabled, report.Build.NativeHIDSupported)
	fmt.Fprintf(&text, "nb1：%s · revision=%s · modified=%s\n", moduleLabel(report.Build.Main), report.Build.Revision, report.Build.Modified)
	for _, module := range report.Build.Dependencies {
		fmt.Fprintf(&text, "%s：%s\n", module.Path, moduleLabel(module))
	}
	fmt.Fprintf(&text, "daemon：%s · %s · HTTP=%s · API=%q · health=%q\n", report.Daemon.Endpoint, report.Daemon.State, report.Daemon.HTTPResponse, report.Daemon.APIVersion, report.Daemon.HealthStatus)
	if report.Daemon.Error != "" {
		fmt.Fprintf(&text, "诊断错误：%q\n", report.Daemon.Error)
	}
	for _, device := range report.Devices {
		fmt.Fprintf(&text, "设备 %q：%s · queue=%d · active_job=%q · firmware=%d\n", device.Serial, device.Connection, device.QueueDepth, device.ActiveJob, device.Firmware)
		fmt.Fprintf(&text, "  observed：%s", device.Observed.State)
		if device.Observed.SampledAt != nil {
			fmt.Fprintf(&text, " · sampled_at=%s · LED1=%q · LED2=%q", device.Observed.SampledAt.Format(time.RFC3339Nano), device.Observed.LED1, device.Observed.LED2)
		}
		if device.Observed.AgeMS != nil {
			fmt.Fprintf(&text, " · age=%dms", *device.Observed.AgeMS)
		}
		fmt.Fprintln(&text)
		if device.LastError != "" {
			fmt.Fprintf(&text, "  last_error：%q\n", device.LastError)
		}
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(&text, "待检查 [%s] %q：%s\n  建议：%s\n", issue.Code, issue.Serial, issue.Message, issue.Advice)
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
		return "构建信息不可用"
	case "local_unversioned":
		return "本地工作区/替换，实际版本未编号（requested=" + module.RequestedVersion + "）"
	case "replacement":
		return module.Version + "（替换，requested=" + module.RequestedVersion + "）"
	default:
		return module.Version
	}
}
