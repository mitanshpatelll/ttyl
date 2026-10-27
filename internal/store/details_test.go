package store

import (
	"path/filepath"
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
