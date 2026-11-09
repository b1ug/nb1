package taskuser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ordinary() tokenEvidence {
	return tokenEvidence{sid: "S-1-5-21-1-2-3-1001", integritySID: "S-1-16-8192", elevationType: 1,
		sessionID: 1, groups: []tokenGroup{{"S-1-5-4", 7}, {"S-1-5-32-545", 7}}}
}

func TestAllowedIdentityAndFixedTaskPath(t *testing.T) {
	for _, kind := range []string{"ordinary", "filtered", "entra", "entra-filtered", "entra-final-500", "entra-whoami-sample"} {
		t.Run(kind, func(t *testing.T) {
			e := ordinary()
			switch kind {
			case "filtered":
				e.elevationType = 3
				e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 0x10})
			case "entra":
				e.sid = "S-1-12-1-111-222-333-444"
			case "entra-filtered":
				e.sid = "S-1-12-1-111-222-333-444"
				e.elevationType = 3
				e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 0x10})
			case "entra-final-500":
				e.sid = "S-1-12-1-111-222-333-500"
			case "entra-whoami-sample":
				e.sid = "S-1-12-3687709483-1112055202-2756941246-4106396469"
			}
			c, err := classify(e)
			if err != nil || !c.Valid() || c.SID() != e.sid || c.TaskPath() != `\B1UG-nb1-`+e.sid || c.SessionID() != 1 || c.IntegrityRID() != 8192 || c.ElevationType() != e.elevationType {
				t.Fatalf("context=%+v err=%v", c, err)
			}
			want := "none"
			if kind == "filtered" || kind == "entra-filtered" {
				want = "deny_only"
			}
			if c.AdminMembership() != want {
				t.Fatalf("administrator evidence = %q", c.AdminMembership())
			}
		})
	}
}

func TestRejectedAndUnknownIdentityAlwaysReturnsZero(t *testing.T) {
	cases := map[string]func(*tokenEvidence){
		"missing user":                        func(e *tokenEvidence) { e.sid = "" },
		"SYSTEM":                              func(e *tokenEvidence) { e.sid = "S-1-5-18" },
		"LocalService":                        func(e *tokenEvidence) { e.sid = "S-1-5-19" },
		"NetworkService":                      func(e *tokenEvidence) { e.sid = "S-1-5-20" },
		"service SID":                         func(e *tokenEvidence) { e.sid = "S-1-5-80-1-2-3-4-5" },
		"RID500":                              func(e *tokenEvidence) { e.sid = "S-1-5-21-1-2-3-500" },
		"unknown family":                      func(e *tokenEvidence) { e.sid = "S-1-99-1-2-3-4" },
		"path injection":                      func(e *tokenEvidence) { e.sid += `\outside` },
		"noncanonical decimal":                func(e *tokenEvidence) { e.sid = "S-1-5-21-01-2-3-1001" },
		"overflow RID":                        func(e *tokenEvidence) { e.sid = "S-1-5-21-1-2-3-4294967296" },
		"elevated":                            func(e *tokenEvidence) { e.elevated = 1 },
		"nonzero elevation DWORD":             func(e *tokenEvidence) { e.elevated = 17 },
		"full type despite zero elevation":    func(e *tokenEvidence) { e.elevationType = 2 },
		"unknown type":                        func(e *tokenEvidence) { e.elevationType = 0 },
		"high integrity":                      func(e *tokenEvidence) { e.integritySID = "S-1-16-12288" },
		"system integrity":                    func(e *tokenEvidence) { e.integritySID = "S-1-16-16384" },
		"low outside supported contract":      func(e *tokenEvidence) { e.integritySID = "S-1-16-4096" },
		"medium plus":                         func(e *tokenEvidence) { e.integritySID = "S-1-16-8448" },
		"unknown label":                       func(e *tokenEvidence) { e.integritySID = "S-1-5-8192" },
		"missing label":                       func(e *tokenEvidence) { e.integritySID = "" },
		"session zero":                        func(e *tokenEvidence) { e.sessionID = 0 },
		"session sentinel":                    func(e *tokenEvidence) { e.sessionID = ^uint32(0) },
		"service logon":                       func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"S-1-5-6", 7}) },
		"batch logon":                         func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"S-1-5-3", 7}) },
		"enabled admin":                       func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 7}) },
		"admin neither enabled nor deny-only": func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 0}) },
		"admin enabled and deny-only": func(e *tokenEvidence) {
			e.elevationType = 3
			e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 0x14})
		},
		"default with deny-only admin":       func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"S-1-5-32-544", 0x10}) },
		"limited without filtering evidence": func(e *tokenEvidence) { e.elevationType = 3 },
		"no groups":                          func(e *tokenEvidence) { e.groups = nil },
		"interactive disabled":               func(e *tokenEvidence) { e.groups[0].attributes = 0 },
		"interactive deny-only":              func(e *tokenEvidence) { e.groups[0].attributes = 0x10 },
		"invalid group":                      func(e *tokenEvidence) { e.groups = append(e.groups, tokenGroup{"bad", 7}) },
		"duplicate group":                    func(e *tokenEvidence) { e.groups = append(e.groups, e.groups[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := ordinary()
			mutate(&e)
			c, err := classify(e)
			if !errors.Is(err, ErrIdentity) || c != (Context{}) || c.Valid() {
				t.Fatalf("context=%+v err=%v", c, err)
			}
		})
	}
	if (Context{}).Valid() {
		t.Fatal("zero Context is verified")
	}
}

