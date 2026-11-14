package exchange

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/schema"
)

// maxPlayRepeat follows the SDK Pattern's one-byte finite repeat range.
// Zero remains the SDK's infinite-loop value, spelled "forever" in play text.
const maxPlayRepeat = 255

func validatePlayLine(line string) error {
	if len(line) > 4096 || !utf8.ValidString(line) || strings.ContainsAny(line, "\r\n\v\f\u0085\u2028\u2029") {
		return fmt.Errorf("play text requires valid UTF-8 lines of at most 4096 bytes")
	}
	return nil
}

// ParsePlayText reads explicit title:/repeat: metadata and complete SDK states.
// It returns no PatternSet if any line is malformed or metadata is duplicated.
func ParsePlayText(lines []string) (*schema.PatternSet, error) {
	var seq blink1.StateSequence
	title, repeatTimes := "", uint(1)
	seenTitle, seenRepeat := false, false
	for index, line := range lines {
		if err := validatePlayLine(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		parts := strings.SplitN(strings.TrimLeft(line, " \t"), ":", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			switch key {
			case "title":
				if seenTitle {
					return nil, fmt.Errorf("line %d: duplicate title metadata", index+1)
				}
				// One optional separator is syntax; the remaining title is literal.
				title = parts[1]
				if len(title) > 0 && (title[0] == ' ' || title[0] == '\t') {
					title = title[1:]
				}
				seenTitle = true
				continue
			case "repeat":
				if seenRepeat {
					return nil, fmt.Errorf("line %d: duplicate repeat metadata", index+1)
				}
				value := strings.TrimSpace(parts[1])
				if strings.EqualFold(value, "forever") {
					repeatTimes = 0
				} else {
					for _, digit := range value {
						if digit < '0' || digit > '9' {
							return nil, fmt.Errorf("line %d: repeat must be 1..255 or forever", index+1)
						}
					}
					times, err := strconv.ParseUint(value, 10, 8)
					if err != nil {
						return nil, fmt.Errorf("line %d: repeat must be 1..255 or forever: %w", index+1, err)
					}
					if times == 0 {
						return nil, fmt.Errorf("line %d: numeric repeat 0 is not allowed; use forever", index+1)
					}
					repeatTimes = uint(times)
				}
				seenRepeat = true
				continue
			}
		}
		state, err := blink1.ParseStateQuery(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		seq = append(seq, state)
	}
	ps := &schema.PatternSet{Name: title, RepeatTimes: repeatTimes, Sequence: seq}
	ps.AutoFill()
	if err := ValidatePatternSet(*ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// ValidatePatternSet checks the application repeat range and delegates every
// single-state execution rule to the SDK, without opening a device.
func ValidatePatternSet(ps schema.PatternSet) error {
	if err := ps.Validate(); err != nil {
		return err
	}
	if ps.RepeatTimes > maxPlayRepeat {
		return fmt.Errorf("repeat exceeds SDK Pattern limit of %d", maxPlayRepeat)
	}
	if ps.Name != "" {
		if err := validatePlayLine("title: " + ps.Name); err != nil {
			return fmt.Errorf("title: %w", err)
		}
	}
	for index, state := range ps.Sequence {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("state[%d]: %w", index, err)
		}
	}
	return nil
}

// EncodePlayText emits canonical metadata and explicit compact SDK state data.
// On failure it returns no lines, so callers can validate before opening files.
func EncodePlayText(ps schema.PatternSet) ([]string, error) {
	if err := ValidatePatternSet(ps); err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(ps.Sequence)+2)
	if ps.Name != "" {
		lines = append(lines, "title: "+ps.Name)
	}
	repeat := "forever"
	if ps.RepeatTimes != 0 {
		repeat = strconv.FormatUint(uint64(ps.RepeatTimes), 10)
	}
	lines = append(lines, "repeat: "+repeat)
	for index, state := range ps.Sequence {
		text, err := state.MarshalText()
		if err != nil {
			return nil, fmt.Errorf("state[%d]: %w", index, err)
		}
		lines = append(lines, string(text))
	}
	return lines, nil
}
