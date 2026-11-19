package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/daemonclient"
)

const defaultEndpoint = daemonclient.DefaultEndpoint

type daemon struct {
	endpoint string
	timeout  time.Duration
	core     *daemonclient.Client
}

func newDaemon(endpoint string, timeout time.Duration, token string) (*daemon, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	client, err := daemonclient.NewWithToken(endpoint, token)
	if err != nil {
		return nil, err
	}
	return &daemon{endpoint: strings.TrimRight(endpoint, "/"), timeout: timeout, core: client}, nil
}

func (d *daemon) close() {
	d.core.Close()
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
