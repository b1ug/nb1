// Package mcpserver exposes the daemon's tools through the official Go MCP SDK.
package mcpserver

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/b1ug/nb1/internal/doctor"
	"github.com/b1ug/nb1/internal/remoteaccess"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	DefaultTimeout  = 10 * time.Second
	maxMessageBytes = 1 << 20
)

// Version reports the linker-provided version or the source build identity.
func Version(linkedVersion string) string {
	if summary := strings.TrimSpace(linkedVersion); summary != "" {
		return summary
	}
	build := doctor.ReadBuild(false)
	dirty := ""
	if build.Modified == "true" {
		dirty = "-dirty"
	}
	if revision := build.Revision; revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		return "candidate-" + revision + dirty
	}
	if version := build.Main.Version; version != "" && version != "(devel)" {
		if strings.HasSuffix(version, "-dirty") || strings.HasSuffix(version, "+dirty") {
			return version
		}
		return version + dirty
	}
	return "development"
}

// RunStdioCommand runs MCP independently of the human CLI's configuration and logs.
func RunStdioCommand(name string, args []string, version string, diagnostics io.Writer) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	endpoint := flags.String("endpoint", defaultEndpoint, "loopback daemon HTTP origin")
	timeout := flags.Duration("timeout", DefaultTimeout, "per-request HTTP timeout; does not cancel accepted daemon jobs")
	tokenFile := flags.String("token-file", "", "private Bearer token file for the loopback API")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "unexpected positional arguments")
		return 2
	}
	var token string
	var err error
	flags.Visit(func(value *flag.Flag) {
		if value.Name == "token-file" {
			token, err = remoteaccess.LoadToken(*tokenFile)
		}
	})
	if err != nil {
		fmt.Fprintf(diagnostics, "load local API token: %v\n", err)
		return 2
	}
	d, err := newDaemon(*endpoint, *timeout, token)
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return 2
	}
	defer d.close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := newServer(d, version, d.endpoint, "This stdio adapter selects its local HTTP endpoint with --endpoint (default "+defaultEndpoint+"). Use --token-file for an authenticated loopback API. It does not read nb1 YAML, open USB, or execute the CLI.")
	if err != nil {
		fmt.Fprintf(diagnostics, "initialize MCP text: %v\n", err)
		return 2
	}
	if err := server.Run(ctx, &mcp.StdioTransport{MaxLineLength: maxMessageBytes}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	return 0
}

// NewHTTPHandler reuses the same tools for a Streamable HTTP endpoint. Its
// caller must enforce TLS, the configured Host and Origin policy, and Bearer
// authentication before dispatching to this handler. This lets the caller use
// an explicitly configured authority even if its TLS listener binds loopback.
// endpoint addresses the loopback backend. publicEndpoint is the canonical HTTPS
// MCP URL returned to remote clients for Job recovery. The returned cleanup
// closes the daemon client's idle connections.
func NewHTTPHandler(endpoint, publicEndpoint string, timeout time.Duration, version, token string) (http.Handler, func(), error) {
	d, err := newDaemon(endpoint, timeout, token)
	if err != nil {
		return nil, nil, err
	}
	server, err := newServer(d, version, publicEndpoint, "This remote service uses Streamable HTTP.")
	if err != nil {
		d.close()
		return nil, nil, err
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		MaxRequestBodyBytes:        maxMessageBytes,
		DisableLocalhostProtection: true,
	})
	return handler, d.close, nil
}
