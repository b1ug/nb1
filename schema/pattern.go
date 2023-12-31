package schema

import (
	"errors"

	b1 "github.com/b1ug/blink1-go"
)

// PatternSet is a set of patterns to execute.
type PatternSet struct {
	Name        string           `json:"name,omitempty"` // Name of the pattern
	RepeatTimes uint             `json:"repeat"`         // How many times to repeat, 0 means infinite
	Sequence    b1.StateSequence `json:"seq,omitempty"`  // Slice of states to execute in pattern, non-empty patterns will be set to the device automatically
	Length      uint             `json:"len"`            // Length of the sequence, it's not necessary to set this field, it will be set automatically
}

// AutoFill fills the length of the sequence automatically.
func (ps *PatternSet) AutoFill() {
	ps.Length = uint(len(ps.Sequence))
}

// Validate validates the pattern set.
func (ps *PatternSet) Validate() error {
	if len(ps.Sequence) == 0 {
		return errors.New("empty sequence")
	}
	return nil
}
