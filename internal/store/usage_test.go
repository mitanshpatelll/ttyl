package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

func TestUsage(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 10, 23, 15, 0, 0, 0, time.Local)
	yesterday := now.AddDate(0, 0, -1)

	st.AddUsage(now, 90*time.Second, 1, 0, 0)
	st.AddUsage(now, 1500*time.Millisecond, 0, 2, 1) // rounds to 2s
	st.AddUsage(yesterday, time.Minute, 1, 0, 0)
	st.Apply(core.BatchEvent{Platform: wa, Messages: []core.Message{
		msg("c", "in-today", false, now.Add(-time.Hour)),
		msg("c", "out-today", true, now.Add(-time.Hour)),
		msg("c", "in-yesterday", false, yesterday),
	}})

	active, ext, err := st.UsageToday(now)
	if err != nil || active != 92*time.Second || ext != 1 {
		t.Errorf("today = %v, %d ext, %v; want 1m32s, 1", active, ext, err)
	}
	days, err := st.UsageDays(now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 || !days[2].Day.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("days = %+v", days)
	}
	if d := days[2]; d.Opens != 1 || d.Sent != 2 || d.Received != 1 || d.Extensions != 1 {
		t.Errorf("today = %+v", d)
	}
	if d := days[1]; d.Active != time.Minute || d.Received != 1 {
		t.Errorf("yesterday = %+v", d)
	}
	if d := days[0]; d.Active != 0 || d.Opens != 0 {
		t.Errorf("empty day = %+v", d)
	}
}
