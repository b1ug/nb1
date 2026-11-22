package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1ug/nb1/internal/appconfig"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/daemonaddr"
	"github.com/b1ug/nb1/internal/daemonclient"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func TestLoopbackListenBoundary(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8931", "127.0.0.1:0", "localhost:8931", "[::1]:8931"} {
		if err := validateLocalAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{":8931", "0.0.0.0:8931", "192.168.1.2:8931", "example.com:8931", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1"} {
		if err := validateLocalAddress(address); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
}

func TestServerPortConfiguration(t *testing.T) {
	command := &cobra.Command{}
	if net.JoinHostPort("127.0.0.1", strconv.Itoa(daemonaddr.Port)) != daemonaddr.Listen {
		t.Fatal("numeric default port and default listen address disagree")
	}
	if serverCmd.Flags().Lookup("port").DefValue != "5885" || serverCmd.Flags().Lookup("listen").DefValue != "127.0.0.1:5885" {
		t.Fatal("server flags must default to the same loopback port as the profile")
	}
	command.Flags().String("listen", daemonaddr.Listen, "")
	command.Flags().Uint32("port", daemonaddr.Port, "")
	t.Setenv("NB1_PORT", "8931")
	if address, err := serverAddress(command, daemonaddr.Listen); err != nil || address != "127.0.0.1:8931" {
		t.Fatalf("configured legacy port: %s %v", address, err)
	}
	t.Setenv("NB1_PORT", "invalid")
	if _, err := serverAddress(command, "127.0.0.1:8931"); err == nil {
		t.Fatal("invalid environment port accepted")
	}
	if err := command.Flags().Set("port", "8932"); err != nil {
		t.Fatal(err)
	}
	if address, err := serverAddress(command, "127.0.0.1:8931"); err != nil || address != "127.0.0.1:8932" {
		t.Fatalf("explicit port precedence: %s %v", address, err)
	}
	if err := command.Flags().Set("listen", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if _, err := serverAddress(command, "127.0.0.1:0"); err == nil {
		t.Fatal("listen and port conflict accepted")
	}
}

type runtimeOutput struct{ line chan []byte }

func (w runtimeOutput) Write(b []byte) (int, error) {
	w.line <- append([]byte(nil), b...)
	return len(b), nil
}

type lifecycleDriver struct{ backend *lifecycleBackend }

func (d lifecycleDriver) Open(string) (control.Backend, error) { return d.backend, nil }

type lifecycleBackend struct {
	mu       sync.Mutex
	lastStop string
	closed   bool
	set      chan struct{}
	once     sync.Once
}

func (*lifecycleBackend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: "test-device", Capabilities: []string{"set"}}
}
func (b *lifecycleBackend) Set(control.Step) error { b.once.Do(func() { close(b.set) }); return nil }
func (*lifecycleBackend) Read() (control.Observation, error) {
	return control.Observation{LED1: "#000000", LED2: "#000000", SampledAt: time.Now().UTC()}, nil
}
func (b *lifecycleBackend) Stop(mode string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastStop = mode
	return nil
}
func (b *lifecycleBackend) Close() { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true }

func TestDaemonContextShutdownCleansActiveJob(t *testing.T) {
	backend := &lifecycleBackend{set: make(chan struct{})}
	engine, err := control.New(lifecycleDriver{backend}, control.Options{Serials: []string{"test-device"}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := runtimeOutput{line: make(chan []byte, 64)}
	done := make(chan error, 1)
	go func() { done <- serveRuntime(ctx, listener, engine, output, 2*time.Second) }()
	var address struct {
		URL string `json:"url"`
	}
	select {
	case line := <-output.line:
		if err := json.Unmarshal(line, &address); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no startup address")
	}
	client, err := daemonclient.New(address.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	requestCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for {
		state, err := client.Device(requestCtx, "test-device")
		if err != nil {
			t.Fatal(err)
		}
		if state.Connection == "online" {
			break
		}
		select {
		case <-requestCtx.Done():
			t.Fatal("runtime did not connect")
		case <-time.After(time.Millisecond):
		}
	}
	job, err := client.Submit(requestCtx, "test-device", control.Program{Steps: []control.Step{{Color: "#ff0000", HoldMS: 30000}}, Repeat: 1, OnFinish: "hold"}, "queue")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.set:
	case <-requestCtx.Done():
		t.Fatal("job did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon shutdown did not finish")
	}
	job, err = engine.Job(job.ID)
	if err != nil || job.Status != "cancelled" {
		t.Fatalf("shutdown job=%v err=%v", job, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !backend.closed || backend.lastStop != "off" {
		t.Fatalf("closed=%t stop=%q", backend.closed, backend.lastStop)
	}
}

type testBearerTransport struct {
	token string
	next  http.RoundTripper
}

func TestDisabledRemoteKeepsCredentialsDormant(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	remote, err := bindRemote(&appconfig.RemoteServerConfig{Listen: "127.0.0.1:0", TokenFile: missing, TLSCertFile: missing, TLSKeyFile: missing})
	if err != nil || remote != nil {
		t.Fatalf("disabled listener touched saved credentials: %v, %v", remote, err)
	}
}

func (transport testBearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	request := req.Clone(req.Context())
	request.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.next.RoundTrip(request)
}

func TestRemoteHTTPSharesDaemonAndRequiresBearer(t *testing.T) {
	const serial, peer = "20000001", "20000002"
	const token = "test-only-generated-looking-token-from-fixture"
	const localToken = "different-fixture-token-for-the-local-api"
	backend := &sequenceControlBackend{led1: "#000000", led2: "#000000"}
	peerBackend := &sequenceControlBackend{led1: "#000000", led2: "#000000"}
	driver := sequenceDirectTestDriver{open: func(target string) (control.Backend, error) {
		switch target {
		case serial:
			return backend, nil
		case peer:
			return peerBackend, nil
		default:
			return nil, fmt.Errorf("unknown test serial %q", target)
		}
	}}
	engine, err := control.New(driver, control.Options{Serials: []string{serial, peer}, PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	secure, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secure.Close() })
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	transport := fixture.Client().Transport.(*http.Transport).Clone()
	fixture.Close()
	defer transport.CloseIdleConnections()
	remote := &remoteListener{Listener: secure, authority: secure.Addr().String(), token: token,
		tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output := runtimeOutput{line: make(chan []byte, 64)}
	done := make(chan error, 1)
	go func() {
		done <- serveRuntimeWithTrace(ctx, local, engine, output, time.Second, nil, remote, localToken, httpapi.WithSequenceAPI())
	}()
	select {
	case <-output.line:
	case <-ctx.Done():
		t.Fatal("daemon did not start")
	}
	plainClient := &http.Client{Timeout: time.Second}
	localClient := &http.Client{Transport: testBearerTransport{localToken, http.DefaultTransport}, Timeout: time.Second}
	unauthorized := &http.Client{Transport: transport, Timeout: time.Second}
	authorized := &http.Client{Transport: testBearerTransport{token, transport}, Timeout: time.Second}
	localURL, secureURL := "http://"+local.Addr().String(), "https://"+secure.Addr().String()
	call := func(client *http.Client, method, url, input string, status int) string {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if input != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s: status %d (want %d), body %q, error %v", method, url, response.StatusCode, status, raw, err)
		}
		if status == http.StatusUnauthorized && !strings.HasPrefix(response.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Fatal("missing Bearer challenge")
		}
		return string(raw)
	}
	call(plainClient, http.MethodGet, localURL+"/healthz", "", http.StatusOK)
	call(plainClient, http.MethodGet, localURL+"/demo/", "", http.StatusForbidden)
	call(plainClient, http.MethodGet, localURL+"/v1/devices", "", http.StatusUnauthorized)
	call(localClient, http.MethodGet, localURL+"/demo/", "", http.StatusForbidden)
	call(unauthorized, http.MethodGet, secureURL+"/v1/devices", "", http.StatusUnauthorized)
	call(unauthorized, http.MethodPost, secureURL+"/v1/jobs", "{}", http.StatusUnauthorized)
	call(unauthorized, http.MethodPost, secureURL+"/mcp", "{}", http.StatusUnauthorized)
	call(&http.Client{Transport: testBearerTransport{localToken, transport}, Timeout: time.Second},
		http.MethodGet, secureURL+"/v1/devices", "", http.StatusUnauthorized)
	call(authorized, http.MethodGet, secureURL+"/demo/", "", http.StatusNotFound)
	var localDevices string
	for strings.Count(localDevices, `"connection":"online"`) != 2 {
		localDevices = call(localClient, http.MethodGet, localURL+"/v1/devices", "", http.StatusOK)
		if ctx.Err() != nil {
			t.Fatal("simulated device did not come online")
		}
		if strings.Count(localDevices, `"connection":"online"`) != 2 {
			time.Sleep(time.Millisecond)
		}
	}
	call(plainClient, http.MethodGet, localURL+"/v1/presence", "", http.StatusOK)
	call(plainClient, http.MethodGet, localURL+"/v1/service-status", "", http.StatusUnauthorized)
	call(localClient, http.MethodGet, localURL+"/v1/service-status", "", http.StatusOK)
	call(authorized, http.MethodGet, secureURL+"/v1/service-status", "", http.StatusNotFound)
	remoteDevices := call(authorized, http.MethodGet, secureURL+"/v1/devices", "", http.StatusOK)
	if !strings.Contains(localDevices, serial) || !strings.Contains(remoteDevices, serial) {
		t.Fatal("both listeners must address the same daemon device")
	}
	job := call(authorized, http.MethodPost, secureURL+"/v1/jobs",
		`{"id":"j_RemoteTLS","serial":"20000001","action":{"kind":"set","color":"red"}}`, http.StatusAccepted)
	localJob := call(localClient, http.MethodGet, localURL+"/v1/jobs/j_RemoteTLS", "", http.StatusOK)
	if !strings.Contains(job, "j_RemoteTLS") || !strings.Contains(localJob, "j_RemoteTLS") {
		t.Fatal("the loopback client must see the same job submitted via HTTPS")
	}
	for !strings.Contains(localJob, `"status":"succeeded"`) {
		time.Sleep(time.Millisecond)
		localJob = call(localClient, http.MethodGet, localURL+"/v1/jobs/j_RemoteTLS", "", http.StatusOK)
	}
	agent := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := agent.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: secureURL + "/mcp", HTTPClient: authorized, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 16 {
		t.Fatalf("MCP discovery: tools=%v err=%v", tools, err)
	}
	devices, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_devices"})
	if err != nil || devices.IsError {
		t.Fatalf("MCP list_devices: result=%v err=%v", devices, err)
	}
	blob, err := json.Marshal(devices.StructuredContent)
	if err != nil || !strings.Contains(string(blob), serial) {
		t.Fatalf("MCP returned a different device list: %s, %v", blob, err)
	}
	played, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "run_light", Arguments: map[string]any{
		"id": "j_RemoteMCP", "serial": peer, "action": map[string]any{"kind": "set", "color": "blue"},
	}})
	if err != nil || played.IsError {
		t.Fatalf("MCP run_light: result=%v err=%v", played, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		job := call(localClient, http.MethodGet, localURL+"/v1/jobs/j_RemoteMCP", "", http.StatusOK)
		if strings.Contains(job, `"status":"succeeded"`) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	left, _ := backend.Read()
	right, _ := peerBackend.Read()
	if left.LED1 != "#ff0000" || left.LED2 != "#ff0000" || right.LED1 != "#0000ff" || right.LED2 != "#0000ff" {
		t.Fatalf("remote device colors after independent requests: got %v and %v", left, right)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not close both listeners")
	}
}
