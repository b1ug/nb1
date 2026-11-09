package taskquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fixtureSID = "S-1-5-21-11-22-33-1001"

var fixturePath = `\B1UG-nb1-` + fixtureSID

func TestMain(m *testing.M) {
	helper := (len(os.Args) == 2 && os.Args[1] == "--taskquery-private-helper") ||
		(len(os.Args) == 10 && os.Args[1] == "-NoLogo")
	mode := ""
	if helper {
		mode = os.Getenv("NB1_TASKQUERY_HELPER")
	}
	switch mode {
	case "spec":
		var input interface{}
		if json.NewDecoder(os.Stdin).Decode(&input) != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"argv": os.Args[1:], "input": input,
			"secret_inherited": os.Getenv("NB1_TASKQUERY_PARENT_SECRET"), "path": os.Getenv("PATH")})
		os.Exit(0)
	case "output":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maxOutput*16))
		time.Sleep(time.Second)
		os.Exit(0)
	case "diagnostic":
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("private-command-environment", 4096))
		time.Sleep(time.Second)
		os.Exit(0)
	case "wait":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeRunner struct {
	calls  int
	input  []byte
	result bridgeResult
	err    error
	runFn  func(context.Context)
}

func (r *fakeRunner) run(ctx context.Context, input []byte) (bridgeResult, error) {
	r.calls++
	r.input = append([]byte(nil), input...)
	if r.runFn != nil {
		r.runFn(ctx)
	}
	return r.result, r.err
}

func present(sid, path string, state uint32) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":1,"outcome":"present","caller_sid":%q,"task_path":%q,"stage":"complete","enabled":false,"scheduler_state":%d,"last_task_result":-2147024891}`, sid, path, state))
}

func fixture(runner runner) *Reader {
	owner := identity{valid: true, sid: fixtureSID, taskPath: fixturePath, session: 1}
	return &Reader{owner: owner, current: func() (identity, error) { return owner, nil }, bridge: runner,
		now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.FixedZone("fixture", 8*3600)) }}
}

func TestPresentKeepsSelectedFieldsAndNoOwnership(t *testing.T) {
	for state := uint32(0); state <= 4; state++ {
		bridge := &fakeRunner{result: bridgeResult{output: present(fixtureSID, fixturePath, state)}}
		status, err := fixture(bridge).Query(context.Background())
		if err != nil || status.Presence != Present || status.Ownership != "not_checked" || status.Task == nil ||
			status.Task.Enabled || status.Task.SchedulerState != state || status.Task.LastTaskResult != -2147024891 ||
			status.CheckedAt.Location() != time.UTC || bridge.calls != 1 {
			t.Fatalf("state%d: %#v, %v", state, status, err)
		}
		var request request
		if json.Unmarshal(bridge.input, &request) != nil || request.SID != fixtureSID || request.TaskPath != fixturePath || request.SchemaVersion != 1 {
			t.Fatal("fixed identity request")
		}
	}
}

func TestEntraContextIsNotNarrowedByQuery(t *testing.T) {
	sid := "S-1-12-1-11-22-33-44"
	path := `\B1UG-nb1-` + sid
	bridge := &fakeRunner{result: bridgeResult{output: present(sid, path, 3)}}
	reader := fixture(bridge)
	owner := identity{valid: true, sid: sid, taskPath: path, session: 9}
	reader.owner = owner
	reader.current = func() (identity, error) { return owner, nil }
	status, err := reader.Query(context.Background())
	if err != nil || status.Presence != Present || bridge.calls != 1 {
		t.Fatal(status, err)
	}
}

