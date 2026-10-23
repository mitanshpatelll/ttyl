// Package whatsapp adapts whatsmeow (WhatsApp multi-device, linked as a companion device) to core.Platform.
package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"rsc.io/qr"

	"github.com/mitanshpatelll/dmz/internal/core"
)

type Client struct {
	cli    *whatsmeow.Client
	events chan core.Event
	log    zerolog.Logger
	ctx    context.Context // for store lookups inside event handlers

	mu         sync.Mutex
	knownGroup map[string]bool // groups whose name we have fetched this session
}

// New opens the session database at dbPath. Session keys live there: anyone with this file is logged in as you.
func New(ctx context.Context, dbPath string, log zerolog.Logger) (*Client, error) {
	store.SetOSInfo("dmz", [3]uint32{0, 1, 0})
	wlog := waLog.Zerolog(log.With().Str("component", "whatsmeow").Logger())
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on&_busy_timeout=5000", wlog)
	if err != nil {
		return nil, fmt.Errorf("open whatsapp session: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load whatsapp device: %w", err)
	}
	c := &Client{
		cli:        whatsmeow.NewClient(device, wlog),
		events:     make(chan core.Event, 256),
		log:        log,
		ctx:        ctx,
		knownGroup: map[string]bool{},
	}
	c.cli.AddEventHandler(c.handle)
	return c, nil
}

func (c *Client) ID() core.PlatformID       { return core.WhatsApp }
func (c *Client) Events() <-chan core.Event { return c.events }
func (c *Client) LoggedIn() bool            { return c.cli.Store.ID != nil }
func (c *Client) Disconnect()               { c.cli.Disconnect() }

func (c *Client) Connect(ctx context.Context) error {
	if c.cli.IsConnected() {
		return nil
	}
	c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateConnecting})
	return c.cli.Connect()
}

// Login links dmz as a new companion device by QR code, drawing each code to w. Blocks until paired.
func (c *Client) Login(ctx context.Context, w io.Writer) error {
	qrs, err := c.cli.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := c.cli.Connect(); err != nil {
		return err
	}
	for {
		var item whatsmeow.QRChannelItem
		var ok bool
		select {
		case <-ctx.Done():
			c.cli.Disconnect()
			return ctx.Err()
		case item, ok = <-qrs:
		}
		if !ok {
			return errors.New("pairing aborted")
		}
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			fmt.Fprint(w, "\033[H\033[2J")
			fmt.Fprintln(w, "Link dmz to WhatsApp")
			fmt.Fprintln(w, "Phone: WhatsApp → Settings → Linked devices → Link a device, then scan:")
			fmt.Fprintln(w)
			qrterminal.GenerateHalfBlock(item.Code, qr.L, w)
			fmt.Fprintf(w, "\nCode refreshes in %s. Ctrl+C to cancel.\n", item.Timeout.Round(time.Second))
		case whatsmeow.QRChannelSuccess.Event:
			return nil
		default:
			if item.Error != nil {
				return fmt.Errorf("pairing failed: %w", item.Error)
			}
			return fmt.Errorf("pairing failed: %s", item.Event)
		}
	}
}

// PhoneToChatID turns "+1 555-123-4567" into the DM chat ID "15551234567@s.whatsapp.net".
func PhoneToChatID(phone string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	return types.NewJID(digits, types.DefaultUserServer).String()
}

func (c *Client) Send(ctx context.Context, chatID, text string) (core.Message, error) {
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return core.Message{}, err
	}
	resp, err := c.cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return core.Message{}, err
	}
	kind, meta := textKind(text)
	return core.Message{
		Platform:  core.WhatsApp,
		ChatID:    chatID,
		ID:        resp.ID,
		SenderID:  c.cli.Store.GetJID().ToNonAD().String(),
		FromMe:    true,
		Timestamp: resp.Timestamp,
		Kind:      kind,
		Body:      text,
		Meta:      meta,
	}, nil
}

