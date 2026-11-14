package transporttrace

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"
)

type nativeTestError struct {
	code uint64
	hint string
}

func (e nativeTestError) Error() string          { return e.hint }
func (e nativeTestError) NativeCode() uint64     { return e.code }
func (e nativeTestError) NativePlatform() string { return "darwin" }

type platformTestError struct {
	nativeTestError
	platform string
}

func (e platformTestError) NativePlatform() string { return e.platform }

func TestTraceFixedStorageBudget(t *testing.T) {
	size := int(unsafe.Sizeof(Event{}))
	if size > MaxRecordBytes || size*(Capacity+QueueCapacity) >= 1<<20 || size*(2*Capacity+QueueCapacity) >= 2<<20 {
		t.Fatalf("event %d bytes exceeds backing budget", size)
	}
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				check(typ.Field(i).Type)
			}
		case reflect.Array:
			check(typ.Elem())
		case reflect.Bool, reflect.Uint8, reflect.Uint32, reflect.Uint64, reflect.Int32, reflect.Int64:
		default:
			t.Fatalf("record retains variable/pointer data: %v", typ)
		}
	}
	check(reflect.TypeOf(Event{}))
	t.Logf("fixed event bytes=%d; ring+queue=%d; with one snapshot=%d", size, size*(Capacity+QueueCapacity), size*(2*Capacity+QueueCapacity))
}

func eventObject(t *testing.T, e Event) map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(b) || len(b)+1 > MaxLineBytes {
		t.Fatalf("invalid/oversize JSON: %d bytes", len(b))
	}
	var v map[string]interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTraceAttemptTimingIdentityZeroAndUTF8(t *testing.T) {
	now := time.Unix(10, 0)
	r := newRecorder(func() time.Time { v := now; now = now.Add(time.Millisecond); return v })
	r.SetInstance("daemon_" + strings.Repeat("1", 32))
	s := EmptyScope(FrameTarget)
	s.JobID = "framejob_" + strings.Repeat("a", 32)
	s.Pattern = true
	s.Frame = true
	s.PatternID = "orbit"
	s.Revision = "sha256:" + strings.Repeat("b", 64)
	s.Position.Repeat = 0
	s.Position.Frame = 0
	s.Position.ExecutedFrame = 0
	s.Position.Target = 0
	op := r.Operation("20005601", s, SetColor, Target{LED: 1, RGB: [3]byte{0, 0, 153}, FadeMS: 750, HasColor: true})
	a := op.Begin(1)
	if r.Stats().InFlight != 1 || r.Snapshot().Events[0].End {
		t.Fatal("in-flight begin missing before native return")
	}
	a.End(fmt.Errorf("SDK: %w", nativeTestError{0, strings.Repeat("色", 100)}), false, false, 0)
	snap := r.Snapshot()
	if len(snap.Events) != 2 || snap.Stats.InFlight != 0 || snap.Events[1].DurationNS != int64(time.Millisecond) {
		t.Fatalf("timing/edges: %+v", snap.Stats)
	}
	end := eventObject(t, snap.Events[1])
	if end["native_code"] != float64(0) || end["native_platform"] != "darwin" || end["hint_truncated"] != true || !utf8.ValidString(end["sdk_error_hint"].(string)) {
		t.Fatalf("native/UTF8 evidence: %+v", end)
	}
	if len(end["sdk_error_hint"].(string)) > 160 || end["job_id"] != s.JobID || end["revision"] != s.Revision || end["phase"] != "frame_target" {
		t.Fatal(end)
	}
	pos := end["position"].(map[string]interface{})
	if pos["frame"] != float64(0) || pos["target"] != float64(0) {
		t.Fatal("zero index omitted")
	}
	op.Begin(2).End(nil, false, false, 0)
	good := eventObject(t, r.Snapshot().Events[3])
	if _, ok := good["native_code"]; ok {
		t.Fatal("missing native code converted to success0")
	}
	if _, ok := good["sdk_error_hint"]; ok {
		t.Fatal("previous error leaked into success")
	}
	r.Close()
	r.Close()
}

func TestTraceMaximumEncodedRecordAndPrivacy(t *testing.T) {
	r := New()
	s := EmptyScope(FrameTarget)
	s.JobID = "framejob_" + strings.Repeat("a", 32)
	s.Pattern = true
	s.Frame = true
	s.PatternID = strings.Repeat("x", 64)
	s.Revision = "sha256:" + strings.Repeat("c", 64)
	s.Mode = "hold"
	s.Position = Position{9999, 1023, 1023, 1023, 1, 1}
	r.Operation("20005601", s, SetColor, Target{LED: 2, RGB: [3]byte{255, 255, 255}, FadeMS: 600000, HasColor: true}).Begin(3).End(nativeTestError{^uint64(0), strings.Repeat("<", 160)}, true, false, 0)
	for _, e := range r.Snapshot().Events {
		eventObject(t, e)
	}
	encoded, _ := json.Marshal(r.Snapshot().Events)
	for _, word := range []string{"request_id", "source", "title", "library", "path", "report_buffer"} {
		if strings.Contains(string(encoded), `"`+word+`"`) {
			t.Fatalf("private field leaked: %s", word)
		}
	}
	r.Close()
}

