package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/mcpserver"
	"github.com/b1ug/nb1/internal/remoteaccess"
	"github.com/spf13/cobra"
)

type remoteListener struct {
	net.Listener
	authority string
	token     string
	tlsConfig *tls.Config
}

func bindRemote(config *appconfig.RemoteServerConfig) (*remoteListener, error) {
	if config == nil || !config.Enabled {
		return nil, nil
	}
	token, err := remoteaccess.LoadToken(config.TokenFile)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load remote TLS certificate and key: %w", err)
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return nil, fmt.Errorf("bind remote listener: %w", err)
	}
	authority := config.Host
	if authority == "" {
		authority = listener.Addr().String()
	}
	return &remoteListener{Listener: listener, authority: strings.TrimSuffix(authority, ":443"), token: token,
		tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}, nil
}

func (remote *remoteListener) servers(engine *control.Engine, localOrigin, localToken string, options ...httpapi.Option) (http.Handler, *http.Server, func(), error) {
	tools, closeTools, err := mcpserver.NewHTTPHandler(localOrigin, "https://"+remote.authority+"/mcp", 10*time.Second, mcpserver.Version(config.GitSummary), localToken)
	if err != nil {
		return nil, nil, nil, err
	}
	local, shared := httpapi.NewWithRemote(engine, func(api http.Handler) http.Handler {
		return remoteaccess.Handler(remote.authority, remote.token, api, tools)
	}, options...)
	server := &http.Server{Handler: shared, TLSConfig: remote.tlsConfig, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	return local, server, closeTools, nil
}

func shutdownHTTPServers(ctx context.Context, servers ...*http.Server) error {
	// Close all listeners together before waiting for outstanding requests.
	completed := make(chan error, len(servers))
	for _, server := range servers {
		go func() {
			err := server.Shutdown(ctx)
			if err != nil {
				_ = server.Close()
			}
			completed <- err
		}()
	}
	var shutdownErr error
	for range servers {
		err := <-completed
		if shutdownErr == nil {
			shutdownErr = err
		} else if err != nil {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	return shutdownErr
}

func init() {
	// main dispatches stdio before Cobra logging/config initialization. The
	// entry here makes the same command discoverable through nb1 help.
	command := &cobra.Command{Use: "mcp", Short: "Run the MCP server over stdio",
		Long: "Run nb1 mcp as the Agent's subprocess. It exchanges JSON-RPC on stdin/stdout and connects to a running local nb1 server over HTTP.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("start with nb1 mcp, followed by its own options")
		}}
	command.Flags().String("endpoint", daemonclient.DefaultEndpoint, "loopback daemon HTTP origin")
	command.Flags().Duration("timeout", 10*time.Second, "per-request HTTP timeout")
	command.Flags().String("token-file", "", "private Bearer token file for the loopback API")
	rootCmd.AddCommand(command)
}
