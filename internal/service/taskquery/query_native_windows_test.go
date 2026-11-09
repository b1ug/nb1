//go:build windows
// +build windows

package taskquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/service/taskuser"
)

const nativeQueryResultPrefix = "NB1_TASKQUERY_NATIVE_RESULT "

// Keep the native fixture output limited to the verified current identity,
// fixed query Status schema and enumerated test phases/errors. In particular,
// never include raw errors, bridge stdout/stderr, XML, actions or environment.
type nativeQueryReport struct {
	SchemaVersion  int     `json:"schema_version"`
	Status         string  `json:"status"`
	Phase          string  `json:"phase"`
	CurrentSID     string  `json:"current_sid,omitempty"`
	TaskPath       string  `json:"task_path,omitempty"`
	ErrorKind      string  `json:"error_kind,omitempty"`
	SelectedStatus *Status `json:"selected_status,omitempty"`
}

// TestNativeQueryOptIn queries only this process's verified fixed task path.
// It expects an independently prepared ordinary user whose task is missing.
// It never prepares that user, registers a task, or changes production absence
// semantics. Without explicit opt-in, even identity and trust calls are skipped.
func TestNativeQueryOptIn(t *testing.T) {
	if os.Getenv("NB1_TASKQUERY_NATIVE") != "1" {
		t.Skip("native query requires explicit NB1_TASKQUERY_NATIVE=1 and an independently expected current SID")
	}
	report := nativeQueryReport{SchemaVersion: 1, Status: "FAIL", Phase: "configuration"}
	defer func() {
		encoded, err := json.Marshal(report)
		if err != nil {
			// Do not fall back to formatting the raw error or any native output.
			fmt.Println(nativeQueryResultPrefix + `{"schema_version":1,"status":"FAIL","phase":"report","error_kind":"encode_report"}`)
			t.Error("selected native query report could not be encoded")
			return
		}
		if len(encoded) > 2048 {
			fmt.Println(nativeQueryResultPrefix + `{"schema_version":1,"status":"FAIL","phase":"report","error_kind":"report_size"}`)
			t.Error("selected native query report exceeded its fixed output budget")
			return
		}
		if _, err := fmt.Println(nativeQueryResultPrefix + string(encoded)); err != nil {
			t.Error("selected native query report could not be written")
		}
	}()

	expectedSID := os.Getenv("NB1_TASKQUERY_EXPECT_SID")
	if expectedSID == "" {
		report.ErrorKind = "expected_sid_required"
		t.Fatal("native query requires an independently known current SID")
	}
	report.Phase = "identity"
	owner, err := taskuser.Current()
	if err != nil || !owner.Valid() {
		report.ErrorKind = "current_identity_rejected"
		t.Fatal("production Current must verify the ordinary current user")
	}
	// Only verified identity data is retained. The arbitrary expected-SID input
	// is compared exactly and is never copied into the report or bridge code.
	report.CurrentSID, report.TaskPath = owner.SID(), owner.TaskPath()
	if owner.SID() != expectedSID || owner.TaskPath() != `\B1UG-nb1-`+owner.SID() {
		report.ErrorKind = "expected_identity_mismatch"
		t.Fatal("verified current identity must exactly match the expected SID and fixed task path")
	}

	report.Phase = "constructor"
	reader, err := New()
	if err != nil || reader == nil {
		report.ErrorKind = nativeQueryErrorKind(err)
		if err == nil {
			report.ErrorKind = "constructor_nil"
		}
		t.Fatal("production query constructor must verify identity and system bridge trust")
	}
	if !reader.owner.valid || reader.owner.sid != owner.SID() ||
		reader.owner.taskPath != owner.TaskPath() || reader.owner.session != owner.SessionID() {
		report.ErrorKind = "constructor_identity_mismatch"
		t.Fatal("production reader must remain bound to the same independently expected current identity")
	}

	report.Phase = "query"
	// Leave the product's fixed query budget in control. This fixture performs
	// one cold-profile Query, with no diagnostic prewarm or retry.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	selected, err := reader.Query(ctx)
	report.SelectedStatus = &selected
	if !errors.Is(err, ErrAbsenceUnverified) {
		report.ErrorKind = nativeQueryErrorKind(err)
		if err == nil {
			report.ErrorKind = "unexpected_query_success"
		}
		t.Fatal("fresh-user query must return the exact unverified-missing contract; other query results are failures")
	}

	report.Phase = "missing_contract"
	if selected.SchemaVersion != 1 || selected.CheckedAt.IsZero() ||
		selected.TaskPath != owner.TaskPath() || selected.Presence != Unknown ||
		selected.Ownership != "not_checked" || selected.Task != nil || selected.Failure == nil ||
		selected.Failure.Kind != "not_found_unverified" || selected.Failure.Stage != "get_task" ||
		selected.Failure.HRESULT != "0x80070002" {
		report.ErrorKind = "missing_contract_mismatch"
		t.Fatal("observed missing contract must preserve unknown/not_checked and the exact get_task HRESULT")
	}
	report.Status, report.Phase = "PASS", "missing_contract_observed"
	// PASS is evidence about this missing-query observation, not an Absent
	// product result, registration ownership, management, daemon or USB state.
}

func nativeQueryErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, ErrIdentity):
		return "identity"
	case errors.Is(err, ErrTrust):
		return "trust"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrTransport):
		return "transport"
	case errors.Is(err, ErrProtocol):
		return "protocol"
	case errors.Is(err, ErrNative):
		return "native"
	case errors.Is(err, ErrAbsenceUnverified):
		return "absence_unverified"
	default:
		return "unclassified"
	}
}
