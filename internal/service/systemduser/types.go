// Package systemduser manages one explicitly requested current-user unit.
// It does not use sudo, system-wide managers, USB, or human systemctl status.
package systemduser

import (
	"context"
	"errors"
	"time"

	"github.com/b1ug/nb1/internal/service"
)

const UnitName = "nb1.service"

const (
	operationBudget = 30 * time.Second
	queryBudget     = 3 * time.Second
	maxOutput       = 64 << 10
)

var ErrUnsupported = errors.New("systemd用户服务管理只支持Linux普通用户；其他平台可继续使用service render")

// Result is adapter data, never copied wholesale into a status or log.
type Result struct {
	Output    string
	ExitCode  int
	Truncated bool
}

type Runner interface {
	Run(context.Context, ...string) (Result, error)
}

type Properties struct {
	ID               string `json:"id"`
	LoadState        string `json:"load_state"`
	ActiveState      string `json:"active_state"`
	SubState         string `json:"sub_state"`
	FragmentPath     string `json:"fragment_path"`
	SourcePath       string `json:"source_path"`
	DropInPaths      string `json:"drop_in_paths"`
	UnitFileState    string `json:"unit_file_state"`
	Transient        bool   `json:"transient"`
	NeedDaemonReload bool   `json:"need_daemon_reload"`
	Job              uint32 `json:"job"`
	NoNewPrivileges  *bool  `json:"no_new_privileges"`
}

// Status keeps disk ownership, manager loading, activity and enablement apart.
// ManagedEnabledForLogin describes our one persistent default.target link;
// UnitFileState can also reveal activation arranged elsewhere.
type Status struct {
	SchemaVersion          int             `json:"schema_version"`
	CheckedAt              time.Time       `json:"checked_at"`
	Platform               string          `json:"platform"`
	Label                  string          `json:"label"`
	Target                 string          `json:"target"`
	UnitPath               string          `json:"unit_path"`
	EnableLinkPath         string          `json:"enable_link_path"`
	ConfigState            string          `json:"config_state"`
	EnableLinkState        string          `json:"enable_link_state"`
	ManagedEnabledForLogin *bool           `json:"managed_enabled_for_login"`
	Registration           string          `json:"registration"`
	RegistrationOwned      bool            `json:"registration_owned"`
	Config                 *service.Config `json:"config"`
	Properties             *Properties     `json:"properties"`
	Actions                []string        `json:"actions"`
	Notes                  []string        `json:"notes"`
}
