package store

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// testKey turns on at-rest encryption so tests cover the sealed path.
var testKey = make([]byte, 32)

const wa = core.WhatsApp

func msg(chat, id string, fromMe bool, ts time.Time) core.Message {
	return core.Message{Platform: wa, ChatID: chat, ID: id, SenderID: chat, FromMe: fromMe, Timestamp: ts, Kind: core.KindText, Body: id}
}

func TestVisibilityAndUnread(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetVIP(wa, []string{"vip"}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }
	err = st.Apply(core.BatchEvent{
		Platform: wa,
		Chats:    []core.Chat{{Platform: wa, ID: "mom", Name: "Mom"}, {Platform: wa, ID: "vip"}},
		Messages: []core.Message{
			msg("mom", "m1", false, at(1)),
			msg("mom", "m2", true, at(2)),
			msg("mom", "m3", false, at(10)), // after cutoff: held
			msg("vip", "v1", false, at(10)), // after cutoff, but VIP
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A rename with an empty name must not wipe the real one.
	if err := st.Apply(core.BatchEvent{Platform: wa, Chats: []core.Chat{{Platform: wa, ID: "mom"}}}); err != nil {
		t.Fatal(err)
	}

	cut := at(5).UnixMilli()
	chats, held, err := st.Summaries(cut)
	if err != nil {
		t.Fatal(err)
	}
	if held != 1 {
		t.Errorf("held = %d, want 1", held)
	}
	if len(chats) != 2 || chats[0].ID != "vip" || chats[1].ID != "mom" {
		t.Fatalf("chats = %+v, want vip then mom", chats)
	}
	if mom := chats[1]; mom.Name != "Mom" || mom.Last.ID != "m2" || mom.Unread != 1 {
		t.Errorf("mom = name %q last %q unread %d, want Mom m2 1", mom.Name, mom.Last.ID, mom.Unread)
	}

	msgs, err := st.Messages(wa, "mom", cut, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Errorf("visible messages = %+v, want m1 m2", msgs)
	}

	// Once delivered and read, nothing is unread; duplicates are ignored.
	if err := st.Apply(core.BatchEvent{Platform: wa, Messages: []core.Message{msg("mom", "m1", false, at(1))}}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRead(wa, "mom", at(10)); err != nil {
		t.Fatal(err)
	}
	chats, held, _ = st.Summaries(math.MaxInt64)
	if held != 0 || chats[0].ID != "mom" || chats[0].Unread != 0 {
		t.Errorf("live: held %d, first %q unread %d; want 0, mom, 0", held, chats[0].ID, chats[0].Unread)
	}
	if n, _ := st.Messages(wa, "mom", math.MaxInt64, 10); len(n) != 3 {
		t.Errorf("live messages = %d, want 3", len(n))
	}
}

func TestMigratesV01Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	// v0.1 created the schema without setting user_version.
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO chats (platform, id, name) VALUES ('whatsapp', 'c', 'Mom');
		INSERT INTO messages (platform, chat_id, id, sender_id, from_me, ts, kind, body) VALUES ('whatsapp', 'c', 'm', 'c', 0, 1, 'text', 'kept')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var rawBody, rawName string
	var version int
	st.db.QueryRow(`SELECT body FROM messages WHERE id = 'm'`).Scan(&rawBody)
	st.db.QueryRow(`SELECT name FROM chats WHERE id = 'c'`).Scan(&rawName)
	st.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != len(migrations) || !strings.HasPrefix(rawBody, sealedPrefix) || !strings.HasPrefix(rawName, sealedPrefix) {
		t.Errorf("after migration: user_version %d, raw body %q, raw name %q; want v%d and both sealed", version, rawBody, rawName, len(migrations))
	}
	chats, _, err := st.Summaries(math.MaxInt64)
	if err != nil || len(chats) != 1 || chats[0].Name != "Mom" || chats[0].Last.Body != "kept" {
		t.Errorf("decrypted summaries = %+v, %v", chats, err)
	}
}

func TestEncryptionAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	a, b := msg("c", "a", false, time.UnixMilli(1)), msg("c", "b", false, time.UnixMilli(2))
	a.Body, b.Body = "secret a", "secret b"
	if err := st.Apply(core.BatchEvent{Platform: wa, Chats: []core.Chat{{Platform: wa, ID: "c", Name: "Mom"}}, Messages: []core.Message{a, b}}); err != nil {
		t.Fatal(err)
	}
	var raw string
	st.db.QueryRow(`SELECT group_concat(body || sender_name || meta) FROM messages`).Scan(&raw)
	if strings.Contains(raw, "secret") {
		t.Errorf("plaintext on disk: %q", raw)
	}
	// A ciphertext moved to another row must not decrypt there.
	if _, err := st.db.Exec(`UPDATE messages SET body = (SELECT body FROM messages WHERE id = 'a') WHERE id = 'b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Messages(wa, "c", math.MaxInt64, 10); !errors.Is(err, ErrWrongKey) {
		t.Errorf("swapped ciphertext: err = %v, want ErrWrongKey", err)
	}
	st.Close()

	other := make([]byte, 32)
	other[0] = 1
	if _, err := Open(path, other); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong key: err = %v, want ErrWrongKey", err)
	}
	if _, err := Open(path, nil); err == nil {
		t.Error("opening an encrypted store without a key should fail")
	}
}

func TestEditsDeletesReactions(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	photo := msg("c", "p", false, base)
	photo.Kind, photo.Media, photo.HasMedia = core.KindImage, []byte("handle"), true
	text := msg("c", "t", false, base.Add(time.Minute))
	ref := func(id string) core.MessageRef { return core.MessageRef{ChatID: "c", ID: id} }

	// Reactions and edits in the same batch as their targets still land (messages go in first).
	err = st.Apply(core.BatchEvent{
		Platform: wa,
		Messages: []core.Message{photo, text},
		Edits:    []core.Edit{{MessageRef: ref("t"), Kind: core.KindText, Body: "fixed typo"}},
		Reactions: []core.Reaction{
			{MessageRef: ref("t"), SenderID: "a", Emoji: "❤️", Timestamp: base.Add(2 * time.Minute)},
			{MessageRef: ref("t"), SenderID: "b", Emoji: "❤️", Timestamp: base.Add(2 * time.Minute)},
			{MessageRef: ref("t"), SenderID: "c", Emoji: "😂", Timestamp: base.Add(3 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := st.Media(wa, "c", "p"); string(h) != "handle" {
		t.Errorf("media handle = %q", h)
	}
	// b removes their reaction; an older removal from c must not win over c's newer reaction.
	err = st.Apply(core.BatchEvent{Platform: wa, Reactions: []core.Reaction{
		{MessageRef: ref("t"), SenderID: "b", Timestamp: base.Add(4 * time.Minute)},
		{MessageRef: ref("t"), SenderID: "c", Timestamp: base.Add(time.Minute)},
	}, Deletes: []core.MessageRef{ref("p")}})
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := st.Messages(wa, "c", math.MaxInt64, 10)
	if err != nil {
		t.Fatal(err)
	}
	p, tx := msgs[0], msgs[1]
	if !p.Deleted || p.HasMedia || p.Body != "" {
		t.Errorf("deleted photo = %+v", p)
	}
	if h, _ := st.Media(wa, "c", "p"); h != nil {
		t.Error("deleted message kept its media handle")
	}
	if tx.Body != "fixed typo" || !tx.Edited {
		t.Errorf("edited text = %+v", tx)
	}
	want := []core.ReactionCount{{Emoji: "❤️", Count: 1}, {Emoji: "😂", Count: 1}}
	if len(tx.Reactions) != 2 || tx.Reactions[0] != want[0] || tx.Reactions[1] != want[1] {
		t.Errorf("reactions = %+v, want %+v", tx.Reactions, want)
	}
}

func TestDeleteWithoutChat(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tg := core.Telegram
	st.Apply(core.BatchEvent{Platform: tg, Messages: []core.Message{
		{Platform: tg, ChatID: "u1", ID: "7", SenderID: "u1", Timestamp: time.UnixMilli(1), Kind: core.KindText, Body: "a"},
	}})
	st.Apply(core.BatchEvent{Platform: wa, Messages: []core.Message{msg("x", "7", false, time.UnixMilli(1))}})
	st.Apply(core.BatchEvent{Platform: tg, Deletes: []core.MessageRef{{ID: "7"}}})
	if m, _ := st.Messages(tg, "u1", math.MaxInt64, 10); len(m) != 1 || !m[0].Deleted {
		t.Errorf("telegram message = %+v", m)
	}
	if m, _ := st.Messages(wa, "x", math.MaxInt64, 10); len(m) != 1 || m[0].Deleted {
		t.Error("delete leaked into another platform")
	}
}
