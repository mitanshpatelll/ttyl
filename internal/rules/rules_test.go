package rules

import (
	"testing"
	"time"
)

func TestWindows(t *testing.T) {
	r, err := New([]string{"19:00", "12:30"})
	if err != nil {
		t.Fatal(err)
	}
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.Local) }
	cases := []struct {
		now, cutoff, next time.Time
	}{
		{day(23, 8, 0), day(22, 19, 0), day(23, 12, 30)},   // before first window: yesterday's last
		{day(23, 12, 30), day(23, 12, 30), day(23, 19, 0)}, // exactly at a window
		{day(23, 15, 0), day(23, 12, 30), day(23, 19, 0)},
		{day(23, 22, 0), day(23, 19, 0), day(24, 12, 30)}, // after last window: tomorrow's first
	}
	for _, c := range cases {
		if got := r.Cutoff(c.now); !got.Equal(c.cutoff) {
			t.Errorf("Cutoff(%v) = %v, want %v", c.now, got, c.cutoff)
		}
		if got := r.Next(c.now); !got.Equal(c.next) {
			t.Errorf("Next(%v) = %v, want %v", c.now, got, c.next)
		}
	}
}

func TestLiveAndInvalid(t *testing.T) {
	if r, _ := New(nil); !r.Live() {
		t.Error("no windows should be live")
	}
	if _, err := New([]string{"7pm"}); err == nil {
		t.Error("want error for 7pm")
	}
}
