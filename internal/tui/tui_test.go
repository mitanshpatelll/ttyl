package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/ttyl/internal/app"
	"github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
	"github.com/mitanshpatelll/ttyl/internal/rules"
	"github.com/mitanshpatelll/ttyl/internal/store"
)

// testKey turns on at-rest encryption so tests cover the sealed path.
var testKey = make([]byte, 32)

type fakePlatform struct{ sent []string }

func (f *fakePlatform) ID() core.PlatformID           { return core.WhatsApp }
func (f *fakePlatform) Connect(context.Context) error { return nil }
func (f *fakePlatform) Disconnect()                   {}
func (f *fakePlatform) Events() <-chan core.Event     { return nil }
func (f *fakePlatform) MarkRead(context.Context, core.Chat, []core.Message) error {
	return nil
}
func (f *fakePlatform) Send(_ context.Context, chatID, text string) (core.Message, error) {
	f.sent = append(f.sent, text)
	return core.Message{Platform: core.WhatsApp, ChatID: chatID, ID: "sent", FromMe: true, Timestamp: time.Now(), Kind: core.KindText, Body: text}, nil
}

// step feeds a message to the model and runs resulting commands synchronously (ignoring ticks).
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	for _, out := range run(cmd) {
		m = step(t, m, out)
	}
	return m
}

func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond): // tick or blink: not needed here
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, run(c)...)
		}
		return out
	}
	switch msg.(type) {
	case nil, tickMsg, spinner.TickMsg:
		return nil
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestLayoutAndFlow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	err = st.Apply(core.BatchEvent{
		Platform: core.WhatsApp,
		Chats: []core.Chat{
			{Platform: core.WhatsApp, ID: "15550001@s.whatsapp.net", Name: "Mom"},
			{Platform: core.WhatsApp, ID: "123@g.us", Name: "Weekend trip planning with a very long group name", IsGroup: true},
			{Platform: core.WhatsApp, ID: "15550002@s.whatsapp.net"},
		},
		Messages: []core.Message{
			{Platform: core.WhatsApp, ChatID: "15550001@s.whatsapp.net", ID: "a", SenderID: "15550001@s.whatsapp.net", SenderName: "Mom", Timestamp: now.Add(-time.Hour), Kind: core.KindText, Body: "did you eat? " + strings.Repeat("long text ", 30)},
			{Platform: core.WhatsApp, ChatID: "15550001@s.whatsapp.net", ID: "b", SenderID: "15550001@s.whatsapp.net", SenderName: "Mom", Timestamp: now.Add(-50 * time.Minute), Kind: core.KindVoice, Meta: "0:12"},
			{Platform: core.WhatsApp, ChatID: "123@g.us", ID: "c", SenderID: "15550003@s.whatsapp.net", SenderName: "Ann", Timestamp: now.Add(-48 * time.Hour), Kind: core.KindLink, Body: "lol https://instagram.com/reel/abc", Meta: "https://instagram.com/reel/abc"},
			{Platform: core.WhatsApp, ChatID: "15550002@s.whatsapp.net", ID: "d", SenderID: "15550002@s.whatsapp.net", Timestamp: now.Add(-10 * 24 * time.Hour), Kind: core.KindImage},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rules.New(nil)
	fake := &fakePlatform{}
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, fake)

	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, RefreshMsg{})
	checkFits(t, m, "inbox")
	if !strings.Contains(m.View(), "+15550002") || !strings.Contains(m.View(), "voice 0:12") {
		t.Errorf("inbox missing fallback name or voice tag:\n%s", m.View())
	}

	m = step(t, m, key("enter")) // open Mom
	checkFits(t, m, "chat")
	if m.open == nil || m.open.Name != "Mom" || len(m.messages) != 2 {
		t.Fatalf("open = %+v, %d messages", m.open, len(m.messages))
	}
	if m.visible[0].Unread != 0 {
		t.Errorf("unread after open = %d", m.visible[0].Unread)
	}

	m = step(t, m, key("i"))
	for _, r := range "on my way" {
		m = step(t, m, key(string(r)))
	}
	m = step(t, m, key("enter"))
	if len(fake.sent) != 1 || fake.sent[0] != "on my way" || len(m.messages) != 3 {
		t.Errorf("sent %v, %d messages after send", fake.sent, len(m.messages))
	}
	checkFits(t, m, "compose")
	t.Logf("\n%s", m.View())

	m = step(t, m, key("esc"))
	m = step(t, m, key("esc"))
	m = step(t, m, key("/"))
	for _, r := range "week" {
		m = step(t, m, key(string(r)))
	}
	if len(m.visible) != 1 || !m.visible[0].IsGroup {
		t.Errorf("filter visible = %+v", m.visible)
	}
	checkFits(t, m, "filter")
	t.Logf("\n%s", m.View())
}

func checkFits(t *testing.T, m Model, label string) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Errorf("%s: %d lines, want %d", label, len(lines), m.height)
	}
	for i, l := range lines {
		if w := cellwidth.String(l); w != m.width {
			t.Errorf("%s: line %d is %d wide, max %d: %q", label, i, w, m.width, l)
		}
	}
}