func (c *Client) MarkRead(ctx context.Context, chat core.Chat, msgs []core.Message) error {
	chatJID, err := types.ParseJID(chat.ID)
	if err != nil {
		return err
	}
	// WhatsApp takes one receipt per sender; in DMs the sender is left empty.
	bySender := map[string][]types.MessageID{}
	for _, m := range msgs {
		sender := ""
		if chat.IsGroup {
			sender = m.SenderID
		}
		bySender[sender] = append(bySender[sender], m.ID)
	}
	for sender, ids := range bySender {
		var senderJID types.JID
		if sender != "" {
			if senderJID, err = types.ParseJID(sender); err != nil {
				return err
			}
		}
		if err := c.cli.MarkRead(ctx, ids, time.Now(), chatJID, senderJID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) emit(evt core.Event) {
	select {
	case c.events <- evt:
	case <-c.ctx.Done():
	}
}

func (c *Client) handle(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		chat, msg, ok := c.convert(v)
		if !ok {
			return
		}
		c.emit(core.BatchEvent{Platform: core.WhatsApp, Chats: []core.Chat{chat}, Messages: []core.Message{msg}})
		if chat.IsGroup {
			go c.fetchGroupName(chat.ID)
		}
	case *events.HistorySync:
		c.handleHistory(v)
	case *events.Receipt:
		// Read on the phone or another linked device: clear unread here too.
		if v.IsFromMe && (v.Type == types.ReceiptTypeRead || v.Type == types.ReceiptTypeReadSelf) {
			chatID := c.normalize(v.Chat).String()
			c.emit(core.BatchEvent{Platform: core.WhatsApp, ReadUpTo: map[string]time.Time{chatID: v.Timestamp}})
		}
	case *events.Connected:
		c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateConnected})
		go c.refreshNames()
	case *events.Disconnected:
		c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateDisconnected, Detail: "reconnecting"})
	case *events.StreamReplaced:
		c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateDisconnected, Detail: "another dmz took over this session"})
	case *events.ConnectFailure:
		c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateDisconnected, Detail: v.Reason.String()})
	case *events.LoggedOut:
		c.emit(core.StatusEvent{Platform: core.WhatsApp, State: core.StateLoggedOut, Detail: "unlinked from phone, restart dmz to link again"})
	case *events.AppStateSyncComplete:
		go c.refreshNames()
	}
}

func (c *Client) handleHistory(v *events.HistorySync) {
	batch := core.BatchEvent{Platform: core.WhatsApp, ReadUpTo: map[string]time.Time{}}
	for _, conv := range v.Data.GetConversations() {
		jid, err := types.ParseJID(conv.GetID())
		if err != nil || skipChat(jid) {
			continue
		}
		chatJID := c.normalize(jid)
		chat := core.Chat{Platform: core.WhatsApp, ID: chatJID.String(), IsGroup: jid.Server == types.GroupServer, Name: conv.GetName()}
		if chat.Name == "" && !chat.IsGroup {
			chat.Name = c.contactName(chatJID, "")
		}
		batch.Chats = append(batch.Chats, chat)

		var incoming []time.Time
		for _, hm := range conv.GetMessages() {
			evt, err := c.cli.ParseWebMessage(jid, hm.GetMessage())
			if err != nil {
				continue
			}
			_, msg, ok := c.convert(evt)
			if !ok {
				continue
			}
			batch.Messages = append(batch.Messages, msg)
			if !msg.FromMe {
				incoming = append(incoming, msg.Timestamp)
			}
		}
		// The phone reports an unread count; turn it into a read marker just before the oldest unread message.
		slices.SortFunc(incoming, func(a, b time.Time) int { return b.Compare(a) })
		switch n := int(conv.GetUnreadCount()); {
		case len(incoming) == 0:
		case n == 0:
			batch.ReadUpTo[chat.ID] = incoming[0]
		case n <= len(incoming):
			batch.ReadUpTo[chat.ID] = incoming[n-1].Add(-time.Millisecond)
		}
	}
	c.emit(batch)
	c.emit(core.SyncEvent{Platform: core.WhatsApp, Progress: int(v.Data.GetProgress())})
}

// refreshNames pushes contact and group names known to WhatsApp into the store.
func (c *Client) refreshNames() {
	var chats []core.Chat
	contacts, err := c.cli.Store.Contacts.GetAllContacts(c.ctx)
	if err != nil {
		c.log.Warn().Err(err).Msg("load contacts")
	}
	for jid, info := range contacts {
		if name := pickName(info, ""); name != "" {
			chats = append(chats, core.Chat{Platform: core.WhatsApp, ID: c.normalize(jid).String(), Name: name})
		}
	}
	groups, err := c.cli.GetJoinedGroups(c.ctx)
	if err != nil {
		c.log.Warn().Err(err).Msg("load groups")
	}
	c.mu.Lock()
	for _, g := range groups {
		c.knownGroup[g.JID.String()] = true
		chats = append(chats, core.Chat{Platform: core.WhatsApp, ID: g.JID.String(), Name: g.Name, IsGroup: true})
	}
	c.mu.Unlock()
	if len(chats) > 0 {
		c.emit(core.BatchEvent{Platform: core.WhatsApp, Chats: chats})
	}
}

func (c *Client) fetchGroupName(chatID string) {
	c.mu.Lock()
	known := c.knownGroup[chatID]
	c.knownGroup[chatID] = true
	c.mu.Unlock()
	if known {
		return
	}
	jid, _ := types.ParseJID(chatID)
	info, err := c.cli.GetGroupInfo(c.ctx, jid)
	if err != nil {
		c.log.Warn().Err(err).Str("chat", chatID).Msg("group info")
		return
	}
	c.emit(core.BatchEvent{Platform: core.WhatsApp, Chats: []core.Chat{{Platform: core.WhatsApp, ID: chatID, Name: info.Name, IsGroup: true}}})
}

