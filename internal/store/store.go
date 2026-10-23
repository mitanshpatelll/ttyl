// Package store persists chats and messages from all platforms in one SQLite file.
//
// Visibility queries take a cutoff (unix ms): incoming messages newer than it are
// "held" by delivery windows unless the chat is VIP. Pass math.MaxInt64 for live delivery.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// migrations[i] upgrades the schema from user_version i to i+1. Never edit a shipped entry.
var migrations = []string{
	// 1: v0.1 (IF NOT EXISTS: v0.1 created these without setting user_version)
	`CREATE TABLE IF NOT EXISTS chats (
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
	CREATE INDEX IF NOT EXISTS messages_chat_ts ON messages (platform, chat_id, ts);`,

	// 2: edits, deletes, media handles, reactions
	`ALTER TABLE messages ADD COLUMN edited  INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE messages ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE messages ADD COLUMN media   BLOB;
	CREATE TABLE reactions (
		platform   TEXT    NOT NULL,
		chat_id    TEXT    NOT NULL,
		message_id TEXT    NOT NULL,
		sender_id  TEXT    NOT NULL,
		emoji      TEXT    NOT NULL,
		ts         INTEGER NOT NULL,
		PRIMARY KEY (platform, chat_id, message_id, sender_id)
	);`,

	// 3: settings (key check and encryption state)
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,

	// 4: daily usage for the time budget and dmz stats
	`CREATE TABLE usage (
		day        TEXT    PRIMARY KEY, -- local date, YYYY-MM-DD
		seconds    INTEGER NOT NULL DEFAULT 0,
		opens      INTEGER NOT NULL DEFAULT 0,
		sent       INTEGER NOT NULL DEFAULT 0,
		extensions INTEGER NOT NULL DEFAULT 0
	);`,
}

type Store struct {
	db     *sql.DB
	sealer sealer
	vip    map[core.PlatformID]map[string]bool
}