type fakeToken struct {
	e                  tokenEvidence
	failAt, closeError string
	calls              []string
}

func (f *fakeToken) step(name string) error {
	f.calls = append(f.calls, name)
	if f.failAt == name {
		return errors.New("injected query failure")
	}
	return nil
}
func (f *fakeToken) userSID() (string, error)       { return f.e.sid, f.step("user") }
func (f *fakeToken) elevation() (uint32, error)     { return f.e.elevated, f.step("elevation") }
func (f *fakeToken) elevationType() (uint32, error) { return f.e.elevationType, f.step("type") }
func (f *fakeToken) integritySID() (string, error)  { return f.e.integritySID, f.step("integrity") }
func (f *fakeToken) groups() ([]tokenGroup, error)  { return f.e.groups, f.step("groups") }
func (f *fakeToken) sessionID() (uint32, error)     { return f.e.sessionID, f.step("session") }
func (f *fakeToken) close() error {
	f.calls = append(f.calls, "close")
	if f.closeError != "" {
		return errors.New(f.closeError)
	}
	return nil
}

func TestInjectedReaderAlwaysClosesOwnedToken(t *testing.T) {
	for _, fail := range []string{"", "user", "elevation", "type", "integrity", "groups", "session", "classification", "close", "query_and_close", "open_partial"} {
		t.Run(fail, func(t *testing.T) {
			f := &fakeToken{e: ordinary(), failAt: fail}
			if fail == "classification" {
				f.e.elevated = 1
			}
			if fail == "close" || fail == "query_and_close" {
				f.closeError = "injected close failure"
			}
			if fail == "query_and_close" {
				f.failAt = "user"
			}
			c, err := readContext(func() (tokenReader, error) {
				if fail == "open_partial" {
					return f, errors.New("partial open")
				}
				return f, nil
			})
			if fail == "" {
				if err != nil || !c.Valid() {
					t.Fatalf("context=%+v err=%v", c, err)
				}
			} else if !errors.Is(err, ErrIdentity) || c != (Context{}) {
				t.Fatalf("context=%+v err=%v", c, err)
			}
			count := 0
			for _, name := range f.calls {
				if name == "close" {
					count++
				}
			}
			if count != 1 || f.calls[len(f.calls)-1] != "close" {
				t.Fatalf("calls=%v", f.calls)
			}
			if fail == "query_and_close" && (!strings.Contains(err.Error(), "TokenUser") || !strings.Contains(err.Error(), "close")) {
				t.Fatal(err)
			}
			if fail == "open_partial" && !reflect.DeepEqual(f.calls, []string{"close"}) {
				t.Fatal(f.calls)
			}
		})
	}
}

func TestOpenFailureOrMissingReaderCannotYieldContext(t *testing.T) {
	for _, failure := range []error{nil, errors.New("open failed")} {
		c, err := readContext(func() (tokenReader, error) { return nil, failure })
		if !errors.Is(err, ErrIdentity) || c != (Context{}) {
			t.Fatalf("context=%+v err=%v", c, err)
		}
	}
}