// normalize maps device and LID addresses to the plain phone-number JID when known,
// so one person is one chat no matter how WhatsApp addressed the message.
func (c *Client) normalize(jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server == types.HiddenUserServer {
		if pn, err := c.cli.Store.LIDs.GetPNForLID(c.ctx, jid); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return jid
}

func (c *Client) contactName(jid types.JID, pushName string) string {
	info, err := c.cli.Store.Contacts.GetContact(c.ctx, jid)
	if (err != nil || !info.Found) && jid.Server == types.DefaultUserServer {
		if lid, err := c.cli.Store.LIDs.GetLIDForPN(c.ctx, jid); err == nil && !lid.IsEmpty() {
			info, _ = c.cli.Store.Contacts.GetContact(c.ctx, lid)
		}
	}
	return pickName(info, pushName)
}

func pickName(info types.ContactInfo, pushName string) string {
	for _, n := range []string{info.FullName, info.FirstName, info.PushName, pushName, info.BusinessName} {
		if n != "" {
			return n
		}
	}
	return ""
}

// skipChat drops status updates (stories) and channels: feeds are exactly what dmz is avoiding.
func skipChat(jid types.JID) bool {
	return jid == types.StatusBroadcastJID || jid.Server == types.NewsletterServer
}

func (c *Client) convert(evt *events.Message) (core.Chat, core.Message, bool) {
	info := evt.Info
	if skipChat(info.Chat) {
		return core.Chat{}, core.Message{}, false
	}
	kind, body, meta := describe(evt.Message)
	if kind == "" {
		return core.Chat{}, core.Message{}, false
	}
	chatJID, senderJID := c.normalize(info.Chat), c.normalize(info.Sender)
	chat := core.Chat{Platform: core.WhatsApp, ID: chatJID.String(), IsGroup: info.IsGroup}
	msg := core.Message{
		Platform:  core.WhatsApp,
		ChatID:    chat.ID,
		ID:        info.ID,
		SenderID:  senderJID.String(),
		FromMe:    info.IsFromMe,
		Timestamp: info.Timestamp,
		Kind:      kind,
		Body:      body,
		Meta:      meta,
	}
	if !info.IsFromMe {
		msg.SenderName = c.contactName(senderJID, info.PushName)
		if !info.IsGroup {
			chat.Name = msg.SenderName
		}
	}
	return chat, msg, true
}

// describe maps a WhatsApp message to a kind, text and detail. Unsupported kinds (reactions,
// edits, protocol messages) return an empty kind and are skipped in v0.1.
func describe(m *waE2E.Message) (kind core.Kind, body, meta string) {
	switch {
	case m == nil:
	case m.GetConversation() != "":
		kind, meta = textKind(m.GetConversation())
		return kind, m.GetConversation(), meta
	case m.GetExtendedTextMessage() != nil:
		t := m.GetExtendedTextMessage()
		kind, meta = textKind(t.GetText())
		if t.GetMatchedText() != "" {
			kind, meta = core.KindLink, t.GetMatchedText()
		}
		return kind, t.GetText(), meta
	case m.GetImageMessage() != nil:
		return core.KindImage, m.GetImageMessage().GetCaption(), ""
	case m.GetVideoMessage() != nil:
		v := m.GetVideoMessage()
		if v.GetGifPlayback() {
			return core.KindGIF, v.GetCaption(), ""
		}
		return core.KindVideo, v.GetCaption(), duration(v.GetSeconds())
	case m.GetPtvMessage() != nil:
		return core.KindVideo, "", duration(m.GetPtvMessage().GetSeconds())
	case m.GetAudioMessage() != nil:
		a := m.GetAudioMessage()
		if a.GetPTT() {
			return core.KindVoice, "", duration(a.GetSeconds())
		}
		return core.KindAudio, "", duration(a.GetSeconds())
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		return core.KindDocument, d.GetCaption(), d.GetFileName()
	case m.GetStickerMessage() != nil:
		return core.KindSticker, "", ""
	case m.GetLocationMessage() != nil:
		l := m.GetLocationMessage()
		return core.KindLocation, strings.TrimSpace(l.GetName() + " " + l.GetAddress()), ""
	case m.GetLiveLocationMessage() != nil:
		return core.KindLocation, m.GetLiveLocationMessage().GetCaption(), "live"
	case m.GetContactMessage() != nil:
		return core.KindContact, "", m.GetContactMessage().GetDisplayName()
	case m.GetContactsArrayMessage() != nil:
		return core.KindContact, "", m.GetContactsArrayMessage().GetDisplayName()
	default:
		for _, p := range []*waE2E.PollCreationMessage{
			m.GetPollCreationMessage(), m.GetPollCreationMessageV2(), m.GetPollCreationMessageV3(), m.GetPollCreationMessageV5(),
		} {
			if p != nil {
				return core.KindPoll, p.GetName(), ""
			}
		}
	}
	return "", "", ""
}

func textKind(text string) (core.Kind, string) {
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "http://") {
			return core.KindLink, f
		}
	}
	return core.KindText, ""
}

func duration(seconds uint32) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
