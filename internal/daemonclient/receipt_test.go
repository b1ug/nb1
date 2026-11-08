package daemonclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/b1ug/nb1/internal/control"
)

func TestPrepareReceiptUsesServerDeadlineAndRandomNonce(t *testing.T) {
	info := control.InstanceInfo{DaemonInstanceID: "daemon_0123456789abcdef0123456789abcdef", Idempotency: control.IdempotencyInfo{Supported: true, RequestIDPrefix: "req_60000_"}}
	prepared, err := prepareReceipt(info, strings.NewReader(strings.Repeat("\xab", 16)))
	if err != nil || prepared.ExpectedDaemonInstanceID != info.DaemonInstanceID || prepared.RequestID != "req_60000_abababababababababababababababab" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	if _, err := prepareReceipt(info, strings.NewReader("short")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("random source failure ignored: %v", err)
	}
	for _, prefix := range []string{"", "req_0_", "req_01_", "req_+1_", "req_1e3_", "req_9223372036854775808_", "req_100_abc"} {
		invalid := info
		invalid.Idempotency.RequestIDPrefix = prefix
		if _, err := prepareReceipt(invalid, strings.NewReader(strings.Repeat("a", 16))); err == nil {
			t.Fatalf("invalid prefix accepted: %q", prefix)
		}
	}
	info.Idempotency.Supported = false
	if _, err := prepareReceipt(info, strings.NewReader(strings.Repeat("a", 16))); err == nil {
		t.Fatal("unsupported daemon prepared a binding")
	}
}

func TestReceiptClientPreservesUnknownAndNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		status                   int
		body, instance, replayed string
		unknown                  bool
	}{
		{410, `{"error":{"code":"request_expired","message":"expired"}}`, "", "", true},
		{409, `{"error":{"code":"daemon_instance_changed","message":"restarted"}}`, "", "", true},
		{404, `{"error":{"code":"request_not_found","message":"not found at lookup time"}}`, "", "", true},
		{429, `{"error":{"code":"idempotency_capacity","message":"full"}}`, "", "", false},
		{202, `{"id":"job-1"}`, "daemon_other", "false", true},
		{202, `{"id":"job-1"}`, "", "", true},
		{202, `broken`, "", "", true},
	} {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("X-NB1-Daemon-Instance-ID", tc.instance)
			w.Header().Set("X-NB1-Replayed", tc.replayed)
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		client, err := New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		binding := control.ReceiptRequest{RequestID: "req_60000_0123456789abcdef0123456789abcdef", ExpectedDaemonInstanceID: "daemon_0123456789abcdef0123456789abcdef"}
		_, err = client.SubmitWithReceipt(context.Background(), "20005601", control.Program{}, "replace", binding)
		var receiptErr *ReceiptError
		if !errors.As(err, &receiptErr) || receiptErr.Receipt != binding || receiptErr.OutcomeUnknown != tc.unknown || atomic.LoadInt32(&calls) != 1 {
			t.Fatalf("status=%d: error=%v calls=%d", tc.status, err, calls)
		}
		client.Close()
		server.Close()
	}
}

func TestReceiptLookupFailureKeepsOriginalOutcomeUnknown(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{
		{http.StatusNotFound, "not_found"}, // Replaced by an older daemon.
		{http.StatusNotImplemented, "idempotency_unsupported"},
		{http.StatusBadRequest, "invalid_request"},
		{http.StatusTooManyRequests, "rate_limited"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if r.Method != http.MethodGet {
					t.Errorf("lookup unexpectedly sent %s", r.Method)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"code":"`+tc.code+`","message":"lookup unavailable"}}`)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			binding := control.ReceiptRequest{RequestID: "req_60000_0123456789abcdef0123456789abcdef", ExpectedDaemonInstanceID: "daemon_0123456789abcdef0123456789abcdef"}
			_, err = client.Request(context.Background(), "20005601", binding)
			var receiptErr *ReceiptError
			if !errors.As(err, &receiptErr) || !receiptErr.OutcomeUnknown || receiptErr.Receipt != binding || atomic.LoadInt32(&calls) != 1 {
				t.Fatalf("lookup lost original uncertainty or retried: %v calls=%d", err, calls)
			}
		})
	}
}
