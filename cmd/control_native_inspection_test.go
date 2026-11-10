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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type nativeCLIReader struct{ failed bool }

func (r nativeCLIReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	led := byte(0)
	if position%2 == 1 {
		led = 2
	}
	return nativeinspection.LineRead{Position: position, Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: []byte{1, 'R', 16, 32, 48, 0, 50, led, 0xee}}}, nil
}
func (r nativeCLIReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	value := nativeinspection.PlaybackRead{Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: []byte{1, 'S', 1, 0, 2, 0, 1, 0, 0}}}
	if r.failed {
		return value, nativeCLIError{}
	}
	return value, nil
}

type nativeCLIError struct{}

func (nativeCLIError) Error() string          { return "fixture native failure" }
func (nativeCLIError) NativePlatform() string { return "fixture" }
func (nativeCLIError) NativeCode() uint64     { return ^uint64(0) }

func nativeCLIFixture(t *testing.T, request nativeinspection.Request, failed bool) (nativeinspection.Result, []byte) {
	t.Helper()
	fw, lines := 205, 32
	profile := nativeinspection.Profile{SDKModel: "mk2", SDKReferenceFirmware: &fw, SDKApplicability: "model-only", RAMLines: &lines, OperationQualification: "unverified",
		ReadReference:   nativeinspection.ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture-not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"},
		PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &lines, Sources: []string{"fixture-not-hardware"}}, EndEncoding: "exclusive"}}
	value, _ := nativeinspection.Collect(context.Background(), nativeCLIReader{failed}, request, nativeinspection.Identity{Serial: "20000001", Product: "blink(1) mk2", Firmware: 204, FirmwareSource: "cached-on-open"}, profile)
	value.DaemonInstanceID, value.DeviceSessionID = "daemon_fixture", "session_0000000000000001"
	data, err := nativeinspection.MarshalResult(value, "20000001", request)
	if err != nil {
		t.Fatal(err)
	}
	return value, data
}

func executeNativeCLI(ctx context.Context, args ...string) (string, error) {
	command := newControlCommand()
	// Real root flags are inherited by the standalone control command in the
	// application. Local test flags exercise rejection even for explicit false.
	command.PersistentFlags().Bool("wait", false, "fixture inherited wait flag")
	command.PersistentFlags().Bool("direct", false, "fixture inherited legacy direct flag")
	var output, diagnostic bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&diagnostic)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	return output.String(), err
}

func TestControlNativeInspectionOneGETAndJSON(t *testing.T) {
	for _, request := range []nativeinspection.Request{{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 2}}, {Kind: nativeinspection.Playback}} {
		t.Run(request.Kind, func(t *testing.T) {
			want, body := nativeCLIFixture(t, request, false)
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				payload, _ := io.ReadAll(r.Body)
				if r.Method != "GET" || r.URL.Path != "/v1/devices/20000001/native-sequence/"+request.Kind || len(payload) != 0 || !r.Close {
					t.Errorf("unexpected prepare/Job/direct fallback request: %s %s", r.Method, r.URL)
				}
				if request.Range == nil && r.URL.RawQuery != "" || request.Range != nil && (len(r.URL.Query()) != 2 || r.URL.Query().Get("start_line") != "0" || r.URL.Query().Get("line_count") != "2") {
					t.Errorf("changed explicit range: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", want.DaemonInstanceID)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			args := []string{"--endpoint", server.URL, "native-sequence", request.Kind, "20000001"}
			if request.Range != nil {
				args = append(args, "--start-line", "0", "--line-count", "2")
			}
			output, err := executeNativeCLI(context.Background(), args...)
			got, decodeErr := nativeinspection.DecodeResult([]byte(output), "20000001", request)
			if err != nil || decodeErr != nil || !got.Complete || atomic.LoadInt32(&calls) != 1 || got.Contract != nativeinspection.Contract {
				t.Fatalf("read failed/retried: err=%v decode=%v calls=%d output=%s", err, decodeErr, calls, output)
			}
			if got.RAM != nil && (got.RAM.Sequence == nil || got.RAM.Sequence.Lines[0].LED != 0 || !strings.Contains(output, `"led": "all"`)) {
				t.Fatalf("nullable/raw/all-LED result changed: %s", output)
			}
		})
	}
}

func TestControlNativeInspectionPartialPrintsJSONBeforeFailure(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	want, body := nativeCLIFixture(t, request, true)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", want.DaemonInstanceID)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	output, err := executeNativeCLI(context.Background(), "--endpoint", server.URL, "native-sequence", "playback", "20000001")
	var failure *daemonclient.InspectionError
	got, decodeErr := nativeinspection.DecodeResult([]byte(output), "20000001", request)
	if !errors.As(err, &failure) || decodeErr != nil || got.Complete || got.Status != "partial" || got.Error == nil || got.Error.NativeCode == nil || *got.Error.NativeCode != "18446744073709551615" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("partial data lost or command did not fail: err=%v decode=%v calls=%d output=%s", err, decodeErr, calls, output)
	}
	if failure.Result.Playback.Report.RawHex != got.Playback.Report.RawHex || !strings.Contains(output, `"ram": null`) {
		t.Fatalf("typed error or explicit nullable field lost: %s", output)
	}
}

