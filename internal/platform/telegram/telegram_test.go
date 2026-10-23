package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/dmz/internal/core"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(context.Background(), Config{Dir: t.TempDir(), Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	c.self = 1
	return c
}

func TestDescribe(t *testing.T) {
	doc := func(attrs ...tg.DocumentAttributeClass) *tg.Message {
		return &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 7, AccessHash: 8, MimeType: "audio/ogg", Attributes: attrs}}}
	}
	cases := []struct {
		name string
		msg  *tg.Message
		kind core.Kind
		body string
		meta string
	}{
		{"text", &tg.Message{Message: "hi"}, core.KindText, "hi", ""},
		{"link preview", &tg.Message{Message: "see https://example.com", Media: &tg.MessageMediaWebPage{}}, core.KindLink, "see https://example.com", "https://example.com"},
		{"photo", &tg.Message{Message: "sunset", Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}, core.KindImage, "sunset", ""},
		{"voice", doc(&tg.DocumentAttributeAudio{Voice: true, Duration: 75}), core.KindVoice, "", "1:15"},
		{"video", doc(&tg.DocumentAttributeVideo{Duration: 9.6}, &tg.DocumentAttributeFilename{FileName: "v.mp4"}), core.KindVideo, "", "0:09"},
		{"sticker beats animated", doc(&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeSticker{}), core.KindSticker, "", ""},
		{"file", doc(&tg.DocumentAttributeFilename{FileName: "cv.pdf"}), core.KindDocument, "", "cv.pdf"},
		{"venue", &tg.Message{Media: &tg.MessageMediaVenue{Title: "Cafe", Address: "Main St"}}, core.KindLocation, "Cafe Main St", ""},
		{"poll", &tg.Message{Media: &tg.MessageMediaPoll{Poll: tg.Poll{Question: tg.TextWithEntities{Text: "lunch?"}}}}, core.KindPoll, "lunch?", ""},
		{"dice without text", &tg.Message{Media: &tg.MessageMediaDice{}}, "", "", ""},
		{"empty", &tg.Message{}, "", "", ""},
	}
	for _, c := range cases {
		kind, body, meta, _ := describe(c.msg)
		if kind != c.kind || body != c.body || meta != c.meta {
			t.Errorf("%s: got (%q, %q, %q), want (%q, %q, %q)", c.name, kind, body, meta, c.kind, c.body, c.meta)
		}
	}
}

func TestConvert(t *testing.T) {
	c := newTestClient(t)
	c.state.learn("u42", peer{Name: "Ann", Hash: 99})
	c.state.learn("g5", peer{Name: "News", Broadcast: true})

	in, ok := c.convert(&tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 42}, Date: 1700000000, Message: "hey",
		Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 3, AccessHash: 4, FileReference: []byte{1}, Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "m", Size: 10}, &tg.PhotoSizeProgressive{Type: "y", Sizes: []int{5, 500}}, &tg.PhotoSize{Type: "x", Size: 100},
		}}}})
	if !ok || in.ChatID != "u42" || in.SenderID != "u42" || in.SenderName != "Ann" || in.FromMe || in.ID != "10" || !in.HasMedia {
		t.Fatalf("incoming = %+v", in)
	}
	var ref mediaRef
	if err := json.Unmarshal(in.Media, &ref); err != nil || ref.Size != "y" || ref.Chat != "u42" || ref.Msg != 10 {
		t.Errorf("media ref = %+v, %v", ref, err)
	}

	out, _ := c.convert(&tg.Message{ID: 11, Out: true, PeerID: &tg.PeerUser{UserID: 42}, Message: "yo", EditDate: 5})
	if !out.FromMe || out.SenderID != "u1" || out.SenderName != "" || !out.Edited {
		t.Errorf("outgoing = %+v", out)
	}
	group, _ := c.convert(&tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 6}, FromID: &tg.PeerUser{UserID: 42}, Message: "hi all"})
	if group.ChatID != "g6" || group.SenderID != "u42" {
		t.Errorf("group = %+v", group)
	}
	if _, ok := c.convert(&tg.Message{ID: 13, PeerID: &tg.PeerChannel{ChannelID: 5}, Message: "post"}); ok {
		t.Error("broadcast channel post was kept")
	}
	if _, ok := c.convert(&tg.MessageService{ID: 14}); ok {
		t.Error("service message was kept")
	}
}

func TestPeersAndInputPeer(t *testing.T) {
	c := newTestClient(t)
	c.learnUser(&tg.User{ID: 42, AccessHash: 99, FirstName: "Ann", LastName: "Lee"})
	c.learnUser(&tg.User{ID: 42, Min: true, AccessHash: 1, FirstName: "Ann"}) // min objects carry no usable hash
	c.learnChats([]tg.ChatClass{&tg.Channel{ID: 6, AccessHash: 66, Title: "Team", Megagroup: true}, &tg.Chat{ID: 7, Title: "Family"}})

	if p := c.state.peer("u42"); p.Hash != 99 || p.Name != "Ann" {
		t.Errorf("u42 = %+v", p)
	}
	if p, ok := mustPeer(t, c, "g6").(*tg.InputPeerChannel); !ok || p.AccessHash != 66 {
		t.Errorf("g6 input peer = %#v", p)
	}
	if _, ok := mustPeer(t, c, "c7").(*tg.InputPeerChat); !ok {
		t.Error("c7 should be a basic chat")
	}
	if _, ok := mustPeer(t, c, "u1").(*tg.InputPeerSelf); !ok {
		t.Error("own chat should be InputPeerSelf")
	}
	if _, err := c.inputPeer("x"); err == nil {
		t.Error("bad id accepted")
	}
	if got := c.chat("c7"); got.Name != "Family" || !got.IsGroup {
		t.Errorf("chat c7 = %+v", got)
	}
}

func mustPeer(t *testing.T, c *Client, id string) tg.InputPeerClass {
	t.Helper()
	p, err := c.inputPeer(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSentMessage(t *testing.T) {
	if id, _ := sentMessage(&tg.UpdateShortSentMessage{ID: 5, Date: 1}); id != "5" {
		t.Errorf("short: %q", id)
	}
	id, date := sentMessage(&tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageID{ID: 9, RandomID: 1},
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 9, Date: 1700000000}},
	}})
	if id != "9" || date.Unix() != 1700000000 {
		t.Errorf("updates: %q %v", id, date)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram.json")
	s, _ := loadState(path)
	s.learn("u1", peer{Name: "Me", Hash: 3})
	s.markSeen("u1", 10)
	s.markSeen("u1", 4) // never goes back
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
	again, err := loadState(path)
	if err != nil || again.peer("u1").Hash != 3 || again.seen("u1") != 10 {
		t.Errorf("reloaded: %+v, %v", again, err)
	}
}

func TestExplain(t *testing.T) {
	err := explain(fmt.Errorf("callback: auth flow: send code: %w", tgerr.New(400, "API_ID_INVALID")))
	if !strings.Contains(err.Error(), "my.telegram.org") {
		t.Errorf("got %v", err)
	}
	if err := explain(tgerr.New(420, "FLOOD_WAIT_30")); !strings.Contains(err.Error(), "30s") {
		t.Errorf("flood wait: %v", err)
	}
	other := errors.New("boom")
	if explain(other) != other {
		t.Error("unknown errors must pass through")
	}
}
