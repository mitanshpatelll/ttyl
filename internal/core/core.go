// Package core holds the platform-neutral chat model shared by adapters, store and UI.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type PlatformID string

const (
	WhatsApp  PlatformID = "whatsapp"
	Telegram  PlatformID = "telegram"
	Instagram PlatformID = "instagram"
)

type Kind string

const (
	KindText     Kind = "text"
	KindLink     Kind = "link"
	KindImage    Kind = "image"
	KindVideo    Kind = "video"
	KindGIF      Kind = "gif"
	KindAudio    Kind = "audio"
	KindVoice    Kind = "voice"
	KindDocument Kind = "document"
	KindSticker  Kind = "sticker"
	KindLocation Kind = "location"
	KindContact  Kind = "contact"
	KindPoll     Kind = "poll"
)

type Chat struct {
	Platform PlatformID
	ID       string
	Name     string // empty when unknown; never a placeholder, so real names are not overwritten
	IsGroup  bool
}

// Title is the name to show for a chat, falling back to the phone number or raw ID.
func (c Chat) Title() string {
	if c.Name != "" {
		return c.Name
	}
	return FallbackName(c.ID)
}

// FallbackName turns a platform ID like "15551234567@s.whatsapp.net" into "+15551234567".
func FallbackName(id string) string {
	user, server, _ := strings.Cut(id, "@")
	if server == "s.whatsapp.net" {
		return "+" + user
	}
	return user
}

type Message struct {
	Platform   PlatformID
	ChatID     string
	ID         string
	SenderID   string
	SenderName string // empty when unknown
	FromMe     bool
	Timestamp  time.Time
	Kind       Kind
	Body       string // text, or the caption for media
	Meta       string // kind-specific detail: duration, file name, URL
	Edited     bool
	Deleted    bool // deleted by the sender; content is gone
	HasMedia   bool
	Reactions  []ReactionCount // filled when read from the store

	// Media is an opaque, platform-specific handle for downloading the attachment later.
	// Only set when writing; read it back with Store.Media.
	Media []byte
}

type ReactionCount struct {
	Emoji string
	Count int
}

// MessageRef points at a message that may or may not be stored yet. An empty ChatID matches
// the ID in any chat of the platform (Telegram reports DM deletes without the chat).
type MessageRef struct {
	ChatID string
	ID     string
}

type Edit struct {
	MessageRef
	Kind Kind
	Body string
	Meta string
}

type Reaction struct {
	MessageRef
	SenderID  string
	Emoji     string // empty removes the sender's reaction
	Timestamp time.Time
}

// TextKind classifies plain text: a link (with its first URL) or text.
func TextKind(text string) (Kind, string) {
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "http://") {
			return KindLink, f
		}
	}
	return KindText, ""
}

// FormatDuration renders seconds as m:ss for media placeholders.
func FormatDuration(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func (m Message) Sender() string {
	if m.FromMe {
		return "You"
	}
	if m.SenderName != "" {
		return m.SenderName
	}
	return FallbackName(m.SenderID)
}

// ChatSummary is a chat as the inbox shows it.
type ChatSummary struct {
	Chat
	Last   Message
	Unread int
}

type ConnState int

const (
	StateConnecting ConnState = iota
	StateConnected
	StateDisconnected
	StateLoggedOut
)

// Event is anything a platform adapter reports to the app.
type Event interface{ isEvent() }

// BatchEvent carries chats and messages to persist, from live traffic or history sync.
type BatchEvent struct {
	Platform  PlatformID
	Chats     []Chat // created, or renamed when Name is set
	Messages  []Message
	Edits     []Edit
	Deletes   []MessageRef
	Reactions []Reaction
	ReadUpTo  map[string]time.Time // chat ID -> read on another device up to this time
}

type StatusEvent struct {
	Platform PlatformID
	State    ConnState
	Detail   string
}

// SyncEvent reports history sync progress in percent.
type SyncEvent struct {
	Platform PlatformID
	Progress int
}

func (BatchEvent) isEvent()  {}
func (StatusEvent) isEvent() {}
func (SyncEvent) isEvent()   {}

type Platform interface {
	ID() PlatformID
	Connect(ctx context.Context) error
	Disconnect()
	Send(ctx context.Context, chatID, text string) (Message, error)
	// MarkRead sends read receipts ("seen") for the given incoming messages.
	MarkRead(ctx context.Context, chat Chat, msgs []Message) error
	Events() <-chan Event
}

// MediaDownloader is implemented by platforms that can fetch attachments on demand.
type MediaDownloader interface {
	// Download fetches the attachment described by handle (Message.Media) and suggests a file name.
	Download(ctx context.Context, msg Message, handle []byte) (data []byte, filename string, err error)
}
