package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/colorinput"
)

const greenResolutionJSON = `{"schema_version":1,"color_contract":"nb1-color-input/v1","input_kind":"name","canonical_color":"#008000","rgb8":{"r":0,"g":128,"b":0},"palette":"css-named-2026"}`

func TestColorClientPostsOnlyStrictResolutionEnvelope(t *testing.T) {
	for _, raw := range []string{`"green"`, `"#12AbEF"`, `"#0f8"`, `{"space":"rgb8","r":0,"g":255,"b":136}`, `{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50}`} {
		t.Run(raw, func(t *testing.T) {
			expected, err := colorinput.ResolveJSON([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/colors/resolve" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected color route: %s %s %s", r.Method, r.URL, r.Header.Get("Content-Type"))
				}
				var envelope map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || len(envelope) != 1 || string(envelope["color"]) != raw {
					t.Errorf("color intent changed or execution fields added: %s %v", envelope, err)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_ = json.NewEncoder(w).Encode(expected)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			actual, err := client.ResolveColor(context.Background(), json.RawMessage(raw))
			if err != nil || !reflect.DeepEqual(actual, expected) || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("resolution: %#v %v calls=%d", actual, err, calls)
			}
		})
	}
}

func TestColorClientRejectsInvalidInputBeforeHTTP(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { atomic.AddInt32(&calls, 1); w.WriteHeader(500) }))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, raw := range []string{
		``, `null`, `[]`, `""`, `"random"`, `"rgb(0,255,0)"`, `"transparent"`,
		`{"color":"green"}`, `{"space":"rgb8","r":0,"g":1}`, `{"space":"rgb8","r":0,"g":1,"b":256}`,
		`{"space":"rgb8","r":0,"g":1.0,"b":2}`, `{"space":"rgb8","r":0,"g":1,"b":2,"\u0072":0}`,
		`{"space":"hsv","h_deg":120,"s_pct":100,"v_pct":50,"V_pct":50}`, `"green" {}`,
		`"green"` + strings.Repeat(" ", colorinput.MaxRequestBytes),
	} {
		if out, err := client.ResolveColor(context.Background(), json.RawMessage(raw)); err == nil || !reflect.DeepEqual(out, colorinput.Result{}) {
			t.Fatalf("invalid input reached resolution: %s %#v %v", raw, out, err)
		}
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("invalid color made %d requests", calls)
	}
}

func TestColorClientRejectsResponseProtocolAndSwappedColor(t *testing.T) {
	good := greenResolutionJSON
	tests := []struct {
		name, body, media string
		status            int
	}{
		{"accepted202", good, "application/json", 202},
		{"empty204", "", "application/json", 204},
		{"wrongMedia", good, "text/plain", 200},
		{"missingMedia", good, "", 200},
		{"null", "null", "application/json", 200},
		{"array", "[]", "application/json", 200},
		{"trailing", good + `{}`, "application/json", 200},
		{"unknownJob", strings.TrimSuffix(good, "}") + `,"job":{}}`, "application/json", 200},
		{"schemaAlias", strings.Replace(good, "schema_version", "Schema_version", 1), "application/json", 200},
		{"escapedDuplicate", strings.TrimSuffix(good, "}") + `,"schema_\u0076ersion":1}`, "application/json", 200},
		{"nestedDuplicate", strings.Replace(good, `"r":0`, `"r":0,"\u0072":0`, 1), "application/json", 200},
		{"missingZero", strings.Replace(good, `"r":0,`, ``, 1), "application/json", 200},
		{"nullZero", strings.Replace(good, `"r":0`, `"r":null`, 1), "application/json", 200},
		{"schema2", strings.Replace(good, `"schema_version":1`, `"schema_version":2`, 1), "application/json", 200},
		{"unknownContract", strings.Replace(good, "nb1-color-input/v1", "future/v2", 1), "application/json", 200},
		{"unknownPalette", strings.Replace(good, "css-named-2026", "future", 1), "application/json", 200},
		{"missingPalette", strings.Replace(good, `,"palette":"css-named-2026"`, ``, 1), "application/json", 200},
		{"unknownKind", strings.Replace(good, `"input_kind":"name"`, `"input_kind":"css"`, 1), "application/json", 200},
		{"uppercaseHex", strings.Replace(good, "#008000", "#00A000", 1), "application/json", 200},
		{"rgbMismatch", strings.Replace(good, `"g":128`, `"g":127`, 1), "application/json", 200},
		{"swappedValidColor", strings.Replace(strings.Replace(good, "#008000", "#00ff00", 1), `"g":128`, `"g":255`, 1), "application/json", 200},
		{"sameColorWrongInputKind", strings.Replace(strings.Replace(good, `"input_kind":"name"`, `"input_kind":"hex6"`, 1), `,"palette":"css-named-2026"`, ``, 1), "application/json", 200},
		{"oversized", good + strings.Repeat(" ", 2<<20), "application/json", 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.media != "" {
					w.Header().Set("Content-Type", tc.media)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			out, err := client.ResolveColor(context.Background(), json.RawMessage(`"green"`))
			if err == nil || !reflect.DeepEqual(out, colorinput.Result{}) || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("invalid response exposed or retried: %#v %v calls=%d", out, err, calls)
			}
			var receipt *ReceiptError
			if errors.As(err, &receipt) {
				t.Fatal("color error incorrectly classified as unknown Job submission")
			}
		})
	}
}

func TestColorClientUnsupportedAndRedirectNeverFallback(t *testing.T) {
	for _, status := range []int{404, 501, 307} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Location", "/v1/devices")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("old daemon"))
		}))
		client, err := New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		out, err := client.ResolveColor(context.Background(), json.RawMessage(`"green"`))
		var failure *Error
		if !errors.As(err, &failure) || failure.Status != status || !reflect.DeepEqual(out, colorinput.Result{}) || atomic.LoadInt32(&calls) != 1 {
			t.Fatalf("unsupported/redirect retried: %#v %v calls=%d", out, err, calls)
		}
		if status != 307 && (failure.Code != "color_resolution_unsupported" || !strings.Contains(failure.Message, "no direct fallback")) {
			t.Fatalf("unsupported code: %#v", failure)
		}
		client.Close()
		server.Close()
	}
}

func TestColorClientPreservesCallerDeadlineWithoutRetry(t *testing.T) {
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
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := client.ResolveColor(cancelled, json.RawMessage(`"green"`)); !errors.Is(err, context.Canceled) || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("cancelled request contacted daemon: %v calls=%d", err, calls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.ResolveColor(ctx, json.RawMessage(`"green"`))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("caller deadline lost or retried: %v calls=%d", err, calls)
	}
}
