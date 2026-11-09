package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/colorinput"
)

func executeColorCommand(ctx context.Context, args ...string) (string, error) {
	command := newControlCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func colorFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "color.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestControlColorDirectIsPureAndUsesNewContract(t *testing.T) {
	blockedLockDir := colorFile(t, "not a device lock directory")
	for _, tc := range []struct {
		name, value, canonical string
		file                   bool
	}{
		{"CSS green", "green", "#008000", false},
		{"CSS lime", "lime", "#00ff00", false},
		{"hex3", "#0f8", "#00ff88", false},
		{"hex6", "#12AbEF", "#12abef", false},
		{"rgb8", `{"space":"rgb8","r":0,"g":255,"b":136}`, "#00ff88", true},
		{"hsv", `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`, "#008000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--mode", "direct", "--endpoint", "not-a-daemon-origin", "--lock-dir", blockedLockDir, "color"}
			if tc.file {
				args = append(args, "--file", colorFile(t, tc.value))
			} else {
				args = append(args, tc.value)
			}
			output, err := executeColorCommand(context.Background(), args...)
			if err != nil {
				t.Fatalf("pure color reached endpoint/ownership: %v", err)
			}
			var result colorinput.Result
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if err := colorinput.ValidateResult(result); err != nil || result.CanonicalColor != tc.canonical {
				t.Fatalf("new contract result: %#v %v", result, err)
			}
			for _, forbidden := range []string{`"accepted"`, `"job"`, `"serial"`, `"policy"`} {
				if strings.Contains(output, forbidden) {
					t.Fatalf("color output gained execution field: %s", output)
				}
			}
		})
	}
	data, err := os.ReadFile(blockedLockDir)
	if err != nil || string(data) != "not a device lock directory" {
		t.Fatal("software color command changed supplied lock path")
	}
}

func TestControlColorArgumentGuardsBeforeFileClientAndOwnership(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	for _, args := range [][]string{
		{"color"}, {"color", "green", "lime"}, {"color", "--file", ""}, {"color", "green", "--file", "missing"},
		{"color", "--file", "missing", "--policy", "replace"}, {"color", "green", "--policy", "reject"},
		{"color", "--file", "missing", "--request-id", ""}, {"color", "--file", "missing", "--expected-daemon-instance-id", ""},
		{"color", "green", "--request-id", "x", "--expected-daemon-instance-id", "y"},
		{"--mode", "direct", "color", "--file", "missing", "--request-id", "x"},
		{"color", "--file", "missing", "--serial", "20005601"}, {"--mode", "direct", "color", "green", "--serial", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "must-not-exist")
			full := append([]string{"--endpoint", server.URL, "--lock-dir", lockDir}, args...)
			output, err := executeColorCommand(context.Background(), full...)
			if err == nil || strings.Contains(err.Error(), "no such file") || output != "" {
				t.Fatalf("arguments not rejected before file/client: %q %v", output, err)
			}
			if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
				t.Fatalf("argument guard touched lock path: %v", err)
			}
		})
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("argument guard contacted daemon %d times", calls)
	}
}

func TestControlColorValidatesBareInputBeforeCreatingClient(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{"color":"green"}`, `{"space":"rgb8","r":0,"g":0,"b":256}`,
		`{"space":"rgb8","r":0,"g":0,"b":1,"\u0062":1}`, `{"Space":"rgb8","r":0,"g":0,"b":0}`,
		`{"space":"hsv","h_deg":0,"s_pct":100,"v_pct":-0}`, `"green" {}`,
		`"green"` + strings.Repeat(" ", colorinput.MaxRequestBytes),
	} {
		path := colorFile(t, body)
		for _, mode := range []string{"daemon", "direct"} {
			output, err := executeColorCommand(context.Background(), "--mode", mode, "--endpoint", "not-a-daemon-origin", "color", "--file", path)
			if err == nil || output != "" || strings.Contains(err.Error(), "daemon endpoint") {
				t.Fatalf("invalid bare input reached client: %s %q %v", body, output, err)
			}
		}
	}
	for _, input := range []string{"", "random", "currentColor", "rgb(0,255,0)", strings.Repeat("a", 65)} {
		if output, err := executeColorCommand(context.Background(), "--endpoint", "not-a-daemon-origin", "color", input); err == nil || output != "" || strings.Contains(err.Error(), "daemon endpoint") {
			t.Fatalf("invalid positional color reached client: %q %s %v", input, output, err)
		}
	}
	// The reader bounds bytes before parsing, and the exact boundary remains a
	// valid bare input in direct mode; no HTTP envelope is read from the file.
	path := colorFile(t, `"green"`+strings.Repeat(" ", colorinput.MaxRequestBytes-len(`"green"`)))
	if _, err := executeColorCommand(context.Background(), "--mode", "direct", "color", "--file", path); err != nil {
		t.Fatalf("exact bounded bare file rejected: %v", err)
	}
}

func TestControlColorDefaultDaemonForwardsColorOnly(t *testing.T) {
	var calls int32
	captured := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "POST" || r.URL.Path != "/v1/colors/resolve" || r.URL.RawQuery != "" {
			t.Errorf("color reached another API: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal(data, &input); err != nil || len(input) != 1 || len(input["color"]) == 0 {
			t.Errorf("unexpected envelope: %s %v", data, err)
			w.WriteHeader(400)
			return
		}
		captured <- string(input["color"])
		result, err := colorinput.ResolveRequestJSON(data)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		raw  string
	}{
		{[]string{"color", "green"}, `"green"`},
		{[]string{"color", "--file", colorFile(t, `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`)}, `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`},
	} {
		output, err := executeColorCommand(context.Background(), append([]string{"--endpoint", server.URL}, tc.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var actual colorinput.Result
		expected, err := colorinput.ResolveJSON([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(output), &actual); err != nil || !reflect.DeepEqual(actual, expected) || <-captured != tc.raw {
			t.Fatalf("default daemon intent/output: %s %#v %v", output, actual, err)
		}
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("color retried or contacted other APIs: %d", calls)
	}
}

func TestControlColorOldDaemonNoFallbackAndTimeout(t *testing.T) {
	blockedLockDir := colorFile(t, "not a lock directory")
	for _, status := range []int{404, 501} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(status) }))
		output, err := executeColorCommand(context.Background(), "--endpoint", server.URL, "--lock-dir", blockedLockDir, "color", "green")
		if err == nil || output != "" || !strings.Contains(err.Error(), "color_resolution_unsupported") || atomic.LoadInt32(&calls) != 1 {
			t.Fatalf("unsupported daemon retried or fell back: %s %v calls=%d", output, err, calls)
		}
		server.Close()
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	output, err := executeColorCommand(ctx, "--timeout", "1s", "--endpoint", server.URL, "color", "green")
	if !errors.Is(err, context.DeadlineExceeded) || output != "" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("caller deadline lost or retried: %s %v calls=%d", output, err, calls)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := executeColorCommand(cancelled, "--mode", "direct", "color", "--file", "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled direct color read file: %v", err)
	}
}
