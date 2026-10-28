package tui

import (
	"fmt"
	"strings"
	"testing"

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
	m := step(t, New(a), tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(t, m, RefreshMsg{})
	resize(m, "inbox")
	m = keys(m, "j", "enter")
	resize(m, "chat")
	m = keys(m, "i", "h", "e", "y")
	resize(m, "compose")
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

	wa1.events <- core.SyncEvent{Platform: core.WhatsApp, Active: true, Done: 768, Total: 1234, Unit: "messages"}
	a.Drain()
	m = step(t, New(a), tea.WindowSizeMsg{Width: 100, Height: 30})
	resize(step(t, m, RefreshMsg{}), "catching up")
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
	m := step(t, New(a), tea.WindowSizeMsg{Width: 60, Height: 20})
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
