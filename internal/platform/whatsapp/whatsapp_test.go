package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/mitanshpatelll/dmz/internal/core"
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