func TestControlNativeInspectionInvalidInputNeverContactsOrLocks(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	lock := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("NB1_LOCK_DIR", lock)
	valid := []string{"native-sequence", "ram", "20000001", "--start-line", "0", "--line-count", "1"}
	cases := [][]string{
		{"native-sequence", "ram", "20000001"}, {"native-sequence", "ram", "20000001", "--start-line", "0"}, {"native-sequence", "ram", "20000001", "--line-count", "1"},
		{"native-sequence", "ram", "20000001", "--start-line=", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=0", "--line-count="},
		{"native-sequence", "ram", "20000001", "--start-line=-1", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=32", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=31", "--line-count=2"},
		{"native-sequence", "ram", "20000001", "--start-line=0", "--line-count=0"}, {"native-sequence", "ram", "20000001", "--start-line=0", "--line-count=33"},
		{"native-sequence", "ram", "20000001", "--start-line=0x1", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=1.0", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=1e0", "--line-count=1"}, {"native-sequence", "ram", "20000001", "--start-line=+1", "--line-count=1"},
		{"native-sequence", "ram", "20000001", "--start-line=999999999999999999999999999999", "--line-count=1"},
		{"native-sequence", "playback", "demo"}, {"native-sequence", "playback", "2000000g"}, {"native-sequence", "playback", "2000000"}, {"native-sequence", "playback", "../20000001"},
		{"native-sequence", "playback"}, {"native-sequence", "playback", "20000001", "extra"}, {"native-sequence", "playback", "20000001", "--start-line=0"},
		{"native-sequence", "ram", "20000001", "--start-line=0", "--line-count=1", "--target=flash"},
	}
	for _, flag := range []string{"--policy=replace", "--policy=reject", "--request-id=", "--request-id=req_fixture", "--expected-daemon-instance-id=", "--expected-daemon-instance-id=daemon_fixture", "--wait=false", "--wait", "--direct=false", "--direct", "--lock-dir=" + lock, "--mode=direct"} {
		entry := append(append([]string(nil), valid...), flag)
		cases = append(cases, entry)
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			all := append([]string{"--endpoint", server.URL}, args...)
			output, err := executeNativeCLI(context.Background(), all...)
			if err == nil || output != "" || atomic.LoadInt32(&calls) != 0 {
				t.Fatalf("invalid input touched daemon or printed a result: err=%v calls=%d output=%s", err, calls, output)
			}
			if _, err := os.Stat(lock); !os.IsNotExist(err) {
				t.Fatalf("read-only command touched ownership: %v", err)
			}
		})
	}
}

func TestControlNativeInspectionRejectsMalformedAndUnsupportedWithoutFallback(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	want, body := nativeCLIFixture(t, request, false)
	for name, response := range map[string]struct {
		status int
		body   []byte
	}{
		"unsupported":    {501, []byte(`{"error":{"code":"native_inspection_unsupported","message":"fixture unavailable"}}`)},
		"missing null":   {200, []byte(strings.Replace(string(body), `"ram":null,`, "", 1))},
		"wrong contract": {200, []byte(strings.Replace(string(body), nativeinspection.Contract, "wrong-contract", 1))},
		"oversized":      {200, []byte(strings.Repeat(" ", nativeinspection.MaxResponseBytes+1))},
	} {
		t.Run(name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-NB1-Daemon-Instance-ID", want.DaemonInstanceID)
				w.WriteHeader(response.status)
				_, _ = w.Write(response.body)
			}))
			defer server.Close()
			output, err := executeNativeCLI(context.Background(), "--endpoint", server.URL, "native-sequence", "playback", "20000001")
			if err == nil || output != "" || calls != 1 {
				t.Fatalf("rejection decoded as data, retried or used fallback: %v calls=%d output=%s", err, calls, output)
			}
			if response.status == 501 {
				var rejection *daemonclient.Error
				if !errors.As(err, &rejection) || rejection.Code != "native_inspection_unsupported" {
					t.Fatalf("original API error lost: %v", err)
				}
			}
		})
	}
}

func TestControlNativeInspectionHelpIsEnglishAndHasExplicitRange(t *testing.T) {
	for _, args := range [][]string{{"native-sequence", "--help"}, {"native-sequence", "ram", "--help"}, {"native-sequence", "playback", "--help"}} {
		output, err := executeNativeCLI(context.Background(), args...)
		if err != nil || !strings.Contains(output, "native-sequence") {
			t.Fatalf("help: %v %s", err, output)
		}
		for _, char := range output {
			if char >= 0x4e00 && char <= 0x9fff {
				t.Fatalf("new help contains Chinese text: %q", output)
			}
		}
		if len(args) > 1 && args[1] == "ram" && (!strings.Contains(output, "--start-line") || !strings.Contains(output, "--line-count")) {
			t.Fatalf("range flags missing: %s", output)
		}
	}
}

func TestControlNativeInspectionCLIUsesDecimalRange(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 1, LineCount: 1}}
	want, body := nativeCLIFixture(t, request, false)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("start_line") != "1" || r.URL.Query().Get("line_count") != "1" {
			t.Errorf("not decimal: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-NB1-Daemon-Instance-ID", want.DaemonInstanceID)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	output, err := executeNativeCLI(context.Background(), "--endpoint", server.URL, "native-sequence", "ram", "20000001", "--start-line=01", "--line-count=01")
	var result map[string]interface{}
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("explicit decimal failed: %v calls=%d output=%s", err, calls, output)
	}
}
