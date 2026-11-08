package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// These wire types follow nb1/internal/control/types.go. This independent
// adapter intentionally does not import the USB or CLI modules.
type step struct {
	LED    int    `json:"led"`
	Color  string `json:"color"`
	FadeMS int    `json:"fade_ms"`
	HoldMS int    `json:"hold_ms"`
}
type program struct {
	Steps    []step `json:"steps"`
	Repeat   int    `json:"repeat,omitempty"`
	OnFinish string `json:"on_finish,omitempty"`
}
type submission struct {
	Program program `json:"program"`
	Policy  string  `json:"policy"`
}

func validToken(value string) bool {
	return value != "" && len(value) <= 128 && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_')
	}) < 0
}

func normalizeProgram(p program) (program, error) {
	if len(p.Steps) < 1 || len(p.Steps) > 1024 {
		return program{}, errors.New("program requires 1–1024 steps")
	}
	p.Steps = append([]step(nil), p.Steps...)
	if p.Repeat == 0 {
		p.Repeat = 1
	}
	if p.Repeat < 1 || p.Repeat > 10000 {
		return program{}, errors.New("repeat must be 1–10000")
	}
	if p.OnFinish == "" {
		p.OnFinish = "off"
	}
	if p.OnFinish != "off" && p.OnFinish != "hold" {
		return program{}, errors.New("on_finish must be off or hold")
	}
	var total int64
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.LED < 0 || s.LED > 2 || s.FadeMS < 0 || s.FadeMS > 600000 || s.FadeMS%10 != 0 || s.HoldMS < 0 || s.HoldMS > 3600000 {
			return program{}, fmt.Errorf("step %d: invalid LED or duration (fade_ms must use 10ms units)", i)
		}
		if len(s.Color) != 7 || s.Color[0] != '#' {
			return program{}, fmt.Errorf("step %d: color must be #RRGGBB", i)
		}
		if _, err := hex.DecodeString(s.Color[1:]); err != nil {
			return program{}, fmt.Errorf("step %d: color must be #RRGGBB", i)
		}
		s.Color = strings.ToLower(s.Color)
		// Include the core's per-step 10ms firmware update allowance. Native
		// I/O adds further overhead, so this is a nominal rather than wall cap.
		total += int64(s.FadeMS) + int64(s.HoldMS) + 10
	}
	if total*int64(p.Repeat) > 24*60*60*1000 {
		return program{}, errors.New("program duration exceeds 24 hours")
	}
	return p, nil
}

func normalizePolicy(policy string) (string, error) {
	if policy == "" {
		policy = "replace"
	}
	if policy != "replace" && policy != "queue" && policy != "reject" {
		return "", errors.New("policy must be replace, queue or reject")
	}
	return policy, nil
}
