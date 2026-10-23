package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rs/zerolog"

	"github.com/mitanshpatelll/dmz/internal/app"
	"github.com/mitanshpatelll/dmz/internal/core"
	"github.com/mitanshpatelll/dmz/internal/rules"
	"github.com/mitanshpatelll/dmz/internal/store"
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
	case nil, tickMsg:
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
	if !strings.Contains(m.View(), "+15550002") || !strings.Contains(m.View(), "[voice 0:12]") {
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
		if w := lipgloss.Width(l); w > m.width {
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
	for _, want := range []string{"[photo]", "[deleted]", "(edited)", "👍", "▌"} {
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

func TestPlatformBadgesOnlyWhenMixed(t *testing.T) {
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
	m = step(t, m, RefreshMsg{})
	if strings.Contains(m.viewInbox(30, 10), "WA ") {
		t.Error("badge shown with a single platform")
	}
	add(core.Telegram, "Dev chat")
	m = step(t, m, RefreshMsg{})
	if inbox := m.viewInbox(30, 10); !strings.Contains(inbox, "WA ") || !strings.Contains(inbox, "TG ") {
		t.Errorf("badges missing:\n%s", inbox)
	}
	checkFits(t, m, "mixed inbox")
}
