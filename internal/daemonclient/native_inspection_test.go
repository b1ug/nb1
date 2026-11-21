package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type nativeClientReader struct {
	err     error
	noRead  bool
	unknown bool
	all     bool
}

func (r nativeClientReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	data := []byte{1, 'R', 16, 32, 48, 0, 50, 1, 0xee}
	if r.unknown {
		data[7] = 255
	}
	if r.all {
		data[7] = 0
	}
	reply := nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: len(data), LengthValid: true, Bytes: data}
	if r.noRead {
		reply = nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9}
	}
	return nativeinspection.LineRead{Position: position, Reply: reply}, r.err
}
func (r nativeClientReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	data := []byte{1, 'S', 1, 0, 2, 0, 1, 0, 0}
	reply := nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: len(data), LengthValid: true, Bytes: data}
	if r.noRead {
		reply = nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9}
	}
	return nativeinspection.PlaybackRead{Reply: reply}, r.err
}

type nativeClientError struct{ code uint64 }

func (e nativeClientError) Error() string          { return "fixture native failure" }
func (e nativeClientError) NativeCode() uint64     { return e.code }
func (e nativeClientError) NativePlatform() string { return "darwin" }

func nativeClientFixture(t *testing.T, request nativeinspection.Request, reader nativeClientReader) (nativeinspection.Result, []byte) {
	t.Helper()
	fw, lines := 205, 32
	profile := nativeinspection.Profile{SDKModel: "mk2", SDKReferenceFirmware: &fw, SDKApplicability: "model-only", RAMLines: &lines, OperationQualification: "unverified",
		ReadReference:   nativeinspection.ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture-not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"},
		PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &lines, Sources: []string{"fixture-not-hardware"}}, EndEncoding: "exclusive"}}
	value, _ := nativeinspection.Collect(context.Background(), reader, request, nativeinspection.Identity{Serial: "20000001", Product: "blink(1) mk2", Firmware: 204, FirmwareSource: "cached-on-open"}, profile)
	value.DaemonInstanceID, value.DeviceSessionID = "daemon_fixture", "session_0000000000000001"
	data, err := nativeinspection.MarshalResult(value, "20000001", request)
	if err != nil {
		t.Fatal(err)
	}
	return value, data
}

func nativeClientHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_fixture")
}

func TestNativeClientOneGETAndCompleteUnrepresentableRead(t *testing.T) {
	for _, request := range []nativeinspection.Request{{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 32}}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 31, LineCount: 1}}, {Kind: nativeinspection.Playback}} {
		t.Run(request.Kind+fmt.Sprint(request.Range), func(t *testing.T) {
			want, body := nativeClientFixture(t, request, nativeClientReader{unknown: true})
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				data, _ := io.ReadAll(r.Body)
				if r.Method != "GET" || r.URL.Path != "/v1/devices/20000001/native-sequence/"+request.Kind || len(data) != 0 || !r.Close {
					t.Errorf("unexpected request: %s %s bytes=%d close=%v", r.Method, r.URL, len(data), r.Close)
				}
				query := r.URL.Query()
				if request.Range == nil && r.URL.RawQuery != "" || request.Range != nil && (len(query) != 2 || query.Get("start_line") != fmt.Sprint(request.Range.StartLine) || query.Get("line_count") != fmt.Sprint(request.Range.LineCount)) {
					t.Errorf("wrong range: %s", r.URL.RawQuery)
				}
				nativeClientHeaders(w)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			transport := client.http.Transport.(*http.Transport)
			got, err := client.InspectNative(context.Background(), "20000001", request)
			encoded, encodeErr := nativeinspection.MarshalResult(got, "20000001", request)
			if err != nil || encodeErr != nil || string(encoded) != string(body) || !got.Complete || atomic.LoadInt32(&calls) != 1 || transport.Proxy != nil || transport.DisableKeepAlives {
				t.Fatalf("read rejected, retried or shared transport changed: err=%v calls=%d value=%#v", err, calls, got)
			}
			if want.RAM != nil && (got.RAM.DecodingStatus != "unrepresentable" || got.RAM.Sequence != nil || got.RAM.Rows[0].LEDCode == nil || *got.RAM.Rows[0].LEDCode != 255) {
				t.Fatalf("raw unknown LED was normalized or deployed: %#v", got.RAM)
			}
		})
	}
}

func TestNativeClientInvalidInputsSendNoHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&calls, 1) }))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	for _, serial := range []string{"", "demo", "../20000001", "2000560", "200000010", "2000560g", "20000001?x=1"} {
		if out, err := client.InspectNative(context.Background(), serial, nativeinspection.Request{Kind: nativeinspection.Playback}); err == nil || out.Contract != "" {
			t.Fatalf("invalid serial accepted: %q", serial)
		}
	}
	for _, request := range []nativeinspection.Request{{}, {Kind: "flash"}, {Kind: nativeinspection.RAM}, {Kind: nativeinspection.Playback, Range: &devicesequence.Range{LineCount: 1}}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: -1, LineCount: 1}}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 31, LineCount: 2}}, {Kind: nativeinspection.RAM, Range: &devicesequence.Range{LineCount: 0}}} {
		if out, err := client.InspectNative(context.Background(), "20000001", request); err == nil || out.Contract != "" {
			t.Fatalf("invalid request accepted: %#v", request)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid input sent %d HTTP requests", calls)
	}
}

func TestNativeClientDecodesAllLEDAndExplicitNulls(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{LineCount: 1}}
	_, body := nativeClientFixture(t, request, nativeClientReader{all: true})
	if !strings.Contains(string(body), `"led":0`) || strings.Contains(string(body), `"led":"all"`) {
		t.Fatalf("all-output destination must use numeric zero: %s", body)
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		nativeClientHeaders(w)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	value, err := client.InspectNative(context.Background(), "20000001", request)
	if err != nil || value.RAM.Sequence == nil || value.RAM.Sequence.Lines[0].LED != 0 || value.Playback != nil || value.Error != nil || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("all LED or null fields lost: %v calls=%d", err, calls)
	}
}

func TestNativeClientPartialAndFailedResultsRetainTypedError(t *testing.T) {
	for _, reader := range []nativeClientReader{{err: nativeClientError{0}}, {err: nativeClientError{^uint64(0)}}, {err: errors.New("fixture request failure"), noRead: true}} {
		t.Run(fmt.Sprint(reader), func(t *testing.T) {
			request := nativeinspection.Request{Kind: nativeinspection.Playback}
			want, body := nativeClientFixture(t, request, reader)
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				nativeClientHeaders(w)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			got, err := client.InspectNative(context.Background(), "20000001", request)
			var failure *InspectionError
			if !errors.As(err, &failure) || got.Contract != nativeinspection.Contract || got.Complete || failure.Result.Status != want.Status || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("observation discarded or retried: out=%#v err=%v calls=%d", got, err, calls)
			}
			encoded, marshalErr := nativeinspection.MarshalResult(got, "20000001", request)
			if marshalErr != nil || string(encoded) != string(body) {
				t.Fatalf("partial wire changed: %v", marshalErr)
			}
			if got.Error.NativeCode != nil && failure.Result.Error.NativeCode != nil {
				*got.Error.NativeCode = "123"
				if *failure.Result.Error.NativeCode != *want.Error.NativeCode {
					t.Fatal("returned result and typed error share mutable native fields")
				}
			}
		})
	}
}

