package telegram

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/mitanshpatelll/ttyl/internal/core"
)

// Chat IDs: "u<id>" users, "c<id>" basic groups, "g<id>" channels and supergroups.
func peerID(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerUser:
		return "u" + strconv.FormatInt(p.UserID, 10)
	case *tg.PeerChat:
		return "c" + strconv.FormatInt(p.ChatID, 10)
	case *tg.PeerChannel:
		return "g" + strconv.FormatInt(p.ChannelID, 10)
	}
	return ""
}

func userID(id int64) string    { return "u" + strconv.FormatInt(id, 10) }
func channelID(id int64) string { return "g" + strconv.FormatInt(id, 10) }

func splitID(chatID string) (kind byte, id int64, ok bool) {
	if len(chatID) < 2 {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(chatID[1:], 10, 64)
	return chatID[0], id, err == nil
}

func displayName(u *tg.User) string {
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	if u.Phone != "" {
		return "+" + u.Phone
	}
	return ""
}

func (c *Client) learnUser(u *tg.User) {
	p := peer{Name: displayName(u)}
	if !u.Min {
		p.Hash = u.AccessHash
	}
	c.state.learn(userID(u.ID), p)
}

func (c *Client) learnUsers(users []tg.UserClass) {
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			c.learnUser(u)
		}
	}
}

func (c *Client) learnChat(ch tg.ChatClass) {
	switch ch := ch.(type) {
	case *tg.Chat:
		c.state.learn("c"+strconv.FormatInt(ch.ID, 10), peer{Name: ch.Title, Group: true})
	case *tg.ChatForbidden:
		c.state.learn("c"+strconv.FormatInt(ch.ID, 10), peer{Name: ch.Title, Group: true})
	case *tg.Channel:
		p := peer{Name: ch.Title, Group: true, Broadcast: ch.Broadcast}
		if !ch.Min {
			p.Hash = ch.AccessHash
		}
		c.state.learn(channelID(ch.ID), p)
	case *tg.ChannelForbidden:
		c.state.learn(channelID(ch.ID), peer{Name: ch.Title, Group: true, Broadcast: ch.Broadcast, Hash: ch.AccessHash})
	}
}

func (c *Client) learnChats(chats []tg.ChatClass) {
	for _, ch := range chats {
		c.learnChat(ch)
	}
}

func (c *Client) learnEntities(e tg.Entities) {
	for _, u := range e.Users {
		c.learnUser(u)
	}
	for _, ch := range e.Chats {
		c.learnChat(ch)
	}
	for _, ch := range e.Channels {
		c.learnChat(ch)
	}
}

func (c *Client) chat(id string) core.Chat {
	p := c.state.peer(id)
	return core.Chat{Platform: core.Telegram, ID: id, Name: p.Name, IsGroup: p.Group}
}

// convert maps a Telegram message to ttyl. Service messages, channel posts and empty
// messages are skipped.
func (c *Client) convert(m tg.MessageClass) (core.Message, bool) {
	msg, ok := m.(*tg.Message)
	if !ok {
		return core.Message{}, false
	}
	chatID := peerID(msg.PeerID)
	if chatID == "" || c.state.peer(chatID).Broadcast {
		return core.Message{}, false
	}
	kind, body, meta, ref := describe(msg)
	if kind == "" {
		return core.Message{}, false
	}
	sender := chatID // incoming DMs carry no from_id
	if msg.Out {
		sender = userID(c.selfID())
	} else if msg.FromID != nil {
		sender = peerID(msg.FromID)
	}
	out := core.Message{
		Platform:  core.Telegram,
		ChatID:    chatID,
		ID:        strconv.Itoa(msg.ID),
		SenderID:  sender,
		FromMe:    msg.Out,
		Timestamp: time.Unix(int64(msg.Date), 0),
		Kind:      kind,
		Body:      body,
		Meta:      meta,
		Edited:    msg.EditDate != 0 && !msg.EditHide,
	}
	if !msg.Out {
		out.SenderName = c.state.peer(sender).Name
	}
	if ref != nil {
		ref.Chat, ref.Msg = chatID, msg.ID
		out.Media, _ = json.Marshal(ref)
		out.HasMedia = true
	}
	return out, true
}