func TestIdentityAndCancellationNeverInvokeBridge(t *testing.T) {
	for _, name := range []string{"invalid", "sid", "path", "session", "error", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			bridge := &fakeRunner{}
			reader := fixture(bridge)
			fresh := reader.owner
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "invalid":
				fresh.valid = false
			case "sid":
				fresh.sid = "another"
			case "path":
				fresh.taskPath = `\other`
			case "session":
				fresh.session++
			case "cancelled":
				cancel()
			}
			reader.current = func() (identity, error) {
				if name == "error" {
					return identity{}, errors.New("private identity details")
				}
				return fresh, nil
			}
			status, err := reader.Query(ctx)
			if err == nil || status.Presence != Unknown || status.Task != nil || bridge.calls != 0 {
				t.Fatal(status, err, bridge.calls)
			}
		})
	}
	var zero Reader
	if _, err := zero.Query(context.Background()); err != ErrIdentity {
		t.Fatal(err)
	}
	var nilReader *Reader
	if _, err := nilReader.Query(context.Background()); err != ErrIdentity {
		t.Fatal(err)
	}
}

func errorResponse(stage, hr string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":1,"outcome":"error","caller_sid":%q,"task_path":%q,"stage":%q,"hresult":%q}`, fixtureSID, fixturePath, stage, hr))
}

func TestNativeFailureNeverBecomesAbsence(t *testing.T) {
	for _, stage := range []string{"activate", "connect", "folder", "get_task", "properties", "release"} {
		for _, hr := range []string{"0x80070002", "0x80070005", "0x80041315", "0x80004005"} {
			bridge := &fakeRunner{result: bridgeResult{output: errorResponse(stage, hr)}}
			status, err := fixture(bridge).Query(context.Background())
			if err == nil || status.Presence != Unknown || status.Ownership != "not_checked" || status.Task != nil || status.Failure.HRESULT != hr || status.Failure.Stage != stage {
				t.Fatal(status, err)
			}
			if stage == "get_task" && hr == "0x80070002" && err != ErrAbsenceUnverified {
				t.Fatal("unverified missing must stay distinct", err)
			}
		}
	}
}

