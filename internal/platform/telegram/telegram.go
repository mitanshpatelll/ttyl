// Package telegram adapts gotd (Telegram's official MTProto API) to core.Platform.
//
// Live messages come through gotd's updates manager, which also fills gaps while connected.
// Messages that arrived while ttyl was closed are caught up from the 100 most recent dialogs on
// every connect. Broadcast channels are dropped like WhatsApp channels; reactions are not shown.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/platform"
	"github.com/mitanshpatelll/ttyl/internal/secret"
)

const (
	sessionKey   = "telegram-session"
	dialogsLimit = 100       // dialogs checked for missed messages on connect
	historyLimit = 50        // messages per history request
	historyPages = 10        // pages fetched per chat to fill a gap (first sync: 1)
	maxDownload  = 200 << 20 // larger attachments are left to the Telegram app
)

type Config struct {
	AppID   int
	AppHash string
	Dir     string       // data dir; telegram.json keeps peers and catch-up marks
	Vault   secret.Vault // the session (auth key) lives in the keychain
	Version string
	Log     zerolog.Logger
}

type Client struct {
	cfg    Config
	state  *state
	events chan core.Event
	log    zerolog.Logger

	mu     sync.Mutex
	api    *tg.Client
	gaps   *updates.Manager
	self   int64
	cancel context.CancelFunc
	done   chan struct{}
	ctx    context.Context // lifetime of the app, for emit
}

var _ platform.Account = (*Client)(nil)

func New(ctx context.Context, cfg Config) (*Client, error) {
	st, err := loadState(filepath.Join(cfg.Dir, "telegram.json"))
	if err != nil {
		return nil, fmt.Errorf("telegram state: %w", err)
	}
	return &Client{cfg: cfg, state: st, events: make(chan core.Event, 256), log: cfg.Log, ctx: ctx}, nil
}

func (c *Client) ID() core.PlatformID       { return core.Telegram }
func (c *Client) Events() <-chan core.Event { return c.events }

func (c *Client) LoggedIn() bool {
	_, ok, err := c.cfg.Vault.Get(sessionKey)
	return ok && err == nil
}

func (c *Client) selfID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.self
}

func (c *Client) currentAPI() (*tg.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		return nil, errors.New("Telegram is not connected")
	}
	return c.api, nil
}

func (c *Client) emit(evt core.Event) {
	select {
	case c.events <- evt:
	case <-c.ctx.Done():
	}
}

func (c *Client) status(state core.ConnState, detail string) {
	c.emit(core.StatusEvent{Platform: core.Telegram, State: state, Detail: detail})
}

// keychainSession stores gotd's session (auth key and DC) in the keychain.
type keychainSession struct{ v secret.Vault }

func (k keychainSession) LoadSession(context.Context) ([]byte, error) {
	s, ok, err := k.v.Get(sessionKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, session.ErrNotFound
	}
	return []byte(s), nil
}

func (k keychainSession) StoreSession(_ context.Context, data []byte) error {
	return k.v.Set(sessionKey, string(data))
}

// newClient builds a gotd client; each Run gets a fresh one.
func (c *Client) newClient(gaps *updates.Manager) *telegram.Client {
	opts := telegram.Options{
		SessionStorage: keychainSession{c.cfg.Vault},
		Device: telegram.DeviceConfig{
			DeviceModel:   "ttyl",
			SystemVersion: runtime.GOOS,
			AppVersion:    c.cfg.Version,
		},
		NoUpdates: gaps == nil,
		Logger:    zlog{c.log},
	}
	if gaps != nil {
		opts.UpdateHandler = gaps
		opts.OnConnectionState = func(s telegram.ConnectionState) {
			if s == telegram.ConnectionStateDisconnected {
				c.status(core.StateDisconnected, "reconnecting")
			}
		}
	}
	return telegram.NewClient(c.cfg.AppID, c.cfg.AppHash, opts)
}

// Login signs in with phone, code and (if set) the two-step password, then connects.
func (c *Client) Login(ctx context.Context, t platform.Terminal) error {
	fmt.Fprintln(t, "Log in to Telegram. ttyl will show up as \"ttyl\" under Settings → Devices.")
	client := c.newClient(nil)
	err := client.Run(ctx, func(ctx context.Context) error {
		return client.Auth().IfNecessary(ctx, auth.NewFlow(termAuth{t}, auth.SendCodeOptions{}))
	})
	if err != nil {
		return explain(err)
	}
	return c.Connect(ctx)
}

