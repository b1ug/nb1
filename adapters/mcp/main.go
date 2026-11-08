package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	flags := flag.NewFlagSet("nb1-mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	endpoint := flags.String("endpoint", defaultEndpoint, "loopback daemon HTTP origin")
	timeout := flags.Duration("timeout", 10*time.Second, "per-request HTTP timeout; does not cancel accepted daemon jobs")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	daemon, err := newDaemon(*endpoint, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer daemon.close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stdout belongs exclusively to the official SDK's JSON-RPC transport.
	if err := newServer(daemon).Run(ctx, &mcp.StdioTransport{MaxLineLength: 1 << 20}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
