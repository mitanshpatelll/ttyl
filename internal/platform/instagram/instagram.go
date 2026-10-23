// Package instagram adapts instameow (mautrix-meta's client for Instagram's private web
// protocol) to ttyl.
//
// Experimental. ttyl logs in with session cookies copied from a browser, loads the inbox the
// web app gets (recent threads with their latest messages), then follows the live socket.
// Older history is not loaded, and end-to-end encrypted chats are not visible.
package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/instameow"
	"go.mau.fi/mautrix-meta/pkg/instameow/slidetypes"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/util/jsontime"

	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/platform"
	"github.com/mitanshpatelll/ttyl/internal/secret"
)

const (
	cookiesKey  = "instagram-cookies"
	maxDownload = 200 << 20
)

type Config struct {
	Vault secret.Vault // cookies live in the keychain
	Log   zerolog.Logger
}

// thread is what ttyl needs about an Instagram thread. Instagram has three IDs per thread:
// the numeric thread key (ttyl's chat ID), the "chat" IGID that live deltas name, and the
// long thread IGID that sending and marking read need.
type thread struct {
	key      int64
	chatIGID string
	threadID string
	name     string
	group    bool
}

type Client struct {
	cfg    Config
	events chan core.Event
	log    zerolog.Logger
	ctx    context.Context // lifetime of the app, for emit

	mu         sync.Mutex
	cli        *instameow.Client
	connecting bool
	cancel     context.CancelFunc
	lastReload time.Time
	reloading  bool
	self       int64
	users      map[int64]string  // FBID -> display name
	threads    map[int64]*thread // by thread key
	byChatID   map[string]int64  // chat IGID -> thread key
}

var _ platform.Account = (*Client)(nil)

func New(ctx context.Context, cfg Config) *Client {
	return &Client{
		cfg: cfg, events: make(chan core.Event, 256), log: cfg.Log, ctx: ctx,
		users: map[int64]string{}, threads: map[int64]*thread{}, byChatID: map[string]int64{},
	}
}

func (c *Client) ID() core.PlatformID       { return core.Instagram }
func (c *Client) Events() <-chan core.Event { return c.events }

func (c *Client) LoggedIn() bool {
	_, ok, err := c.cfg.Vault.Get(cookiesKey)
	return ok && err == nil
}

func (c *Client) emit(evt core.Event) {
	select {
	case c.events <- evt:
	case <-c.ctx.Done():
	}
}

func (c *Client) status(state core.ConnState, detail string) {
	c.emit(core.StatusEvent{Platform: core.Instagram, State: state, Detail: detail})
}

func (c *Client) client() (*instameow.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cli == nil {
		return nil, errors.New("Instagram is not connected")
	}
	return c.cli, nil
}

const warning = `Instagram support is experimental and uses Instagram's private web protocol.
Meta does not allow unofficial clients: an account can be challenged, rate limited or
suspended. Try it with a secondary account first, and keep usage low.

`

const howTo = `Copy cookies from a browser where you are logged in to instagram.com:
  1. Log in to instagram.com in a private window (keep that login only for ttyl).
  2. Developer tools → Application (Safari: Storage) → Cookies → https://www.instagram.com
  3. Paste each value below. Then close the window; don't click "Log out" or ttyl loses access.

`

// Login asks for browser cookies, checks them by loading the inbox, then connects.
func (c *Client) Login(ctx context.Context, t platform.Terminal) error {
	fmt.Fprint(t, warning)
	if answer, err := t.Prompt("Continue? [y/N] ", false); err != nil || !strings.HasPrefix(strings.ToLower(answer), "y") {
		return errors.New("cancelled")
	}
	fmt.Fprint(t, howTo)
	values := map[cookies.MetaCookieName]string{}
	for _, f := range []struct {
		name     cookies.MetaCookieName
		required bool
	}{
		{cookies.IGCookieSessionID, true}, {cookies.IGCookieCSRFToken, true}, {cookies.IGCookieDSUserID, true},
		{cookies.IGCookieMachineID, false}, {cookies.IGCookieDeviceID, false}, {cookies.IGCookieRUR, false},
	} {
		label := string(f.name) + ": "
		if !f.required {
			label = string(f.name) + " (optional, enter to skip): "
		}
		v, err := t.Prompt(label, f.name == cookies.IGCookieSessionID)
		if err != nil {
			return err
		}
		v = strings.Trim(v, "\" ")
		if v == "" && f.required {
			return fmt.Errorf("%s is required", f.name)
		}
		if v != "" {
			values[f.name] = v
		}
	}
	ck := &cookies.Cookies{Platform: types.Instagram}
	ck.UpdateValues(values)
	cli, mailbox, err := c.open(ctx, ck)
	if err != nil {
		return err
	}
	if err := c.saveCookies(cli); err != nil {
		return err
	}
	fmt.Fprintln(t, "Instagram linked.")
	c.start(ctx, cli, mailbox)
	return nil
}

