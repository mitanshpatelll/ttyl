package tui

import (
	"context"
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
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
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
	a := app.New(st, r, false, zerolog.Nop(), fake)

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
