package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultEndpoint = "http://127.0.0.1:8931"
const maxResponseBytes = 2 << 20

type daemon struct {
	endpoint string
	client   *http.Client
}

type failure struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	// A transport error after POST cannot prove that the daemon rejected it.
	// An expired/missing receipt or changed instance can also leave the old
	// keyed intent unknown, even when its lookup returns a definite HTTP error.
	OutcomeUnknown bool `json:"outcome_unknown,omitempty"`
}

type output struct {
	Kind          string         `json:"kind"`
	Data          any            `json:"data,omitempty"`
	Accepted      bool           `json:"accepted"`
	DryRun        bool           `json:"dry_run,omitempty"`
	PollAfterMS   int            `json:"poll_after_ms,omitempty"`
	Error         *failure       `json:"error,omitempty"`
	RequestTicket *requestTicket `json:"request_ticket,omitempty"`
	// Only emitted for keyed dry-runs. Local syntax checks cannot verify the
	// daemon instance, receipt existence or monotonic admission deadline.
	InstanceBindingVerified *bool `json:"instance_binding_verified,omitempty"`
}

func newDaemon(endpoint string, timeout time.Duration) (*daemon, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid daemon endpoint: %w", err)
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || !(strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
		return nil, errors.New("endpoint must be a loopback HTTP origin")
	}
	if timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &daemon{endpoint: strings.TrimRight(u.String(), "/"), client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (d *daemon) close() { d.client.CloseIdleConnections() }

func (d *daemon) request(ctx context.Context, method, path, kind string, input any) output {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return errorOutput("invalid_request", err.Error(), 0, false)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.endpoint+path, body)
	if err != nil {
		return errorOutput("invalid_request", err.Error(), 0, false)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(req)
	if err != nil {
		message := fmt.Sprintf("contact daemon: %v", err)
		if method == http.MethodPost {
			message += "; outcome unknown: inspect the daemon before retrying"
		}
		return errorOutput("daemon_unreachable", message, 0, method == http.MethodPost)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return errorOutput("invalid_daemon_response", err.Error(), response.StatusCode, method == http.MethodPost)
	}
	if len(data) > maxResponseBytes {
		return errorOutput("invalid_daemon_response", "daemon response exceeds 2 MiB", response.StatusCode, method == http.MethodPost)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		code, message := "http_error", response.Status
		if json.Unmarshal(data, &problem) == nil && problem.Error.Code != "" {
			code, message = problem.Error.Code, problem.Error.Message
		}
		return errorOutput(code, message, response.StatusCode, method == http.MethodPost && response.StatusCode >= 500)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return errorOutput("invalid_daemon_response", err.Error(), response.StatusCode, method == http.MethodPost)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return errorOutput("invalid_daemon_response", "expected exactly one JSON response", response.StatusCode, method == http.MethodPost)
	}
	if kind == "devices" {
		if _, ok := value.([]any); !ok {
			return errorOutput("invalid_daemon_response", "expected a device array", response.StatusCode, false)
		}
	} else if _, ok := value.(map[string]any); !ok {
		return errorOutput("invalid_daemon_response", "expected a JSON object", response.StatusCode, method == http.MethodPost)
	}
	out := output{Kind: kind, Data: value, Accepted: response.StatusCode == http.StatusAccepted}
	if kind == "job" {
		job := value.(map[string]any)
		status, _ := job["status"].(string)
		switch status {
		case "queued", "running", "cancelling":
			out.PollAfterMS = 200
		case "succeeded", "cancelled", "failed":
		default:
			return errorOutput("invalid_daemon_response", "unrecognized job status", response.StatusCode, method == http.MethodPost)
		}
		if id, ok := job["id"].(string); !ok || !validToken(id) {
			return errorOutput("invalid_daemon_response", "missing or invalid job id", response.StatusCode, method == http.MethodPost)
		}
	}
	return out
}

func errorOutput(code, message string, status int, unknown bool) output {
	return output{Kind: "error", Error: &failure{Code: code, Message: message, HTTPStatus: status, OutcomeUnknown: unknown}}
}
