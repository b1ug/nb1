package exchange

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/1set/gut/ystring"
	"github.com/b1ug/blink1-go"
	"github.com/b1ug/nb1/schema"
	"github.com/b1ug/nb1/util"
)

func ParsePlayText(lines []string) (*schema.PatternSet, error) {
	// parse it
	log.Infow("parse input file", "lines", len(lines))

	// turn into sequence
	var (
		seq             blink1.StateSequence
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
			seq = append(seq, st)
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
	log.Infow("parsed state sequence", "seq", len(seq), "repeat_times", repeatTimes)

	// handle results
	if len(seq) == 0 {
		return nil, errors.New("no valid seq found")
	}
	if !findRepeatTimes {
		// default repeat times is 1
		repeatTimes = 1
	}
	return &schema.PatternSet{
		Name:        title,
		RepeatTimes: repeatTimes,
		Sequence:    seq,
		Length:      uint(len(seq)),
	}, nil
}

// EncodePlayText encodes a pattern set into a slice of strings.
func EncodePlayText(ps *schema.PatternSet) []string {
	if ps == nil {
		return nil
	}

	ls := make([]string, 0, len(ps.Sequence)+2)
	if t := ps.Name; ystring.IsNotBlank(t) {
		ls = append(ls, "Title: "+t)
	}

	if r := ps.RepeatTimes; r == 0 {
		ls = append(ls, "(Repeat Forever)")
	} else if r == 1 {
		ls = append(ls, "(Repeat Once)")
	} else if r == 2 {
		ls = append(ls, "(Repeat Twice)")
	} else {
		ls = append(ls, fmt.Sprintf("(Repeat: %d times)", r))
	}

	var (
		lastLED   blink1.LEDIndex
		lastColor string
	)
	for i, st := range ps.Sequence {
		// color
		hn, ok := util.ConvColorToNameOrHex(st.Color)
		if ok {
			hn = strings.ToTitle(hn)
		}

		// led
		var l, t string
		switch st.LED {
		case blink1.LEDAll:
			l = "all LEDs"
		default:
			l = "LED " + strconv.Itoa(int(st.LED))
		}

		// fade time
		switch f := st.FadeTime; {
		case f < time.Second:
			t = fmt.Sprintf("%d msec", f.Milliseconds())
		case f == time.Second:
			t = "1 second"
		default:
			t = fmt.Sprintf("%v seconds", f.Seconds())
		}

		// check state
		instantly := st.FadeTime < 10*time.Millisecond
		isMaintain := lastLED == st.LED && lastColor == hn

		// sentence
		var sent string
		if isMaintain {
			if instantly {
				sent = fmt.Sprintf("Shift %s to %s instantly", l, hn)
			} else {
				sent = fmt.Sprintf("Maintain %s in %s for %s", l, hn, t)
			}
		} else {
			if instantly {
				sent = fmt.Sprintf("Immediately transition %s to %s", l, hn)
			} else {
				sent = fmt.Sprintf("Transition %s to %s over %s", l, hn, t)
			}
		}

		// add index
		ls = append(ls, strconv.Itoa(i+1)+". "+sent)

		// for next run
		lastLED, lastColor = st.LED, hn
	}

	return ls
}