func TestNativeClientRejectsMalformedNullableShapeAndHashes(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{LineCount: 1}}
	_, valid := nativeClientFixture(t, request, nativeClientReader{})
	mutate := func(change func(map[string]interface{})) []byte {
		var object map[string]interface{}
		if err := json.Unmarshal(valid, &object); err != nil {
			t.Fatal(err)
		}
		change(object)
		out, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	cases := map[string][]byte{
		"duplicate": append([]byte(`{"kind":"ram",`), valid[1:]...),
		"trailing":  append(append([]byte(nil), valid...), []byte(" {}")...),
		"oversized": []byte(strings.Repeat(" ", nativeinspection.MaxResponseBytes+1)),
		"array":     []byte("[]"), "null": []byte("null"),
		"unknown":          mutate(func(v map[string]interface{}) { v["unexpected"] = true }),
		"missing nullable": mutate(func(v map[string]interface{}) { delete(v, "playback") }),
		"null scalar":      mutate(func(v map[string]interface{}) { v["kind"] = nil }),
		"wrong serial":     mutate(func(v map[string]interface{}) { v["device"].(map[string]interface{})["serial"] = "00000000" }),
		"wrong range": mutate(func(v map[string]interface{}) {
			v["ram"].(map[string]interface{})["requested_range"].(map[string]interface{})["start_line"] = 1
		}),
		"digest changed": mutate(func(v map[string]interface{}) {
			v["ram"].(map[string]interface{})["sequence_digest"] = "sha256:" + strings.Repeat("0", 64)
		}),
		"RGB array short": mutate(func(v map[string]interface{}) {
			v["ram"].(map[string]interface{})["rows"].([]interface{})[0].(map[string]interface{})["rgb8"] = []int{16, 32}
		}),
		"base64 raw": mutate(func(v map[string]interface{}) {
			v["ram"].(map[string]interface{})["rows"].([]interface{})[0].(map[string]interface{})["report"].(map[string]interface{})["raw_hex"] = "AVIQIDAAADIB7g=="
		}),
		"lone surrogate": []byte(strings.Replace(string(valid), `"product":"blink(1) mk2"`, `"product":"\ud800"`, 1)),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				nativeClientHeaders(w)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			out, err := client.InspectNative(context.Background(), "20000001", request)
			var malformed *DecodeError
			if !errors.As(err, &malformed) || out.Contract != "" || calls != 1 {
				t.Fatalf("malformed response accepted/retried: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestNativeClientHeadersStatusAndNoRedirectFallback(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	_, body := nativeClientFixture(t, request, nativeClientReader{})
	for name, change := range map[string]func(http.ResponseWriter){
		"missing instance":   func(w http.ResponseWriter) { w.Header().Del("X-NB1-Daemon-Instance-ID") },
		"changed instance":   func(w http.ResponseWriter) { w.Header().Set("X-NB1-Daemon-Instance-ID", "daemon_other") },
		"duplicate instance": func(w http.ResponseWriter) { w.Header().Add("X-NB1-Daemon-Instance-ID", "daemon_fixture") },
		"Location":           func(w http.ResponseWriter) { w.Header().Set("Location", "/v1/jobs/unexpected") },
		"replayed":           func(w http.ResponseWriter) { w.Header().Set("X-NB1-Replayed", "false") },
		"media":              func(w http.ResponseWriter) { w.Header().Set("Content-Type", "text/plain") },
		"duplicate media":    func(w http.ResponseWriter) { w.Header().Add("Content-Type", "application/json") },
	} {
		t.Run(name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				nativeClientHeaders(w)
				change(w)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			out, err := client.InspectNative(context.Background(), "20000001", request)
			var malformed *DecodeError
			if !errors.As(err, &malformed) || out.Contract != "" || calls != 1 {
				t.Fatalf("headers accepted/retried: %v calls=%d", err, calls)
			}
		})
	}
	for _, status := range []int{201, 409, 501, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				nativeClientHeaders(w)
				w.WriteHeader(status)
				if status == 201 {
					_, _ = w.Write(body)
				} else {
					_, _ = io.WriteString(w, `{"error":{"code":"fixture_rejection","message":"fixture API rejection"}}`)
				}
			}))
			defer server.Close()
			client, _ := New(server.URL)
			defer client.Close()
			out, err := client.InspectNative(context.Background(), "20000001", request)
			var rejection *Error
			if !errors.As(err, &rejection) || rejection.Status != status || out.Contract != "" || calls != 1 || status != 201 && rejection.Code != "fixture_rejection" {
				t.Fatalf("rejection changed/retried: %v calls=%d", err, calls)
			}
		})
	}
	var originCalls, targetCalls int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&targetCalls, 1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&originCalls, 1)
		if r.Header.Get("Authorization") != "Bearer "+testClientToken {
			t.Error("native read omitted its Authorization header")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client, _ := NewWithToken(origin.URL, testClientToken)
	defer client.Close()
	_, err := client.InspectNative(context.Background(), "20000001", request)
	var rejection *Error
	if !errors.As(err, &rejection) || rejection.Status != 307 || originCalls != 1 || targetCalls != 0 {
		t.Fatalf("redirect followed/retried: %v origin=%d target=%d", err, originCalls, targetCalls)
	}
}

