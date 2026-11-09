// Package daemonclient communicates with the local HTTP service; it never opens USB.
package daemonclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/httpapi"
)

const DefaultEndpoint = "http://127.0.0.1:8931"

type Client struct {
	endpoint string
	http     *http.Client
}

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return fmt.Sprintf("daemon %s (%d): %s", e.Code, e.Status, e.Message) }

// DecodeError means an HTTP response arrived but its body could not be decoded.
// It is distinct from an API rejection: callers cannot infer a submission's
// outcome from malformed response data.
type DecodeError struct {
	Status int
	Err    error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("decode daemon response: %v", e.Err) }
func (e *DecodeError) Unwrap() error { return e.Err }

// ReceiptError preserves the binding after an uncertain submission or lookup.
// Expiry/instance changes cannot establish whether the original intent ran.
type ReceiptError struct {
	Err            error
	Receipt        control.ReceiptRequest
	OutcomeUnknown bool
}

func (e *ReceiptError) Error() string {
	return fmt.Sprintf("%v (request_id=%s expected_daemon_instance_id=%s outcome_unknown=%t; do not automatically replace the binding)", e.Err, e.Receipt.RequestID, e.Receipt.ExpectedDaemonInstanceID, e.OutcomeUnknown)
}
func (e *ReceiptError) Unwrap() error { return e.Err }

func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid daemon endpoint: %w", err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || !(strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) {
		return nil, fmt.Errorf("daemon endpoint must be a loopback HTTP origin, for example %s", DefaultEndpoint)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: &http.Client{
		Timeout: 10 * time.Second, Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// Health describes the HTTP service only; it does not prove a device is online.
type Health struct {
	Status     string `json:"status"`
	APIVersion string `json:"api_version"`
}

func (c *Client) Health(ctx context.Context) (out Health, err error) {
	err = c.do(ctx, http.MethodGet, "/healthz", nil, &out)
	return
}

func (c *Client) Devices(ctx context.Context) (out []control.DeviceState, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/devices", nil, &out)
	return
}
func (c *Client) Device(ctx context.Context, serial string) (out control.DeviceState, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(serial), nil, &out)
	return
}
func (c *Client) Submit(ctx context.Context, serial string, program control.Program, policy string) (out control.Job, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", httpapi.SubmitRequest{Program: &program, Policy: policy}, &out)
	return
}

// SubmitEffect sends the high-level intent to the daemon's authoritative
// compiler; this HTTP adapter does not maintain a second effect compiler.
func (c *Client) SubmitEffect(ctx context.Context, serial string, effect control.Effect, policy string) (out control.Job, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", httpapi.SubmitRequest{Effect: &effect, Policy: policy}, &out)
	return
}

func (c *Client) Instance(ctx context.Context) (out control.InstanceInfo, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/instance", nil, &out)
	return
}

// Prepare generates a binding without reserving a receipt or submitting a job.
func (c *Client) Prepare(ctx context.Context) (control.ReceiptRequest, error) {
	info, err := c.Instance(ctx)
	if err != nil {
		return control.ReceiptRequest{}, err
	}
	return prepareReceipt(info, rand.Reader)
}

func prepareReceipt(info control.InstanceInfo, random io.Reader) (control.ReceiptRequest, error) {
	if !info.Idempotency.Supported {
		return control.ReceiptRequest{}, &Error{Status: http.StatusNotImplemented, Code: "idempotency_unsupported", Message: "daemon does not support request receipts"}
	}
	instance := info.DaemonInstanceID
	prefix := info.Idempotency.RequestIDPrefix
	if len(instance) != 39 || !strings.HasPrefix(instance, "daemon_") || !lowerHex(instance[7:]) ||
		!strings.HasPrefix(prefix, "req_") || !strings.HasSuffix(prefix, "_") || len(prefix) < 6 || len(prefix) > 24 {
		return control.ReceiptRequest{}, fmt.Errorf("invalid daemon request preparation metadata")
	}
	deadline := prefix[4 : len(prefix)-1]
	if deadline[0] == '0' {
		return control.ReceiptRequest{}, fmt.Errorf("invalid daemon request prefix deadline")
	}
	for _, digit := range deadline {
		if digit < '0' || digit > '9' {
			return control.ReceiptRequest{}, fmt.Errorf("invalid daemon request prefix deadline")
		}
	}
	if value, err := strconv.ParseInt(deadline, 10, 64); err != nil || value <= 0 {
		return control.ReceiptRequest{}, fmt.Errorf("invalid daemon request prefix deadline")
	}
	var nonce [16]byte
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return control.ReceiptRequest{}, fmt.Errorf("generate request nonce: %w", err)
	}
	return control.ReceiptRequest{RequestID: prefix + hex.EncodeToString(nonce[:]), ExpectedDaemonInstanceID: instance}, nil
}

func lowerHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func (c *Client) SubmitWithReceipt(ctx context.Context, serial string, program control.Program, policy string, receipt control.ReceiptRequest) (out control.ReceiptResult, err error) {
	return c.submitWithReceipt(ctx, serial, httpapi.SubmitRequest{Program: &program, Policy: policy}, receipt)
}