// open creates a client and loads the inbox, which also proves the cookies work.
func (c *Client) open(ctx context.Context, ck *cookies.Cookies) (*instameow.Client, *slidetypes.Mailbox, error) {
	if missing := ck.GetMissingCookieNames(); len(missing) > 0 {
		return nil, nil, fmt.Errorf("missing cookies: %v", missing)
	}
	cli := instameow.NewClient(instameow.ClientParams{
		Cookies:       ck,
		Log:           c.log.With().Str("component", "instameow").Logger(),
		DisableTyping: true, // ttyl never sends typing indicators
	})
	_, mailbox, err := cli.LoadIndex(ctx)
	if err != nil {
		return nil, nil, explain(err)
	}
	if cli.GetOwnFBID() == 0 {
		return nil, nil, errors.New("Instagram did not say which account these cookies belong to")
	}
	return cli, mailbox, nil
}

func explain(err error) error {
	switch {
	case errors.Is(err, httpclient.ErrChallengeRequired), errors.Is(err, httpclient.ErrCheckpointRequired):
		return errors.New("Instagram wants you to confirm it's you: open instagram.com in the browser, finish the check, then copy fresh cookies")
	case errors.Is(err, httpclient.ErrConsentRequired):
		return errors.New("Instagram needs you to accept something in the app or browser first")
	case errors.Is(err, httpclient.ErrAccountSuspended):
		return errors.New("Instagram says this account is suspended")
	case errors.Is(err, httpclient.ErrTokenInvalidated):
		return errors.New("the cookies are not valid (any more): log in again in the browser and run ttyl login instagram")
	case errors.Is(err, httpclient.ErrRateLimited):
		return errors.New("Instagram is rate limiting this account: wait a while before trying again")
	}
	return err
}

func (c *Client) saveCookies(cli *instameow.Client) error {
	data, err := json.Marshal(cli.GetCookies())
	if err != nil {
		return err
	}
	return c.cfg.Vault.Set(cookiesKey, string(data))
}

// Connect loads the inbox and opens the live socket in the background.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	busy := c.cli != nil || c.connecting
	c.connecting = true
	c.mu.Unlock()
	if busy {
		return nil
	}
	done := func() {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
	}
	raw, ok, err := c.cfg.Vault.Get(cookiesKey)
	if err != nil || !ok {
		done()
		return fmt.Errorf("no Instagram cookies: %v", err)
	}
	ck := &cookies.Cookies{Platform: types.Instagram}
	if err := json.Unmarshal([]byte(raw), ck); err != nil {
		done()
		return fmt.Errorf("stored Instagram cookies are corrupt: %w", err)
	}
	c.status(core.StateConnecting, "")
	go func() {
		defer done()
		cli, mailbox, err := c.open(ctx, ck)
		if err != nil {
			c.log.Error().Err(err).Msg("instagram")
			c.status(core.StateDisconnected, err.Error())
			return
		}
		c.start(ctx, cli, mailbox)
	}()
	return nil
}

func (c *Client) start(ctx context.Context, cli *instameow.Client, mailbox *slidetypes.Mailbox) {
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cli, c.cancel, c.self = cli, cancel, cli.GetOwnFBID()
	c.mu.Unlock()
	cli.SetEventHandler(c.handleEvent)
	c.handleMailbox(mailbox)
	go cli.Connect(runCtx)
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	cli, cancel := c.cli, c.cancel
	c.cli, c.cancel = nil, nil
	c.mu.Unlock()
	if cli == nil {
		return
	}
	if err := c.saveCookies(cli); err != nil { // Instagram rotates some cookies; keep the fresh ones
		c.log.Warn().Err(err).Msg("save instagram cookies")
	}
	cancel()
	cli.Disconnect()
}

