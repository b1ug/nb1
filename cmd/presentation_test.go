package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

type cleanupObservedWriter struct {
	bytes.Buffer
	closed    *bool
	premature bool
}

func (w *cleanupObservedWriter) Write(data []byte) (int, error) {
	if !*w.closed {
		w.premature = true
	}
	return w.Buffer.Write(data)
}

func TestPresentedCommandPreservesCleanupAndStreams(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	directory := t.TempDir()
	profile := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(profile, appconfig.Template(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NB1_CONFIG", profile)
	primary := errors.New("device_offline serial=灯甲 job_id=j_7K3XJNY9ZM")
	closing := errors.New("close failed for 灯甲")
	for _, test := range []struct {
		name                 string
		args, pre, run, post error
		close                error
		cancel               bool
	}{
		{name: "success"},
		{name: "argument error", args: primary},
		{name: "pre-run error", pre: primary},
		{name: "run error", run: primary},
		{name: "post-run error", post: primary},
		{name: "close error", close: closing},
		{name: "primary error wins", run: primary, close: closing},
		{name: "caller cancellation", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			closed, count := false, 0
			stderr := &cleanupObservedWriter{closed: &closed}
			root := &cobra.Command{Use: "fixture", Long: "unaltered source description"}
			root.SetOut(&stdout)
			root.SetErr(stderr)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				cancel()
			}
			root.SetContext(ctx)
			const result = "{\"status\":\"accepted\",\"job_id\":\"j_7K3XJNY9ZM\"}\n"
			root.AddCommand(&cobra.Command{
				Use: "run", Args: func(*cobra.Command, []string) error { return test.args },
				PreRunE: func(*cobra.Command, []string) error { return test.pre },
				RunE: func(cmd *cobra.Command, _ []string) error {
					if cmd.Context() != ctx {
						t.Fatal("presentation replaced the caller context")
					}
					if test.cancel {
						return cmd.Context().Err()
					}
					_, err := io.WriteString(cmd.OutOrStdout(), result)
					if err != nil {
						return err
					}
					return test.run
				},
				PostRunE: func(*cobra.Command, []string) error { return test.post },
			})
			err := executePresentedCommand(root, []string{"run"}, func() error {
				count++
				closed = true
				return test.close
			})
			want := test.args
			for _, candidate := range []error{test.pre, test.run, test.post, test.close} {
				if want == nil {
					want = candidate
				}
			}
			if test.cancel {
				want = context.Canceled
			}
			if !errors.Is(err, want) || count != 1 || stderr.premature {
				t.Fatalf("cleanup/error contract differs: err=%v want=%v calls=%d premature=%t stderr=%q", err, want, count, stderr.premature, stderr.String())
			}
			wantedStdout := result
			if test.args != nil || test.pre != nil || test.cancel {
				wantedStdout = ""
			}
			if stdout.String() != wantedStdout {
				t.Fatalf("machine stdout changed: %q", stdout.String())
			}
			if want == nil && stderr.Len() != 0 {
				t.Fatalf("success acquired a diagnostic: %q", stderr.String())
			}
			if want != nil && strings.Count(stderr.String(), want.Error()) != 1 {
				t.Fatalf("original error/IDs are missing or duplicated: %q", stderr.String())
			}
			if test.run != nil && test.close != nil && strings.Contains(stderr.String(), closing.Error()) {
				t.Fatalf("close error replaced the original failure: %q", stderr.String())
			}
			if bytes.ContainsRune(stderr.Bytes(), '\x1b') || root.Long != "unaltered source description" || root.OutOrStdout() != &stdout || root.ErrOrStderr() != stderr {
				t.Fatal("plain mode or restored command state differs")
			}
		})
	}
}

func TestPresentedRootHelpRetainsLogoAndHiddenCompletion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"control", "--help"}} {
		stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), args)
		visible := ansi.Strip(stdout)
		if err != nil || stderr != "" || !strings.Contains(visible, "USAGE") || !strings.Contains(visible, "COMMANDS") {
			t.Fatalf("actual Fang help failed: args=%v err=%v stdout=%q stderr=%q", args, err, stdout, stderr)
		}
		rootHelp := len(args) == 1
		if strings.Contains(visible, "88888P") != rootHelp || strings.Contains(visible, "completion [command]") {
			t.Fatalf("root artwork or hidden completion visibility differs: %q", visible)
		}
	}
	stdout, stderr, err := executeRootDiagnosticProcess(t, t.TempDir(), []string{"completion", "bash"})
	if err != nil || stderr != "" || !strings.Contains(stdout, "__nb1") {
		t.Fatalf("established hidden completion stopped working: %v %q %q", err, stdout, stderr)
	}
}

func TestPresentedDocsKeepUnstyledSourceDescriptions(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	directory := t.TempDir()
	output := filepath.Join(directory, "manual")
	stdout, stderr, err := executeRootDiagnosticProcess(t, directory, []string{"docs", "-o", output})
	if err != nil || stderr != "" {
		t.Fatalf("docs generation failed: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	data, err := os.ReadFile(filepath.Join(output, "nb1.md"))
	if err != nil || bytes.ContainsRune(data, '\x1b') || bytes.Contains(data, []byte("88888P")) || !bytes.Contains(data, []byte("nb1 controls blink(1)")) {
		t.Fatalf("presentation leaked into source docs: err=%v data=%q", err, data)
	}
}

type blockedPresentationWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockedPresentationWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(data), nil
}

func TestPresentationOutputFiltersOnlyPresentationAndDoesNotBlockActivation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var raw bytes.Buffer
	output, writer := newPresentationOutput(&raw)
	const content = "user content \x1b[31mred\x1b[0m\n"
	if _, err := io.WriteString(writer, content); err != nil {
		t.Fatal(err)
	}
	output.activate()
	if _, err := io.WriteString(writer, content); err != nil {
		t.Fatal(err)
	}
	if raw.String() != content+ansi.Strip(content) {
		t.Fatalf("command data was filtered, or plain presentation retained ANSI: %q", raw.String())
	}
	blocked := &blockedPresentationWriter{entered: make(chan struct{}), release: make(chan struct{})}
	output, writer = newPresentationOutput(blocked)
	done := make(chan struct{})
	go func() { _, _ = io.WriteString(writer, "pending command output"); close(done) }()
	<-blocked.entered
	activated := make(chan struct{})
	go func() { output.activate(); close(activated) }()
	select {
	case <-activated:
	case <-time.After(time.Second):
		close(blocked.release)
		t.Fatal("presentation activation waited on a blocked diagnostic sink")
	}
	close(blocked.release)
	<-done
}
