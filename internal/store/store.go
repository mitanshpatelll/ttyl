// Package store persists chats and messages from all platforms in one SQLite file.
//
// Visibility queries take a cutoff (unix ms): incoming messages newer than it are
// "held" by delivery windows unless the chat is VIP. Pass math.MaxInt64 for live delivery.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mitanshpatelll/dmz/internal/core"
)

const schema = `
CREATE TABLE IF NOT EXISTS chats (
	platform     TEXT    NOT NULL,
	id           TEXT    NOT NULL,
	name         TEXT    NOT NULL DEFAULT '',
	is_group     INTEGER NOT NULL DEFAULT 0,
	vip          INTEGER NOT NULL DEFAULT 0,
	last_read_ts INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (platform, id)
);
CREATE TABLE IF NOT EXISTS messages (
	platform    TEXT    NOT NULL,
	chat_id     TEXT    NOT NULL,
	id          TEXT    NOT NULL,
	sender_id   TEXT    NOT NULL,
	sender_name TEXT    NOT NULL DEFAULT '',
	from_me     INTEGER NOT NULL,
	ts          INTEGER NOT NULL,
	kind        TEXT    NOT NULL,
	body        TEXT    NOT NULL DEFAULT '',
	meta        TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (platform, chat_id, id)
);
CREATE INDEX IF NOT EXISTS messages_chat_ts ON messages (platform, chat_id, ts);
`

type Store struct {
	db  *sql.DB
	vip map[core.PlatformID]map[string]bool
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, vip: map[core.PlatformID]map[string]bool{}}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// SetVIP marks exactly these chats as VIP for a platform, including chats created later.
func (s *Store) SetVIP(platform core.PlatformID, chatIDs []string) error {
	set := map[string]bool{}
	for _, id := range chatIDs {
		set[id] = true
	}
	s.vip[platform] = set

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE chats SET vip = 0 WHERE platform = ?`, platform); err != nil {
		return err
	}
	for id := range set {
		if _, err := tx.Exec(`UPDATE chats SET vip = 1 WHERE platform = ? AND id = ?`, platform, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Apply persists a batch in one transaction. Duplicate messages are ignored.
func (s *Store) Apply(b core.BatchEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	upsertChat, err := tx.Prepare(`
		INSERT INTO chats (platform, id, name, is_group, vip) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (platform, id) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			is_group = excluded.is_group`)
	if err != nil {
		return err
	}
	defer upsertChat.Close()
	for _, c := range b.Chats {
		if _, err := upsertChat.Exec(b.Platform, c.ID, c.Name, c.IsGroup, s.vip[b.Platform][c.ID]); err != nil {
			return err
		}
	}

	ensureChat, err := tx.Prepare(`INSERT INTO chats (platform, id, vip) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer ensureChat.Close()
	insertMsg, err := tx.Prepare(`
		INSERT INTO messages (platform, chat_id, id, sender_id, sender_name, from_me, ts, kind, body, meta)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer insertMsg.Close()
	for _, m := range b.Messages {
		if _, err := ensureChat.Exec(b.Platform, m.ChatID, s.vip[b.Platform][m.ChatID]); err != nil {
			return err
		}
		if _, err := insertMsg.Exec(b.Platform, m.ChatID, m.ID, m.SenderID, m.SenderName, m.FromMe,
			m.Timestamp.UnixMilli(), m.Kind, m.Body, m.Meta); err != nil {
			return err
		}
	}

	for chatID, ts := range b.ReadUpTo {
		if err := markRead(tx, b.Platform, chatID, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Summaries returns chats that have a visible message, newest first, and the number of held messages.
func (s *Store) Summaries(cut int64) ([]core.ChatSummary, int, error) {
	rows, err := s.db.Query(`
		SELECT c.platform, c.id, c.name, c.is_group,
		       m.id, m.sender_id, m.sender_name, m.from_me, m.ts, m.kind, m.body, m.meta,
		       (SELECT COUNT(*) FROM messages u
		         WHERE u.platform = c.platform AND u.chat_id = c.id AND u.from_me = 0
		           AND u.ts > c.last_read_ts AND (u.ts <= ?1 OR c.vip = 1))
		FROM chats c
		JOIN messages m ON m.rowid = (
			SELECT x.rowid FROM messages x
			WHERE x.platform = c.platform AND x.chat_id = c.id
			  AND (x.ts <= ?1 OR x.from_me = 1 OR c.vip = 1)
			ORDER BY x.ts DESC LIMIT 1)
		ORDER BY m.ts DESC`, cut)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []core.ChatSummary
	for rows.Next() {
		var cs core.ChatSummary
		var ts int64
		if err := rows.Scan(&cs.Platform, &cs.ID, &cs.Name, &cs.IsGroup,
			&cs.Last.ID, &cs.Last.SenderID, &cs.Last.SenderName, &cs.Last.FromMe, &ts,
			&cs.Last.Kind, &cs.Last.Body, &cs.Last.Meta, &cs.Unread); err != nil {
			return nil, 0, err
		}
		cs.Last.Platform, cs.Last.ChatID, cs.Last.Timestamp = cs.Platform, cs.ID, time.UnixMilli(ts)
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var held int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM messages m
		JOIN chats c ON c.platform = m.platform AND c.id = m.chat_id
		WHERE m.ts > ? AND m.from_me = 0 AND c.vip = 0`, cut).Scan(&held)
	return out, held, err
}

