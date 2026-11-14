//go:build windows
// +build windows

package taskuser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const nativeResultPrefix = "NB1_TASKUSER_NATIVE_RESULT "

type nativeFacts struct {
	SID                string `json:"sid"`
	SessionID          uint32 `json:"session_id"`
	TokenIsElevated    uint32 `json:"token_is_elevated"`
	ElevationType      uint32 `json:"elevation_type"`
	IntegritySID       string `json:"integrity_sid"`
	InteractiveEnabled bool   `json:"interactive_enabled"`
	ServiceOrBatch     bool   `json:"service_or_batch"`
	AdminGroup         string `json:"admin_group"`
	DomainRID500       bool   `json:"domain_rid_500"`
}

type nativeContextSummary struct {
	Valid           bool   `json:"valid"`
	SID             string `json:"sid"`
	TaskPath        string `json:"task_path"`
	SessionID       uint32 `json:"session_id"`
	IntegrityRID    uint32 `json:"integrity_rid"`
	ElevationType   uint32 `json:"elevation_type"`
	AdminMembership string `json:"admin_membership"`
}

type nativeHandleObservation struct {
	WarmupCalls      int    `json:"warmup_calls"`
	Iterations       int    `json:"iterations"`
	Completed        int    `json:"completed"`
	BudgetMS         int    `json:"budget_ms"`
	SettleBudgetMS   int    `json:"settle_budget_ms"`
	AllowedGrowth    uint32 `json:"allowed_growth"`
	Baseline         uint32 `json:"baseline"`
	Final            uint32 `json:"final"`
	Peak             uint32 `json:"peak"`
	ElapsedMS        int64  `json:"elapsed_ms"`
	PersistentGrowth bool   `json:"persistent_growth"`
}

type nativeReport struct {
	SchemaVersion      int                      `json:"schema_version"`
	Status             string                   `json:"status"`
	Expected           string                   `json:"expected"`
	ExpectedSID        string                   `json:"expected_sid"`
	CollectionFields   int                      `json:"collection_fields"`
	CollectionComplete bool                     `json:"collection_complete"`
	CollectorClosed    bool                     `json:"collector_closed"`
	Facts              *nativeFacts             `json:"facts,omitempty"`
	Current            *nativeContextSummary    `json:"current,omitempty"`
	Handles            *nativeHandleObservation `json:"handle_observation,omitempty"`
	FailurePhase       string                   `json:"failure_phase,omitempty"`
}

// collectNativeForTest deliberately does NOT classify while collecting. All six
// queries and Close must succeed, including for an expected administrator reject.
func collectNativeForTest(report *nativeReport) (e tokenEvidence, err error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return e, fmt.Errorf("open current TOKEN_QUERY: %w", err)
	}
	native := nativeToken{token}
	defer func() {
		if closeErr := native.close(); closeErr != nil {
			if err == nil {
				err = fmt.Errorf("collector token close: %w", closeErr)
			} else {
				err = fmt.Errorf("%w; collector token close: %v", err, closeErr)
			}
		} else {
			report.CollectorClosed = true
		}
		if err == nil && report.CollectionFields == 6 {
			report.CollectionComplete = true
		}
	}()
	if e.sid, err = native.userSID(); err != nil {
		return e, fmt.Errorf("TokenUser: %w", err)
	}
	report.CollectionFields++
	if e.elevated, err = native.elevation(); err != nil {
		return e, fmt.Errorf("TokenElevation: %w", err)
	}
	report.CollectionFields++
	if e.elevationType, err = native.elevationType(); err != nil {
		return e, fmt.Errorf("TokenElevationType: %w", err)
	}
	report.CollectionFields++
	if e.integritySID, err = native.integritySID(); err != nil {
		return e, fmt.Errorf("TokenIntegrityLevel: %w", err)
	}
	report.CollectionFields++
	if e.groups, err = native.groups(); err != nil {
		return e, fmt.Errorf("TokenGroups: %w", err)
	}
	report.CollectionFields++
	if e.sessionID, err = native.sessionID(); err != nil {
		return e, fmt.Errorf("TokenSessionId: %w", err)
	}
	report.CollectionFields++
	return e, nil
}

