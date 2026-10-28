package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// TestEverySize draws every screen at sizes from tiny to huge: each must fill the terminal
// exactly, line by line, and never panic.
func TestEverySize(t *testing.T) {
	a, wa1, _ := galleryApp(t)
	widths := []int{1, 8, 20, 30, 40, 50, 60, 70, 80, 90, 100, 120, 160, 220, 320}
	heights := []int{1, 3, 5, 8, 10, 12, 16, 20, 24, 40, 70}
	var bad []string
	check := func(m Model, label string) {
		lines := strings.Split(m.View(), "\n")
		if len(lines) != m.height {
			bad = append(bad, fmt.Sprintf("%dx%d %s: %d lines", m.width, m.height, label, len(lines)))
			return
		}
		for i, l := range lines {
			if w := cellwidth.String(l); w != m.width {
				bad = append(bad, fmt.Sprintf("%dx%d %s: line %d is %d wide", m.width, m.height, label, i, w))
				return
			}
		} // A popup is drawn whole: both corners of its left edge are on screen.
		if popup := strings.HasPrefix(label, "search") || label == "help"; popup && !m.tooSmall() {
			view := m.View()
			if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
				bad = append(bad, fmt.Sprintf("%dx%d %s: popup cut off", m.width, m.height, label))
			}
		}
	}
	keys := func(m Model, ks ...string) Model {
		for _, k := range ks {
			m = step(t, m, key(k))
		}
		return m
	}
	resize := func(m Model, label string) {
		for _, w := range widths {
			for _, h := range heights {
				check(step(t, m, tea.WindowSizeMsg{Width: w, Height: h}), label)
			}
		}
	}
	// Each screen is set up once, then the window is resized through every size.
	m := step(t, readyNew(a), tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(t, m, RefreshMsg{})
	resize(m, "inbox")
	m = keys(m, "j", "enter")
	resize(m, "chat")
	m = keys(m, "i", "h", "e", "y")
	resize(m, "compose")
	m = keys(m, " ", ":", "s", "m")
	resize(m, "compose, emoji list")
	m = keys(m, "esc")
	m = keys(m, "esc", "/")
	resize(m, "search from chat")
	m = keys(m, "esc", "esc", "/")
	resize(m, "search")
	m = keys(m, "w", ":", " ", "j")
	resize(m, "search typed")
	m = keys(m, "x", "y", "z")
	resize(m, "search, no match")
	m = keys(m, "esc", "?")
	resize(m, "help")
	m = keys(m, "x", "tab", " ", "e")
	resize(m, "details off")
	m.notes.toast = &toast{id: 1, chat: m.inbox.Chats[len(m.inbox.Chats)-1], more: 2}
	resize(m, "toast")
	m.notes.toast = nil

	wa1.events <- core.SyncEvent{Platform: core.WhatsApp, Active: true, Done: 768, Total: 1234, Unit: "messages"}
	a.Drain()
	m = step(t, New(a), tea.WindowSizeMsg{Width: 100, Height: 30})
	resize(step(t, m, RefreshMsg{}), "startup loader")
	m = step(t, readyNew(a), tea.WindowSizeMsg{Width: 100, Height: 30})
	resize(step(t, m, RefreshMsg{}), "catching up later")
	small := step(t, New(a), tea.WindowSizeMsg{Width: 20, Height: 5})
	if !strings.Contains(small.View(), "window too small") || !strings.Contains(small.View(), "20×5, need 30×8") {
		t.Errorf("too small:\n%s", small.View())
	}
	if len(bad) > 0 {
		t.Errorf("%d screens don't fit:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// TestOnePane: on a narrow window the list and the chat take turns on the whole screen.
func TestOnePane(t *testing.T) {
	a, _, _ := galleryApp(t)
	m := step(t, readyNew(a), tea.WindowSizeMsg{Width: 60, Height: 20})
	m = step(t, m, RefreshMsg{})
	if v := m.View(); !strings.Contains(v, "UNREAD") || strings.Contains(v, "no chat open") || m.leftW() != 60 {
		t.Fatalf("list should fill the screen:\n%s", v)
	}
	m = step(t, m, key("enter"))
	if v := m.View(); strings.Contains(v, "UNREAD") || !strings.Contains(v, "grandmother") || m.chatW() != 60 {
		t.Fatalf("chat should fill the screen:\n%s", v)
	}
	m = step(t, m, key("/"))
	if v := m.View(); !strings.Contains(v, "Find a chat") || strings.Contains(v, "UNREAD") {
		t.Errorf("search from the chat should sit over the chat:\n%s", v)
	}
	m = step(t, m, key("esc"))
	m = step(t, m, key(" "))
	m = step(t, m, key("e"))
	if m.detailsW() != 0 || m.note == "" {
		t.Errorf("details on one pane: width %d, note %q", m.detailsW(), m.note)
	}
	m = step(t, m, key("esc"))
	if v := m.View(); !strings.Contains(v, "UNREAD") || m.chatW() != 0 {
		t.Errorf("esc should go back to the list:\n%s", v)
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30}) // wider: both again
	if m.leftW() == 0 || m.chatW() == 0 {
		t.Errorf("two panes at 120: list %d, chat %d", m.leftW(), m.chatW())
	}
	checkFits(t, m, "two panes again")
}

// Motion is off in tests so every key lands at once; TestMotion turns it on.
func TestMain(m *testing.M) {
	useAnimations = false
	os.Exit(m.Run())
}

// frames runs animation frames until nothing moves, checking each fits the screen.
func frames(t *testing.T, m Model, label string) (Model, int) {
	t.Helper()
	n := 0
	for ; m.anim.ticking && n < 200; n++ {
		next, _ := m.Update(frameMsg{})
		m = next.(Model)
		checkFits(t, m, fmt.Sprintf("%s frame %d", label, n))
	}
	if m.anim.ticking {
		t.Fatalf("%s: still moving after %d frames", label, n)
	}
	return m, n
}

func TestMotion(t *testing.T) {
	useAnimations = true
	defer func() { useAnimations = false }()
	a, _, _ := galleryApp(t)
	m := step(t, New(a), tea.WindowSizeMsg{Width: 120, Height: 30})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("enter")) // past the loader: the list slides in
	if m.leftW() >= m.targetLeftW() || !m.anim.ticking {
		t.Fatalf("list should start sliding in: %d of %d", m.leftW(), m.targetLeftW())
	}
	m, n := frames(t, m, "list in")
	if m.leftW() != m.targetLeftW() || n < 5 || n > 60 {
		t.Errorf("list in: %d of %d after %d frames", m.leftW(), m.targetLeftW(), n)
	}

	m = step(t, m, key("enter")) // open Mom: the details panel slides in, getting wider every frame
	last := m.detailsW()
	for m.anim.ticking {
		next, _ := m.Update(frameMsg{})
		m = next.(Model)
		if m.detailsW() < last {
			t.Fatalf("panel went back from %d to %d", last, m.detailsW())
		}
		last = m.detailsW()
		checkFits(t, m, "panel in")
	}
	if m.detailsW() != m.targetDetailsW() || m.chatView.Width != m.chatW() {
		t.Errorf("panel %d of %d, chat laid out at %d of %d", m.detailsW(), m.targetDetailsW(), m.chatView.Width, m.chatW())
	}

	m = step(t, m, key(" "))
	m = step(t, m, key("e")) // and out
	m, _ = frames(t, m, "panel out")
	if m.detailsW() != 0 || m.chatW() != m.width-m.leftW() {
		t.Errorf("panel out: %d, chat %d", m.detailsW(), m.chatW())
	}

	m = step(t, m, key("g")) // the chat scrolls instead of jumping
	if m.scrollY() == m.chatView.YOffset && m.anim.ticking {
		t.Error("scroll jumped")
	}
	m, _ = frames(t, m, "scroll")
	if m.scrollY() != m.chatView.YOffset {
		t.Errorf("scroll ended at %d, want %d", m.scrollY(), m.chatView.YOffset)
	}

	m = step(t, m, key("/")) // the search box drops in
	if m.searchIn() >= 1 {
		t.Error("search box should come in")
	}
	m, _ = frames(t, m, "search in")
	if m.searchIn() != 1 {
		t.Errorf("search box at %v", m.searchIn())
	}

	// A resize jumps straight to the new layout.
	m = step(t, m, key("esc"))
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	if m.anim.ticking || m.leftW() != m.targetLeftW() {
		t.Errorf("resize animated: list %d of %d", m.leftW(), m.targetLeftW())
	}
	checkFits(t, m, "after resize")
}

// BenchmarkPanelFrame is one frame of the details panel sliding next to a long chat: the
// chat is laid out again at each width.
func BenchmarkPanelFrame(b *testing.B) {
	useAnimations = true
	defer func() { useAnimations = false }()
	t := &testing.T{}
	a, _, _ := galleryApp(t)
	m := step(t, readyNew(a), tea.WindowSizeMsg{Width: 160, Height: 50})
	m = step(t, m, RefreshMsg{})
	m = step(t, m, key("enter"))
	var msgs []core.Message
	for i := range 300 {
		msgs = append(msgs, core.Message{Platform: core.WhatsApp, ChatID: "mom", ID: fmt.Sprint("x", i), SenderID: "mom", FromMe: i%3 == 0,
			Timestamp: time.Now().Add(-time.Duration(300-i) * time.Minute), Kind: core.KindText, Body: strings.Repeat("a fairly long message ", 1+i%4)})
	}
	m.messages = msgs
	b.ResetTimer()
	for i := range b.N {
		m.anim.panel.snap(float64(28 + i%20)) // a new width each frame
		m.relayout()
		_ = m.View()
	}
}
