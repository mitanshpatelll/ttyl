package whatsapp

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

func TestDescribe(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		kind core.Kind
		body string
		meta string
	}{
		{"text", &waE2E.Message{Conversation: proto.String("hi")}, core.KindText, "hi", ""},
		{"link", &waE2E.Message{Conversation: proto.String("look https://instagram.com/reel/x")}, core.KindLink, "look https://instagram.com/reel/x", "https://instagram.com/reel/x"},
		{"voice", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Seconds: proto.Uint32(72)}}, core.KindVoice, "", "1:12"},
		{"photo", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("sunset")}}, core.KindImage, "sunset", ""},
		{"file", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("cv.pdf")}}, core.KindDocument, "", "cv.pdf"},
		{"poll", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: proto.String("lunch?")}}, core.KindPoll, "lunch?", ""},
		{"reaction skipped", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}, "", "", ""},
		{"nil skipped", nil, "", "", ""},
	}
	for _, c := range cases {
		kind, body, meta := describe(c.msg)
		if kind != c.kind || body != c.body || meta != c.meta {
			t.Errorf("%s: got (%q, %q, %q), want (%q, %q, %q)", c.name, kind, body, meta, c.kind, c.body, c.meta)
		}
	}
}

func TestPhoneToChatID(t *testing.T) {
	if got := PhoneToChatID("+1 (555) 123-4567"); got != "15551234567@s.whatsapp.net" {
		t.Errorf("got %q", got)
	}
}

func TestAddRoutesEvents(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, filepath.Join(t.TempDir(), "wa.db"), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	// Pretend we are paired, which is when whatsmeow sets up the contact and LID stores.
	me := types.NewJID("15559999", types.DefaultUserServer)
	c.cli.Store.ID = &me
	sig := make([]byte, 64)
	c.cli.Store.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: sig, AccountSignatureKey: sig[:32], DeviceSignature: sig}
	if err := c.cli.Store.Save(ctx); err != nil {
		t.Fatal(err)
	}
	mom := types.NewJID("15550001", types.DefaultUserServer)
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	event := func(id string, m *waE2E.Message) *events.Message {
		return &events.Message{
			Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: mom, Sender: mom}, ID: id, PushName: "Mom", Timestamp: at},
			Message: m,
		}
	}
	key := &waCommon.MessageKey{ID: proto.String("1")}

	var b core.BatchEvent
	if msg, ok := c.add(&b, event("1", &waE2E.Message{Conversation: proto.String("helo")})); !ok || msg.SenderName != "Mom" {
		t.Fatalf("text: ok %v, %+v", ok, msg)
	}
	c.add(&b, event("2", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: key, Text: proto.String("❤️"), SenderTimestampMS: proto.Int64(at.UnixMilli())}}))
	c.add(&b, event("3", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: key, EditedMessage: &waE2E.Message{Conversation: proto.String("hello")},
	}}))
	c.add(&b, event("4", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: key}}))
	photo, ok := c.add(&b, event("5", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String("sunset"), Mimetype: proto.String("image/jpeg"), JPEGThumbnail: make([]byte, 5000),
	}}))

	story := event("6", &waE2E.Message{Conversation: proto.String("story")})
	story.Info.Chat = types.StatusBroadcastJID
	if _, ok := c.add(&b, story); ok {
		t.Error("status update was kept")
	}

	if len(b.Messages) != 2 || len(b.Chats) != 2 || b.Chats[0].Name != "Mom" {
		t.Errorf("messages %d, chats %+v", len(b.Messages), b.Chats)
	}
	if len(b.Reactions) != 1 || b.Reactions[0].ID != "1" || b.Reactions[0].Emoji != "❤️" {
		t.Errorf("reactions = %+v", b.Reactions)
	}
	if len(b.Edits) != 1 || b.Edits[0].ID != "1" || b.Edits[0].Body != "hello" {
		t.Errorf("edits = %+v", b.Edits)
	}
	if len(b.Deletes) != 1 || b.Deletes[0].ID != "1" {
		t.Errorf("deletes = %+v", b.Deletes)
	}
	if !ok || !photo.HasMedia || len(photo.Media) == 0 || len(photo.Media) > 200 {
		t.Errorf("photo handle: ok %v, %d bytes (thumbnail should be stripped)", ok, len(photo.Media))
	}
	var handle waE2E.Message
	if err := proto.Unmarshal(photo.Media, &handle); err != nil || handle.GetImageMessage().GetMimetype() != "image/jpeg" {
		t.Errorf("handle does not round-trip: %v", err)
	}
}

func TestExtension(t *testing.T) {
	for mt, want := range map[string]string{"audio/ogg; codecs=opus": ".ogg", "image/jpeg": ".jpg", "x/unknown": ".bin"} {
		if got := extension(mt); got != want {
			t.Errorf("extension(%q) = %q, want %q", mt, got, want)
		}
	}
}

func TestOfflineSyncProgress(t *testing.T) {
	c, err := New(context.Background(), filepath.Join(t.TempDir(), "wa.db"), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	syncs := func() []core.SyncEvent {
		var out []core.SyncEvent
		for {
			select {
			case evt := <-c.events:
				if s, ok := evt.(core.SyncEvent); ok {
					out = append(out, s)
				}
			default:
				return out
			}
		}
	}

	c.handle(&events.OfflineSyncPreview{Total: 80, Messages: 30, Receipts: 50})
	for range 30 {
		c.handle(&events.UndecryptableMessage{})
	}
	c.handle(&events.OfflineSyncCompleted{Count: 80})
	want := []core.SyncEvent{
		{Platform: core.WhatsApp, Active: true, Total: 30, Unit: "messages"},
		{Platform: core.WhatsApp, Active: true, Done: 25, Total: 30, Unit: "messages"},
		{Platform: core.WhatsApp, Active: true, Done: 30, Total: 30, Unit: "messages"},
		{Platform: core.WhatsApp},
	}
	if got := syncs(); !slices.Equal(got, want) {
		t.Errorf("offline sync events = %+v, want %+v", got, want)
	}

	// Only receipts missed: nothing worth a loader. A dropped connection also ends counting.
	c.handle(&events.OfflineSyncPreview{Total: 5, Receipts: 5})
	c.handle(&events.UndecryptableMessage{})
	c.handle(&events.OfflineSyncCompleted{Count: 5})
	c.handle(&events.OfflineSyncPreview{Total: 9, Messages: 9})
	c.handle(&events.Disconnected{})
	c.handle(&events.UndecryptableMessage{})
	c.handle(&events.OfflineSyncCompleted{})
	if got := syncs(); len(got) != 1 || got[0].Total != 9 {
		t.Errorf("after receipts-only and dropped syncs = %+v, want only the 9-message start", got)
	}
}
