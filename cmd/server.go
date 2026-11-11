package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/devicebackend"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/ownership"
	"github.com/b1ug/nb1/internal/transporttrace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serverCmd = newServerCommand()

func newServerCommand() *cobra.Command {
	var listen, lockDir string
	var serials []string
	var shutdownTimeout time.Duration
	var transportTrace bool
	command := &cobra.Command{
		Use: "server", Aliases: aliasesServer, Short: "Run the local blink(1) device daemon",
		Long:    "Keep explicitly selected devices open and serve the v1 HTTP API on loopback. API, CLI and agents share each device worker. Stop with Ctrl-C or SIGTERM; shutdown cancels jobs and turns devices off. No system service is installed.",
		Args:    cobra.NoArgs,
		Example: "  nb1 server --devices 20005601\n  nb1 --device 20005601 server --listen 127.0.0.1:8931\n  nb1 server --devices 20005601 --port 8932",
		RunE: func(cmd *cobra.Command, _ []string) error {
			address, err := serverAddress(cmd, listen)
			if err != nil {
				return err
			}
			if base := config.GetBaseURL(); base != "" && base != "/" {
				return fmt.Errorf("--base/base_url is not supported by the v1 API; use the root endpoint")
			}
			if len(serials) == 0 {
				if preferred := strings.TrimSpace(config.GetPreferredDevice()); preferred != "" {
					serials = []string{preferred}
				}
			}
			if len(serials) == 0 {
				return fmt.Errorf("select device serials with --devices or --device; the daemon does not select the first HID device")
			}
			if shutdownTimeout <= 0 {
				return fmt.Errorf("--shutdown-timeout must be positive")
			}
			if lockDir == "" {
				lockDir, err = ownership.DefaultDir()
				if err != nil {
					return err
				}
			}
			// Bind first so a port conflict cannot open or reset hardware.
			listener, err := net.Listen("tcp", address)
			if err != nil {
				return err
			}
			if tcp, ok := listener.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
				listener.Close()
				return fmt.Errorf("daemon listener must bind a loopback address")
			}
			var recorder *transporttrace.Recorder
			driver := devicebackend.New(lockDir)
			if transportTrace {
				recorder = transporttrace.New()
				driver = devicebackend.NewWithTrace(lockDir, recorder)
			}
			engine, err := control.New(driver, control.Options{Serials: serials, ShutdownMode: "off"})
			if err != nil {
				listener.Close()
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if recorder != nil {
				recorder.SetInstance(engine.Instance().DaemonInstanceID)
				transportTraceBuildStamp(recorder)
			}
			return serveRuntimeWithTrace(ctx, listener, engine, cmd.OutOrStdout(), shutdownTimeout, recorder,
				httpapi.WithSequenceAPI())
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8931", "loopback host:port (port 0 chooses a free port)")
	command.Flags().StringSliceVar(&serials, "devices", nil, "comma-separated device serial numbers")
	command.Flags().StringVar(&lockDir, "lock-dir", os.Getenv("NB1_LOCK_DIR"), "shared ownership directory (NB1_LOCK_DIR or persistent per-user config)")
	command.Flags().DurationVar(&shutdownTimeout, "shutdown-timeout", 10*time.Second, "maximum time to await graceful cleanup; does not cancel blocked native I/O")
	command.Flags().BoolVar(&transportTrace, "transport-trace", false, "opt-in bounded private native-attempt diagnostics in this daemon's log; no extra USB calls")
	command.Flags().Uint32("port", 8931, "compatibility flag for a loopback port; cannot be combined with --listen")
	command.Flags().String("base", "", "reserved legacy base URL; the v1 API uses the root endpoint")
	_ = viper.BindPFlag("port", command.Flags().Lookup("port"))
	_ = viper.BindPFlag("base_url", command.Flags().Lookup("base"))
	return command
}

func serverAddress(cmd *cobra.Command, listen string) (string, error) {
	if cmd.Flags().Changed("listen") && cmd.Flags().Changed("port") {
		return "", fmt.Errorf("choose either --listen or --port")
	}
	envValue, envPort := os.LookupEnv("NB1_PORT")
	if !cmd.Flags().Changed("listen") && (cmd.Flags().Changed("port") || viper.InConfig("port") || envPort) {
		portValue := fmt.Sprint(viper.Get("port"))
		if cmd.Flags().Changed("port") {
			port, err := cmd.Flags().GetUint32("port")
			if err != nil {
				return "", err
			}
			portValue = strconv.FormatUint(uint64(port), 10)
		} else if envPort {
			portValue = envValue
		}
		port, err := strconv.ParseUint(portValue, 10, 16)
		if err != nil {
			return "", fmt.Errorf("configured port must be an integer between 0 and 65535")
		}
		listen = net.JoinHostPort("127.0.0.1", strconv.FormatUint(port, 10))
	}
	if err := validateLocalAddress(listen); err != nil {
		return "", err
	}
	return listen, nil
}

func validateLocalAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid --listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("--listen must use a loopback IP or localhost")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("listen port must be between 0 and 65535")
	}
	return nil
}

func serveRuntime(parent context.Context, listener net.Listener, engine *control.Engine, output io.Writer, shutdownTimeout time.Duration, options ...httpapi.Option) error {
	return serveRuntimeWithTrace(parent, listener, engine, output, shutdownTimeout, nil, options...)
}

func serveRuntimeWithTrace(parent context.Context, listener net.Listener, engine *control.Engine, output io.Writer, shutdownTimeout time.Duration, recorder *transporttrace.Recorder, options ...httpapi.Option) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		if recorder != nil {
			recorder.Close()
		}
		listener.Close()
		return err
	}
	if recorder != nil {
		go func() { <-engine.Done(); recorder.Close() }()
	}
	server := &http.Server{Handler: httpapi.New(engine, options...), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	// Exactly two typed records are queued, even if the single log writer is
	// blocked or has failed. Capacity two means neither send waits on the sink.
	// The caller never joins the logger as part of native/lease cleanup.
	lifecycle := make(chan runtimeShutdownRecord, 2)
	defer close(lifecycle)
	logging := startRuntimeLoggingWithLifecycle(engine, output, map[string]interface{}{"event": "daemon_listening", "url": "http://" + listener.Addr().String(), "api_version": "v1", "devices": engine.Devices()}, lifecycle, recorder)
	selected := waitRuntimeShutdown(parent, engine.Done(), served, logging.startupErrors)
	began := time.Now()
	instance := engine.Instance().DaemonInstanceID
	lifecycle <- selected.record(instance, "daemon_shutdown_trigger")
	cause := selected.cause
	stopCtx, stop := context.WithTimeout(context.Background(), shutdownTimeout)
	defer stop()
	httpErr := server.Shutdown(stopCtx)
	if httpErr != nil {
		_ = server.Close()
		if cause == nil {
			cause = httpErr
		}
	}
	cancel()
	engineErr := engine.Close(stopCtx)
	if engineErr != nil {
		if cause != nil {
			cause = fmt.Errorf("%v; device cleanup: %w", cause, engineErr)
		} else {
			cause = fmt.Errorf("device cleanup: %w", engineErr)
		}
	}
	lifecycle <- selected.result(instance, began, httpErr, engineErr, cause, engine.Done())
	return cause
}

func init() { rootCmd.AddCommand(serverCmd) }