// explain turns the Telegram errors people actually hit while logging in into plain advice.
func explain(err error) error {
	for code, advice := range map[string]string{
		"API_ID_INVALID":         "Telegram rejected api_id / api_hash: copy them again from https://my.telegram.org",
		"API_ID_PUBLISHED_FLOOD": "this api_id is blocked for being public: create your own at https://my.telegram.org",
		"PHONE_NUMBER_INVALID":   "that phone number is not valid: include the country code, e.g. +15551234567",
		"PHONE_NUMBER_BANNED":    "Telegram has banned this phone number",
		"PHONE_CODE_INVALID":     "wrong code: run ttyl login telegram again",
		"PHONE_CODE_EXPIRED":     "the code expired: run ttyl login telegram again",
		"PASSWORD_HASH_INVALID":  "wrong two-step verification password",
	} {
		if tgerr.Is(err, code) {
			return errors.New(advice)
		}
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("Telegram asks to wait %s before trying again", d.Round(time.Second))
	}
	return err
}

type termAuth struct{ t platform.Terminal }

var errNoSignUp = errors.New("this number has no Telegram account; sign up in the Telegram app first")

func (a termAuth) Phone(context.Context) (string, error) {
	return a.t.Prompt("Phone number with country code (+…): ", false)
}

func (a termAuth) Password(context.Context) (string, error) {
	return a.t.Prompt("Two-step verification password: ", true)
}

func (a termAuth) Code(context.Context, *tg.AuthSentCode) (string, error) {
	return a.t.Prompt("Code Telegram sent you: ", false)
}

func (a termAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errNoSignUp
}
func (a termAuth) SignUp(context.Context) (auth.UserInfo, error) { return auth.UserInfo{}, errNoSignUp }

// Connect starts the client in the background; status events report progress.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel, c.done = cancel, make(chan struct{})
	done := c.done
	c.mu.Unlock()

	c.status(core.StateConnecting, "")
	dispatcher := tg.NewUpdateDispatcher()
	c.register(dispatcher)
	gaps := updates.New(updates.Config{Handler: dispatcher, Logger: zlog{c.log}})
	client := c.newClient(gaps)

	go func() {
		defer close(done)
		err := client.Run(runCtx, func(ctx context.Context) error {
			st, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if !st.Authorized {
				c.status(core.StateLoggedOut, "session ended, run ttyl login telegram")
				return nil
			}
			c.mu.Lock()
			c.api, c.gaps, c.self = client.API(), gaps, st.User.ID
			c.mu.Unlock()
			c.learnUser(st.User)
			c.status(core.StateConnected, "")
			return gaps.Run(ctx, client.API(), st.User.ID, updates.AuthOptions{
				Forget:  true,
				OnStart: func(ctx context.Context) { go c.catchUp(ctx) },
			})
		})
		c.mu.Lock()
		c.api, c.gaps, c.cancel = nil, nil, nil
		c.mu.Unlock()
		if err != nil && runCtx.Err() == nil {
			c.log.Error().Err(err).Msg("telegram")
			c.status(core.StateDisconnected, err.Error())
		}
		if err := c.state.save(); err != nil {
			c.log.Warn().Err(err).Msg("save telegram state")
		}
	}()
	return nil
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// Logout ends the session on Telegram's side and deletes it locally either way.
func (c *Client) Logout(ctx context.Context) error {
	c.Disconnect()
	client := c.newClient(nil)
	err := client.Run(ctx, func(ctx context.Context) error {
		_, err := client.API().AuthLogOut(ctx)
		return err
	})
	if derr := c.cfg.Vault.Delete(sessionKey); derr != nil {
		return derr
	}
	os.Remove(c.state.path)
	if err != nil {
		return fmt.Errorf("local session deleted, but Telegram said: %w (end it under Settings → Devices)", err)
	}
	return nil
}

