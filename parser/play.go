package parser

import (
	"errors"

	"github.com/1set/gut/ystring"
	"github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/schema"
)

func ParsePlayText(lines []string) (*schema.PatternSet, error) {
	// parse it
	log.Infow("parse input file", "lines", len(lines))

	// turn into states
	var (
		states          []blink1.LightState
		title           string
		repeatTimes     uint
		findRepeatTimes bool
		findTitle       bool
	)
	for _, line := range lines {
		// skip blank lines
		if ystring.IsBlank(line) {
			continue
		}
		// parse as title
		if tl, err := blink1.ParseTitle(line); err == nil {
			if findTitle {
				log.Warnw("duplicate title, take the first one", "line", line, "old_value", title, "new_value", tl)
			} else {
				title = tl
				findTitle = true
			}
			continue
		}
		// parse as state query
		if st, err := blink1.ParseStateQuery(line); err == nil {
			states = append(states, st)
			continue
		}
		// parse as repeat times, only take the first one
		if rt, err := blink1.ParseRepeatTimes(line); err != nil {
			continue
		} else if findRepeatTimes {
			log.Warnw("duplicate repeat times, take the first one", "line", line, "old_value", repeatTimes, "new_value", rt)
		} else {
			repeatTimes = rt
			findRepeatTimes = true
		}
	}
	log.Infow("parsed states", "states", len(states), "repeat_times", repeatTimes)

	// handle results
	if len(states) == 0 {
		return nil, errors.New("no valid states found")
	}
	if !findRepeatTimes {
		// default repeat times is 1
		repeatTimes = 1
	}
	return &schema.PatternSet{
		Name:        title,
		RepeatTimes: repeatTimes,
		Sequence:    states,
	}, nil
}