func TestProtocolRejectsAmbiguousOrSensitiveOutput(t *testing.T) {
	good := string(present(fixtureSID, fixturePath, 3))
	bad := []string{
		"", "[]", good + good, good + " trailing",
		strings.Replace(good, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		strings.Replace(good, `"enabled":false`, `"Enabled":false`, 1),
		strings.Replace(good, `"enabled":false`, `"enabled":null`, 1),
		strings.Replace(good, `"enabled":false,`, "", 1),
		strings.Replace(good, `"scheduler_state":3`, `"scheduler_state":5`, 1),
		strings.Replace(good, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(good, fixtureSID, "other", 1),
		strings.Replace(good, `"present"`, `"absent"`, 1),
		strings.Replace(good, `"complete"`, `"get_task"`, 1),
		strings.Replace(good, `"last_task_result":-2147024891`, `"last_task_result":2147483648`, 1),
		strings.Replace(good, `"enabled":false`, `"xml":"private command environment","enabled":false`, 1),
		string(errorResponse("unknown", "0x80070005")),
		string(errorResponse("get_task", "0x8007000f")),
	}
	for index, output := range bad {
		status, err := fixture(&fakeRunner{result: bridgeResult{output: []byte(output)}}).Query(context.Background())
		if err != ErrProtocol || status.Presence != Unknown || status.Task != nil {
			t.Fatalf("case%d: %#v %v", index, status, err)
		}
		encoded, _ := json.Marshal(status)
		if strings.Contains(string(encoded), "private command") || strings.Contains(err.Error(), "private command") {
			t.Fatal("raw native data leaked")
		}
	}
}

func TestTransportAndSharedBudget(t *testing.T) {
	for _, bridge := range []*fakeRunner{
		{err: errors.New("private native diagnostic")},
		{result: bridgeResult{exitCode: 1, output: present(fixtureSID, fixturePath, 3)}},
		{result: bridgeResult{truncated: true, output: present(fixtureSID, fixturePath, 3)}},
		{result: bridgeResult{output: []byte(strings.Repeat("x", maxOutput+1))}},
		{err: ErrTrust},
	} {
		status, err := fixture(bridge).Query(context.Background())
		if err == nil || status.Presence != Unknown || status.Task != nil || bridge.calls != 1 {
			t.Fatal(status, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("diagnostic leaked")
		}
	}
	bridge := &fakeRunner{runFn: func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > queryBudget {
			t.Error("unbounded query")
		}
	}}
	_, _ = fixture(bridge).Query(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	bridge = &fakeRunner{runFn: func(ctx context.Context) { <-ctx.Done() }}
	status, err := fixture(bridge).Query(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || status.Presence != Unknown || bridge.calls != 1 {
		t.Fatal(status, err)
	}
}

func TestNativeRunnerUsesFixedCodeAndCleanEnvironment(t *testing.T) {
	t.Setenv("NB1_TASKQUERY_PARENT_SECRET", "private-parent-value")
	bridge := nativeRunner{binary: os.Args[0], environment: []string{"NB1_TASKQUERY_HELPER=spec", "PATH=fixed-system-path"}, checkTrust: func() error { return nil }}
	input := []byte(`{"schema_version":1,"sid":"not-a-code-fragment","task_path":"literal"}`)
	result, err := bridge.run(context.Background(), input)
	if err != nil || result.exitCode != 0 || result.truncated {
		t.Fatal(err, result.exitCode)
	}
	var out struct {
		Argv   []string               `json:"argv"`
		Input  map[string]interface{} `json:"input"`
		Secret string                 `json:"secret_inherited"`
		Path   string                 `json:"path"`
	}
	if json.Unmarshal(result.output, &out) != nil || out.Secret != "" || out.Path != "fixed-system-path" || out.Input["sid"] != "not-a-code-fragment" {
		t.Fatal("stdin/env isolation failed")
	}
	if len(out.Argv) != 9 || out.Argv[1] != "-NoProfile" || out.Argv[2] != "-NonInteractive" || out.Argv[7] != "-Command" || out.Argv[8] != queryScript {
		t.Fatal("bridge argv changed")
	}
	if strings.Contains(strings.Join(out.Argv, " "), "not-a-code-fragment") {
		t.Fatal("input became code")
	}
	bridge.checkTrust = func() error { return ErrTrust }
	if _, err = bridge.run(context.Background(), input); err != ErrTrust {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bridge.checkTrust = func() error { t.Fatal("expired context reached native trust check"); return nil }
	if _, err = bridge.run(ctx, input); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestExecuteBoundsOutputAndCancelsPrivateChild(t *testing.T) {
	for _, mode := range []string{"output", "diagnostic", "wait"} {
		t.Run(mode, func(t *testing.T) {
			budget := 2 * time.Second
			if mode == "wait" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			result, err := execute(ctx, os.Args[0], []string{"--taskquery-private-helper"}, "", []string{"NB1_TASKQUERY_HELPER=" + mode}, nil)
			if err == nil || len(result.output) > maxOutput {
				t.Fatal("unbounded process/output")
			}
			if mode != "wait" && !result.truncated {
				t.Fatal("overflow not recorded")
			}
			if mode == "diagnostic" && strings.Contains(string(result.output), "private-command") {
				t.Fatal("stderr leaked")
			}
		})
	}
}

func TestNonWindowsConstructorUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production token/COM probing is not a default software test")
	}
	if reader, err := New(); reader != nil || err != ErrUnsupported {
		t.Fatal(reader, err)
	}
}

// Explicit opt-in is required even on Windows. This observes only the fixed
// current-user registration; it does not create, run, stop or delete any task.
func TestNativeCurrentTaskQueryOptIn(t *testing.T) {
	if os.Getenv("NB1_TASKQUERY_NATIVE") != "1" {
		t.Skip("native Windows token/COM query is opt-in")
	}
	if runtime.GOOS != "windows" {
		t.Fatal("native task query requires Windows")
	}
	reader, err := New()
	if err != nil {
		t.Fatal(err)
	}
	status, err := reader.Query(context.Background())
	if err != nil && err != ErrAbsenceUnverified {
		t.Fatalf("read-only query: %v; selected failure=%+v", err, status.Failure)
	}
	if status.Ownership != "not_checked" || (status.Presence != Present && status.Presence != Unknown) {
		t.Fatal("unexpected ownership/presence")
	}
	encoded, marshalErr := json.Marshal(status)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Log("SELECTED_READONLY_SNAPSHOT", string(encoded))
	t.Log("Observation only: missing HRESULT remains unknown, not a native absence contract.")
}
