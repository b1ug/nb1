package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

const nativeRAMPath = "/v1/devices/20005601/native-sequence/ram"
const nativePlaybackPath = "/v1/devices/20005601/native-sequence/playback"

type nativeHTTPSpy struct {
	fakeService
	result       nativeinspection.Result
	err          error
	calls        int
	deviceCalls  int
	jobCalls     int
	cancelCalls  int
	serial       string
	request      nativeinspection.Request
	requestValue interface{}
	changeRange  bool
}

type nativeContextKey struct{}

func (s *nativeHTTPSpy) Device(serial string) (control.DeviceState, error) {
	s.deviceCalls++
	return s.fakeService.Device(serial)
}
func (s *nativeHTTPSpy) Job(id string) (control.Job, error) {
	s.jobCalls++
	return s.fakeService.Job(id)
}
func (s *nativeHTTPSpy) Cancel(ctx context.Context, id string) (control.Job, error) {
	s.cancelCalls++
	return s.fakeService.Cancel(ctx, id)
}
func (s *nativeHTTPSpy) InspectNative(ctx context.Context, serial string, request nativeinspection.Request) (nativeinspection.Result, error) {
	s.calls++
	if s.changeRange && request.Range != nil {
		request.Range.StartLine++
	}
	s.serial, s.request, s.requestValue = serial, request.Clone(), ctx.Value(nativeContextKey{})
	return s.result.Clone(), s.err
}

type nativeHTTPReader struct {
	read func(int) (nativeinspection.LineRead, error)
	play func() (nativeinspection.PlaybackRead, error)
}

func (r nativeHTTPReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	if r.read != nil {
		return r.read(position)
	}
	return nativeinspection.LineRead{Position: position, Reply: nativeinspection.Reply{
		RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9,
		LengthValid: true, Bytes: []byte{1, 'R', 16, 32, 48, 0, 50, 1, 0xee},
	}}, nil
}
func (r nativeHTTPReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	if r.play != nil {
		return r.play()
	}
	return nativeinspection.PlaybackRead{Reply: nativeinspection.Reply{
		RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9,
		LengthValid: true, Bytes: []byte{1, 'S', 1, 0, 2, 0, 1, 0, 0},
	}}, nil
}

func nativeHTTPFixture(t *testing.T, request nativeinspection.Request, reader nativeHTTPReader) (nativeinspection.Result, error) {
	t.Helper()
	firmware, lines := 205, 32
	profile := nativeinspection.Profile{
		SDKModel: "mk2", SDKReferenceFirmware: &firmware, SDKApplicability: "model-only", RAMLines: &lines,
		OperationQualification: "unverified",
		ReadReference:          nativeinspection.ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture-not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"},
		PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{
			Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &lines, Sources: []string{"fixture-not-hardware"},
		}, EndEncoding: "exclusive"},
	}
	result, err := nativeinspection.Collect(context.Background(), reader, request,
		nativeinspection.Identity{Serial: "20005601", Product: "blink(1) mk2", Firmware: 204, FirmwareSource: "cached-on-open"}, profile)
	result.DaemonInstanceID, result.DeviceSessionID = "daemon_fixture", "session_0000000000000001"
	if validation := nativeinspection.ValidateResult(result, "20005601", request); validation != nil {
		t.Fatalf("invalid test fixture: %v", validation)
	}
	return result, err
}

func nativeHTTP(t *testing.T, service control.Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:8931"+path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), nativeContextKey{}, "original-request-context"))
	w := httptest.NewRecorder()
	httpapi.New(service).ServeHTTP(w, r)
	return w
}

func assertNoNativeJobs(t *testing.T, s *nativeHTTPSpy, w *httptest.ResponseRecorder) {
	t.Helper()
	if s.submitCount != 0 || s.jobCalls != 0 || s.cancelCalls != 0 || w.Header().Get("Location") != "" || w.Header().Get("X-NB1-Replayed") != "" {
		t.Fatalf("inspection used Job or receipt operations: submit=%d query=%d cancel=%d headers=%v", s.submitCount, s.jobCalls, s.cancelCalls, w.Header())
	}
}

func sameNativeWire(got, want nativeinspection.Result) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && bytes.Equal(gotJSON, wantJSON)
}

