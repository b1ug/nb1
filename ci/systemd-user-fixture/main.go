// systemd-user-fixture is a stdlib-only, USB-free lifecycle test executable.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	devices []string
	listen  string
}

var serial = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)

func parse(args []string) (config, error) {
	var c config
	if len(args) == 0 || args[0] != "server" {
		return c, errors.New("fixture requires the server subcommand")
	}
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var devices, lockDir string
	flags.StringVar(&devices, "devices", "", "explicit comma-separated fixture serials")
	flags.StringVar(&c.listen, "listen", "", "explicit loopback host:port")
	flags.StringVar(&lockDir, "lock-dir", "", "accepted for argv compatibility; no hardware or locks are opened")
	if err := flags.Parse(args[1:]); err != nil {
		return c, err
	}
	if flags.NArg() != 0 || devices == "" {
		return c, errors.New("explicit devices and no positional arguments are required")
	}
	seen := make(map[string]bool)
	for _, item := range strings.Split(devices, ",") {
		key := strings.ToLower(item)
		if !serial.MatchString(item) || seen[key] || len(c.devices) == 64 {
			return c, errors.New("fixture devices must be distinct eight-digit hexadecimal serials")
		}
		seen[key] = true
		c.devices = append(c.devices, item)
	}
	host, port, err := net.SplitHostPort(c.listen)
	if err != nil {
		return c, errors.New("an explicit loopback host:port is required")
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 1 || n > 65535 {
		return c, errors.New("fixture requires a loopback IP and a nonzero port")
	}
	if lockDir != "" && !filepath.IsAbs(lockDir) {
		return c, errors.New("lock-dir must be absolute when present")
	}
	return c, nil
}

func handler(c config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Fixture bool     `json:"fixture"`
			PID     int      `json:"pid"`
			Devices []string `json:"devices"`
			Listen  string   `json:"listen"`
		}{true, os.Getpid(), c.devices, c.listen})
	})
	return mux
}

func serve(ctx context.Context, c config, listener net.Listener) error {
	server := &http.Server{Handler: handler(c), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func main() {
	c, err := parse(os.Args[1:])
	if err == nil {
		var listener net.Listener
		listener, err = net.Listen("tcp", c.listen)
		if err == nil {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			err = serve(ctx, c, listener)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "systemd-user fixture:", err)
		os.Exit(1)
	}
}