func (f *fakePlatform) Download(context.Context, core.Message, []byte) ([]byte, string, error) {
	return []byte("x"), "photo.jpg", nil
}

func TestChatSelectionMediaAndPaging(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chat := "15550001@s.whatsapp.net"
	start := time.Now().Add(-time.Duration(app.HistoryPage+50) * time.Minute)
	var msgs []core.Message
	for i := range app.HistoryPage + 50 {
		msgs = append(msgs, core.Message{Platform: core.WhatsApp, ChatID: chat, ID: fmt.Sprintf("m%03d", i), SenderID: chat,
			Timestamp: start.Add(time.Duration(i) * time.Minute), Kind: core.KindText, Body: fmt.Sprintf("message %d", i)})
	}
	last := len(msgs) - 1
	msgs[last].Kind, msgs[last].Media, msgs[last].HasMedia = core.KindImage, []byte("h"), true
	msgs[last-1].Edited = true
	err = st.Apply(core.BatchEvent{
		Platform:  core.WhatsApp,
		Chats:     []core.Chat{{Platform: core.WhatsApp, ID: chat, Name: "Mom"}},
		Messages:  msgs,
		Deletes:   []core.MessageRef{{ChatID: chat, ID: msgs[last-2].ID}},
		Reactions: []core.Reaction{{MessageRef: core.MessageRef{ChatID: chat, ID: msgs[last-1].ID}, SenderID: "x", Emoji: "👍", Timestamp: time.Now()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rules.New(nil)
	var revealed string
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop(),
		Reveal: func(p string) error { revealed = p; return nil }}, &fakePlatform{})

	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("enter"))
	if len(m.messages) != app.HistoryPage || m.sel != app.HistoryPage-1 {
		t.Fatalf("loaded %d, sel %d", len(m.messages), m.sel)
	}
	view := m.View()
	for _, want := range []string{"photo", "message deleted", "(edited)", "👍", "▌"} {
		if !strings.Contains(view, want) {
			t.Errorf("chat view missing %q:\n%s", want, view)
		}
	}
	checkFits(t, m, "chat with reactions")

	m = step(t, m, key("o"))
	if !strings.HasSuffix(revealed, "photo.jpg") || !strings.Contains(m.note, "photo.jpg") {
		t.Errorf("o: revealed %q, note %q", revealed, m.note)
	}
	m = step(t, m, key("k"))
	m = step(t, m, key("o"))
	if m.note != "no attachment on this message" {
		t.Errorf("o on text: note %q", m.note)
	}

	// Walking past the oldest loaded message loads the next page and keeps the selection.
	m = step(t, m, key("g"))
	oldest := m.messages[0].ID
	m = step(t, m, key("k"))
	if len(m.messages) != app.HistoryPage+50 || m.messages[m.sel].ID != oldest {
		t.Errorf("after paging: %d messages, selected %q, want %q", len(m.messages), m.messages[m.sel].ID, oldest)
	}
	if top := m.starts[m.sel]; top < m.chatView.YOffset || top >= m.chatView.YOffset+m.chatView.Height {
		t.Errorf("selected message (line %d) not in view [%d, %d)", top, m.chatView.YOffset, m.chatView.YOffset+m.chatView.Height)
	}
}

func TestBudgetLockAndExtend(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, _ := rules.New(nil)
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop(), DailyLimit: time.Minute}, &fakePlatform{})
	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = step(t, m, RefreshMsg{})
	if !strings.Contains(m.View(), "1m left") {
		t.Errorf("header should show the budget:\n%s", m.View())
	}

	a.RecordActive(time.Now(), 61*time.Second)
	m = step(t, m, RefreshMsg{})
	if !m.inbox.Locked() || !strings.Contains(m.View(), "used up") {
		t.Fatalf("not locked after the budget:\n%s", m.View())
	}
	checkFits(t, m, "lock screen")
	m = step(t, m, key("/")) // normal keys do nothing while locked
	if m.focus == focusFilter {
		t.Error("filter opened while locked")
	}

	m = step(t, m, key("e"))
	for _, r := range "nope" {
		m = step(t, m, key(string(r)))
	}
	m = step(t, m, key("enter"))
	if !m.inbox.Locked() || m.flash == "" {
		t.Error("wrong word should not unlock")
	}
	m = step(t, m, key("e"))
	for _, r := range "extend" {
		m = step(t, m, key(string(r)))
	}
	m = step(t, m, key("enter"))
	if m.inbox.Locked() || m.inbox.Limit != time.Minute+app.ExtensionStep {
		t.Errorf("after extend: locked %v, limit %v", m.inbox.Locked(), m.inbox.Limit)
	}
}

