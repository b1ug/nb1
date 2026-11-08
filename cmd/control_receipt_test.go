package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

const commandInstance = "daemon_0123456789abcdef0123456789abcdef"
const commandRequestID = "req_60000_0123456789abcdef0123456789abcdef"

type receiptCommandService struct {
	commandService
	keyed, lookups, jobLookups int32
	last                       control.ReceiptRequest
}

func (*receiptCommandService) Instance() control.InstanceInfo {
	return control.InstanceInfo{DaemonInstanceID: commandInstance, Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}}
}
func (s *receiptCommandService) SubmitWithReceipt(_ context.Context, serial string, p control.Program, policy string, receipt control.ReceiptRequest) (control.ReceiptResult, error) {
	atomic.AddInt32(&s.keyed, 1)
	s.last = receipt
	return control.ReceiptResult{Job: control.Job{ID: "job-receipt", Device: serial, Status: "queued", Program: p}, DaemonInstanceID: commandInstance}, nil
}
func (s *receiptCommandService) Request(serial, id, instance string) (control.Job, error) {
	atomic.AddInt32(&s.lookups, 1)
	if id != commandRequestID || instance != commandInstance {
		return control.Job{}, control.ErrRequestNotFound
	}
	return control.Job{ID: "job-receipt", Device: serial, Status: "succeeded"}, nil
}
func (s *receiptCommandService) Job(string) (control.Job, error) {
	atomic.AddInt32(&s.jobLookups, 1)
	return control.Job{}, control.ErrNotFound // Ordinary history may have expired.
}

func executeReceiptCommand(args ...string) (string, error) {
	command := newControlCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.Execute()
	return out.String(), err
}

func TestControlOptionalPrepareSubmitAndLookup(t *testing.T) {
	s := &receiptCommandService{}
	server := httptest.NewServer(httpapi.New(s))
	defer server.Close()
	output, err := executeReceiptCommand("--endpoint", server.URL, "prepare")
	var prepared control.ReceiptRequest
	if jsonErr := json.Unmarshal([]byte(output), &prepared); err != nil || jsonErr != nil || prepared.ExpectedDaemonInstanceID != commandInstance || !strings.HasPrefix(prepared.RequestID, "req_60000_") || len(prepared.RequestID) != len("req_60000_")+32 {
		t.Fatalf("prepare: %s %v %v", output, err, jsonErr)
	}
	programPath := filepath.Join(t.TempDir(), "program.json")
	if err := os.WriteFile(programPath, []byte(`{"steps":[{"color":"#102030"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"set", "20005601", "#102030", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"run", "20005601", programPath, "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"request", "20005601", commandRequestID, "--expected-daemon-instance-id", commandInstance},
	} {
		output, err := executeReceiptCommand(append([]string{"--endpoint", server.URL}, args...)...)
		if err != nil || !strings.Contains(output, `"id": "job-receipt"`) {
			t.Fatalf("%v: %s %v", args, output, err)
		}
	}
	if atomic.LoadInt32(&s.keyed) != 2 || atomic.LoadInt32(&s.submitted) != 0 || s.last.RequestID != commandRequestID || s.last.ExpectedDaemonInstanceID != commandInstance {
		t.Fatalf("keyed submit lost binding or downgraded: keyed=%d unkeyed=%d last=%+v", s.keyed, s.submitted, s.last)
	}
}

func TestControlReceiptWaitUsesReceiptInsteadOfEvictedJobHistory(t *testing.T) {
	previous := waitComplete
	waitComplete = true
	defer func() { waitComplete = previous }()
	s := &receiptCommandService{}
	server := httptest.NewServer(httpapi.New(s))
	defer server.Close()
	output, err := executeReceiptCommand("--endpoint", server.URL, "set", "20005601", "#102030", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance)
	if err != nil || !strings.Contains(output, `"status": "succeeded"`) || atomic.LoadInt32(&s.jobLookups) != 0 || atomic.LoadInt32(&s.lookups) == 0 {
		t.Fatalf("wait used ordinary history: %s %v jobs=%d receipts=%d", output, err, s.jobLookups, s.lookups)
	}
}

func TestControlDirectRejectsAnyReceiptExtensionBeforeOwnership(t *testing.T) {
	for _, args := range [][]string{
		{"prepare"},
		{"request", "20005601", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"set", "20005601", "#102030", "--request-id", commandRequestID},
		{"set", "20005601", "#102030", "--request-id=", "--expected-daemon-instance-id="},
		{"set", "20005601", "#102030", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"run", "20005601", "not-an-existing-file", "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance},
		{"devices", "--expected-daemon-instance-id="},
	} {
		lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
		_, err := executeReceiptCommand(append([]string{"--mode", "direct", "--lock-dir", lockDir}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "request receipts belong to the daemon") {
			t.Fatalf("direct extension accepted: %v: %v", args, err)
		}
		if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
			t.Fatalf("direct extension reached ownership: %v", err)
		}
	}
}

func TestControlDaemonRejectsPartialOrIrrelevantBindingsBeforeHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"set", "20005601", "#102030", "--request-id", commandRequestID},
		{"set", "20005601", "#102030", "--request-id=", "--expected-daemon-instance-id="},
		{"request", "20005601", commandRequestID},
		{"prepare", "--request-id", commandRequestID},
	} {
		_, err := executeReceiptCommand(append([]string{"--endpoint", "http://127.0.0.1:1"}, args...)...)
		if err == nil || strings.Contains(err.Error(), "contact daemon") {
			t.Fatalf("invalid flags reached HTTP: %v: %v", args, err)
		}
	}
}