func (c *Client) SubmitEffectWithReceipt(ctx context.Context, serial string, effect control.Effect, policy string, receipt control.ReceiptRequest) (out control.ReceiptResult, err error) {
	return c.submitWithReceipt(ctx, serial, httpapi.SubmitRequest{Effect: &effect, Policy: policy}, receipt)
}

func (c *Client) submitWithReceipt(ctx context.Context, serial string, input httpapi.SubmitRequest, receipt control.ReceiptRequest) (out control.ReceiptResult, err error) {
	if receipt.RequestID == "" || receipt.ExpectedDaemonInstanceID == "" {
		return out, fmt.Errorf("request_id and expected_daemon_instance_id must both be nonempty")
	}
	input.RequestID, input.ExpectedDaemonInstanceID = &receipt.RequestID, &receipt.ExpectedDaemonInstanceID
	headers, err := c.doResponse(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", input, &out.Job)
	if err != nil {
		return out, receiptError(err, receipt)
	}
	out.DaemonInstanceID = headers.Get("X-NB1-Daemon-Instance-ID")
	replayed := headers.Get("X-NB1-Replayed")
	if out.Job.ID == "" || out.Job.Device != serial || out.DaemonInstanceID != receipt.ExpectedDaemonInstanceID || (replayed != "true" && replayed != "false") {
		return control.ReceiptResult{}, &ReceiptError{Err: fmt.Errorf("daemon did not confirm the request binding"), Receipt: receipt, OutcomeUnknown: true}
	}
	out.Replayed = replayed == "true"
	return out, nil
}

func (c *Client) Request(ctx context.Context, serial string, receipt control.ReceiptRequest) (out control.Job, err error) {
	if receipt.RequestID == "" || receipt.ExpectedDaemonInstanceID == "" {
		return out, fmt.Errorf("request_id and expected_daemon_instance_id must both be nonempty")
	}
	headers, err := c.doResponse(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(serial)+"/requests/"+url.PathEscape(receipt.RequestID)+
		"?expected_daemon_instance_id="+url.QueryEscape(receipt.ExpectedDaemonInstanceID), nil, &out)
	if err != nil {
		// A lookup rejection describes this query, not whether the original
		// intent ran. A replaced/older daemon may no longer expose this route.
		return out, &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: true}
	}
	if out.ID == "" || out.Device != serial || headers.Get("X-NB1-Daemon-Instance-ID") != receipt.ExpectedDaemonInstanceID {
		return control.Job{}, &ReceiptError{Err: fmt.Errorf("daemon did not confirm the request binding"), Receipt: receipt, OutcomeUnknown: true}
	}
	return out, nil
}

func receiptError(err error, receipt control.ReceiptRequest) error {
	unknown := true
	var failure *Error
	if errors.As(err, &failure) {
		unknown = failure.Status >= 500 || failure.Code == "" || failure.Code == "daemon_instance_changed" || failure.Code == "request_expired" || failure.Code == "request_not_found" || failure.Code == "http_error"
	}
	return &ReceiptError{Err: err, Receipt: receipt, OutcomeUnknown: unknown}
}
func (c *Client) Job(ctx context.Context, id string) (out control.Job, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, &out)
	return
}
func (c *Client) Cancel(ctx context.Context, id string) (out control.Job, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, &out)
	return
}

func (c *Client) do(ctx context.Context, method, path string, input, output interface{}) error {
	_, err := c.doResponse(ctx, method, path, input, output)
	return err
}

func (c *Client) doResponse(ctx context.Context, method, path string, input, output interface{}) (http.Header, error) {
	return c.doResponseExpected(ctx, method, path, input, output, 0)
}

// expectedStatus opts the new read-only compiler routes into an exact status
// and strict response contract. Existing request/Job response behavior remains.
func (c *Client) doResponseExpected(ctx context.Context, method, path string, input, output interface{}, expectedStatus int) (http.Header, error) {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact daemon: %w", err)
	}
	defer resp.Body.Close()
	reader := io.LimitReader(resp.Body, 2<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure httpapi.ErrorResponse
		if err := json.NewDecoder(reader).Decode(&failure); err != nil {
			return nil, &Error{Status: resp.StatusCode, Code: "http_error", Message: resp.Status}
		}
		return nil, &Error{Status: resp.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	if expectedStatus != 0 {
		if resp.StatusCode != expectedStatus {
			return nil, &Error{Status: resp.StatusCode, Code: "unexpected_status", Message: fmt.Sprintf("read-only compiler response requires HTTP %d", expectedStatus)}
		}
		if err := decodeCompilerResponse(resp, output); err != nil {
			return nil, &DecodeError{Status: resp.StatusCode, Err: err}
		}
		return resp.Header, nil
	}
	if err := json.NewDecoder(reader).Decode(output); err != nil {
		return nil, &DecodeError{Status: resp.StatusCode, Err: err}
	}
	return resp.Header, nil
}
