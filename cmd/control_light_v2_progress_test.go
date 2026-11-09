package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/lightactionv2"
)

func lightV2ProgressFixture(t *testing.T) (control.FrameJob, control.FrameJob) {
	t.Helper()
	compiled, err := lightactionv2.CompileRequest([]byte(cliLightV2Input))
	if err != nil {
		t.Fatal(err)
	}
	queued := cliLightV2QueuedJob(compiled.Program)
	running := queued
	running.Status = "running"
	started := queued.CreatedAt.Add(time.Millisecond)
	running.StartedAt = &started
	running.CompletedFrames = 1
	running.CompletedTargetWrites = len(queued.Program.Initial) * 2
	return queued, running
}

type lightV2ProgressCounts struct {
	posts int32
	gets  int32
	extra int32
}

// Every supplied snapshot is a legal individual response. The tests below
// exercise only the additional consistency required across successive reads.
func lightV2ProgressServer(t *testing.T, acceptance control.FrameJob, snapshots []control.FrameJob) (*httptest.Server, *lightV2ProgressCounts) {
	t.Helper()
	counts := &lightV2ProgressCounts{}
	receiptPath := "/v1/devices/demo/frame-requests/" + commandRequestID
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/devices/demo/light":
			atomic.AddInt32(&counts.posts, 1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("X-NB1-Daemon-Instance-ID", commandInstance)
			w.Header().Set("X-NB1-Replayed", "false")
			w.Header().Set("Location", receiptPath+"?expected_daemon_instance_id="+commandInstance)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(acceptance)
		case r.Method == http.MethodGet && r.URL.Path == receiptPath && r.URL.RawQuery == "expected_daemon_instance_id="+commandInstance:
			index := int(atomic.AddInt32(&counts.gets, 1)) - 1
			if index >= len(snapshots) {
				atomic.AddInt32(&counts.extra, 1)
				t.Errorf("continued querying after the supplied trusted/invalid snapshots")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"code":"unexpected_poll","message":"must stop"}}`)
				return
			}
			w.Header().Set("X-NB1-Daemon-Instance-ID", commandInstance)
			_ = json.NewEncoder(w).Encode(snapshots[index])
		default:
			atomic.AddInt32(&counts.extra, 1)
			t.Errorf("unexpected prepare, cancellation or fallback: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	return server, counts
}

func TestControlLightV2WaitRejectsChangedAcceptanceCreationTime(t *testing.T) {
	queued, running := lightV2ProgressFixture(t)
	running.CreatedAt = running.CreatedAt.Add(-time.Second)
	server, counts := lightV2ProgressServer(t, queued, []control.FrameJob{running})
	defer server.Close()
	text, err := executeLightV2WaitCommand(context.Background(), "--endpoint", server.URL, "light", "run", "demo", previewFile(t, cliLightV2Input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--wait", "--timeout", "2s")
	var output lightV2RunOutput
	if err == nil || json.Unmarshal([]byte(text), &output) != nil || !output.Accepted || !output.OutcomeUnknown || output.Job == nil || !reflect.DeepEqual(*output.Job, queued) {
		t.Fatalf("acceptance baseline changed or invalid snapshot replaced trusted Job: %s; %v", text, err)
	}
	if output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance || atomic.LoadInt32(&counts.posts) != 1 || atomic.LoadInt32(&counts.gets) != 1 || atomic.LoadInt32(&counts.extra) != 0 {
		t.Fatal("failed progress binding retried or lost the original ticket", text)
	}
}

func TestControlLightV2PollingPreservesLastTrustedSnapshot(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(*control.FrameJob)
	}{
		{"created-at", func(j *control.FrameJob) { j.CreatedAt = j.CreatedAt.Add(-time.Second) }},
		{"started-at", func(j *control.FrameJob) { value := j.StartedAt.Add(time.Nanosecond); j.StartedAt = &value }},
		{"frame-progress", func(j *control.FrameJob) { j.CompletedFrames = 0; j.CompletedTargetWrites = 2 }},
		{"write-progress", func(j *control.FrameJob) { j.CompletedTargetWrites-- }},
		{"running-to-queued", func(j *control.FrameJob) {
			j.Status, j.StartedAt = "queued", nil
			j.CompletedFrames, j.CompletedTargetWrites = 0, 0
		}},
	}
	for _, mode := range []string{"run", "request"} {
		for _, change := range changes {
			t.Run(mode+"/"+change.name, func(t *testing.T) {
				queued, trusted := lightV2ProgressFixture(t)
				// Five writes are valid at one completed frame (the next frame
				// may have sent its first target). A change to four is also a
				// valid standalone snapshot, so only cross-read validation can
				// detect the write-progress regression.
				trusted.CompletedTargetWrites = 5
				changed := trusted
				change.mutate(&changed)
				server, counts := lightV2ProgressServer(t, queued, []control.FrameJob{trusted, changed})
				defer server.Close()
				text, err := executeLightV2WaitCommand(context.Background(), "--endpoint", server.URL, "light", mode, "demo", previewFile(t, cliLightV2Input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--wait", "--timeout", "2s")
				var output lightV2RunOutput
				if err == nil || json.Unmarshal([]byte(text), &output) != nil || !output.Accepted || !output.OutcomeUnknown || output.Job == nil || !reflect.DeepEqual(*output.Job, trusted) {
					t.Fatalf("bad snapshot accepted or replaced last trusted state: %s; %v", text, err)
				}
				wantPosts := int32(0)
				if mode == "run" {
					wantPosts = 1
				}
				if output.RequestTicket == nil || output.RequestTicket.RequestID != commandRequestID || output.RequestTicket.ExpectedDaemonInstanceID != commandInstance || atomic.LoadInt32(&counts.posts) != wantPosts || atomic.LoadInt32(&counts.gets) != 2 || atomic.LoadInt32(&counts.extra) != 0 {
					t.Fatalf("invalid progress caused retry/prepare or discarded ticket: %s", text)
				}
			})
		}
	}
}

func TestControlLightV2StopsPollingAtFirstTrustedTerminalSnapshot(t *testing.T) {
	for _, mode := range []string{"run", "request"} {
		t.Run(mode, func(t *testing.T) {
			queued, finished := lightV2ProgressFixture(t)
			finished.Status = "succeeded"
			end := finished.StartedAt.Add(time.Millisecond)
			finished.FinishedAt = &end
			finished.CompletedFrames, finished.CompletedTargetWrites = finished.TotalFrames, finished.PlannedTargetWrites
			server, counts := lightV2ProgressServer(t, queued, []control.FrameJob{finished})
			defer server.Close()
			text, err := executeLightV2WaitCommand(context.Background(), "--endpoint", server.URL, "light", mode, "demo", previewFile(t, cliLightV2Input), "--request-id", commandRequestID, "--expected-daemon-instance-id", commandInstance, "--wait", "--timeout", "2s")
			var output lightV2RunOutput
			if err != nil || json.Unmarshal([]byte(text), &output) != nil || output.OutcomeUnknown || output.Job == nil || !reflect.DeepEqual(*output.Job, finished) || atomic.LoadInt32(&counts.gets) != 1 || atomic.LoadInt32(&counts.extra) != 0 {
				t.Fatalf("terminal receipt did not stop polling: %s; %v", text, err)
			}
		})
	}
}

func TestControlLightV2ProgressValidatorFreezesTerminalSnapshots(t *testing.T) {
	_, terminal := lightV2ProgressFixture(t)
	terminal.Status = "succeeded"
	terminal.CompletedFrames, terminal.CompletedTargetWrites = terminal.TotalFrames, terminal.PlannedTargetWrites
	end := terminal.StartedAt.Add(time.Millisecond)
	terminal.FinishedAt = &end
	if err := daemonclient.ValidateLightV2Progress(terminal, terminal); err != nil {
		t.Fatalf("identical terminal replay rejected: %v", err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*control.FrameJob)
	}{
		{"status", func(j *control.FrameJob) { j.Status, j.Error = "failed", "changed result" }},
		{"finished-at", func(j *control.FrameJob) { value := j.FinishedAt.Add(time.Nanosecond); j.FinishedAt = &value }},
		{"terminal-to-running", func(j *control.FrameJob) { j.Status, j.FinishedAt = "running", nil }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := terminal
			change.mutate(&changed)
			if err := daemonclient.ValidateLightV2Progress(terminal, changed); err == nil {
				t.Fatal("terminal result changed after it was trusted")
			}
		})
	}
}
