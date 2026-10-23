package store

import (
	"database/sql"
	"errors"
	"time"
)

// Usage is one day of dmz use. Received is counted from stored messages, not tracked.
type Usage struct {
	Day        time.Time // local midnight
	Active     time.Duration
	Opens      int
	Sent       int
	Received   int
	Extensions int
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// AddUsage adds to the counters of the local day containing at.
func (s *Store) AddUsage(at time.Time, active time.Duration, opens, sent, extensions int) error {
	_, err := s.db.Exec(`
		INSERT INTO usage (day, seconds, opens, sent, extensions) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (day) DO UPDATE SET
			seconds = seconds + excluded.seconds, opens = opens + excluded.opens,
			sent = sent + excluded.sent, extensions = extensions + excluded.extensions`,
		dayKey(at), int64(active.Round(time.Second).Seconds()), opens, sent, extensions)
	return err
}

// UsageToday returns active time and budget extensions for the local day containing now.
func (s *Store) UsageToday(now time.Time) (active time.Duration, extensions int, err error) {
	var seconds int64
	err = s.db.QueryRow(`SELECT seconds, extensions FROM usage WHERE day = ?`, dayKey(now)).Scan(&seconds, &extensions)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return time.Duration(seconds) * time.Second, extensions, err
}

// UsageDays returns usage for the n local days ending with the day containing now, oldest first.
func (s *Store) UsageDays(now time.Time, n int) ([]Usage, error) {
	y, m, d := now.Date()
	out := make([]Usage, n)
	for i := range out {
		day := time.Date(y, m, d-(n-1-i), 0, 0, 0, 0, now.Location())
		u := Usage{Day: day}
		var seconds int64
		err := s.db.QueryRow(`SELECT seconds, opens, sent, extensions FROM usage WHERE day = ?`, dayKey(day)).
			Scan(&seconds, &u.Opens, &u.Sent, &u.Extensions)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		u.Active = time.Duration(seconds) * time.Second
		next := time.Date(y, m, d-(n-1-i)+1, 0, 0, 0, 0, now.Location())
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE from_me = 0 AND ts >= ? AND ts < ?`,
			day.UnixMilli(), next.UnixMilli()).Scan(&u.Received); err != nil {
			return nil, err
		}
		out[i] = u
	}
	return out, nil
}
