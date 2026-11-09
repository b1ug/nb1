//go:build windows
// +build windows

package taskquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/service/taskuser"
)

const nativeTimingResultPrefix = "NB1_TASKQUERY_TIMING_RESULT "

type nativeTimingSample struct {
	Label               string `json:"label"`
	IdentityMS          int64  `json:"identity_ms"`
	TrustCheckMS        int64  `json:"trust_check_ms"`
	TrustChecks         int    `json:"trust_checks"`
	BridgeMS            int64  `json:"bridge_ms"`
	ParseMS             int64  `json:"parse_ms"`
	BridgeInvoked       bool   `json:"bridge_invoked"`
	RunReturned         bool   `json:"run_returned"`
	PrivatePSExitProven bool   `json:"private_ps_exit_proven"`
	ResponseReceived    bool   `json:"response_received"`
	ErrorKind           string `json:"error_kind"`
	SelectedStatus      Status `json:"selected_status"`
}

type nativeTimingReport struct {
	SchemaVersion          int                  `json:"schema_version"`
	Mode                   string               `json:"mode"`
	Status                 string               `json:"status"`
	Phase                  string               `json:"phase"`
	CurrentSID             string               `json:"current_sid"`
	TaskPath               string               `json:"task_path"`
	GOARCH                 string               `json:"goarch"`
	GoVersion              string               `json:"go_version"`
	SourceRevision         string               `json:"source_revision"`
	ProductionQueryInvoked bool                 `json:"production_query_invoked"`
	ProductionAcceptance   bool                 `json:"production_acceptance"`
	IdentityMS             int64                `json:"identity_ms"`
	ConstructorMS          int64                `json:"constructor_ms"`
	Samples                []nativeTimingSample `json:"samples"`
	ErrorKind              string               `json:"error_kind,omitempty"`
}

func nativeTimingSourceRevision() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			if setting.Key != "vcs.revision" || len(setting.Value) != 40 {
				continue
			}
			valid := true
			for _, c := range setting.Value {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					valid = false
				}
			}
			if valid {
				return setting.Value
			}
		}
	}
	// Test binaries need not embed VCS metadata. The external fixture must bind
	// its independently recorded source commit and the exact test binary hash.
	return "not_embedded"
}