func TestTraceBoundedLossDetachedSnapshotsAndConcurrentClose(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for serial := 0; serial < 64; serial++ {
		wg.Add(1)
		go func(serial int) {
			defer wg.Done()
			for i := 0; i < 32; i++ {
				r.Operation(fmt.Sprintf("%08d", serial), EmptyScope(IdleObserve), QueryRGB, Target{LED: 1}).Begin(1).End(nil, false, false, 0)
			}
		}(serial)
	}
	wg.Wait()
	s := r.Snapshot()
	want := uint64(64 * 32 * 2)
	if len(s.Events) != Capacity || s.Stats.Events != want || s.Stats.Evicted != want-Capacity || s.Stats.QueueDropped != want-QueueCapacity || s.Stats.InFlight != 0 {
		t.Fatal(s.Stats)
	}
	for i, e := range s.Events {
		if e.Sequence != s.Stats.EarliestSequence+uint64(i) {
			t.Fatal("unordered ring snapshot")
		}
	}
	s.Events[0].Serial[0] = 'x'
	if r.Snapshot().Events[0].Serial[0] == 'x' {
		t.Fatal("snapshot aliases ring")
	}
	wg.Add(2)
	go func() { defer wg.Done(); r.Close() }()
	go func() {
		defer wg.Done()
		for i := 0; i < 64; i++ {
			r.Operation("20005601", EmptyScope(IdleObserve), QueryRGB, Target{}).Begin(1).End(nil, false, false, 0)
		}
	}()
	wg.Wait()
	if !r.Stats().Closed {
		t.Fatal("not closed")
	}
}

func TestTraceRejectsInvalidScopeEnumsWithoutExecutionAuthority(t *testing.T) {
	r := New()
	for _, s := range []Scope{{Phase: Phase(255)}, {JobID: strings.Repeat("a", 42)}, {PatternID: "\n"}, {Revision: strings.Repeat("b", 72)}, {Mode: "reset"}, {Position: Position{Repeat: -2}}} {
		if r.Operation("20005601", s, SetColor, Target{}) != nil {
			t.Fatalf("invalid scope retained: %+v", s)
		}
	}
	if r.Operation("123456789", EmptyScope(IdleObserve), QueryRGB, Target{}) != nil || r.Operation("20005601", EmptyScope(IdleObserve), Operation(255), Target{}) != nil {
		t.Fatal("invalid serial/kind")
	}
	if r.Stats().InvalidScopes != 8 || r.Stats().Events != 0 {
		t.Fatal(r.Stats())
	}
	var nilOp *OperationTrace
	nilOp.Begin(1).End(errors.New("ignored"), false, false, 0)
	r.Close()
}

func TestTraceUTF8BoundsAndKnownBuildIdentity(t *testing.T) {
	for _, value := range []string{strings.Repeat("雪", 100), "x\xffy", strings.Repeat("<", 160)} {
		var dest [16]byte
		n, cut, replaced := copyUTF8(dest[:], value)
		if n > 16 || !utf8.Valid(dest[:n]) || cut != (len(value) > 16) || replaced != !utf8.ValidString(value) {
			t.Fatalf("copy(%q): n=%d cut=%v replaced=%v", value, n, cut, replaced)
		}
	}
	r := New()
	defer r.Close()
	r.SetSource("bad", false)
	if r.Header().SourceKnown {
		t.Fatal("unverified build identity claimed")
	}
	r.SetSource(strings.Repeat("a", 40), false)
	h := r.Header()
	if !h.SourceKnown || h.SourceRevision != strings.Repeat("a", 40) || h.SourceModified {
		t.Fatal(h)
	}
	now := time.Unix(1, 0)
	clock := newRecorder(func() time.Time { return now })
	defer clock.Close()
	a := clock.Operation("20005601", EmptyScope(IdleObserve), QueryRGB, Target{}).Begin(1)
	now = now.Add(10 * time.Millisecond)
	a.Returned()
	now = now.Add(time.Second)
	a.End(errors.New("classified after SDK returned"), false, false, 0)
	end := clock.Snapshot().Events[1]
	if end.DurationNS != int64(10*time.Millisecond) || end.MonotonicNS != int64(10*time.Millisecond) {
		t.Fatal("classification time charged to SDK", end)
	}
	for _, platform := range []string{"", strings.Repeat("雪", 10), "x\xffy"} {
		op := r.Operation("20005601", EmptyScope(IdleObserve), QueryRGB, Target{})
		op.Begin(1).End(platformTestError{nativeTestError{7, "general error"}, platform}, false, false, 0)
		wire := eventObject(t, r.Snapshot().Events[len(r.Snapshot().Events)-1])
		if platform == "" {
			if _, ok := wire["native_code"]; ok {
				t.Fatal("empty platform invented code namespace", wire)
			}
		} else if len(platform) > 16 {
			if wire["native_platform_truncated"] != true || !utf8.ValidString(wire["native_platform"].(string)) {
				t.Fatal(wire)
			}
		} else if !utf8.ValidString(platform) && wire["native_platform_utf8_replaced"] != true {
			t.Fatal(wire)
		}
	}
}
