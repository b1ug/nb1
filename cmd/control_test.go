package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
)

type commandService struct{ submitted int32 }

func (*commandService) Devices() []control.DeviceState {
	return []control.DeviceState{{DeviceInfo: control.DeviceInfo{Serial: "20005601"}, Connection: "online"}}
}

type waitingService struct {
	commandService
	polls    int32
	cancels  int32
	terminal string
}

func (s *waitingService) Job(id string) (control.Job, error) {
	status := "running"
	if atomic.AddInt32(&s.polls, 1) > 1 && s.terminal != "" {
		status = s.terminal
	}
	return control.Job{ID: id, Status: status}, nil
}
func (s *waitingService) Cancel(_ context.Context, id string) (control.Job, error) {
	atomic.AddInt32(&s.cancels, 1)
	return control.Job{ID: id, Status: "cancelling"}, nil
}

func TestControlWaitsForTerminalResult(t *testing.T) {
	previous := waitComplete
	waitComplete = true
	defer func() { waitComplete = previous }()
	for _, tc := range []struct {
		args     []string
		terminal string
	}{
		{[]string{"set", "20005601", "#ff0000"}, "completed"},
		{[]string{"cancel", "job-test"}, "cancelled"},
	} {
		t.Run(tc.terminal, func(t *testing.T) {
			service := &waitingService{terminal: tc.terminal}
			server := httptest.NewServer(httpapi.New(service))
			defer server.Close()
			command := newControlCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(append([]string{"--endpoint", server.URL}, tc.args...))
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), `"status": "`+tc.terminal+`"`) {
				t.Fatalf("did not wait: %s", output.String())
			}
		})
	}
}

func TestWaitingTimeoutDoesNotCancelDaemonJob(t *testing.T) {
	service := &waitingService{}
	server := httptest.NewServer(httpapi.New(service))
	defer server.Close()
	client, err := daemonclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := waitRemoteJob(ctx, client, "job-test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error: %v", err)
	}
	if atomic.LoadInt32(&service.cancels) != 0 {
		t.Fatal("waiting client cancelled a persistent daemon job")
	}
}
func (s *commandService) Device(serial string) (control.DeviceState, error) {
	return s.Devices()[0], nil
}
func (s *commandService) Submit(_ context.Context, serial string, p control.Program, _ string) (control.Job, error) {
	atomic.AddInt32(&s.submitted, 1)
	return control.Job{ID: "job-test", Device: serial, Status: "queued", Program: p}, nil
}
func (*commandService) Job(id string) (control.Job, error) {
	return control.Job{ID: id, Status: "running"}, nil
}
func (*commandService) Cancel(_ context.Context, id string) (control.Job, error) {
	return control.Job{ID: id, Status: "cancelling"}, nil
}

func TestControlCommandDaemonRequests(t *testing.T) {
	service := &commandService{}
	server := httptest.NewServer(httpapi.New(service))
	defer server.Close()
	for _, tc := range []struct {
		args     []string
		contains string
	}{
		{[]string{"devices"}, `"20005601"`}, {[]string{"state", "20005601"}, `"online"`},
		{[]string{"set", "20005601", "#800000", "--led", "2", "--fade-ms", "100"}, `"on_finish": "hold"`},
		{[]string{"job", "job-test"}, `"running"`}, {[]string{"cancel", "job-test"}, `"cancelling"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			command := newControlCommand()
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&out)
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(append([]string{"--endpoint", server.URL}, tc.args...))
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.contains) {
				t.Fatalf("output=%s", out.String())
			}
			var result interface{}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("output not pure JSON: %v: %s", err, out.String())
			}
		})
	}
	if count := atomic.LoadInt32(&service.submitted); count != 1 {
		t.Fatalf("submitted %d times", count)
	}
}

func TestControlInvalidInputBeforeDirectOpen(t *testing.T) {
	for _, args := range [][]string{
		{"set", "not-a-device", "#ff0000", "--led", "256"},
		{"set", "not-a-device", "red"},
		{"set", "not-a-device", "#ff0000", "--fade-ms", "1"},
		{"set", "not-a-device", "#ff0000", "--policy", "invalid"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "must-not-be-created")
			command := newControlCommand()
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SetArgs(append([]string{"--mode", "direct", "--lock-dir", lockDir}, args...))
			if err := command.Execute(); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
				t.Fatalf("invalid input reached hardware ownership: %v", err)
			}
		})
	}
}

func TestControlDoesNotFallbackToUSB(t *testing.T) {
	command := newControlCommand()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"--endpoint", "http://127.0.0.1:1", "--timeout", "100ms", "set", "not-a-device", "#ff0000"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "contact daemon") {
		t.Fatalf("expected daemon transport error, got %v", err)
	}
}

func TestProgramFileBoundary(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"steps":[],"extra":true}`, `{"steps":[]} {}`, `{"steps":[{"color":"#ff0000","raw":true}]}`} {
		path := filepath.Join(t.TempDir(), "program.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readControlProgram(path); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	path := filepath.Join(t.TempDir(), "valid.json")
	if err := os.WriteFile(path, []byte(`{"steps":[{"led":1,"color":"#ff0000","fade_ms":0,"hold_ms":10}],"repeat":1,"on_finish":"off"}`), 0600); err != nil {
		t.Fatal(err)
	}
	program, err := readControlProgram(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Normalize(program); err != nil {
		t.Fatal(err)
	}
}