func (c *Client) register(d tg.UpdateDispatcher) {
	onNew := func(e tg.Entities, m tg.MessageClass) error {
		c.learnEntities(e)
		if msg, ok := c.convert(m); ok {
			c.emit(core.BatchEvent{Platform: core.Telegram, Chats: []core.Chat{c.chat(msg.ChatID)}, Messages: []core.Message{msg}})
			c.state.markSeen(msg.ChatID, atoi(msg.ID)) // only once it is on its way to the store
		}
		return nil
	}
	onEdit := func(e tg.Entities, m tg.MessageClass) error {
		c.learnEntities(e)
		// Reaction changes also arrive as edits; only real edits have an edit date.
		if msg, ok := c.convert(m); ok && msg.Edited {
			c.emit(core.BatchEvent{Platform: core.Telegram, Edits: []core.Edit{{
				MessageRef: core.MessageRef{ChatID: msg.ChatID, ID: msg.ID}, Kind: msg.Kind, Body: msg.Body, Meta: msg.Meta,
			}}})
		}
		return nil
	}
	deleted := func(chatID string, ids []int) error {
		b := core.BatchEvent{Platform: core.Telegram}
		for _, id := range ids {
			b.Deletes = append(b.Deletes, core.MessageRef{ChatID: chatID, ID: strconv.Itoa(id)})
		}
		c.emit(b)
		return nil
	}
	read := func(chatID string, maxID int) error {
		c.emit(core.BatchEvent{Platform: core.Telegram, ReadUpToMessage: map[string]string{chatID: strconv.Itoa(maxID)}})
		return nil
	}

	d.OnNewMessage(func(_ context.Context, e tg.Entities, u *tg.UpdateNewMessage) error { return onNew(e, u.Message) })
	d.OnNewChannelMessage(func(_ context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return onNew(e, u.Message)
	})
	d.OnEditMessage(func(_ context.Context, e tg.Entities, u *tg.UpdateEditMessage) error { return onEdit(e, u.Message) })
	d.OnEditChannelMessage(func(_ context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return onEdit(e, u.Message)
	})
	// DM and basic-group message IDs are unique per account, so Telegram omits the chat.
	d.OnDeleteMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteMessages) error {
		return deleted("", u.Messages)
	})
	d.OnDeleteChannelMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		return deleted(channelID(u.ChannelID), u.Messages)
	})
	d.OnReadHistoryInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryInbox) error {
		return read(peerID(u.Peer), u.MaxID)
	})
	d.OnReadChannelInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelInbox) error {
		return read(channelID(u.ChannelID), u.MaxID)
	})
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// catchUp loads recent dialogs and fetches history for chats with messages ttyl hasn't seen.
// Seen marks are saved when the client stops, after these events have been drained to the store.
func (c *Client) catchUp(ctx context.Context) {
	api, err := c.currentAPI()
	if err != nil {
		return
	}
	res, err := retry(ctx, func() (tg.MessagesDialogsClass, error) {
		return api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: dialogsLimit})
	})
	if err != nil {
		c.log.Error().Err(err).Msg("telegram dialogs")
		return
	}
	dialogs, ok := res.AsModified()
	if !ok {
		return
	}
	c.learnUsers(dialogs.GetUsers())
	c.learnChats(dialogs.GetChats())

	type target struct {
		chatID       string
		top, readMax int
	}
	var targets []target
	batch := core.BatchEvent{Platform: core.Telegram}
	for _, m := range dialogs.GetMessages() {
		if msg, ok := c.convert(m); ok {
			batch.Messages = append(batch.Messages, msg)
		}
	}
	for _, d := range dialogs.GetDialogs() {
		dlg, ok := d.(*tg.Dialog)
		if !ok {
			continue
		}
		id := peerID(dlg.Peer)
		if id == "" || c.state.peer(id).Broadcast {
			continue
		}
		batch.Chats = append(batch.Chats, c.chat(id))
		if dlg.TopMessage > c.state.seen(id) {
			targets = append(targets, target{id, dlg.TopMessage, dlg.ReadInboxMaxID})
		}
	}
	c.emit(batch)

	for i, t := range targets {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond): // stay well under Telegram's rate limits
			}
		}
		msgs, err := c.history(ctx, t.chatID, c.state.seen(t.chatID))
		if err != nil {
			// Keep what arrived, but don't move the seen mark: the gap is retried next time.
			c.log.Warn().Err(err).Str("chat", t.chatID).Msg("telegram history")
			c.emit(core.BatchEvent{Platform: core.Telegram, Messages: msgs})
			continue
		}
		b := core.BatchEvent{Platform: core.Telegram, Chats: []core.Chat{c.chat(t.chatID)}, Messages: msgs, ReadUpTo: map[string]time.Time{}}
		// Everything up to the dialog's read marker was read on another device.
		for _, m := range msgs {
			if !m.FromMe && atoi(m.ID) <= t.readMax && m.Timestamp.After(b.ReadUpTo[t.chatID]) {
				b.ReadUpTo[t.chatID] = m.Timestamp
			}
		}
		c.emit(b)
		c.state.markSeen(t.chatID, t.top)
	}
}

