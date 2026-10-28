package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

func TestDetailsQueries(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	m := func(id, sender, name string, kind core.Kind, at int) core.Message {
		return core.Message{Platform: core.WhatsApp, ChatID: "g", ID: id, SenderID: sender, SenderName: name,
			Timestamp: base.Add(time.Duration(at) * time.Minute), Kind: kind, Meta: id}
	}
	err = st.Apply(core.BatchEvent{Platform: core.WhatsApp,
		Chats: []core.Chat{{Platform: core.WhatsApp, ID: "g", IsGroup: true}},
		Messages: []core.Message{
			m("text", "raj", "Raj", core.KindText, 1),
			m("photo", "raj", "", core.KindImage, 2), // newest from Raj has no name: the older one names him
			m("link", "ann", "Ann", core.KindLink, 3),
			m("gone", "ann", "Ann", core.KindDocument, 4),
			m("held", "zed", "Zed", core.KindImage, 60), // after the delivery cutoff below
		},
		Deletes: []core.MessageRef{{ChatID: "g", ID: "gone"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cut := base.Add(30 * time.Minute).UnixMilli()

	shared, err := st.Shared(core.WhatsApp, "g", cut, 10)
	if err != nil || len(shared) != 2 || shared[0].ID != "link" || shared[1].ID != "photo" {
		t.Errorf("Shared = %+v, %v; want link then photo", shared, err)
	}
	counts, err := st.KindCounts(core.WhatsApp, "g", cut)
	if err != nil || counts[core.KindImage] != 1 || counts[core.KindDocument] != 0 || counts[core.KindText] != 1 {
		t.Errorf("KindCounts = %v, %v", counts, err)
	}
	people, err := st.Senders(core.WhatsApp, "g", cut, 10)
	// Both wrote twice (a deleted message still counts); Ann wrote last. Zed is still held.
	if err != nil || len(people) != 2 || people[0].Name != "Ann" || people[1].Name != "Raj" || people[1].Messages != 2 {
		t.Errorf("Senders = %+v, %v", people, err)
	}

	if v, _ := st.Pref("details"); v != "" {
		t.Errorf("unset pref = %q", v)
	}
	if err := st.SetPref("details", "off"); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Pref("details"); v != "off" {
		t.Errorf("pref = %q", v)
	}
}

func TestReceipts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	mine := func(id string, at time.Duration) core.Message {
		return core.Message{Platform: core.Telegram, ChatID: "c", ID: id, SenderID: "me", FromMe: true, Timestamp: now.Add(at), Kind: core.KindText, Body: id}
	}
	theirs := core.Message{Platform: core.Telegram, ChatID: "c", ID: "3", SenderID: "x", Timestamp: now, Kind: core.KindText, Body: "hi"}
	if err := s.Apply(core.BatchEvent{Platform: core.Telegram, Messages: []core.Message{mine("2", -3*time.Minute), mine("9", -2*time.Minute), mine("10", -time.Minute), theirs}}); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		msgs, err := s.Messages(core.Telegram, "c", now.Add(time.Hour).UnixMilli(), 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range msgs {
			out = append(out, fmt.Sprintf("%s:%d", m.ID, m.Status))
		}
		return strings.Join(out, " ")
	}
	apply := func(r core.Receipt) {
		t.Helper()
		r.ChatID = "c"
		if err := s.Apply(core.BatchEvent{Platform: core.Telegram, Receipts: []core.Receipt{r}}); err != nil {
			t.Fatal(err)
		}
	}
	apply(core.Receipt{UpToID: "9", Status: core.Seen}) // numeric: 10 is after 9, though "10" < "9" as text
	if got := status(); got != "2:2 9:2 10:0 3:0" {
		t.Errorf("up to ID 9: %s", got)
	}
	apply(core.Receipt{IDs: []string{"10"}, Status: core.Delivered})
	apply(core.Receipt{IDs: []string{"9", "3"}, Status: core.Delivered}) // never back, never theirs
	if got := status(); got != "2:2 9:2 10:1 3:0" {
		t.Errorf("by ID: %s", got)
	}
	apply(core.Receipt{UpTo: now, Status: core.Seen})
	if got := status(); got != "2:2 9:2 10:2 3:0" {
		t.Errorf("up to a time: %s", got)
	}
}