func selectedNativeFacts(e tokenEvidence) nativeFacts {
	facts := nativeFacts{SID: e.sid, SessionID: e.sessionID, TokenIsElevated: e.elevated,
		ElevationType: e.elevationType, IntegritySID: e.integritySID, AdminGroup: "none"}
	parts, err := sidParts(e.sid)
	facts.DomainRID500 = err == nil && len(parts) == 6 && parts[0] == 5 && parts[1] == 21 && parts[5] == 500
	for _, group := range e.groups {
		switch group.sid {
		case "S-1-5-4":
			facts.InteractiveEnabled = group.attributes&0x4 != 0 && group.attributes&0x10 == 0
		case "S-1-5-3", "S-1-5-6":
			facts.ServiceOrBatch = true
		case "S-1-5-32-544":
			if group.attributes&0x4 != 0 {
				facts.AdminGroup = "enabled"
			} else if group.attributes&0x10 != 0 {
				facts.AdminGroup = "deny_only"
			} else {
				facts.AdminGroup = "unverified"
			}
		}
	}
	return facts
}

func summarizeNativeContext(c Context) nativeContextSummary {
	return nativeContextSummary{c.Valid(), c.SID(), c.TaskPath(), c.SessionID(), c.IntegrityRID(), c.ElevationType(), c.AdminMembership()}
}