// Logout forgets the cookies. The browser session they came from stays valid until it is
// logged out in Instagram (Accounts Center → Password and security → Where you're logged in).
func (c *Client) Logout(context.Context) error {
	c.Disconnect()
	if err := c.cfg.Vault.Delete(cookiesKey); err != nil {
		return err
	}
	return errors.New("cookies deleted from ttyl; to end that session for good, log it out in Instagram: Accounts Center → Password and security → Where you're logged in")
}

func (c *Client) handleEvent(ctx context.Context, evt slidetypes.ClientEvent) error {
	switch e := evt.(type) {
	case *slidetypes.Connected:
		c.status(core.StateConnected, "")
	case *slidetypes.Disconnected:
		c.status(core.StateDisconnected, "reconnecting")
	case *slidetypes.AuthError:
		c.log.Error().Err(e.Error).Msg("instagram auth")
		c.status(core.StateLoggedOut, "cookies stopped working, run ttyl login instagram")
	case *slidetypes.ResnapshotRequired:
		go c.reloadMailbox(ctx)
	case *slidetypes.Delta:
		c.handleDelta(ctx, e)
	}
	return nil
}

// reloadMailbox fetches the inbox again, e.g. when the socket says it lost track. Requests are
// coalesced: one at a time and at most one a minute, since bursts are what trips Instagram.
func (c *Client) reloadMailbox(ctx context.Context) {
	c.mu.Lock()
	if c.reloading || time.Since(c.lastReload) < time.Minute {
		c.mu.Unlock()
		return
	}
	c.reloading, c.lastReload = true, time.Now()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.reloading = false
		c.mu.Unlock()
	}()
	cli, err := c.client()
	if err != nil {
		return
	}
	resp, err := cli.GetMailbox(ctx)
	if err != nil {
		c.log.Warn().Err(err).Msg("instagram mailbox")
		return
	}
	c.handleMailbox(resp.GetMailbox())
}

func threadChatID(key int64) string { return strconv.FormatInt(key, 10) }

// learn records a thread, its users and their names.
func (c *Client) learn(info *slidetypes.ThreadInfo) *thread {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &thread{key: info.ThreadKey, chatIGID: info.ID, threadID: info.ThreadID, name: info.ThreadTitle, group: info.IsGroup}
	for _, u := range info.Users {
		if u == nil {
			continue
		}
		name := u.FullName
		if name == "" {
			name = u.Username
		}
		if name != "" && u.InteropMessagingUserFBID != 0 {
			c.users[u.InteropMessagingUserFBID] = name
		}
		if !info.IsGroup && t.name == "" && u.InteropMessagingUserFBID != c.self {
			t.name = name
		}
	}
	c.threads[t.key] = t
	if t.chatIGID != "" {
		c.byChatID[t.chatIGID] = t.key
	}
	return t
}

func (c *Client) chatFor(key int64) core.Chat {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.threads[key]; t != nil {
		return core.Chat{Platform: core.Instagram, ID: threadChatID(key), Name: t.name, IsGroup: t.group}
	}
	return core.Chat{Platform: core.Instagram, ID: threadChatID(key)}
}

func (c *Client) handleMailbox(mailbox *slidetypes.Mailbox) {
	if mailbox == nil {
		return
	}
	b := core.BatchEvent{Platform: core.Instagram, ReadUpTo: map[string]time.Time{}}
	threads := append([]slidetypes.WrappedThreadInfo{}, mailbox.PinnedThreadsV2...)
	for _, edge := range mailbox.ThreadsByFolder.Edges {
		threads = append(threads, edge.Node)
	}
	self := c.selfID()
	for _, wrapped := range threads {
		info := wrapped.AsIGDirectThread
		if info == nil || info.ThreadKey == 0 {
			continue
		}
		t := c.learn(info)
		chatID := threadChatID(t.key)
		b.Chats = append(b.Chats, c.chatFor(t.key))
		if info.SlideMessages != nil {
			for _, edge := range info.SlideMessages.Edges {
				if msg, ok := c.convert(t.key, edge.Node); ok {
					b.Messages = append(b.Messages, msg)
					b.Reactions = append(b.Reactions, reactions(chatID, edge.Node)...)
				}
			}
		}
		for _, r := range info.SlideReadReceipts {
			if r.ParticipantFBID == self {
				b.ReadUpTo[chatID] = r.WatermarkTimestampMS.Time
			}
		}
	}
	c.emit(b)
}