// Messages returns up to limit of the newest visible messages in a chat, oldest first.
func (s *Store) Messages(platform core.PlatformID, chatID string, cut int64, limit int) ([]core.Message, error) {
	return s.messages(platform, chatID, `
		AND (m.ts <= ? OR m.from_me = 1 OR c.vip = 1)
		ORDER BY m.ts DESC LIMIT ?`, cut, limit)
}

// Unread returns visible incoming messages in a chat newer than its read marker, oldest first.
func (s *Store) Unread(platform core.PlatformID, chatID string, cut int64) ([]core.Message, error) {
	return s.messages(platform, chatID, `
		AND m.from_me = 0 AND m.ts > c.last_read_ts AND (m.ts <= ? OR c.vip = 1)
		ORDER BY m.ts DESC`, cut)
}

// messages runs a chat message query; filter continues the WHERE clause and binds args.
func (s *Store) messages(platform core.PlatformID, chatID, filter string, args ...any) ([]core.Message, error) {
	rows, err := s.db.Query(`
		SELECT m.id, m.sender_id, m.sender_name, m.from_me, m.ts, m.kind, m.body, m.meta
		FROM messages m JOIN chats c ON c.platform = m.platform AND c.id = m.chat_id
		WHERE m.platform = ? AND m.chat_id = ? `+filter, append([]any{platform, chatID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Message
	for rows.Next() {
		m := core.Message{Platform: platform, ChatID: chatID}
		var ts int64
		if err := rows.Scan(&m.ID, &m.SenderID, &m.SenderName, &m.FromMe, &ts, &m.Kind, &m.Body, &m.Meta); err != nil {
			return nil, err
		}
		m.Timestamp = time.UnixMilli(ts)
		out = append(out, m)
	}
	slices.Reverse(out)
	return out, rows.Err()
}

// MarkRead moves a chat's local read marker forward. It never sends anything to the platform.
func (s *Store) MarkRead(platform core.PlatformID, chatID string, until time.Time) error {
	return markRead(s.db, platform, chatID, until)
}

func markRead(db interface {
	Exec(string, ...any) (sql.Result, error)
}, platform core.PlatformID, chatID string, until time.Time) error {
	_, err := db.Exec(`UPDATE chats SET last_read_ts = MAX(last_read_ts, ?) WHERE platform = ? AND id = ?`,
		until.UnixMilli(), platform, chatID)
	return err
}