func TestNativeInspectionExplicitRangeAndNoJob(t *testing.T) {
	for _, rangeValue := range []devicesequence.Range{{StartLine: 0, LineCount: 32}, {StartLine: 31, LineCount: 1}, {StartLine: 0, LineCount: 1}} {
		t.Run(fmt.Sprintf("%d-%d", rangeValue.StartLine, rangeValue.LineCount), func(t *testing.T) {
			request := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &rangeValue}
			result, err := nativeHTTPFixture(t, request, nativeHTTPReader{})
			if err != nil {
				t.Fatal(err)
			}
			service := &nativeHTTPSpy{result: result}
			w := nativeHTTP(t, service, "GET", fmt.Sprintf("%s?start_line=%d&line_count=%d", nativeRAMPath, rangeValue.StartLine, rangeValue.LineCount), "")
			var got nativeinspection.Result
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !sameNativeWire(got, result) || service.calls != 1 || service.deviceCalls != 1 || service.serial != "20005601" || !reflect.DeepEqual(service.request, request) || service.requestValue != "original-request-context" {
				t.Fatalf("status=%d calls=%d deviceCalls=%d body=%s", w.Code, service.calls, service.deviceCalls, w.Body)
			}
			if w.Header().Get("X-NB1-Daemon-Instance-ID") != result.DaemonInstanceID || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("headers=%v", w.Header())
			}
			if got.RAM.Rows[0].Report.RawHex != "0152102030003201ee" || got.RAM.Rows[0].Report.UnknownTailHex != "ee" {
				t.Fatalf("raw prefix or unknown tail changed: %#v", got.RAM.Rows[0].Report)
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

func TestNativeInspectionRejectsParametersBeforeService(t *testing.T) {
	for _, query := range []string{
		"", "?", "?start_line=0", "?line_count=1", "?start_line=&line_count=1", "?start_line=0&line_count=",
		"?start_line=0&line_count=1&target=flash", "?start_line=0&line_count=1&unexpected=", "?start_line=0&line_count=1&",
		"?start_line=0&start_line=0&line_count=1", "?start_line=0&line_count=1&line_count=1", "?start_line=0&start%5fline=1&line_count=1",
		"?start_line=-1&line_count=1", "?start_line=32&line_count=1", "?start_line=0&line_count=0", "?start_line=0&line_count=33", "?start_line=31&line_count=2",
		"?start_line=%2B1&line_count=1", "?start_line=1.0&line_count=1", "?start_line=0x1&line_count=1", "?start_line=1e0&line_count=1", "?start_line=%201&line_count=1",
		"?start_line=999999999999999999999999999999999&line_count=1", "?start_line=0&line_count=99999999999999999999999999999999", "?start_line=0;line_count=1", "?start_line=%zz&line_count=1",
	} {
		t.Run(query, func(t *testing.T) {
			service := &nativeHTTPSpy{}
			w := nativeHTTP(t, service, "GET", nativeRAMPath+query, "")
			if w.Code != 400 || service.calls != 0 || service.deviceCalls != 0 {
				t.Fatalf("status=%d nativeCalls=%d deviceCalls=%d body=%s", w.Code, service.calls, service.deviceCalls, w.Body)
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

func TestNativeInspectionMethodsBodiesAndUnknownTargets(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", nativeRAMPath + "?start_line=0&line_count=1", "", 405},
		{"HEAD", nativePlaybackPath, "", 405}, {"PUT", nativePlaybackPath, "", 405}, {"OPTIONS", nativePlaybackPath, "", 405}, {"DELETE", nativePlaybackPath, "", 405},
		{"GET", nativeRAMPath + "?start_line=0&line_count=1", "{}", 400}, {"GET", nativePlaybackPath, "{}", 400},
		{"GET", nativePlaybackPath + "?", "", 400}, {"GET", nativePlaybackPath + "?target=ram", "", 400}, {"GET", nativePlaybackPath + "?start_line=0&line_count=1", "", 400},
		{"GET", "/v1/devices/20005601/native-sequence/flash", "", 404}, {"GET", nativeRAMPath + "/extra", "", 404}, {"GET", nativePlaybackPath + "/", "", 404},
		{"GET", "/v1/devices/bad%20serial/native-sequence/playback", "", 400}, {"GET", "/v1/devices//native-sequence/playback", "", 400},
		{"GET", "/v1/devices/demo/native-sequence/playback", "", 400}, {"GET", "/v1/devices/2000560/native-sequence/playback", "", 400}, {"GET", "/v1/devices/200056010/native-sequence/playback", "", 400}, {"GET", "/v1/devices/2000560g/native-sequence/playback", "", 400},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			service := &nativeHTTPSpy{}
			w := nativeHTTP(t, service, tc.method, tc.path, tc.body)
			if w.Code != tc.status || service.calls != 0 || service.deviceCalls != 0 {
				t.Fatalf("status=%d calls=%d metadata=%d body=%s", w.Code, service.calls, service.deviceCalls, w.Body)
			}
			if tc.status == 405 && w.Header().Get("Allow") != "GET" {
				t.Fatalf("Allow=%s", w.Header().Get("Allow"))
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

func TestNativeInspectionOptionalServiceAndUnknownSerial(t *testing.T) {
	w := nativeHTTP(t, &fakeService{}, "GET", nativePlaybackPath, "")
	if w.Code != 501 {
		t.Fatalf("old service: %d %s", w.Code, w.Body)
	}
	service := &nativeHTTPSpy{}
	w = nativeHTTP(t, service, "GET", "/v1/devices/00000000/native-sequence/playback", "")
	if w.Code != 404 || service.calls != 0 || service.deviceCalls != 1 {
		t.Fatalf("unknown serial reached collection: status=%d calls=%d metadata=%d", w.Code, service.calls, service.deviceCalls)
	}
	assertNoNativeJobs(t, service, w)
}

func TestNativeInspectionNoCollectionErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{control.ErrInvalid, 400}, {nativeinspection.ErrInvalid, 400}, {control.ErrNotFound, 404}, {control.ErrBusy, 409},
		{control.ErrOffline, 503}, {control.ErrClosed, 503}, {nativeinspection.ErrUnsupported, 501}, {context.Canceled, 503}, {context.DeadlineExceeded, 503}, {errors.New("fixture failure"), 500},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			service := &nativeHTTPSpy{err: fmt.Errorf("wrapped: %w", tc.err)}
			w := nativeHTTP(t, service, "GET", nativePlaybackPath, "")
			if w.Code != tc.status || service.calls != 1 || w.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
				t.Fatalf("status=%d calls=%d header=%v body=%s", w.Code, service.calls, w.Header(), w.Body)
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

type nativeHTTPError struct{ code uint64 }

func (e nativeHTTPError) Error() string          { return "fixture native failure" }
func (e nativeHTTPError) NativePlatform() string { return "darwin" }
func (e nativeHTTPError) NativeCode() uint64     { return e.code }

func TestNativeInspectionPartialPreservesHexNullAndNativeCode(t *testing.T) {
	for _, code := range []uint64{0, ^uint64(0)} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			request := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 2}}
			reader := nativeHTTPReader{read: func(position int) (nativeinspection.LineRead, error) {
				if position == 0 {
					return (nativeHTTPReader{}).ReadLine(position)
				}
				return nativeinspection.LineRead{Position: position, Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 3, LengthValid: true, Bytes: []byte{1, 'R', 99}}}, nativeHTTPError{code}
			}}
			result, err := nativeHTTPFixture(t, request, reader)
			if err == nil {
				t.Fatal("fixture lost native error")
			}
			service := &nativeHTTPSpy{result: result, err: err}
			w := nativeHTTP(t, service, "GET", nativeRAMPath+"?start_line=0&line_count=2", "")
			var got nativeinspection.Result
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !sameNativeWire(got, result) || got.Complete || got.Status != "partial" || got.Error == nil || service.calls != 1 {
				t.Fatalf("partial discarded: status=%d calls=%d body=%s", w.Code, service.calls, w.Body)
			}
			row := got.RAM.Rows[1]
			if row.Report.RawHex != "015263" || row.RGB8 != nil || row.DurationTicks != nil || row.LEDCode != nil || row.Error == nil || row.Error.NativeCode == nil || *row.Error.NativeCode != fmt.Sprint(code) {
				t.Fatalf("partial raw/native fields changed: %#v", row)
			}
			if !strings.Contains(w.Body.String(), `"rgb8":null`) || !strings.Contains(w.Body.String(), `"native_code":"`+fmt.Sprint(code)+`"`) {
				t.Fatalf("null or decimal-string contract changed: %s", w.Body)
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

func TestNativeInspectionFailedRequestIsStillACollection(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	result, err := nativeHTTPFixture(t, request, nativeHTTPReader{play: func() (nativeinspection.PlaybackRead, error) {
		return nativeinspection.PlaybackRead{Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9}}, errors.New("fixture request write failed")
	}})
	service := &nativeHTTPSpy{result: result, err: err}
	w := nativeHTTP(t, service, "GET", nativePlaybackPath, "")
	var got nativeinspection.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !sameNativeWire(got, result) || got.Complete || got.Status != "failed" || got.Playback.Report.ReadAttempted || got.Error == nil {
		t.Fatalf("failed request collection was discarded: status=%d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"native_code":null`) || !strings.Contains(w.Body.String(), `"mode":null`) {
		t.Fatalf("unavailable fields were not null: %s", w.Body)
	}
	assertNoNativeJobs(t, service, w)
}

func TestNativeInspectionRejectsMalformedResultsEvenWithoutError(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	valid, err := nativeHTTPFixture(t, request, nativeHTTPReader{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*nativeinspection.Result){
		"contract": func(r *nativeinspection.Result) { r.Contract = "wrong" },
		"instance": func(r *nativeinspection.Result) { r.DaemonInstanceID = "" },
		"session":  func(r *nativeinspection.Result) { r.DeviceSessionID = "" },
		"serial":   func(r *nativeinspection.Result) { r.Device.Serial = "another_device" },
		"kind":     func(r *nativeinspection.Result) { r.Kind = nativeinspection.RAM },
		"time":     func(r *nativeinspection.Result) { r.StartedAt = time.Time{} },
		"status":   func(r *nativeinspection.Result) { r.Status = "" },
		"raw hex":  func(r *nativeinspection.Result) { r.Playback.Report.RawHex = "not-hex" },
	} {
		t.Run(name, func(t *testing.T) {
			result := valid.Clone()
			mutate(&result)
			service := &nativeHTTPSpy{result: result}
			w := nativeHTTP(t, service, "GET", nativePlaybackPath, "")
			if w.Code != 500 || service.calls != 1 || w.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
				t.Fatalf("malformed fake accepted: status=%d header=%v body=%s", w.Code, w.Header(), w.Body)
			}
		})
	}
	for _, service := range []*nativeHTTPSpy{{}, {result: valid, err: errors.New("unexpected extra service error")}} {
		w := nativeHTTP(t, service, "GET", nativePlaybackPath, "")
		if w.Code != 500 {
			t.Fatalf("inconsistent result accepted: status=%d body=%s", w.Code, w.Body)
		}
	}
}

func TestNativeInspectionSameOriginAndLoopback(t *testing.T) {
	for _, tc := range []struct {
		host, origin string
		status       int
	}{
		{"127.0.0.1:8931", "", 200}, {"localhost:8931", "http://localhost:8931", 200}, {"[::1]:8931", "http://[::1]:8931", 200},
		{"evil.invalid:8931", "", 403}, {"127.0.0.1:8931", "http://evil.invalid", 403}, {"127.0.0.1:8931", "http://127.0.0.1:4173", 403}, {"127.0.0.1:8931", "null", 403}, {"127.0.0.1:8931", "https://127.0.0.1:8931", 403},
	} {
		t.Run(tc.host+tc.origin, func(t *testing.T) {
			result, err := nativeHTTPFixture(t, nativeinspection.Request{Kind: nativeinspection.Playback}, nativeHTTPReader{})
			if err != nil {
				t.Fatal(err)
			}
			service := &nativeHTTPSpy{result: result}
			r := httptest.NewRequest("GET", "http://127.0.0.1:8931"+nativePlaybackPath, nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			httpapi.New(service).ServeHTTP(w, r)
			wantCalls := 0
			if tc.status == 200 {
				wantCalls = 1
			}
			if w.Code != tc.status || service.calls != wantCalls || service.deviceCalls != wantCalls || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d calls=%d metadata=%d header=%v", w.Code, service.calls, service.deviceCalls, w.Header())
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}

func TestNativeInspectionReplyRemainsBoundToOriginalRange(t *testing.T) {
	otherRequest := nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 1, LineCount: 1}}
	result, err := nativeHTTPFixture(t, otherRequest, nativeHTTPReader{})
	if err != nil {
		t.Fatal(err)
	}
	service := &nativeHTTPSpy{result: result, changeRange: true}
	w := nativeHTTP(t, service, "GET", nativeRAMPath+"?start_line=0&line_count=1", "")
	if w.Code != 500 || service.calls != 1 || w.Header().Get("X-NB1-Daemon-Instance-ID") != "" {
		t.Fatalf("service moved the request target: status=%d calls=%d header=%v body=%s", w.Code, service.calls, w.Header(), w.Body)
	}
	assertNoNativeJobs(t, service, w)
}

func TestNativeInspectionResponseByteBudgetIncludesNewline(t *testing.T) {
	request := nativeinspection.Request{Kind: nativeinspection.Playback}
	base, err := nativeHTTPFixture(t, request, nativeHTTPReader{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		target int
		status int
	}{
		{"aggregate exceeds field limits", -1, 500},
		{"one newline byte remains", nativeinspection.MaxResponseBytes - 1, 200},
		{"encoded alone fills budget", nativeinspection.MaxResponseBytes, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := base.Clone()
			if tc.target < 0 {
				result.Playback.Issues = make([]devicesequence.Issue, 128)
				for i := range result.Playback.Issues {
					result.Playback.Issues[i] = devicesequence.Issue{Code: "fixture_issue", Path: "observation", Message: strings.Repeat("x", 2048)}
				}
			} else {
				result.Playback.Issues = make([]devicesequence.Issue, 32)
				for i := range result.Playback.Issues {
					result.Playback.Issues[i] = devicesequence.Issue{Code: "fixture_issue", Path: "observation"}
				}
				encoded, _ := json.Marshal(result)
				remaining := tc.target - len(encoded)
				for i := range result.Playback.Issues {
					n := remaining
					if n > 2048 {
						n = 2048
					}
					if n < 0 {
						t.Fatal("fixture already exceeds target")
					}
					result.Playback.Issues[i].Message = strings.Repeat("x", n)
					remaining -= n
				}
				if remaining != 0 {
					t.Fatal("fixture cannot reach exact target")
				}
			}
			if err := nativeinspection.ValidateResult(result, "20005601", request); err != nil {
				t.Fatalf("must exercise aggregate byte guard after field validation: %v", err)
			}
			encoded, _ := json.Marshal(result)
			if tc.target >= 0 && len(encoded) != tc.target || tc.target < 0 && len(encoded) <= nativeinspection.MaxResponseBytes {
				t.Fatalf("wrong fixture size %d", len(encoded))
			}
			service := &nativeHTTPSpy{result: result}
			w := nativeHTTP(t, service, "GET", nativePlaybackPath, "")
			if w.Code != tc.status || service.calls != 1 || w.Body.Len() > nativeinspection.MaxResponseBytes {
				t.Fatalf("status=%d body bytes=%d calls=%d", w.Code, w.Body.Len(), service.calls)
			}
			if tc.status == 200 {
				if w.Body.Len() != nativeinspection.MaxResponseBytes {
					t.Fatalf("HTTP newline was not counted: %d", w.Body.Len())
				}
				if _, err := nativeinspection.DecodeResult(w.Body.Bytes(), "20005601", request); err != nil {
					t.Fatalf("maximum valid HTTP body cannot be decoded by clients: %v", err)
				}
			} else if w.Header().Get("X-NB1-Daemon-Instance-ID") != "" || !strings.Contains(w.Body.String(), "invalid_native_inspection_result") {
				t.Fatalf("oversized fake exposed a collection envelope: headers=%v", w.Header())
			}
			assertNoNativeJobs(t, service, w)
		})
	}
}