func TestActiveTimeIgnoresBlurAndSleep(t *testing.T) {
	m := Model{focused: true, activeFrom: time.Now().Add(-20 * time.Second)}
	if d := m.takeActive(time.Now()); d < 19*time.Second || d > 21*time.Second {
		t.Errorf("focused 20s counted as %v", d)
	}
	m.focused, m.activeFrom = false, time.Now().Add(-20*time.Second)
	if d := m.takeActive(time.Now()); d != 0 {
		t.Errorf("blurred time counted: %v", d)
	}
	m.focused, m.activeFrom = true, time.Now().Add(-time.Hour)
	if d := m.takeActive(time.Now()); d != 0 {
		t.Errorf("sleep gap counted: %v", d)
	}
}

func TestSectionsPerPlatform(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	add := func(p core.PlatformID, chat string) {
		st.Apply(core.BatchEvent{Platform: p, Chats: []core.Chat{{Platform: p, ID: chat, Name: chat}},
			Messages: []core.Message{{Platform: p, ChatID: chat, ID: "1", SenderID: chat, Timestamp: time.Now(), Kind: core.KindText, Body: "hi"}}})
	}
	r, _ := rules.New(nil)
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &fakePlatform{})
	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 16})

	add(core.WhatsApp, "Mom")
	add(core.Telegram, "Dev chat")
	add(core.Instagram, "Sam")
	m = step(t, m, RefreshMsg{})
	inbox := m.viewInbox(30, 16)
	wa, ig, tg := strings.Index(inbox, "WhatsApp"), strings.Index(inbox, "Instagram"), strings.Index(inbox, "Telegram")
	if wa < 0 || ig < wa || tg < ig {
		t.Errorf("sections should be WhatsApp, Instagram, Telegram:\n%s", inbox)
	}
	// The cursor walks the chats in the order they are shown.
	var order []string
	for range 3 {
		order = append(order, m.visible[m.cursor].Name)
		m = step(t, m, key("j"))
	}
	if strings.Join(order, ",") != "Mom,Sam,Dev chat" {
		t.Errorf("cursor order = %v", order)
	}
	checkFits(t, m, "sections")
}

func TestHelpOverlay(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, _ := rules.New(nil)
	m := New(app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &fakePlatform{}))
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m = step(t, m, key("?"))
	if !strings.Contains(m.View(), "open attachment") {
		t.Fatalf("help not shown:\n%s", m.View())
	}
	checkFits(t, m, "help")
	m = step(t, m, key("j"))
	if m.help {
		t.Error("any key should close help")
	}
	m = step(t, m, key("/"))
	m = step(t, m, key("?"))
	if m.help || m.filter.Value() != "?" {
		t.Error("? while typing a filter must type, not open help")
	}
}

type failingPlatform struct{ fakePlatform }

func (f *failingPlatform) Send(context.Context, string, string) (core.Message, error) {
	return core.Message{}, errors.New("offline")
}

func TestDraftComesBackOnlyToItsChat(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, id := range []string{"a", "b"} {
		st.Apply(core.BatchEvent{Platform: core.WhatsApp, Chats: []core.Chat{{Platform: core.WhatsApp, ID: id, Name: id}},
			Messages: []core.Message{{Platform: core.WhatsApp, ChatID: id, ID: id, SenderID: id, Timestamp: time.Now(), Kind: core.KindText, Body: id}}})
	}
	r, _ := rules.New(nil)
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &failingPlatform{})
	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("i"))
	for _, r := range "hello" {
		m = step(t, m, key(string(r)))
	}
	m = step(t, m, key("enter"))
	if m.compose.Value() != "hello" || !strings.Contains(m.flash, "offline") {
		t.Errorf("after failed send: draft %q, flash %q", m.compose.Value(), m.flash)
	}

	// A failure that arrives after switching chats must not land in the other chat.
	other := *m.open
	other.ID = "elsewhere"
	next, _ := m.Update(sentMsg{chatKey: chatKey(other), text: "stray", err: errors.New("offline")})
	m = next.(Model)
	m.compose.Reset()
	next, _ = m.Update(sentMsg{chatKey: chatKey(other), text: "stray", err: errors.New("offline")})
	if m = next.(Model); m.compose.Value() != "" {
		t.Errorf("draft for another chat restored here: %q", m.compose.Value())
	}
}

