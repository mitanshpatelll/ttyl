package store

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitanshpatelll/dmz/internal/core"
)

const wa = core.WhatsApp

func msg(chat, id string, fromMe bool, ts time.Time) core.Message {
	return core.Message{Platform: wa, ChatID: chat, ID: id, SenderID: chat, FromMe: fromMe, Timestamp: ts, Kind: core.KindText, Body: id}
}

func TestVisibilityAndUnread(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
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
	if _, err := db.Exec(`INSERT INTO messages (platform, chat_id, id, sender_id, from_me, ts, kind, body) VALUES ('whatsapp', 'c', 'm', 'c', 0, 1, 'text', 'kept')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var body string
	var version, deleted int
	if err := st.db.QueryRow(`SELECT body, deleted FROM messages WHERE id = 'm'`).Scan(&body, &deleted); err != nil {
		t.Fatal(err)
	}
	st.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if body != "kept" || deleted != 0 || version != len(migrations) {
		t.Errorf("after migration: body %q, deleted %d, user_version %d (want kept, 0, %d)", body, deleted, version, len(migrations))
	}
}

func TestEditsDeletesReactions(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
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
