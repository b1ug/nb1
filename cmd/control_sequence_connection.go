package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/b1ug/nb1/internal/control"
	"github.com/spf13/cobra"
)

// Give startup guidance only for a failed connection attempt. A lost response,
// read timeout or API rejection must retain its own meaning and original Job ID.
func sequenceControlConnectionError(cmd *cobra.Command, endpoint string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var dial *net.OpError
	if !errors.As(err, &dial) || dial.Op != "dial" {
		return err
	}
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	if n, parseErr := strconv.Atoi(port); parseErr != nil || n < 1 || n > 65535 {
		return err
	}
	serial := "SERIAL"
	args := cmd.Flags().Args()
	if (cmd.Name() == "run" || cmd.Name() == "state") && len(args) > 0 && control.ValidDeviceSerial(args[0]) {
		serial = args[0]
	} else if cmd.Name() == "preview" {
		if selected, flagErr := cmd.Flags().GetString("serial"); flagErr == nil && control.ValidDeviceSerial(selected) {
			serial = selected
		}
	}
	return fmt.Errorf("cannot connect to daemon at %s: %w\n"+
		"Start a daemon explicitly: nb1 server --devices %s --listen %s\n"+
		"For an installed user service: nb1 service start (uses its saved endpoint).\n"+
		"For direct USB inspection: nb1 --device %s read state", endpoint, err, serial, net.JoinHostPort(u.Hostname(), port), serial)
}
