// Package control owns device execution independently of HTTP, CLI and agents.
package control

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid request")
	ErrNotFound  = errors.New("not found")
	ErrBusy      = errors.New("device busy")
	ErrOffline   = errors.New("device offline")
	ErrClosed    = errors.New("runtime closed")
	ErrQueueFull = errors.New("device queue full")
)

type DeviceInfo struct {
	Serial       string   `json:"serial"`
	Product      string   `json:"product"`
	Path         string   `json:"path,omitempty"`
	Firmware     int      `json:"firmware,omitempty"`
	Capabilities []string `json:"capabilities"`
}

type Observation struct {
	LED1      string    `json:"led1"`
	LED2      string    `json:"led2"`
	SampledAt time.Time `json:"sampled_at"`
}

type DeviceState struct {
	DeviceInfo
	Connection string       `json:"connection"`
	ActiveJob  string       `json:"active_job,omitempty"`
	QueueDepth int          `json:"queue_depth"`
	Desired    *Step        `json:"desired,omitempty"`
	Observed   *Observation `json:"observed,omitempty"`
	LastError  string       `json:"last_error,omitempty"`
}

// Durations are host-side milliseconds. Observed colors are raw device RGB;
// this initial executor sends exact RGB with no implicit gamma conversion.
type Step struct {
	LED    int    `json:"led"`
	Color  string `json:"color"`
	FadeMS int    `json:"fade_ms"`
	HoldMS int    `json:"hold_ms"`
}

type Program struct {
	Steps    []Step `json:"steps"`
	Repeat   int    `json:"repeat"`
	OnFinish string `json:"on_finish"`
}

type Job struct {
	ID             string     `json:"id"`
	Device         string     `json:"device"`
	Status         string     `json:"status"`
	Program        Program    `json:"program"`
	CompletedSteps int        `json:"completed_steps"`
	TotalSteps     int        `json:"total_steps"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Error          string     `json:"error,omitempty"`
}

// Backend is used by exactly one device worker. Stop must stop autonomous
// pattern/tickle before applying off or freezing the currently observed RGB.
// Calls are synchronous: a request deadline cannot promise to cancel native I/O.
type Backend interface {
	Info() DeviceInfo
	Set(Step) error
	Read() (Observation, error)
	Stop(mode string) error
	Close()
}

type Driver interface {
	Open(serial string) (Backend, error)
}

type Options struct {
	Serials      []string
	QueueSize    int
	PollInterval time.Duration
	MaxHistory   int
	// Default off for daemon shutdown; direct commands may release a completed
	// stable state with hold. Interrupted active jobs always clean up with off.
	ShutdownMode string
	// ReadOnly permits monitoring without freezing or changing device output.
	ReadOnly bool
}

// Service is the common interface used by HTTP and local clients. Cancel only
// becomes terminal after the worker has confirmed hardware cleanup.
type Service interface {
	Devices() []DeviceState
	Device(serial string) (DeviceState, error)
	Submit(ctx context.Context, serial string, program Program, policy string) (Job, error)
	Job(id string) (Job, error)
	Cancel(ctx context.Context, id string) (Job, error)
}
