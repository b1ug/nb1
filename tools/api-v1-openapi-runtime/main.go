// Command api-v1-openapi-runtime checks documentation requests against the real
// v1 HTTP handler using a software-only device and a temporary local library.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b1ug/nb1/internal/actionv1"
	"github.com/b1ug/nb1/internal/control"
	"github.com/b1ug/nb1/internal/flashreset"
	"github.com/b1ug/nb1/internal/httpapi"
	"github.com/b1ug/nb1/internal/libraryv1"
)

type request struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

type observation struct {
	Name   string          `json:"name"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

type backend struct{ serial, one, two string }

func (b *backend) Info() control.DeviceInfo {
	return control.DeviceInfo{Serial: b.serial, Product: "OpenAPI software fixture",
		Capabilities: []string{"set", "program", "raw-rgb", "mk2-basic", "hold", "observe", "cancel"}}
}
func (b *backend) Set(step control.Step) error {
	if step.LED == 0 || step.LED == 1 {
		b.one = step.Color
	}
	if step.LED == 0 || step.LED == 2 {
		b.two = step.Color
	}
	return nil
}
func (b *backend) Read() (control.Observation, error) {
	return control.Observation{LED1: b.one, LED2: b.two, SampledAt: time.Now().UTC()}, nil
}
func (b *backend) Stop(mode string) error {
	if mode == "off" {
		b.one, b.two = "#000000", "#000000"
	}
	return nil
}
func (*backend) Close() {}

func (b *backend) ResetFlash(_ context.Context, preset string) (flashreset.Result, error) {
	return flashreset.Result{Serial: b.serial, Preset: preset, Firmware: 204,
		Lines: 16, Staged: 16, Verified: 16, Status: flashreset.Acknowledged}, nil
}

type driver map[string]*backend

func (d driver) Open(serial string) (control.Backend, error) {
	if device := d[serial]; device != nil {
		return device, nil
	}
	return nil, control.ErrNotFound
}

func run() error {
	var requests []request
	if err := json.NewDecoder(os.Stdin).Decode(&requests); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "nb1-openapi-library-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	store := libraryv1.New(dir)
	if _, err := store.SaveColor("status-ok", "#00ff00"); err != nil {
		return err
	}
	compiled, err := actionv1.Compile([]byte(`{"kind":"pulse","color":"lime"}`), nil)
	if err != nil {
		return err
	}
	if _, err := store.SavePattern(libraryv1.Pattern{ID: "gentle-pulse", Title: "Gentle pulse", Sequence: compiled}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "triggers.json"), []byte(`{"schema_version":1,"triggers":{"build.done":{"pattern_id":"gentle-pulse"}}}`), 0600); err != nil {
		return err
	}
	serials := []string{"fixture-0001", "20000001"}
	fixtures := driver{}
	for _, serial := range serials {
		fixtures[serial] = &backend{serial: serial, one: "#000000", two: "#000000"}
	}
	engine, err := control.New(fixtures, control.Options{Serials: serials, PollInterval: time.Hour})
	if err != nil {
		return err
	}
	if err := engine.Start(context.Background()); err != nil {
		return err
	}
	defer engine.Close(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for _, serial := range serials {
		for {
			state, err := engine.Device(serial)
			if err != nil {
				return err
			}
			if state.Connection == "online" {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("software fixture did not start")
			}
			time.Sleep(time.Millisecond)
		}
	}
	handler := httpapi.New(engine, httpapi.WithSequenceAPI(), httpapi.WithLibraryDirectory(dir), httpapi.WithSequenceRouting("fixture-0001", false, dir))
	results := make([]observation, 0, len(requests))
	for _, input := range requests {
		r := httptest.NewRequest(input.Method, "http://127.0.0.1"+input.Path, strings.NewReader(input.Body))
		if input.Body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if input.Method == "POST" && strings.HasSuffix(input.Path, "/flash/reset") {
			issue := httptest.NewRequest("GET", "http://127.0.0.1"+input.Path+"-ticket", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, issue)
			var ticket httpapi.FlashResetTicket
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &ticket) != nil || ticket.Ticket == "" {
				return fmt.Errorf("issue fixture Flash reset ticket: HTTP %d", response.Code)
			}
			r.Header.Set(httpapi.FlashTicketHeader, ticket.Ticket)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		results = append(results, observation{Name: input.Name, Status: w.Code, Body: json.RawMessage(w.Body.Bytes())})
		if input.Method == "POST" && input.Path == "/v1/jobs" && w.Code >= 200 && w.Code < 300 {
			var job control.SequenceJob
			if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
				return err
			}
			if _, err := engine.CancelSequence(context.Background(), job.ID); err != nil {
				return err
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				current, err := engine.SequenceJob(job.ID)
				if err != nil {
					return err
				}
				if current.FinishedAt != nil {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("software Job cleanup timed out")
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
