package store

import (
	"slices"
	"strings"
	"time"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

// SharedKinds are what a chat's details list as shared: media, files and links.
var SharedKinds = []core.Kind{core.KindImage, core.KindVideo, core.KindGIF, core.KindAudio, core.KindVoice, core.KindDocument, core.KindLink}

// Sender is someone who wrote in a chat.
type Sender struct {
	ID       string
	Name     string // empty when unknown
	Messages int    // how many of their messages are stored
	Last     time.Time
}

// Shared returns up to limit of the newest visible media, file and link messages in a chat,
// newest first. Deleted messages are left out.
func (s *Store) Shared(platform core.PlatformID, chatID string, cut int64, limit int) ([]core.Message, error) {
	kinds := make([]any, len(SharedKinds))
	for i, k := range SharedKinds {
		kinds[i] = string(k)
	}
	args := append(append([]any{cut}, kinds...), limit)
	msgs, err := s.messages(platform, chatID, `
		AND (m.ts <= ? OR m.from_me = 1 OR c.vip = 1) AND m.deleted = 0
		AND m.kind IN (?`+strings.Repeat(", ?", len(kinds)-1)+`)
		ORDER BY m.ts DESC LIMIT ?`, args...)
	slices.Reverse(msgs) // messages returns oldest first
	return msgs, err
}

// KindCounts counts a chat's visible, not deleted messages by kind.
func (s *Store) KindCounts(platform core.PlatformID, chatID string, cut int64) (map[core.Kind]int, error) {
	rows, err := s.db.Query(`
		SELECT m.kind, COUNT(*) FROM messages m JOIN chats c ON c.platform = m.platform AND c.id = m.chat_id
		WHERE m.platform = ? AND m.chat_id = ? AND m.deleted = 0 AND (m.ts <= ? OR m.from_me = 1 OR c.vip = 1)
		GROUP BY m.kind`, platform, chatID, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.Kind]int{}
	for rows.Next() {
		var k core.Kind
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// Senders lists who wrote visible messages in a chat (not you), most active first.
func (s *Store) Senders(platform core.PlatformID, chatID string, cut int64, limit int) ([]Sender, error) {
	// One row per sender: their newest message that carries a name, for the name.
	rows, err := s.db.Query(`
		SELECT id, sender_id, sender_name, ts, n FROM (
			SELECT m.id, m.sender_id, m.sender_name, m.ts,
			       COUNT(*) OVER (PARTITION BY m.sender_id) AS n,
			       ROW_NUMBER() OVER (PARTITION BY m.sender_id ORDER BY m.sender_name <> '' DESC, m.ts DESC) AS r
			FROM messages m JOIN chats c ON c.platform = m.platform AND c.id = m.chat_id
			WHERE m.platform = ? AND m.chat_id = ? AND m.from_me = 0 AND (m.ts <= ? OR c.vip = 1))
		WHERE r = 1 ORDER BY n DESC, ts DESC LIMIT ?`, platform, chatID, cut, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sender
	for rows.Next() {
		var id string
		var ts int64
		var p Sender
		if err := rows.Scan(&id, &p.ID, &p.Name, &ts, &p.Messages); err != nil {
			return nil, err
		}
		name, err := s.sealer.open(p.Name, msgAAD(string(platform), chatID, id, "sender_name")...)
		if err != nil {
			name = unreadable
		}
		p.Name, p.Last = core.Clean(name), time.UnixMilli(ts)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Pref reads a UI preference; "" when unset.
func (s *Store) Pref(key string) (string, error) { return s.setting("ui." + key) }

// SetPref saves a UI preference.
func (s *Store) SetPref(key, value string) error { return s.putSetting("ui."+key, value) }