func reactions(chatID string, m *slidetypes.Message) []core.Reaction {
	var out []core.Reaction
	for _, r := range m.Reactions {
		if r == nil {
			continue
		}
		out = append(out, core.Reaction{
			MessageRef: core.MessageRef{ChatID: chatID, ID: m.MessageID},
			SenderID:   strconv.FormatInt(r.SenderFBID, 10), Emoji: r.Reaction, Timestamp: r.ReactionTimestampMS.Time,
		})
	}
	return out
}

// threadForDelta finds the thread a live event belongs to.
func (c *Client) threadForDelta(d *slidetypes.Delta, msg *slidetypes.Message) (int64, bool) {
	c.mu.Lock()
	key, ok := c.byChatID[d.ThreadIGID]
	c.mu.Unlock()
	if ok {
		return key, true
	}
	if msg != nil && msg.ThreadFBID != "" {
		if key, err := strconv.ParseInt(msg.ThreadFBID, 10, 64); err == nil {
			return key, true
		}
	}
	return 0, false
}

func (c *Client) handleDelta(ctx context.Context, d *slidetypes.Delta) {
	var msg *slidetypes.Message
	switch e := d.Data.(type) {
	case *slidetypes.NewMessageEvent:
		msg = e.Message
	case *slidetypes.UpdateThreadNameEvent:
		msg = e.Message
	}
	key, ok := c.threadForDelta(d, msg)
	if !ok {
		// A thread ttyl hasn't seen yet: reload the inbox to learn it.
		c.log.Debug().Str("thread", d.ThreadIGID).Str("type", d.TypeName).Msg("instagram delta for unknown thread")
		go c.reloadMailbox(ctx)
		return
	}
	chatID := threadChatID(key)
	b := core.BatchEvent{Platform: core.Instagram}
	self := c.selfID()

	switch e := d.Data.(type) {
	case *slidetypes.NewMessageEvent:
		if m, ok := c.convert(key, e.Message); ok {
			b.Chats = []core.Chat{c.chatFor(key)}
			b.Messages = []core.Message{m}
		}
	case *slidetypes.EditMessageEvent:
		kind, meta := core.TextKind(e.TextBody)
		b.Edits = []core.Edit{{MessageRef: core.MessageRef{ChatID: chatID, ID: e.MessageID}, Kind: kind, Body: e.TextBody, Meta: meta}}
	case *slidetypes.DeleteMessageEvent:
		b.Deletes = []core.MessageRef{{ChatID: chatID, ID: e.MessageID}}
	case *slidetypes.CreateReactionEvent:
		b.Reactions = []core.Reaction{{MessageRef: core.MessageRef{ChatID: chatID, ID: e.MessageID},
			SenderID: strconv.FormatInt(e.Reaction.SenderFBID, 10), Emoji: e.Reaction.Reaction, Timestamp: e.Reaction.ReactionTimestampMS.Time}}
	case *slidetypes.DeleteReactionEvent:
		b.Reactions = []core.Reaction{{MessageRef: core.MessageRef{ChatID: chatID, ID: e.MessageID},
			SenderID: strconv.FormatInt(e.Reaction.SenderFBID, 10), Timestamp: time.Now()}}
	case *slidetypes.MarkReadEvent: // read in the app or browser
		b.ReadUpTo = map[string]time.Time{chatID: e.ReadTimestampMS.Time}
	case *slidetypes.ReadReceiptEvent:
		if e.ReadReceipt.ParticipantFBID == self {
			b.ReadUpTo = map[string]time.Time{chatID: e.ReadReceipt.WatermarkTimestampMS.Time}
		}
	case *slidetypes.UpdateThreadNameEvent:
		c.mu.Lock()
		if t := c.threads[key]; t != nil {
			t.name = e.ThreadName
		}
		c.mu.Unlock()
		b.Chats = []core.Chat{c.chatFor(key)}
	default:
		return
	}
	if len(b.Chats)+len(b.Messages)+len(b.Edits)+len(b.Deletes)+len(b.Reactions)+len(b.ReadUpTo) > 0 {
		c.emit(b)
	}
}

