// Package daemonclient communicates with the local HTTP service; it never opens USB.
package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
func (c *Client) Devices(ctx context.Context) (out []control.DeviceState, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/devices", nil, &out)
	return
}
func (c *Client) Device(ctx context.Context, serial string) (out control.DeviceState, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/devices/"+url.PathEscape(serial), nil, &out)
	return
}
func (c *Client) Submit(ctx context.Context, serial string, program control.Program, policy string) (out control.Job, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/devices/"+url.PathEscape(serial)+"/jobs", httpapi.SubmitRequest{Program: program, Policy: policy}, &out)
	return
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
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("contact daemon: %w", err)
	}
	defer resp.Body.Close()
	reader := io.LimitReader(resp.Body, 2<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure httpapi.ErrorResponse
		if err := json.NewDecoder(reader).Decode(&failure); err != nil {
			return &Error{Status: resp.StatusCode, Code: "http_error", Message: resp.Status}
		}
		return &Error{Status: resp.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	if err := json.NewDecoder(reader).Decode(output); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	return nil
}
