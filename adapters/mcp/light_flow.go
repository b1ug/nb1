package main

import (
	"encoding/json"
	"errors"
)

func isLightFlow(a lightActionWire) bool {
	return a.Kind == "alternate" && a.Transition != "" && a.Transition != "cut"
}

// Verify public execution geometry, not the daemon's color-curve algorithm.
// The adapter has no trigonometric tables, color compiler or core dependency.
// runLight separately binds the complete accepted Job to this exact preview.
func validateLightFlowShape(a lightActionWire, p previewProgram) error {
	if len(p.Steps) != 66 || *a.PeriodMS < 1300 || p.Repeat > 15 {
		return errors.New("continuous alternate requires 32 sequential phase pairs and a bounded period/cycle count")
	}
	for i := 0; i < 2; i++ {
		if p.Steps[i].FadeMS != 0 || p.Steps[i].HoldMS != 0 {
			return errors.New("continuous alternate initial writes must contain only the 10ms step overhead")
		}
		if p.Steps[i].Color != p.Steps[64+i].Color {
			return errors.New("continuous alternate must end at its initial color pair")
		}
	}
	duration := *a.PeriodMS - 20
	for index := 1; index <= 64; index++ {
		slot := duration*index/64 - duration*(index-1)/64
		fade := (slot - 10) / 10 * 10
		if fade < 10 || p.Steps[index+1].FadeMS != fade || p.Steps[index+1].HoldMS != slot-10-fade {
			return errors.New("continuous alternate sequential slot timing changed")
		}
	}
	if a.Transition != "orbit" {
		var first, second string
		_ = json.Unmarshal(a.Colors[0], &first)
		_ = json.Unmarshal(a.Colors[1], &second)
		if p.Steps[0].Color != first || p.Steps[1].Color != second || p.Steps[32].Color != second || p.Steps[33].Color != first {
			return errors.New("continuous alternate endpoint colors disagree with the normalized action")
		}
	}
	return nil
}