func (c *Client) selfID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.self
}

func (c *Client) userName(fbid int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.users[fbid]
}

// mediaRef is the download handle for an Instagram attachment: a signed CDN URL.
type mediaRef struct {
	URL  string `json:"u"`
	Name string `json:"n,omitempty"`
}

func (c *Client) convert(key int64, m *slidetypes.Message) (core.Message, bool) {
	if m == nil || m.MessageID == "" {
		return core.Message{}, false
	}
	msg := core.Message{
		Platform:  core.Instagram,
		ChatID:    threadChatID(key),
		ID:        m.MessageID,
		SenderID:  strconv.FormatInt(m.SenderFBID, 10),
		FromMe:    m.SenderFBID == c.selfID(),
		Timestamp: m.TimestampMS.Time,
		Edited:    len(m.SlideEditHistory) > 0,
	}
	if !msg.FromMe {
		msg.SenderName = c.userName(m.SenderFBID)
		if msg.SenderName == "" && m.Sender != nil {
			msg.SenderName = m.Sender.Name
		}
	}
	var url string
	msg.Kind, msg.Body, msg.Meta, url = describe(m)
	if msg.Kind == "" {
		return core.Message{}, false
	}
	if url != "" {
		msg.Media, _ = json.Marshal(mediaRef{URL: url})
		msg.HasMedia = true
	}
	return msg, true
}

// describe maps message content to a kind, text, detail and (for media) a download URL.
func describe(m *slidetypes.Message) (kind core.Kind, body, meta, url string) {
	text := m.TextBody
	switch content := m.Content.Content.(type) {
	case *slidetypes.MessageContentText:
		text = cmpOr(content.TextBody, text)
	case *slidetypes.MessageContentAdminText:
		return "", "", "", "" // "X named the group …" and similar
	case *slidetypes.MessageContentImage:
		return core.KindImage, text, "", firstURL(content.Attachments, false)
	case *slidetypes.MessageContentMultiMedia:
		return core.KindImage, text, fmt.Sprintf("%d items", len(content.Attachments)), firstURL(content.Attachments, false)
	case *slidetypes.MessageContentVideo:
		return core.KindVideo, text, "", firstURL(content.Videos, true)
	case *slidetypes.MessageContentRavenImage: // view-once: never downloaded
		return core.KindImage, text, "view once", ""
	case *slidetypes.MessageContentRavenVideo:
		return core.KindVideo, text, "view once", ""
	case *slidetypes.MessageContentAudio:
		if len(content.AudioAttachments) > 0 && content.AudioAttachments[0] != nil {
			a := content.AudioAttachments[0]
			return core.KindVoice, text, core.FormatDuration(a.PlayableDurationMS / 1000), a.AttachmentCDNURL
		}
		return core.KindVoice, text, "", ""
	case *slidetypes.MessageContentAnimatedMedia:
		for _, a := range content.AnimatedMedia {
			if a == nil {
				continue
			}
			if a.IsSticker {
				return core.KindSticker, text, "", ""
			}
			return core.KindGIF, text, "", cmpOr(a.AttachmentMP4URL, a.AttachmentWebpURL)
		}
		return core.KindGIF, text, "", ""
	case *slidetypes.MessageContentSticker:
		return core.KindSticker, cmpOr(text, content.AltText), "", ""
	case *slidetypes.MessageContentXMA: // shared posts, reels, profiles, stories
		link := ""
		title := ""
		if x := content.XMA; x != nil {
			link = x.TargetURL
			title = cmpOr(x.TitleText, x.XMATitle, x.HeaderTitleText, x.CaptionBodyText)
		}
		if strings.HasPrefix(link, "/") {
			link = "https://www.instagram.com" + link
		}
		body = strings.TrimSpace(strings.Join([]string{cmpOr(content.XMATextBody, text), title, link}, " "))
		if body == "" {
			return "", "", "", ""
		}
		return core.KindLink, body, link, ""
	}
	if text == "" {
		return "", "", "", ""
	}
	kind, meta = core.TextKind(text)
	return kind, text, meta, ""
}