// Open opens or creates the store. With a 32-byte key, message text, names and media handles
// are encrypted at rest (see crypto.go); a nil key keeps them in plaintext (tests, opt-out).
func Open(path string, key []byte) (*Store, error) {
	sl, err := newSealer(key)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, sealer: sl, vip: map[core.PlatformID]map[string]bool{}}
	if err := s.initKey(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database is schema v%d, this dmz only knows v%d: update dmz", version, len(migrations))
	}
	for ; version < len(migrations); version++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			tx.Rollback()
			return fmt.Errorf("v%d: %w", version+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
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

// Apply persists a batch in one transaction. New messages go in first, so edits,
// deletes and reactions in the same batch find their targets. Duplicates are ignored.
func (s *Store) Apply(b core.BatchEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p := b.Platform
	aad := func(chatID, id, col string) []string { return msgAAD(string(p), chatID, id, col) }

	for _, c := range b.Chats {
		if _, err := tx.Exec(`
			INSERT INTO chats (platform, id, name, is_group, vip) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (platform, id) DO UPDATE SET
				name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
				is_group = excluded.is_group`,
			p, c.ID, s.sealer.seal(core.Clean(c.Name), chatAAD(string(p), c.ID)...), c.IsGroup, s.vip[p][c.ID]); err != nil {
			return err
		}
	}
	for _, m := range b.Messages {
		if _, err := tx.Exec(`INSERT INTO chats (platform, id, vip) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			p, m.ChatID, s.vip[p][m.ChatID]); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO messages (platform, chat_id, id, sender_id, sender_name, from_me, ts, kind, body, meta, edited, deleted, media)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			p, m.ChatID, m.ID, m.SenderID, s.sealer.seal(core.Clean(m.SenderName), aad(m.ChatID, m.ID, "sender_name")...),
			m.FromMe, m.Timestamp.UnixMilli(), m.Kind, s.sealer.seal(core.Clean(m.Body), aad(m.ChatID, m.ID, "body")...),
			s.sealer.seal(core.Clean(m.Meta), aad(m.ChatID, m.ID, "meta")...), m.Edited, m.Deleted,
			s.sealer.sealBytes(m.Media, aad(m.ChatID, m.ID, "media")...)); err != nil {
			return err
		}
	}
	for _, e := range b.Edits {
		chatID := e.ChatID
		if chatID == "" { // Instagram edits name only the message; find its chat for the associated data
			err := tx.QueryRow(`SELECT chat_id FROM messages WHERE platform = ? AND id = ?`, p, e.ID).Scan(&chatID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			} else if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			UPDATE messages SET kind = ?, body = ?, meta = ?, edited = 1
			WHERE platform = ? AND chat_id = ? AND id = ? AND deleted = 0`,
			e.Kind, s.sealer.seal(core.Clean(e.Body), aad(chatID, e.ID, "body")...), s.sealer.seal(core.Clean(e.Meta), aad(chatID, e.ID, "meta")...),
			p, chatID, e.ID); err != nil {
			return err
		}
	}
	for _, d := range b.Deletes {
		// Respect the sender: the content is dropped, not just hidden. Empty ChatID = any chat.
		if _, err := tx.Exec(`
			UPDATE messages SET deleted = 1, body = '', meta = '', media = NULL
			WHERE platform = ?1 AND (chat_id = ?2 OR ?2 = '') AND id = ?3`, p, d.ChatID, d.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM reactions WHERE platform = ?1 AND (chat_id = ?2 OR ?2 = '') AND message_id = ?3`,
			p, d.ChatID, d.ID); err != nil {
			return err
		}
	}
	for _, r := range b.Reactions {
		ts := r.Timestamp.UnixMilli()
		var err error
		if r.Emoji == "" {
			_, err = tx.Exec(`DELETE FROM reactions WHERE platform = ? AND chat_id = ? AND message_id = ? AND sender_id = ? AND ts <= ?`,
				p, r.ChatID, r.ID, r.SenderID, ts)
		} else {
			_, err = tx.Exec(`
				INSERT INTO reactions (platform, chat_id, message_id, sender_id, emoji, ts) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (platform, chat_id, message_id, sender_id)
				DO UPDATE SET emoji = excluded.emoji, ts = excluded.ts WHERE excluded.ts >= reactions.ts`,
				p, r.ChatID, r.ID, r.SenderID, core.Clean(r.Emoji), ts)
		}
		if err != nil {
			return err
		}
	}
	for chatID, ts := range b.ReadUpTo {
		if err := markRead(tx, p, chatID, ts); err != nil {
			return err
		}
	}
	for chatID, msgID := range b.ReadUpToMessage {
		if _, err := tx.Exec(`
			UPDATE chats SET last_read_ts = MAX(last_read_ts, COALESCE(
				(SELECT ts FROM messages WHERE platform = ?1 AND chat_id = ?2 AND id = ?3), 0))
			WHERE platform = ?1 AND id = ?2`, p, chatID, msgID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const messageColumns = `m.id, m.sender_id, m.sender_name, m.from_me, m.ts, m.kind, m.body, m.meta, m.edited, m.deleted, m.media IS NOT NULL`

func scanMessage(sc interface{ Scan(...any) error }, m *core.Message, extra ...any) error {
	var ts int64
	dest := append([]any{&m.ID, &m.SenderID, &m.SenderName, &m.FromMe, &ts, &m.Kind, &m.Body, &m.Meta, &m.Edited, &m.Deleted, &m.HasMedia}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return err
	}
	m.Timestamp = time.UnixMilli(ts)
	return nil
}

// unreadable replaces a value that fails to decrypt. A whole-database key mismatch is caught
// at Open; a single bad row (corruption, tampering) must not take the inbox down with it.
const unreadable = "[could not decrypt]"

// openMessage decrypts a scanned message; Platform, ChatID and ID must be set.
func (s *Store) openMessage(m *core.Message) {
	open := func(v *string, col string) {
		plain, err := s.sealer.open(*v, msgAAD(string(m.Platform), m.ChatID, m.ID, col)...)
		if err != nil {
			plain = unreadable
		}
		*v = core.Clean(plain)
	}
	open(&m.SenderName, "sender_name")
	open(&m.Body, "body")
	open(&m.Meta, "meta")
}

func (s *Store) openName(platform core.PlatformID, chatID, name string) string {
	plain, err := s.sealer.open(name, chatAAD(string(platform), chatID)...)
	if err != nil {
		return unreadable
	}
	return core.Clean(plain)
}

// Summaries returns chats that have a visible message, newest first, and the number of held messages.
func (s *Store) Summaries(cut int64) ([]core.ChatSummary, int, error) {
	rows, err := s.db.Query(`
		SELECT `+messageColumns+`, c.platform, c.id, c.name, c.is_group,
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
		if err := scanMessage(rows, &cs.Last, &cs.Platform, &cs.ID, &cs.Name, &cs.IsGroup, &cs.Unread); err != nil {
			return nil, 0, err
		}
		cs.Last.Platform, cs.Last.ChatID = cs.Platform, cs.ID
		s.openMessage(&cs.Last)
		cs.Name = s.openName(cs.Platform, cs.ID, cs.Name)
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

// Messages returns up to limit of the newest visible messages in a chat, oldest first, with reactions.
func (s *Store) Messages(platform core.PlatformID, chatID string, cut int64, limit int) ([]core.Message, error) {
	msgs, err := s.messages(platform, chatID, `
		AND (m.ts <= ? OR m.from_me = 1 OR c.vip = 1)
		ORDER BY m.ts DESC LIMIT ?`, cut, limit)
	if err != nil {
		return nil, err
	}
	return msgs, s.attachReactions(platform, chatID, msgs)
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
		SELECT `+messageColumns+`
		FROM messages m JOIN chats c ON c.platform = m.platform AND c.id = m.chat_id
		WHERE m.platform = ? AND m.chat_id = ? `+filter, append([]any{platform, chatID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Message
	for rows.Next() {
		m := core.Message{Platform: platform, ChatID: chatID}
		if err := scanMessage(rows, &m); err != nil {
			return nil, err
		}
		s.openMessage(&m)
		out = append(out, m)
	}
	slices.Reverse(out)
	return out, rows.Err()
}

func (s *Store) attachReactions(platform core.PlatformID, chatID string, msgs []core.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	// A reaction is never older than its message, so the oldest shown message bounds the scan (a day of clock skew allowed).
	rows, err := s.db.Query(`
		SELECT message_id, emoji, COUNT(*) FROM reactions
		WHERE platform = ? AND chat_id = ? AND ts >= ?
		GROUP BY message_id, emoji ORDER BY MIN(ts)`, platform, chatID, msgs[0].Timestamp.Add(-24*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[string][]core.ReactionCount{}
	for rows.Next() {
		var id string
		var rc core.ReactionCount
		if err := rows.Scan(&id, &rc.Emoji, &rc.Count); err != nil {
			return err
		}
		byID[id] = append(byID[id], rc)
	}
	for i := range msgs {
		msgs[i].Reactions = byID[msgs[i].ID]
	}
	return rows.Err()
}

// Media returns a message's download handle, or nil if it has none.
func (s *Store) Media(platform core.PlatformID, chatID, id string) ([]byte, error) {
	var media []byte
	err := s.db.QueryRow(`SELECT media FROM messages WHERE platform = ? AND chat_id = ? AND id = ?`,
		platform, chatID, id).Scan(&media)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.sealer.openBytes(media, msgAAD(string(platform), chatID, id, "media")...)
}

// DeletePlatform removes every stored chat, message and reaction of a platform.
func (s *Store) DeletePlatform(platform core.PlatformID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"reactions", "messages", "chats"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE platform = ?`, platform); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LastRead is the chat's local read marker: incoming messages after it are new.
func (s *Store) LastRead(platform core.PlatformID, chatID string) (time.Time, error) {
	var ms int64
	err := s.db.QueryRow(`SELECT last_read_ts FROM chats WHERE platform = ? AND id = ?`, platform, chatID).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) || ms == 0 { // never read here: no "new messages" line
		return time.Time{}, nil
	}
	return time.UnixMilli(ms), err
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