// mediaRef is the download handle stored for Telegram attachments.
type mediaRef struct {
	Type  string `json:"t"` // "photo" or "doc"
	ID    int64  `json:"id"`
	Hash  int64  `json:"h"`
	Ref   []byte `json:"r"`
	Size  string `json:"s,omitempty"` // photo size type
	Mime  string `json:"m,omitempty"`
	Name  string `json:"n,omitempty"`
	Bytes int64  `json:"b,omitempty"`
	Chat  string `json:"c"` // to refresh an expired file reference
	Msg   int    `json:"i"`
}

func (r mediaRef) location() tg.InputFileLocationClass {
	if r.Type == "photo" {
		return &tg.InputPhotoFileLocation{ID: r.ID, AccessHash: r.Hash, FileReference: r.Ref, ThumbSize: r.Size}
	}
	return &tg.InputDocumentFileLocation{ID: r.ID, AccessHash: r.Hash, FileReference: r.Ref}
}

func describe(msg *tg.Message) (kind core.Kind, body, meta string, ref *mediaRef) {
	text := msg.Message
	switch media := msg.Media.(type) {
	case nil, *tg.MessageMediaEmpty, *tg.MessageMediaWebPage:
		if text == "" {
			return "", "", "", nil
		}
		kind, meta = core.TextKind(text)
		return kind, text, meta, nil
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return core.KindImage, text, "", nil
		}
		return core.KindImage, text, "", &mediaRef{Type: "photo", ID: photo.ID, Hash: photo.AccessHash, Ref: photo.FileReference, Size: largestSize(photo.Sizes), Mime: "image/jpeg"}
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return core.KindDocument, text, "", nil
		}
		ref = &mediaRef{Type: "doc", ID: doc.ID, Hash: doc.AccessHash, Ref: doc.FileReference, Mime: doc.MimeType, Bytes: doc.Size}
		kind = core.KindDocument
		for _, attr := range doc.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeFilename:
				ref.Name = a.FileName
			case *tg.DocumentAttributeSticker:
				kind = core.KindSticker
			case *tg.DocumentAttributeAnimated:
				if kind != core.KindSticker {
					kind = core.KindGIF
				}
			case *tg.DocumentAttributeAudio:
				if kind == core.KindDocument {
					kind, meta = core.KindAudio, core.FormatDuration(a.Duration)
					if a.Voice {
						kind = core.KindVoice
					}
				}
			case *tg.DocumentAttributeVideo:
				if kind == core.KindDocument {
					kind, meta = core.KindVideo, core.FormatDuration(int(a.Duration))
				}
			}
		}
		if kind == core.KindDocument {
			meta = ref.Name
		}
		return kind, text, meta, ref
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive:
		return core.KindLocation, text, "", nil
	case *tg.MessageMediaVenue:
		return core.KindLocation, strings.TrimSpace(media.Title + " " + media.Address), "", nil
	case *tg.MessageMediaContact:
		return core.KindContact, text, strings.TrimSpace(media.FirstName + " " + media.LastName), nil
	case *tg.MessageMediaPoll:
		return core.KindPoll, media.Poll.Question.Text, "", nil
	}
	// Dice, games, invoices, shared stories: show the caption if there is one.
	if text == "" {
		return "", "", "", nil
	}
	kind, meta = core.TextKind(text)
	return kind, text, meta, nil
}

// largestSize picks the biggest photo size type that can be downloaded.
func largestSize(sizes []tg.PhotoSizeClass) string {
	best, bestBytes := "", -1
	for _, s := range sizes {
		switch s := s.(type) {
		case *tg.PhotoSize:
			if s.Size > bestBytes {
				best, bestBytes = s.Type, s.Size
			}
		case *tg.PhotoSizeProgressive:
			if n := len(s.Sizes); n > 0 && s.Sizes[n-1] > bestBytes {
				best, bestBytes = s.Type, s.Sizes[n-1]
			}
		}
	}
	return best
}