func (c *Client) history(ctx context.Context, chatID string, minID int) ([]core.Message, error) {
	api, err := c.currentAPI()
	if err != nil {
		return nil, err
	}
	peer, err := c.inputPeer(chatID)
	if err != nil {
		return nil, err
	}
	raw, err := pageHistory(ctx, minID, func(offsetID int) ([]tg.MessageClass, error) {
		res, err := retry(ctx, func() (tg.MessagesMessagesClass, error) {
			return api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: historyLimit, MinID: minID, OffsetID: offsetID})
		})
		if err != nil {
			return nil, err
		}
		page, ok := res.AsModified()
		if !ok {
			return nil, nil
		}
		c.learnUsers(page.GetUsers())
		c.learnChats(page.GetChats())
		return page.GetMessages(), nil
	})
	var out []core.Message
	for _, m := range raw {
		if msg, ok := c.convert(m); ok {
			out = append(out, msg)
		}
	}
	return out, err
}

// pageHistory walks back from the newest message until it reaches minID. Each page holds the
// newest historyLimit messages older than the offset. A first sync (minID 0) takes one page.
func pageHistory(ctx context.Context, minID int, fetch func(offsetID int) ([]tg.MessageClass, error)) ([]tg.MessageClass, error) {
	pages := historyPages
	if minID == 0 {
		pages = 1
	}
	var out []tg.MessageClass
	offset := 0
	for i := 0; i < pages; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
		msgs, err := fetch(offset)
		if err != nil {
			return out, err
		}
		out = append(out, msgs...)
		oldest := 0
		for _, m := range msgs {
			if id := m.GetID(); oldest == 0 || id < oldest {
				oldest = id
			}
		}
		if len(msgs) < historyLimit || oldest <= minID+1 {
			break
		}
		offset = oldest
	}
	return out, nil
}

// retry waits out one FLOOD_WAIT and tries again.
func retry[T any](ctx context.Context, f func() (T, error)) (T, error) {
	v, err := f()
	if d, ok := tgerr.AsFloodWait(err); ok && d < 2*time.Minute {
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(d + time.Second):
		}
		return f()
	}
	return v, err
}

func (c *Client) inputPeer(chatID string) (tg.InputPeerClass, error) {
	kind, id, ok := splitID(chatID)
	if !ok {
		return nil, fmt.Errorf("bad telegram chat id %q", chatID)
	}
	p := c.state.peer(chatID)
	switch kind {
	case 'u':
		if id == c.selfID() {
			return &tg.InputPeerSelf{}, nil
		}
		return &tg.InputPeerUser{UserID: id, AccessHash: p.Hash}, nil
	case 'c':
		return &tg.InputPeerChat{ChatID: id}, nil
	case 'g':
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: p.Hash}, nil
	}
	return nil, fmt.Errorf("bad telegram chat id %q", chatID)
}

func (c *Client) Send(ctx context.Context, chatID, text string) (core.Message, error) {
	api, err := c.currentAPI()
	if err != nil {
		return core.Message{}, err
	}
	peer, err := c.inputPeer(chatID)
	if err != nil {
		return core.Message{}, err
	}
	upd, err := message.NewSender(api).To(peer).Text(ctx, text)
	if err != nil {
		return core.Message{}, err
	}
	id, date := sentMessage(upd)
	c.mu.Lock()
	gaps := c.gaps
	c.mu.Unlock()
	if gaps != nil {
		// Keep the update sequence in step; the echoed message is deduplicated by ID.
		if err := gaps.Handle(ctx, upd); err != nil {
			c.log.Debug().Err(err).Msg("telegram sent update")
		}
	}
	kind, meta := core.TextKind(text)
	return core.Message{
		Platform: core.Telegram, ChatID: chatID, ID: id, SenderID: userID(c.selfID()), FromMe: true,
		Timestamp: date, Kind: kind, Body: text, Meta: meta,
	}, nil
}