func TestLockedDoesNotLoadChats(t *testing.T) {
	m := Model{open: &core.Chat{ID: "x"}}
	m.inbox.Limit, m.inbox.Used = time.Minute, time.Hour
	if m.loadChat() != nil {
		t.Error("a locked ttyl must not open (and mark read) the chat")
	}
}

func TestOpeningJumpsToFirstUnread(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Now().Add(-time.Hour)
	var msgs []core.Message
	for i := range 40 {
		msgs = append(msgs, core.Message{Platform: core.WhatsApp, ChatID: "c", ID: fmt.Sprint(i), SenderID: "c", SenderName: "Ann",
			Timestamp: start.Add(time.Duration(i) * time.Minute), Kind: core.KindText, Body: fmt.Sprintf("message %d", i)})
	}
	st.Apply(core.BatchEvent{Platform: core.WhatsApp, Chats: []core.Chat{{Platform: core.WhatsApp, ID: "c", Name: "Ann"}}, Messages: msgs,
		ReadUpTo: map[string]time.Time{"c": start.Add(29 * time.Minute)}}) // 30..39 unread
	r, _ := rules.New(nil)
	m := New(app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &fakePlatform{}))
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("enter"))

	if m.messages[m.sel].ID != "30" {
		t.Errorf("selected %q, want the first unread (30)", m.messages[m.sel].ID)
	}
	if !strings.Contains(m.View(), " NEW ") {
		t.Errorf("divider not in view:\n%s", m.View())
	}
	checkFits(t, m, "jump to unread")
}

func TestCatchUpLoader(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, _ := rules.New(nil)
	wa := &onlinePlatform{id: core.WhatsApp, events: make(chan core.Event, 4)}
	tg := &onlinePlatform{id: core.Telegram, events: make(chan core.Event, 4)}
	ig := &onlinePlatform{id: core.Instagram, events: make(chan core.Event, 4)}
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, wa, tg, ig)
	wa.events <- core.SyncEvent{Platform: core.WhatsApp, Active: true, Done: 768, Total: 1234, Unit: "messages"}
	tg.events <- core.SyncEvent{Platform: core.Telegram, Active: true, Done: 3, Total: 40, Unit: "chats"}
	ig.events <- core.SyncEvent{Platform: core.Instagram, Active: true, Unit: "chats"}
	a.Drain()

	for _, size := range [][2]int{{110, 30}, {80, 24}} {
		m := New(a)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = step(t, m, RefreshMsg{})
		label := fmt.Sprintf("loader %dx%d", size[0], size[1])
		checkFits(t, m, label)
		view := m.View()
		for _, want := range []string{"WA 62%", "TG 7%", "Catching up on what you missed", "768 of 1,234 messages", "3 of 40 chats", "loading chats…", "Syncing your chats"} {
			if !strings.Contains(view, want) {
				t.Errorf("%s: missing %q:\n%s", label, want, view)
			}
		}
		if !m.spinning {
			t.Errorf("%s: spinner not started", label)
		}
	}

	// Done everywhere: the welcome text comes back and the spinner stops at its next tick.
	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, RefreshMsg{})
	wa.events <- core.SyncEvent{Platform: core.WhatsApp}
	tg.events <- core.SyncEvent{Platform: core.Telegram}
	ig.events <- core.SyncEvent{Platform: core.Instagram}
	a.Drain()
	m = step(t, m, RefreshMsg{})
	m = step(t, m, m.spin.Tick())
	if m.spinning || strings.Contains(m.View(), "Catching up") || !strings.Contains(m.View(), "search anyone") {
		t.Errorf("loader still up after sync finished (spinning=%v):\n%s", m.spinning, m.View())
	}
}

