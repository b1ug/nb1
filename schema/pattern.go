package schema

import b1 "github.com/b1ug/blink1-go"

// PatternSet is a set of patterns to execute.
type PatternSet struct {
	Name        string          // Name of the pattern
	RepeatTimes uint            // How many times to repeat, 0 means infinite
	States      []b1.LightState // Slice of states to execute in pattern, non-empty patterns will be set to the device automatically
}

// StateSequence is a sequence of light states to play on blink(1).
type StateSequence []b1.LightState
