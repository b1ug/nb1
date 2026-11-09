package systemduser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/b1ug/nb1/internal/service"
)

type Manager struct {
	configRoot string
	uid        int
	runner     Runner
}

// New is Linux-only and never accepts arbitrary unit names, UIDs, manager
// addresses or executable paths. It performs no systemctl call or writes.
func New() (*Manager, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	uid := os.Geteuid()
	if uid <= 0 || os.Getuid() != uid {
		return nil, fmt.Errorf("请使用当前普通用户；不支持root、sudo或身份切换")
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	var executable string
	for _, path := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && info.Mode().Perm()&0022 == 0 && ownedBy(info, 0) {
			executable = resolved
			break
		}
	}
	if executable == "" {
		return nil, fmt.Errorf("找不到受信任的系统systemctl；未进行任何服务操作")
	}
	return newManager(configRoot, uid, nativeRunner{binary: executable})
}

func newManager(configRoot string, uid int, runner Runner) (*Manager, error) {
	if !filepath.IsAbs(configRoot) || !utf8.ValidString(configRoot) || strings.IndexFunc(configRoot, unicode.IsControl) >= 0 || uid < 0 || runner == nil {
		return nil, fmt.Errorf("invalid current-user systemd context")
	}
	canonical, err := canonicalPath(configRoot)
	if err != nil {
		return nil, err
	}
	if err := safeAncestors(canonical, uid); err != nil {
		return nil, err
	}
	m := &Manager{configRoot: canonical, uid: uid, runner: runner}
	if err := m.directory(canonical, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return m, nil
}

func (m *Manager) initialStatus() Status {
	return Status{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Platform: "linux", Label: UnitName, Target: "user/" + strconv.Itoa(m.uid) + "/" + UnitName,
		UnitPath: m.unitPath(), EnableLinkPath: m.linkPath(), ConfigState: "not_checked", EnableLinkState: "not_checked", Registration: "not_checked", Actions: []string{}, Notes: []string{
			"loaded、active与登录启用分别报告；systemd状态不能替代HTTP/USB就绪检查。",
			"managed_enabled_for_login只描述本工具的default.target持久链接；其他来源看unit_file_state，未知来源不接管。",
			"不配置linger、系统级服务或登录前常驻；Stop保留unit和登录启用，Uninstall才移除受控文件。",
		}}
}

func applySnapshot(status *Status, s *snapshot) {
	status.ConfigState, status.EnableLinkState = "absent", "absent"
	enabled := s.link != nil
	status.ManagedEnabledForLogin = &enabled
	if s.unit != nil {
		status.ConfigState = "managed"
		config := s.unit.definition.Config
		status.Config = &config
	}
	if s.link != nil {
		status.EnableLinkState = "owned"
	}
}

func (m *Manager) inspectStatus(ctx context.Context, configOnly bool) (Status, *snapshot, error) {
	status := m.initialStatus()
	s, err := m.inspect()
	if err != nil {
		status.ConfigState, status.EnableLinkState = "conflict", "conflict"
		return status, s, err
	}
	applySnapshot(&status, s)
	if configOnly {
		return status, s, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, queryBudget)
	defer cancel()
	result, err := m.runner.Run(queryCtx, showArgs()...)
	if err != nil || result.Truncated {
		status.Registration = "unknown"
		return status, s, fmt.Errorf("无法完整查询用户systemd，未推断unit缺失")
	}
	properties, err := parseProperties(result.Output)
	if err != nil {
		status.Registration = "unknown"
		return status, s, err
	}
	status.Properties = &properties
	if result.ExitCode != 0 && !(result.ExitCode == 4 && absentProperties(properties)) {
		status.Registration = "unknown"
		return status, s, fmt.Errorf("systemctl show失败，注册状态未知")
	}
	if absentProperties(properties) {
		status.Registration = "absent"
		return status, s, nil
	}
	if properties.LoadState != "loaded" {
		status.Registration = "unusable"
		return status, s, nil
	}
	status.Registration = "loaded"
	status.RegistrationOwned = m.trustedLoaded(s, properties) == nil
	return status, s, nil
}

func (m *Manager) trustedLoaded(s *snapshot, p Properties) error {
	if s.unit == nil || p.LoadState != "loaded" || p.ID != UnitName || p.Transient || p.NeedDaemonReload || p.SourcePath != "" || p.DropInPaths != "" || p.NoNewPrivileges == nil || !*p.NoNewPrivileges {
		return fmt.Errorf("unit来源、覆盖或reload状态不明确；拒绝操作")
	}
	if !filepath.IsAbs(p.FragmentPath) || strings.IndexFunc(p.FragmentPath, unicode.IsControl) >= 0 {
		return fmt.Errorf("FragmentPath未知，拒绝操作")
	}
	fragment, err := filepath.EvalSymlinks(p.FragmentPath)
	if err != nil || fragment != m.unitPath() {
		return fmt.Errorf("同名unit来自其他位置，拒绝操作")
	}
	wantState := "disabled"
	if s.link != nil {
		wantState = "enabled"
	}
	if p.UnitFileState != wantState {
		return fmt.Errorf("登录启用状态含未知链接、runtime覆盖或其他来源，拒绝接管")
	}
	return nil
}

func (m *Manager) Status(ctx context.Context, configOnly bool) (Status, error) {
	status, _, err := m.inspectStatus(ctx, configOnly)
	return status, err
}

func (m *Manager) run(ctx context.Context, action string, status *Status) error {
	status.Actions = append(status.Actions, action+"_requested")
	result, err := m.runner.Run(ctx, commandArgs(action)...)
	if err != nil || result.ExitCode != 0 || result.Truncated {
		status.Registration = "unknown"
		status.RegistrationOwned = false
		return fmt.Errorf("systemctl %s结果未确认；不重试、不强杀、不自动回滚文件，请重新查询状态", action)
	}
	return nil
}

func carryActions(current Status, previous Status) Status {
	current.Actions = append([]string{}, previous.Actions...)
	return current
}

func (m *Manager) Install(ctx context.Context, config service.Config, start bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	config.Platform = "linux"
	if config.Listen == "" {
		config.Listen = "127.0.0.1:8931"
	}
	content, err := unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: config})
	if err != nil {
		return status, err
	}
	info, err := os.Stat(config.Binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return status, fmt.Errorf("binary必须是明确的已存在绝对可执行普通文件")
	}
	config.Binary, err = filepath.EvalSymlinks(config.Binary)
	if err != nil {
		return status, err
	}
	if err := validateBinary(config.Binary, m.uid); err != nil {
		return status, err
	}
	content, err = unitContent(definition{Version: 1, Unit: UnitName, UID: m.uid, Config: config})
	if err != nil {
		return status, err
	}
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, s, err := m.inspectStatus(ctx, false)
	if err != nil {
		return status, err
	}
	if s.unit != nil && !bytes.Equal(s.unit.content, content) {
		return status, fmt.Errorf("现有托管配置不同；不覆盖升级，请先审阅并正常卸载")
	}
	if status.Registration != "absent" {
		if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
			return status, fmt.Errorf("已有同名外部/未知unit或覆盖，不会安装")
		}
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if s.unit == nil {
		if err := m.directory(m.unitDir(), true); err != nil {
			return status, err
		}
		if err := publish(m.unitPath(), content); err != nil {
			status.ConfigState = "conflict"
			return status, err
		}
		status.Actions = append(status.Actions, "unit_created")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
	}
	// Refresh a newly created/unloaded definition before creating a persistent
	// enable link, so foreign drop-ins cannot silently become login startup.
	if status.Registration == "absent" {
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
			return status, fmt.Errorf("重载后无法确认本工具unit来源；文件保留，未设置登录启用")
		}
		s = current
	}
	if s.link == nil {
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if err := m.directory(filepath.Dir(m.linkPath()), true); err != nil {
			return status, err
		}
		if err := os.Symlink(m.unitPath(), m.linkPath()); err != nil {
			return status, fmt.Errorf("启用链接已存在或创建失败；不会覆盖: %w", err)
		}
		status.Actions = append(status.Actions, "enable_link_created")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
			return status, fmt.Errorf("登录链接已创建但启用结果未确认；保留配置，不自动启动")
		}
	}
	if !start {
		return status, nil
	}
	return m.changeLocked(ctx, true, status.Actions, s)
}