func firstURL(atts []*slidetypes.Attachment, video bool) string {
	for _, a := range atts {
		if a == nil {
			continue
		}
		if video {
			return cmpOr(a.AttachmentCDNURL, a.AttachmentCDNFallbackURL)
		}
		return cmpOr(a.AttachmentCDNURL, a.PreviewCDNURL, a.AttachmentCDNFallbackURL)
	}
	return ""
}

func cmpOr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) threadIGID(chatID string) (string, error) {
	key, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("bad instagram thread id %q", chatID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.threads[key]; t != nil && t.threadID != "" {
		return t.threadID, nil
	}
	return "", errors.New("this Instagram thread is not loaded yet; try again in a moment")
}

func (c *Client) Send(ctx context.Context, chatID, text string) (core.Message, error) {
	cli, err := c.client()
	if err != nil {
		return core.Message{}, err
	}
	threadID, err := c.threadIGID(chatID)
	if err != nil {
		return core.Message{}, err
	}
	otid := strconv.FormatInt(methods.GenerateEpochID(), 10)
	resp, err := cli.SendMessage(ctx, &slidetypes.SendTextRequest{
		IGThreadIGID:       &threadID,
		OfflineThreadingID: otid,
		Text:               slidetypes.SensitiveString{Value: text},
		SendAttribution:    ptr("igd_web_chat_tab:in_thread"),
	})
	if err != nil {
		return core.Message{}, explain(err)
	}
	sent := resp.GetMessage()
	if sent.MessageID == "" {
		return core.Message{}, errors.New("Instagram did not confirm the message")
	}
	ts := sent.TimestampMS.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	kind, meta := core.TextKind(text)
	return core.Message{
		Platform: core.Instagram, ChatID: chatID, ID: sent.MessageID, SenderID: strconv.FormatInt(c.selfID(), 10),
		FromMe: true, Timestamp: ts, Kind: kind, Body: text, Meta: meta,
	}, nil
}

func ptr[T any](v T) *T { return &v }

func (c *Client) MarkRead(ctx context.Context, chat core.Chat, msgs []core.Message) error {
	cli, err := c.client()
	if err != nil {
		return err
	}
	threadID, err := c.threadIGID(chat.ID)
	if err != nil || len(msgs) == 0 {
		return err
	}
	last := msgs[0]
	for _, m := range msgs {
		if m.Timestamp.After(last.Timestamp) {
			last = m
		}
	}
	// The web app sends the read marker in two steps; so does ttyl.
	meta := slidetypes.MarkReadMetadata{IGThreadIGID: threadID}
	if _, err := cli.MarkRead(ctx, &slidetypes.MarkReadRequest{Metadata: meta, Data: slidetypes.MarkReadData{MessageID: last.ID, ItemID: ptr("")}}); err != nil {
		return err
	}
	_, err = cli.MarkReadValidation(ctx, &slidetypes.MarkReadRequest{Metadata: meta, Data: slidetypes.MarkReadData{
		MessageID: last.ID, MessageTimestampMS: jsontime.UnixMilliString{Time: last.Timestamp},
	}})
	return err
}

func (c *Client) Download(ctx context.Context, msg core.Message, handle []byte) ([]byte, string, error) {
	var ref mediaRef
	if err := json.Unmarshal(handle, &ref); err != nil {
		return nil, "", fmt.Errorf("bad media handle: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone:
		return nil, "", errors.New("Instagram's link to this attachment has expired; open it in the app")
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxDownload {
		return nil, "", errors.New("attachment is over 200 MB; open it in the app")
	}
	name := cmpOr(ref.Name, "attachment"+extension(resp.Header.Get("Content-Type")))
	return data, name, nil
}

func extension(mimetype string) string {
	base, _, _ := strings.Cut(mimetype, ";")
	switch strings.TrimSpace(base) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	}
	return ".bin"
}