func TestNativeClientTransportFailureDoesNotRetry(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	if out, err := client.InspectNative(context.Background(), "20000001", nativeinspection.Request{Kind: nativeinspection.Playback}); err == nil || out.Contract != "" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("transport failure retried: %v calls=%d", err, calls)
	}
}

func TestFlashResetClientSinglePostAndBoundResult(t *testing.T) {
	const serial, preset = "20000002", "rainbow"
	complete := flashreset.Result{Serial: serial, Preset: preset, Firmware: 204, Lines: 16, Staged: 16, Verified: 16, Status: flashreset.Acknowledged}
	unconfirmed := complete
	unconfirmed.Status = flashreset.Unconfirmed
	partial := complete
	partial.Status, partial.Staged, partial.Verified = flashreset.StagingFailed, 3, 0
	wrong := complete
	wrong.Serial = "20000001"
	for _, tc := range []struct {
		name    string
		status  int
		wire    any
		want    flashreset.Result
		wantErr string
	}{
		{"acknowledged", 200, complete, complete, ""},
		{"staging failure", 502, httpapi.FlashResetFailure{Error: httpapi.ErrorBody{Code: "flash_staging_failed", Message: "RAM staging failed"}, Result: partial}, partial, "flash_staging_failed"},
		{"unconfirmed", 502, httpapi.FlashResetFailure{Error: httpapi.ErrorBody{Code: "flash_unconfirmed", Message: "Flash save outcome unconfirmed"}, Result: unconfirmed}, unconfirmed, "flash_unconfirmed"},
		{"consumed", 409, httpapi.ErrorResponse{Error: httpapi.ErrorBody{Code: "flash_ticket_used_or_expired", Message: "used or expired"}}, flashreset.Result{}, "outcome unknown"},
		{"unsupported", 422, httpapi.ErrorResponse{Error: httpapi.ErrorBody{Code: "unsupported_capability", Message: "unsupported"}}, flashreset.Result{}, "unsupported_capability"},
		{"wrong target", 200, wrong, flashreset.Result{}, "outcome unknown"},
		{"malformed", 200, map[string]string{"status": "acknowledged"}, flashreset.Result{}, "outcome unknown"},
		{"redirect", 307, map[string]string{}, flashreset.Result{}, "outcome unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const ticket = "0123456789abcdef0123456789abcdef"
			var calls, tickets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/devices/"+serial+"/flash/reset-ticket" {
					tickets.Add(1)
					if r.Header.Get("Authorization") != "Bearer "+testClientToken {
						t.Error("missing ticket credential")
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(httpapi.FlashResetTicket{Ticket: ticket})
					return
				}
				calls.Add(1)
				if r.Header.Get(httpapi.FlashTicketHeader) != ticket || r.Method != "POST" || r.URL.Path != "/v1/devices/"+serial+"/flash/reset" || r.Header.Get("Authorization") != "Bearer "+testClientToken {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				content, _ := io.ReadAll(r.Body)
				if string(content) != `{"preset":"rainbow"}` {
					t.Errorf("body %s", content)
				}
				if tc.status == 307 {
					w.Header().Set("Location", "/other")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.wire)
			}))
			defer server.Close()
			c, err := NewWithToken(server.URL, testClientToken)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.ResetFlash(t.Context(), "invalid", preset); err == nil {
				t.Fatal("accepted invalid serial")
			}
			if _, err := c.ResetFlash(t.Context(), serial, "typo"); err == nil {
				t.Fatal("accepted invalid preset")
			}
			got, err := c.ResetFlash(t.Context(), serial, preset)
			if got != tc.want || (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) || calls.Load() != 1 || tickets.Load() != 1 {
				t.Fatalf("result %+v, err %v, calls %d", got, err, calls.Load())
			}
		})
	}
}
