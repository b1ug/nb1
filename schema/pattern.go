package schema

import b1 "github.com/b1ug/blink1-go"

// PatternSet is a set of patterns to execute.
type PatternSet struct {
	Name        string           `json:"name,omitempty"` // Name of the pattern
	RepeatTimes uint             `json:"repeat"`         // How many times to repeat, 0 means infinite
	Sequence    b1.StateSequence `json:"seq,omitempty"`  // Slice of states to execute in pattern, non-empty patterns will be set to the device automatically
}