// TestNativeQueryTiming is a separate diagnostic mode. It must be the first
// PowerShell query in the independently prepared ordinary profile; it does not
// run Query or turn a warmed observation into production three-second acceptance.
func TestNativeQueryTiming(t *testing.T) {
	if os.Getenv("NB1_TASKQUERY_TIMING") != "1" {
		t.Skip("native timing requires explicit NB1_TASKQUERY_TIMING=1 and an independently expected current SID")
	}
	report := nativeTimingReport{SchemaVersion: 1, Mode: "DIAGNOSTIC_ONLY", Status: "DIAGNOSTIC_FAILED",
		Phase: "configuration", GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		SourceRevision: nativeTimingSourceRevision(), Samples: make([]nativeTimingSample, 0, 2)}
	defer func() {
		encoded, err := json.Marshal(report)
		if err != nil {
			fmt.Println(nativeTimingResultPrefix + `{"schema_version":1,"mode":"DIAGNOSTIC_ONLY","status":"DIAGNOSTIC_FAILED","phase":"report","error_kind":"encode_report"}`)
			t.Error("selected timing report could not be encoded")
			return
		}
		if len(encoded) > 8192 {
			fmt.Println(nativeTimingResultPrefix + `{"schema_version":1,"mode":"DIAGNOSTIC_ONLY","status":"DIAGNOSTIC_FAILED","phase":"report","error_kind":"report_size"}`)
			t.Error("selected timing report exceeded its fixed output budget")
			return
		}
		if _, err := fmt.Println(nativeTimingResultPrefix + string(encoded)); err != nil {
			t.Error("selected timing report could not be written")
		}
	}()
	expectedSID := os.Getenv("NB1_TASKQUERY_EXPECT_SID")
	if expectedSID == "" {
		report.ErrorKind = "expected_sid_required"
		t.Fatal("native timing requires an independently expected current SID")
	}

	report.Phase = "identity"
	started := time.Now()
	owner, err := taskuser.Current()
	report.IdentityMS = time.Since(started).Milliseconds()
	if err != nil || !owner.Valid() {
		report.ErrorKind = "current_identity_rejected"
		t.Fatal("production Current must verify the ordinary current user")
	}
	report.CurrentSID, report.TaskPath = owner.SID(), owner.TaskPath()
	if owner.SID() != expectedSID || owner.TaskPath() != `\B1UG-nb1-`+owner.SID() {
		report.ErrorKind = "expected_identity_mismatch"
		t.Fatal("timing identity must match the expected SID and fixed task path")
	}

	report.Phase = "constructor"
	started = time.Now()
	reader, err := New()
	report.ConstructorMS = time.Since(started).Milliseconds()
	if err != nil || reader == nil {
		report.ErrorKind = nativeQueryErrorKind(err)
		if err == nil {
			report.ErrorKind = "constructor_nil"
		}
		t.Fatal("production query constructor must establish identity and system bridge trust")
	}
	if !reader.owner.valid || reader.owner.sid != owner.SID() || reader.owner.taskPath != owner.TaskPath() || reader.owner.session != owner.SessionID() {
		report.ErrorKind = "constructor_identity_mismatch"
		t.Fatal("timing reader identity changed during construction")
	}
	report.Phase = "runner"
	original, ok := reader.bridge.(nativeRunner)
	if !ok || original.checkTrust == nil {
		report.ErrorKind = "runner_invalid"
		t.Fatal("timing must use the actual production native runner")
	}

	allMissing := true
	for _, label := range []string{"first", "second"} {
		report.Phase = "sample_identity"
		sample := nativeTimingSample{Label: label, SelectedStatus: Status{SchemaVersion: 1,
			CheckedAt: reader.now().UTC(), TaskPath: owner.TaskPath(), Presence: Unknown, Ownership: "not_checked"}}
		started = time.Now()
		fresh, currentErr := reader.current()
		sample.IdentityMS = time.Since(started).Milliseconds()
		if currentErr != nil || !fresh.valid || fresh != reader.owner {
			sample.ErrorKind = "identity"
			sample.SelectedStatus.Failure = &Failure{Kind: "identity", Stage: "parent"}
			report.Samples = append(report.Samples, sample)
			report.ErrorKind = "sample_identity_rejected"
			t.Fatal("timing samples require the same current SID, path and session")
		}
		input, marshalErr := json.Marshal(request{SchemaVersion: 1, SID: fresh.sid, TaskPath: fresh.taskPath})
		if marshalErr != nil { // fixed native identity strings only
			report.ErrorKind = "runner_invalid"
			t.Fatal("fixed timing request could not be encoded")
		}
		instrumented := original // only wrap the existing trust call; script/argv/env stay unchanged
		instrumented.checkTrust = func() error {
			checkStarted := time.Now()
			sample.TrustChecks++
			defer func() { sample.TrustCheckMS += time.Since(checkStarted).Milliseconds() }()
			return original.checkTrust()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		report.Phase = "sample_run"
		sample.BridgeInvoked = true
		started = time.Now()
		result, runErr := instrumented.run(ctx, input)
		sample.BridgeMS = time.Since(started).Milliseconds()
		sample.RunReturned, sample.PrivatePSExitProven = true, true
		// nativeRunner.run either returns before spawning, or execute's Run has
		// returned after Wait. This proves no pending child of these fixed calls,
		// not that a process was necessarily started or other descendants exited.
		sample.ResponseReceived = len(result.output) != 0
		report.Phase = "sample_parse"
		started = time.Now()
		var parseErr error
		switch {
		case ctx.Err() != nil:
			parseErr = ctx.Err()
			sample.SelectedStatus.Failure = &Failure{Kind: "cancelled", Stage: "bridge"}
		case errors.Is(runErr, ErrTrust):
			parseErr = ErrTrust
			sample.SelectedStatus.Failure = &Failure{Kind: "trust", Stage: "bridge"}
		case runErr != nil || result.exitCode != 0 || result.truncated || len(result.output) > maxOutput:
			parseErr = ErrTransport
			sample.SelectedStatus.Failure = &Failure{Kind: "transport", Stage: "bridge"}
		default:
			sample.SelectedStatus, parseErr = parseResponse(result.output, fresh, sample.SelectedStatus)
		}
		sample.ParseMS = time.Since(started).Milliseconds()
		cancel()
		sample.ErrorKind = nativeQueryErrorKind(parseErr)
		report.Samples = append(report.Samples, sample)
		status := sample.SelectedStatus
		if !errors.Is(parseErr, ErrAbsenceUnverified) || sample.TrustChecks != 1 || !sample.ResponseReceived ||
			status.SchemaVersion != 1 || status.CheckedAt.IsZero() || status.TaskPath != owner.TaskPath() ||
			status.Presence != Unknown || status.Ownership != "not_checked" || status.Task != nil || status.Failure == nil ||
			status.Failure.Kind != "not_found_unverified" || status.Failure.Stage != "get_task" || status.Failure.HRESULT != "0x80070002" {
			allMissing = false
		}
	}
	report.Phase = "complete"
	if !allMissing {
		report.ErrorKind = "sample_contract_failed"
		t.Fatal("timing observations did not both return the exact unverified-missing contract")
	}
	report.Status = "DIAGNOSTIC_ONLY"
}
