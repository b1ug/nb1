// Package taskuser verifies only the current Windows process's read-only
// identity. It does not connect to Task Scheduler, run scripts or manage tasks.
package taskuser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrUnsupported = errors.New("Windows current-user task identity is unsupported on this platform")
	ErrIdentity    = errors.New("Windows current-user task identity is unverified or not permitted")
)

// Context has no public constructor or writable identity fields. Its zero value
// is unverified. A successful Current does not prove an active Scheduler session,
// task permission, task registration, process readiness or USB availability.
type Context struct {
	sid, taskPath, adminMembership string
	sessionID, integrityRID        uint32
	elevationType                  uint32
	verified                       bool
}

func (c Context) Valid() bool             { return c.verified }
func (c Context) SID() string             { return c.sid }
func (c Context) TaskPath() string        { return c.taskPath }
func (c Context) SessionID() uint32       { return c.sessionID }
func (c Context) IntegrityRID() uint32    { return c.integrityRID }
func (c Context) ElevationType() uint32   { return c.elevationType }
func (c Context) AdminMembership() string { return c.adminMembership }

type tokenGroup struct {
	sid        string
	attributes uint32
}

type tokenEvidence struct {
	sid, integritySID       string
	elevated, elevationType uint32
	groups                  []tokenGroup
	sessionID               uint32
}

type tokenReader interface {
	userSID() (string, error)
	elevation() (uint32, error)
	elevationType() (uint32, error)
	integritySID() (string, error)
	groups() ([]tokenGroup, error)
	sessionID() (uint32, error)
	close() error
}

func rejected(reason string) error { return fmt.Errorf("%w: %s", ErrIdentity, reason) }

func readContext(open func() (tokenReader, error)) (result Context, err error) {
	reader, openErr := open()
	if reader != nil {
		defer func() {
			if closeErr := reader.close(); closeErr != nil {
				result = Context{}
				if err == nil {
					err = rejected("token close failed: " + closeErr.Error())
				} else {
					err = fmt.Errorf("%w; token close failed: %v", err, closeErr)
				}
			}
		}()
	}
	if openErr != nil {
		return Context{}, rejected("TOKEN_QUERY open failed: " + openErr.Error())
	}
	if reader == nil {
		return Context{}, rejected("token reader missing")
	}
	var e tokenEvidence
	if e.sid, err = reader.userSID(); err != nil {
		return Context{}, rejected("TokenUser query failed: " + err.Error())
	}
	if e.elevated, err = reader.elevation(); err != nil {
		return Context{}, rejected("TokenElevation query failed: " + err.Error())
	}
	if e.elevationType, err = reader.elevationType(); err != nil {
		return Context{}, rejected("TokenElevationType query failed: " + err.Error())
	}
	if e.integritySID, err = reader.integritySID(); err != nil {
		return Context{}, rejected("TokenIntegrityLevel query failed: " + err.Error())
	}
	if e.groups, err = reader.groups(); err != nil {
		return Context{}, rejected("TokenGroups query failed: " + err.Error())
	}
	if e.sessionID, err = reader.sessionID(); err != nil {
		return Context{}, rejected("TokenSessionId query failed: " + err.Error())
	}
	return classify(e)
}

func classify(e tokenEvidence) (Context, error) {
	user, err := sidParts(e.sid)
	// Account authority and permission evidence are separate. Entra's final
	// component is not a local/domain RID, even when its value happens to be 500.
	domain := err == nil && len(user) == 6 && user[0] == 5 && user[1] == 21
	// Microsoft documents S-1-12-1 plus four GUID words and also publishes
	// whoami examples with authority 12 directly followed by four words.
	entra := err == nil && user[0] == 12 && (len(user) == 5 || (len(user) == 6 && user[1] == 1))
	if !domain && !entra {
		return Context{}, rejected("TokenUser account SID family is unknown or not permitted")
	}
	if domain && user[5] == 500 {
		return Context{}, rejected("Builtin/domain Administrator RID 500 is not permitted")
	}
	if e.elevated != 0 {
		return Context{}, rejected("elevated token is not permitted")
	}
	if e.elevationType != 1 && e.elevationType != 3 {
		return Context{}, rejected("full or unknown elevation type is not permitted")
	}
	level, err := sidParts(e.integritySID)
	if err != nil || len(level) != 2 || level[0] != 16 || level[1] != 0x2000 {
		return Context{}, rejected("a verified medium integrity label is required")
	}
	if e.sessionID == 0 || e.sessionID == ^uint32(0) {
		return Context{}, rejected("nonzero ordinary user session is required")
	}
	interactive, admin := false, "none"
	seen := make(map[string]bool)
	for _, group := range e.groups {
		if _, err := sidParts(group.sid); err != nil || seen[group.sid] {
			return Context{}, rejected("malformed or repeated token group SID")
		}
		seen[group.sid] = true
		switch group.sid {
		case "S-1-5-6", "S-1-5-3":
			return Context{}, rejected("service/batch logon token is not permitted")
		case "S-1-5-4":
			interactive = group.attributes&0x4 != 0 && group.attributes&0x10 == 0
		case "S-1-5-32-544":
			if group.attributes&0x4 != 0 || group.attributes&0x10 == 0 {
				return Context{}, rejected("Administrators group is enabled or not proven deny-only")
			}
			admin = "deny_only"
		}
	}
	if !interactive {
		return Context{}, rejected("enabled interactive logon group is required")
	}
	if (e.elevationType == 3) != (admin == "deny_only") {
		return Context{}, rejected("elevation type and administrator filtering evidence disagree")
	}
	return Context{sid: e.sid, taskPath: `\B1UG-nb1-` + e.sid, sessionID: e.sessionID,
		integrityRID: 0x2000, elevationType: e.elevationType, adminMembership: admin, verified: true}, nil
}

// sidParts accepts only canonical decimal SID revision 1 with bounded authority
// and subauthorities. It returns authority followed by subauthority values.
func sidParts(value string) ([]uint64, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 4 || len(parts) > 18 || parts[0] != "S" || parts[1] != "1" {
		return nil, ErrIdentity
	}
	values := make([]uint64, 0, len(parts)-2)
	for i, part := range parts[2:] {
		bits := 32
		if i == 0 {
			bits = 48
		}
		n, err := strconv.ParseUint(part, 10, bits)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return nil, ErrIdentity
		}
		values = append(values, n)
	}
	return values, nil
}
