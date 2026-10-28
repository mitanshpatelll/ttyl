package instagram

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/util/jsontime"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

var at = time.UnixMilli(1700000000000)

func ms(t time.Time) jsontime.UnixMilliString { return jsontime.UnixMilliString{Time: t} }

func message(id string, sender int64, content slidetypes.MessageContent) *slidetypes.Message {
	return &slidetypes.Message{MessageID: id, SenderFBID: sender, TimestampMS: ms(at),
		Content: slidetypes.MessageContentWrapper{Content: content}}
}

func text(s string) slidetypes.MessageContent { return &slidetypes.MessageContentText{TextBody: s} }

func newTestClient() *Client {
	c := New(context.Background(), Config{Log: zerolog.Nop()})
	c.self = 1
	return c
}

func next(t *testing.T, c *Client) core.BatchEvent {
	t.Helper()
	select {
	case evt := <-c.events:
		return evt.(core.BatchEvent)
	case <-time.After(time.Second):
		t.Fatal("no batch")
		return core.BatchEvent{}
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		name       string
		content    slidetypes.MessageContent
		kind       core.Kind
		body, meta string
		media      bool
	}{
		{"text", text("hey"), core.KindText, "hey", "", false},
		{"link", text("see https://x.com"), core.KindLink, "see https://x.com", "https://x.com", false},
		{"photo", &slidetypes.MessageContentImage{Attachments: []*slidetypes.Attachment{{AttachmentCDNURL: "https://cdn/p.jpg"}}}, core.KindImage, "", "", true},
		{"voice", &slidetypes.MessageContentAudio{AudioAttachments: []*slidetypes.AudioAttachment{{PlayableDurationMS: 7400, AttachmentCDNURL: "https://cdn/a"}}}, core.KindVoice, "", "0:07", true},
		{"view once", &slidetypes.MessageContentRavenImage{}, core.KindImage, "", "view once", false},
		{"sticker via animated", &slidetypes.MessageContentAnimatedMedia{AnimatedMedia: []*slidetypes.AnimatedAttachment{{IsSticker: true}}}, core.KindSticker, "", "", false},
		{"shared reel", &slidetypes.MessageContentXMA{XMA: &slidetypes.XMAContent{TargetURL: "/reel/abc/", TitleText: "funny"}}, core.KindLink, "funny https://www.instagram.com/reel/abc/", "https://www.instagram.com/reel/abc/", false},
		{"admin", &slidetypes.MessageContentAdminText{}, "", "", "", false},
		{"unknown, empty", slidetypes.MessageContentUnknown{}, "", "", "", false},
	}
	for _, c := range cases {
		kind, body, meta, url := describe(message("m", 2, c.content))
		if kind != c.kind || body != c.body || meta != c.meta || (url != "") != c.media {
			t.Errorf("%s: got (%q, %q, %q, %q)", c.name, kind, body, meta, url)
		}
	}
}

