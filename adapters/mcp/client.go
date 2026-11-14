package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/jsoninput"
)

const defaultEndpoint = daemonclient.DefaultEndpoint

type daemon struct {
	endpoint string
	timeout  time.Duration
	core     *daemonclient.Client
	http     *http.Client
}

func newDaemon(endpoint string, timeout time.Duration) (*daemon, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	client, err := daemonclient.New(endpoint)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &daemon{endpoint: strings.TrimRight(endpoint, "/"), timeout: timeout, core: client,
		http: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (d *daemon) close() {
	d.core.Close()
	d.http.CloseIdleConnections()
}

type resolvedColor struct {
	Color string `json:"color"`
}

// Color resolution uses the current small v1 response. The shared client's
// existing ResolveColor method belongs to an earlier response contract.
func (d *daemon) resolveColor(ctx context.Context, raw json.RawMessage) (resolvedColor, error) {
	body, err := json.Marshal(struct {
		Color json.RawMessage `json:"color"`
	}{Color: raw})
	if err != nil {
		return resolvedColor{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint+"/v1/colors/resolve", bytes.NewReader(body))
	if err != nil {
		return resolvedColor{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	response, err := d.http.Do(request)
	if err != nil {
		return resolvedColor{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return resolvedColor{}, err
	}
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if len(data) > 2<<20 || !jsoninput.ValidUnicode(data) || mediaErr != nil || media != "application/json" {
		return resolvedColor{}, &daemonclient.DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid color response")}
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Field   string `json:"field,omitempty"`
			} `json:"error"`
		}
		if err := jsoninput.DecodeObject(data, &failure); err != nil || failure.Error.Code == "" {
			return resolvedColor{}, &daemonclient.DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid color error response")}
		}
		return resolvedColor{}, &daemonclient.Error{Status: response.StatusCode, Code: failure.Error.Code, Message: failure.Error.Message}
	}
	var result resolvedColor
	if err := jsoninput.DecodeObject(data, &result); err != nil || len(result.Color) != 7 || result.Color[0] != '#' || strings.ToLower(result.Color) != result.Color {
		return resolvedColor{}, &daemonclient.DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid resolved color")}
	}
	for _, value := range result.Color[1:] {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			return resolvedColor{}, &daemonclient.DecodeError{Status: response.StatusCode, Err: fmt.Errorf("invalid resolved color")}
		}
	}
	return result, nil
}

func uncertainWrite(err error) bool {
	var rejected *daemonclient.Error
	if errors.As(err, &rejected) {
		return rejected.Status >= 500
	}
	var malformed *daemonclient.DecodeError
	var transport *url.Error
	return errors.As(err, &malformed) || errors.As(err, &transport) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