// sentMessage finds the new message's ID and date in sendMessage's reply.
func sentMessage(upd tg.UpdatesClass) (string, time.Time) {
	switch u := upd.(type) {
	case *tg.UpdateShortSentMessage:
		return strconv.Itoa(u.ID), time.Unix(int64(u.Date), 0)
	case *tg.Updates:
		id, date := "", time.Now()
		for _, x := range u.Updates {
			switch x := x.(type) {
			case *tg.UpdateMessageID:
				id = strconv.Itoa(x.ID)
			case *tg.UpdateNewMessage:
				if m, ok := x.Message.(*tg.Message); ok {
					id, date = strconv.Itoa(m.ID), time.Unix(int64(m.Date), 0)
				}
			case *tg.UpdateNewChannelMessage:
				if m, ok := x.Message.(*tg.Message); ok {
					id, date = strconv.Itoa(m.ID), time.Unix(int64(m.Date), 0)
				}
			}
		}
		if id != "" {
			return id, date
		}
	}
	return "local-" + strconv.FormatInt(time.Now().UnixNano(), 36), time.Now()
}

func (c *Client) MarkRead(ctx context.Context, chat core.Chat, msgs []core.Message) error {
	api, err := c.currentAPI()
	if err != nil {
		return err
	}
	maxID := 0
	for _, m := range msgs {
		maxID = max(maxID, atoi(m.ID))
	}
	kind, id, _ := splitID(chat.ID)
	if kind == 'g' {
		_, err = api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: &tg.InputChannel{ChannelID: id, AccessHash: c.state.peer(chat.ID).Hash}, MaxID: maxID,
		})
		return err
	}
	peer, err := c.inputPeer(chat.ID)
	if err != nil {
		return err
	}
	_, err = api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer, MaxID: maxID})
	return err
}

func (c *Client) Download(ctx context.Context, msg core.Message, handle []byte) ([]byte, string, error) {
	var ref mediaRef
	if err := json.Unmarshal(handle, &ref); err != nil {
		return nil, "", fmt.Errorf("bad media handle: %w", err)
	}
	if ref.Bytes > maxDownload {
		return nil, "", fmt.Errorf("attachment is %d MB; open it in the Telegram app", ref.Bytes>>20)
	}
	data, err := c.fetch(ctx, ref)
	if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") {
		// File references expire; asking for the message again gives a fresh one.
		if ref, err = c.refresh(ctx, ref); err == nil {
			data, err = c.fetch(ctx, ref)
		}
	}
	if err != nil {
		return nil, "", err
	}
	name := ref.Name
	if name == "" {
		ext := ".bin"
		if exts, _ := mime.ExtensionsByType(ref.Mime); len(exts) > 0 {
			ext = exts[0]
		}
		if ref.Mime == "image/jpeg" {
			ext = ".jpg"
		}
		name = msg.ID + ext
	}
	return data, name, nil
}

func (c *Client) fetch(ctx context.Context, ref mediaRef) ([]byte, error) {
	api, err := c.currentAPI()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	_, err = downloader.NewDownloader().Download(api, ref.location()).Stream(ctx, &buf)
	return buf.Bytes(), err
}

func (c *Client) refresh(ctx context.Context, ref mediaRef) (mediaRef, error) {
	api, err := c.currentAPI()
	if err != nil {
		return ref, err
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: ref.Msg}}
	var res tg.MessagesMessagesClass
	if kind, id, _ := splitID(ref.Chat); kind == 'g' {
		res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: id, AccessHash: c.state.peer(ref.Chat).Hash}, ID: ids,
		})
	} else {
		res, err = api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return ref, err
	}
	page, ok := res.AsModified()
	if !ok || len(page.GetMessages()) == 0 {
		return ref, errors.New("message is gone")
	}
	msg, ok := c.convert(page.GetMessages()[0])
	if !ok || msg.Media == nil {
		return ref, errors.New("message has no attachment any more")
	}
	var fresh mediaRef
	return fresh, json.Unmarshal(msg.Media, &fresh)
}