func TestMailboxAndDeltas(t *testing.T) {
	c := newTestClient()
	reacted := message("m1", 2, text("hi"))
	reacted.Reactions = []*slidetypes.Reaction{{Reaction: "❤", SenderFBID: 1, ReactionTimestampMS: ms(at)}}
	dm := &slidetypes.ThreadInfo{
		ThreadKey: 10, ID: "chat-10", ThreadID: "long-10",
		Users:             []*slidetypes.User{{InteropMessagingUserFBID: 2, FullName: "Ann"}, {InteropMessagingUserFBID: 1, Username: "me"}},
		SlideMessages:     &slidetypes.SlideMessages{Edges: []slidetypes.Node[*slidetypes.Message]{{Node: reacted}, {Node: message("m2", 1, text("yo"))}}},
		SlideReadReceipts: []slidetypes.ReadReceipt{{ParticipantFBID: 1, WatermarkTimestampMS: ms(at)}, {ParticipantFBID: 2, WatermarkTimestampMS: ms(time.UnixMilli(5))}},
	}
	group := &slidetypes.ThreadInfo{ThreadKey: 11, ID: "chat-11", ThreadID: "long-11", IsGroup: true, ThreadTitle: "Trip"}
	mailbox := &slidetypes.Mailbox{}
	mailbox.ThreadsByFolder.Edges = []slidetypes.Node[slidetypes.WrappedThreadInfo]{{Node: slidetypes.WrappedThreadInfo{AsIGDirectThread: dm}}}
	mailbox.PinnedThreadsV2 = []slidetypes.WrappedThreadInfo{{AsIGDirectThread: group}}

	c.handleMailbox(mailbox)
	b := next(t, c)
	names := map[string]core.Chat{}
	for _, ch := range b.Chats {
		names[ch.ID] = ch
	}
	if names["10"].Name != "Ann" || names["10"].IsGroup || names["11"].Name != "Trip" || !names["11"].IsGroup {
		t.Errorf("chats = %+v", b.Chats)
	}
	if len(b.Messages) != 2 || b.Messages[0].SenderName != "Ann" || !b.Messages[1].FromMe {
		t.Errorf("messages = %+v", b.Messages)
	}
	if len(b.Reactions) != 1 || b.ReadUpTo["10"] != at {
		t.Errorf("reactions %+v read %v (only my own receipt counts)", b.Reactions, b.ReadUpTo)
	}
	if id, _ := c.threadIGID("10"); id != "long-10" {
		t.Errorf("thread IGID = %q", id)
	}

	// Live events name the thread by its chat IGID.
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-10", Data: &slidetypes.NewMessageEvent{Message: message("m3", 2, text("new"))}})
	if b := next(t, c); len(b.Messages) != 1 || b.Messages[0].ChatID != "10" {
		t.Errorf("new message batch = %+v", b)
	}
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-10", Data: &slidetypes.EditMessageEvent{MessageID: "m3", TextBody: "newer"}})
	if b := next(t, c); len(b.Edits) != 1 || b.Edits[0].ChatID != "10" || b.Edits[0].Body != "newer" {
		t.Errorf("edit batch = %+v", b)
	}
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-11", Data: &slidetypes.DeleteMessageEvent{MessageID: "m9"}})
	if b := next(t, c); len(b.Deletes) != 1 || b.Deletes[0].ChatID != "11" {
		t.Errorf("delete batch = %+v", b)
	}
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-10", Data: &slidetypes.MarkReadEvent{ReadTimestampMS: ms(at.Add(time.Minute))}})
	if b := next(t, c); !b.ReadUpTo["10"].Equal(at.Add(time.Minute)) {
		t.Errorf("read batch = %+v", b)
	}
	// Someone else's read receipt: your messages up to its watermark were seen.
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-11", Data: &slidetypes.ReadReceiptEvent{
		ReadReceipt: slidetypes.ReadReceipt{ParticipantFBID: 2, WatermarkTimestampMS: ms(at.Add(2 * time.Minute))}}})
	if b := next(t, c); len(b.Receipts) != 1 || b.Receipts[0].ChatID != "11" || b.Receipts[0].Status != core.Seen ||
		!b.Receipts[0].UpTo.Equal(at.Add(2*time.Minute)) || len(b.ReadUpTo) != 0 {
		t.Errorf("seen batch = %+v", b)
	}
	c.handleDelta(context.Background(), &slidetypes.Delta{ThreadIGID: "chat-11", Data: &slidetypes.ReadReceiptEvent{ReadReceipt: slidetypes.ReadReceipt{ParticipantFBID: 2}}})
	select {
	case evt := <-c.events:
		t.Errorf("a read receipt without a time produced %+v", evt)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMediaHandle(t *testing.T) {
	c := newTestClient()
	msg, ok := c.convert(10, message("m", 2, &slidetypes.MessageContentImage{Attachments: []*slidetypes.Attachment{{PreviewCDNURL: "https://cdn/p.jpg"}}}))
	var ref mediaRef
	if !ok || !msg.HasMedia || json.Unmarshal(msg.Media, &ref) != nil || ref.URL != "https://cdn/p.jpg" {
		t.Errorf("photo = %+v", msg)
	}
	if _, err := c.threadIGID("99"); err == nil {
		t.Error("unknown thread should not be sendable")
	}
}
