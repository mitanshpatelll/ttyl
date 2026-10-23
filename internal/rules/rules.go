// Package rules decides which messages are visible right now (delivery windows).
package rules

import (
	"fmt"
	"slices"
	"time"
)

type Rules struct {
	windows []int // minutes after midnight, sorted
}

// New parses "HH:MM" delivery windows. No windows means live delivery.
func New(windows []string) (Rules, error) {
	var r Rules
	for _, w := range windows {
		t, err := time.Parse("15:04", w)
		if err != nil {
			return Rules{}, fmt.Errorf("delivery window %q: want HH:MM", w)
		}
		r.windows = append(r.windows, t.Hour()*60+t.Minute())
	}
	slices.Sort(r.windows)
	r.windows = slices.Compact(r.windows)
	return r, nil
}

func (r Rules) Live() bool { return len(r.windows) == 0 }

// Cutoff is the most recent delivery window at or before now. Messages after it are held.
func (r Rules) Cutoff(now time.Time) time.Time {
	for i := len(r.windows) - 1; i >= 0; i-- {
		if t := at(now, 0, r.windows[i]); !t.After(now) {
			return t
		}
	}
	return at(now, -1, r.windows[len(r.windows)-1])
}

// Next is the upcoming delivery window after now.
func (r Rules) Next(now time.Time) time.Time {
	for _, w := range r.windows {
		if t := at(now, 0, w); t.After(now) {
			return t
		}
	}
	return at(now, 1, r.windows[0])
}

func at(now time.Time, dayOffset, minutes int) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d+dayOffset, minutes/60, minutes%60, 0, 0, now.Location())
}
