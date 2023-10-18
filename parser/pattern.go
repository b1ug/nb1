package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	b1 "github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
)

var (
	errEmptyDeserial = errors.New("empty string can't be deserialized")
)

// EncodePatternSet serializes a pattern set into a string.
func EncodePatternSet(ps *schema.PatternSet) string {
	ss := simplePatternSet{
		Name:        ps.Name,
		RepeatTimes: ps.RepeatTimes,
		Sequence:    EncodeStateSequence(ps.States),
	}
	data, _ := json.Marshal(ss)
	return string(data)
}

// DecodePatternSet deserializes a PatternSet string into a pattern set object.
func DecodePatternSet(js string) (*schema.PatternSet, error) {
	if js == "" {
		return nil, errEmptyDeserial
	}

	// unmarshal the JSON string into a simple pattern set
	var sps simplePatternSet
	if err := json.Unmarshal([]byte(js), &sps); err != nil {
		return nil, fmt.Errorf("could not deserialize the pattern set string: %w", err)
	}

	// process each serialized light state
	states, err := DecodeStateSequence(sps.Sequence)
	if err != nil {
		return nil, fmt.Errorf("could not deserialize the state sequence: %w", err)
	}

	// map the simple pattern set to a pattern set
	return &schema.PatternSet{
		Name:        sps.Name,
		RepeatTimes: sps.RepeatTimes,
		States:      states,
	}, nil
}

// EncodeStateSequence serializes a slice of light states into a string.
func EncodeStateSequence(states schema.StateSequence) string {
	ls := make([]string, len(states))
	for i := range states {
		ls[i] = serializeLightState(&states[i])
	}
	return strings.Join(ls, stateSeqSeparator)
}

// DecodeStateSequence deserializes a string into a slice of light states.
func DecodeStateSequence(s string) (schema.StateSequence, error) {
	if s == "" {
		return nil, errEmptyDeserial
	}

	// process each serialized light state
	stateParts := strings.Split(s, stateSeqSeparator)
	states := make([]b1.LightState, len(stateParts))
	for i, serializedLightState := range stateParts {
		state, err := deserializeLightState(serializedLightState)
		if err != nil {
			return nil, fmt.Errorf("could not deserialize light state: %w", err)
		}
		states[i] = *state
	}
	return states, nil
}

// simplePatternSet is a converted pattern set for serialization.
type simplePatternSet struct {
	Name        string `json:"name,omitempty"`   // Optional name of the pattern
	RepeatTimes uint   `json:"repeat,omitempty"` // How many times to repeat, 0 means infinite
	Sequence    string `json:"seq,omitempty"`    // Joined slice of states to execute in pattern, non-empty patterns will be set to the device automatically
}

var stateSeqSeparator = ";"

func serializeLightState(st *b1.LightState) string {
	if st == nil {
		return ""
	}
	return fmt.Sprintf(`%sL%dT%d`,
		util.ConvColorToHex(st.Color),
		st.LED,
		st.FadeTime.Milliseconds())
}

// deserializeLightState parses a string into a LightState
func deserializeLightState(s string) (*b1.LightState, error) {
	if s == "" {
		return nil, errEmptyDeserial
	}
	var (
		r, g, b       uint8
		led           b1.LEDIndex
		fadeTimeMilli int
	)
	if _, err := fmt.Sscanf(strings.ToUpper(s), "#%02X%02X%02XL%dT%d", &r, &g, &b, &led, &fadeTimeMilli); err != nil {
		return nil, fmt.Errorf("invalid string format for LightState: %w", err)
	}
	return &b1.LightState{
		Color:    color.RGBA{R: r, G: g, B: b, A: 0xff},
		LED:      led,
		FadeTime: time.Duration(fadeTimeMilli) * time.Millisecond,
	}, nil
}