func TestCommas(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUnreadOnlyAndSearch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	add := func(p core.PlatformID, id, name string, read bool) {
		b := core.BatchEvent{Platform: p, Chats: []core.Chat{{Platform: p, ID: id, Name: name}},
			Messages: []core.Message{{Platform: p, ChatID: id, ID: "1", SenderID: id, Timestamp: now.Add(-time.Minute), Kind: core.KindText, Body: "hi"}}}
		if read {
			b.ReadUpTo = map[string]time.Time{id: now}
		}
		if err := st.Apply(b); err != nil {
			t.Fatal(err)
		}
	}
	add(core.WhatsApp, "mom", "Mom", true)
	add(core.WhatsApp, "raj", "Raj", true)
	add(core.Telegram, "dev", "Dev chat", false)
	r, _ := rules.New(nil)
	m := New(app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &fakePlatform{}))
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = step(t, m, RefreshMsg{})
	names := func() string {
		var out []string
		for _, c := range m.visible {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	if names() != "Dev chat" || !strings.Contains(m.View(), "All caught up") || !strings.Contains(m.View(), "UNREAD") {
		t.Errorf("unread only: %q\n%s", names(), m.View())
	}

	m = step(t, m, key("enter")) // reading it keeps it in the list while it is open
	m = step(t, m, RefreshMsg{})
	if names() != "Dev chat" || m.visible[0].Unread != 0 {
		t.Errorf("open chat left the list: %q", names())
	}
	m = step(t, m, key("esc"))

	for query, want := range map[string]string{"w:": "Mom,Raj", "W: ra": "Raj", "ra": "Raj", "tg:dev": "Dev chat", "i:": ""} {
		m = step(t, m, key("/"))
		for _, r := range query {
			m = step(t, m, key(string(r)))
		}
		if got := names(); got != want {
			t.Errorf("search %q = %q, want %q", query, got, want)
		}
		checkFits(t, m, "search "+query)
		m = step(t, m, key("esc"))
	}

	m = step(t, m, key("a"))
	if names() != "Mom,Raj,Dev chat" || !strings.Contains(m.View(), "ALL CHATS") {
		t.Errorf("all chats: %q", names())
	}
	checkFits(t, m, "all chats")
}

func TestDetailsPanel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	msg := func(id, sender, name string, kind core.Kind, body, meta string) core.Message {
		return core.Message{Platform: core.WhatsApp, ChatID: "trip", ID: id, SenderID: sender, SenderName: name,
			Timestamp: now.Add(-time.Duration(10-len(id)) * time.Minute), Kind: kind, Body: body, Meta: meta}
	}
	err = st.Apply(core.BatchEvent{Platform: core.WhatsApp,
		Chats: []core.Chat{{Platform: core.WhatsApp, ID: "trip", Name: "Weekend trip", IsGroup: true}},
		Messages: []core.Message{
			msg("1", "raj", "Raj", core.KindText, "Saturday 9am?", ""),
			msg("22", "ann", "Ann", core.KindImage, "the view", ""),
			msg("333", "ann", "Ann", core.KindLink, "https://github.com/mitansh/ttyl", "https://github.com/mitansh/ttyl"),
			msg("4444", "raj", "Raj", core.KindDocument, "", "tickets.pdf"),
			msg("55555", "raj", "Raj", core.KindVoice, "", "0:12"),
		}})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rules.New(nil)
	a := app.New(app.Options{Store: st, Rules: r, MediaDir: t.TempDir(), Log: zerolog.Nop()}, &fakePlatform{})
	m := New(a)
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("enter"))
	view := m.View()
	for _, want := range []string{"DETAILS", "Weekend trip", "3 people", "MEDIA", "photo · the view", "voice 0:12", "LINKS", "github.com/mitansh/ttyl", "FILES", "tickets.pdf", "PEOPLE", "Raj", "Ann"} {
		if !strings.Contains(view, want) {
			t.Errorf("details missing %q:\n%s", want, view)
		}
	}
	if m.chatW() >= m.rightW() || m.detailsW() < minDetailsW {
		t.Errorf("chat %d / panel %d of %d", m.chatW(), m.detailsW(), m.rightW())
	}
	checkFits(t, m, "details panel")

	// space then e hides it, and the choice survives a restart.
	m = step(t, m, key(" "))
	m = step(t, m, key("e"))
	if m.detailsW() != 0 || strings.Contains(m.View(), "DETAILS") {
		t.Error("space e did not hide the panel")
	}
	checkFits(t, m, "panel hidden")
	if again := New(a); again.showDetails {
		t.Error("hidden panel not remembered")
	}
	// Any other key after space is a normal key.
	m = step(t, m, key(" "))
	m = step(t, m, key("j"))
	if m.leader {
		t.Error("leader still pending")
	}
	m = step(t, m, key(" "))
	m = step(t, m, key("e"))
	if m.detailsW() == 0 {
		t.Error("space e did not show the panel again")
	}

	// Too narrow: the chat keeps the room.
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	if m.detailsW() != 0 {
		t.Errorf("panel shown at 90 columns (%d)", m.detailsW())
	}
	checkFits(t, m, "narrow")
}