func nativeHandleCounter() (func() (uint32, error), error) {
	process, err := syscall.GetCurrentProcess() // pseudo handle; never Close or duplicate
	if err != nil {
		return nil, err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	if err := proc.Find(); err != nil {
		return nil, err
	}
	return func() (uint32, error) {
		var count uint32
		ok, _, callErr := proc.Call(uintptr(process), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return 0, fmt.Errorf("GetProcessHandleCount: %w", callErr)
		}
		return count, nil
	}, nil
}

// TestNativeCurrentOptIn never runs native queries without the explicit opt-in.
// The expected SID must come from the host harness's independently known user.
func TestNativeCurrentOptIn(t *testing.T) {
	if os.Getenv("NB1_TASKUSER_NATIVE") != "1" {
		t.Skip("native token fixture requires explicit NB1_TASKUSER_NATIVE=1")
	}
	report := nativeReport{SchemaVersion: 1, Status: "FAIL", Expected: os.Getenv("NB1_TASKUSER_EXPECT"),
		ExpectedSID: os.Getenv("NB1_TASKUSER_EXPECT_SID"), FailurePhase: "configuration"}
	defer func() {
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Errorf("encode selected native token report: %v", err)
			return
		}
		fmt.Println(nativeResultPrefix + string(encoded))
	}()
	if report.Expected != "ordinary" && report.Expected != "administrator-rejected" {
		t.Fatal("NB1_TASKUSER_EXPECT must explicitly select ordinary or administrator-rejected")
	}
	if _, err := sidParts(report.ExpectedSID); err != nil {
		t.Fatal("NB1_TASKUSER_EXPECT_SID must be an explicit canonical SID")
	}
	report.FailurePhase = "collect-six-fields"
	evidence, err := collectNativeForTest(&report)
	if err != nil || !report.CollectionComplete || !report.CollectorClosed {
		t.Fatalf("native collection must entirely succeed before any rejection assertion: %v", err)
	}
	facts := selectedNativeFacts(evidence)
	report.Facts = &facts
	if facts.SID != report.ExpectedSID {
		t.Fatal("collected current TokenUser SID differs from the independently expected SID")
	}
	report.FailurePhase = "expected-identity-precondition"
	want, classificationErr := classify(evidence)
	if report.Expected == "ordinary" {
		consistentType := (facts.ElevationType == 1 && facts.AdminGroup == "none") ||
			(facts.ElevationType == 3 && facts.AdminGroup == "deny_only")
		if facts.TokenIsElevated != 0 || facts.IntegritySID != "S-1-16-8192" || !consistentType ||
			!facts.InteractiveEnabled || facts.ServiceOrBatch || facts.SessionID == 0 || facts.DomainRID500 || classificationErr != nil || !want.Valid() {
			t.Fatal("ordinary expectation requires consistent medium, non-elevated interactive user evidence")
		}
	} else {
		if !facts.DomainRID500 && facts.TokenIsElevated == 0 {
			t.Fatal("administrator-rejected expectation requires actual RID500 or elevated scalar proof")
		}
		if !errors.Is(classificationErr, ErrIdentity) || want != (Context{}) {
			t.Fatal("collected administrator identity was not rejected by classification")
		}
	}
	checkCurrent := func() Context {
		current, currentErr := Current()
		if report.Expected == "ordinary" {
			if currentErr != nil || !current.Valid() || current != want || current.SID() != report.ExpectedSID ||
				current.TaskPath() != `\B1UG-nb1-`+report.ExpectedSID {
				t.Fatalf("Current did not return the exact collected ordinary context: %v", currentErr)
			}
		} else {
			// ErrIdentity alone is insufficient: query/Close failures use that
			// sentinel too. Match the independently collected classification
			// error so a native query/Close failure cannot count as rejection.
			if current != (Context{}) || !errors.Is(currentErr, ErrIdentity) || currentErr.Error() != classificationErr.Error() {
				t.Fatalf("Current rejection must match classification, not a query/Close failure: %v", currentErr)
			}
		}
		return current
	}
	report.FailurePhase = "current"
	current := checkCurrent()
	currentSummary := summarizeNativeContext(current)
	report.Current = &currentSummary
	report.FailurePhase = "bounded-handle-observation"
	count, err := nativeHandleCounter()
	if err != nil {
		t.Fatalf("current-process handle counter: %v", err)
	}
	observation := nativeHandleObservation{WarmupCalls: 8, Iterations: 32, BudgetMS: 5000, SettleBudgetMS: 250, AllowedGrowth: 2}
	report.Handles = &observation
	for i := 0; i < observation.WarmupCalls; i++ {
		checkCurrent()
	}
	observation.Baseline, err = count()
	if err != nil {
		t.Fatal(err)
	}
	observation.Peak = observation.Baseline
	started := time.Now()
	for i := 0; i < observation.Iterations; i++ {
		if time.Since(started) >= time.Duration(observation.BudgetMS)*time.Millisecond {
			t.Fatal("bounded native Current observation exceeded its time budget")
		}
		checkCurrent()
		observation.Completed++
		observation.ElapsedMS = time.Since(started).Milliseconds()
		if observation.ElapsedMS >= int64(observation.BudgetMS) {
			t.Fatal("native Current returned after the observation budget; no cancellation is inferred")
		}
		observed, err := count()
		if err != nil {
			t.Fatal(err)
		}
		if observed > observation.Peak {
			observation.Peak = observed
		}
	}
	deadline := time.Now().Add(time.Duration(observation.SettleBudgetMS) * time.Millisecond)
	for {
		observation.Final, err = count()
		if err != nil {
			t.Fatal(err)
		}
		if observation.Final > observation.Peak {
			observation.Peak = observation.Final
		}
		observation.PersistentGrowth = uint64(observation.Final) > uint64(observation.Baseline)+uint64(observation.AllowedGrowth)
		if !observation.PersistentGrowth || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	observation.ElapsedMS = time.Since(started).Milliseconds()
	if observation.PersistentGrowth {
		t.Fatal("current-process handle growth exceeded the bounded observation tolerance")
	}
	report.Status, report.FailurePhase = "PASS", ""
}