func (m *Manager) Start(ctx context.Context) (Status, error) { return m.change(ctx, true) }
func (m *Manager) Stop(ctx context.Context) (Status, error)  { return m.change(ctx, false) }

func (m *Manager) change(ctx context.Context, start bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	return m.changeLocked(ctx, start, nil, nil)
}

func (m *Manager) changeLocked(ctx context.Context, start bool, actions []string, expected *snapshot) (Status, error) {
	status, s, err := m.inspectStatus(ctx, false)
	status.Actions = append([]string{}, actions...)
	if err != nil {
		return status, err
	}
	if expected != nil && !sameSnapshot(expected, s) {
		return status, fmt.Errorf("unit在外层检查后改变；未开始或停止新配置")
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
		return status, fmt.Errorf("未确认是本工具完整unit及启用状态，拒绝开始/停止")
	}
	p := *status.Properties
	if p.Job != 0 {
		return status, fmt.Errorf("unit有待处理任务，不替换其他start/stop操作")
	}
	if (start && p.ActiveState == "active") || (!start && inactive(p)) {
		return status, nil
	}
	if start && !inactive(p) {
		return status, fmt.Errorf("unit不在可开始的稳定状态，未替换当前任务")
	}
	if !start && p.ActiveState != "active" && p.ActiveState != "activating" && p.ActiveState != "reloading" {
		return status, fmt.Errorf("unit停止状态未知，未发出命令")
	}
	if start {
		if err := validateBinary(s.unit.definition.Config.Binary, m.uid); err != nil {
			return status, err
		}
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	action := "stop"
	if start {
		action = "start"
	}
	if err := m.run(ctx, action, &status); err != nil {
		return status, err
	}
	latest, current, err := m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil {
		return status, fmt.Errorf("操作后unit归属未知，文件保留")
	}
	if status.Properties.Job != 0 || start && status.Properties.ActiveState != "active" || !start && !inactive(*status.Properties) {
		return status, fmt.Errorf("systemd操作返回后未确认目标活动状态；不据此声称HTTP或硬件就绪")
	}
	return status, nil
}

func validateBinary(path string, uid int) error {
	if err := safeAncestors(filepath.Dir(path), uid); err != nil {
		return fmt.Errorf("binary路径祖先可被不受信任用户改写，未启动或安装: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 || (!ownedBy(info, uid) && !ownedBy(info, 0)) {
		return fmt.Errorf("binary缺失、非可执行普通文件、成为symlink或可被其他用户改写；未安装或启动")
	}
	return nil
}

func (m *Manager) Uninstall(ctx context.Context, stop bool) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	status := m.initialStatus()
	lease, err := m.managementLease()
	if err != nil {
		return status, err
	}
	defer lease.Close()
	status, s, err := m.inspectStatus(ctx, false)
	if err != nil {
		return status, err
	}
	original := s
	if status.Registration != "absent" {
		if status.Properties == nil || m.trustedLoaded(s, *status.Properties) != nil {
			return status, fmt.Errorf("同名unit归属/覆盖未知，拒绝停止或删除")
		}
		if !inactive(*status.Properties) {
			if !stop {
				return status, fmt.Errorf("unit仍活动；显式Uninstall(stop=true)才会先停止")
			}
			if err := m.unchanged(original); err != nil {
				return status, err
			}
			status, err = m.changeLocked(ctx, false, status.Actions, original)
			if err != nil {
				return status, err
			}
		}
	}
	if err := m.unchanged(original); err != nil {
		return status, err
	}
	if original.link != nil {
		if err := os.Remove(m.linkPath()); err != nil {
			return status, err
		}
		status.Actions = append(status.Actions, "enable_link_removed")
		s, err = m.inspect()
		if err != nil {
			return status, err
		}
		applySnapshot(&status, s)
		if err := m.run(ctx, "daemon-reload", &status); err != nil {
			return status, err
		}
		latest, current, err := m.inspectStatus(ctx, false)
		latest = carryActions(latest, status)
		status = latest
		if err != nil {
			return status, err
		}
		if err := m.unchanged(s); err != nil {
			return status, err
		}
		if status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil || !inactive(*status.Properties) {
			return status, fmt.Errorf("移除本工具链接后仍有未知启用/覆盖或活动状态；保留unit，不删除其他链接")
		}
		s = current
	}
	if s.unit == nil {
		return status, nil
	}
	// Recheck the manager after any stop/link phase and immediately before
	// deleting its definition. An observed new activation is not absence.
	latest, current, err := m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	if status.Registration != "absent" && (status.Properties == nil || m.trustedLoaded(current, *status.Properties) != nil || !inactive(*status.Properties)) {
		return status, fmt.Errorf("删除unit前发现活动、覆盖或未知来源；保留文件")
	}
	if err := m.unchanged(s); err != nil {
		return status, err
	}
	// The lease serializes this tool only. Content/inode rechecks and Remove
	// cannot form an atomic compare-delete against malicious same-UID writers.
	if err := os.Remove(m.unitPath()); err != nil {
		return status, err
	}
	status.Actions = append(status.Actions, "unit_removed")
	status.ConfigState = "absent"
	status.Config = nil
	if err := m.run(ctx, "daemon-reload", &status); err != nil {
		return status, err
	}
	latest, _, err = m.inspectStatus(ctx, false)
	latest = carryActions(latest, status)
	status = latest
	if err != nil {
		return status, err
	}
	if status.Registration != "absent" {
		return status, fmt.Errorf("本工具文件已移除，但manager还有同名来源；不接管或停止它")
	}
	return status, nil
}
