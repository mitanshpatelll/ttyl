package store

import (
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
