// Package tui is the Bubble Tea interface: inbox on the left, open chat on the right.
package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitanshpatelll/dmz/internal/app"
	"github.com/mitanshpatelll/dmz/internal/core"
)

// RefreshMsg tells the UI that stored data changed and should be reloaded.
type RefreshMsg struct{}

type focus int

const (
	focusInbox focus = iota
	focusFilter
	focusChat
	focusCompose
)

type (
	inboxMsg struct {
		inbox app.Inbox
		err   error
	}
	messagesMsg struct {
		chatKey string
		msgs    []core.Message
		err     error
	}
	sentMsg struct{ err error }
	tickMsg struct{}
)

type Model struct {
	app *app.App

	width, height int
	focus         focus

	inbox    app.Inbox
	visible  []core.ChatSummary // inbox after filter
	cursor   int
	offset   int
	selected string // chat under the cursor, kept across reloads

	open     *core.Chat
	messages []core.Message
	chatView viewport.Model

	filter  textinput.Model
	compose textinput.Model

	flash string // last error, cleared on the next key press
}

func New(a *app.App) Model {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter chats"
	compose := textinput.New()
	compose.Prompt = "> "
	compose.Placeholder = "message"
	compose.CharLimit = 4096
	return Model{app: a, filter: filter, compose: compose, chatView: viewport.New(0, 0)}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.loadInbox(), tick()) }

// tick re-evaluates delivery windows and relative timestamps.
func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func chatKey(c core.Chat) string { return string(c.Platform) + "|" + c.ID }

func (m Model) loadInbox() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		in, err := a.Inbox(time.Now())
		return inboxMsg{in, err}
	}
}

func (m Model) loadChat() tea.Cmd {
	if m.open == nil {
		return nil
	}
	a, chat := m.app, *m.open
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msgs, err := a.Open(ctx, chat, time.Now())
		return messagesMsg{chatKey(chat), msgs, err}
	}
}

func (m Model) send(text string) tea.Cmd {
	a, chat := m.app, *m.open
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return sentMsg{a.Send(ctx, chat, text)}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case RefreshMsg:
		return m, tea.Batch(m.loadInbox(), m.loadChat())

	case tickMsg:
		return m, tea.Batch(m.loadInbox(), m.loadChat(), tick())

	case inboxMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
			return m, nil
		}
		m.inbox = msg.inbox
		m.applyFilter()
		if m.open != nil {
			for _, c := range m.inbox.Chats {
				if chatKey(c.Chat) == chatKey(*m.open) {
					m.open.Name = c.Name
				}
			}
		}
		return m, nil

	case messagesMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
			return m, nil
		}
		if m.open == nil || chatKey(*m.open) != msg.chatKey {
			return m, nil // reply for a chat we already left
		}
		follow := len(m.messages) == 0 || m.chatView.AtBottom()
		m.messages = msg.msgs
		m.renderChat()
		if follow {
			m.chatView.GotoBottom()
		}
		m.clearUnread(msg.chatKey)
		return m, nil

	case sentMsg:
		if msg.err != nil {
			m.flash = "send failed: " + msg.err.Error()
		}
		return m, tea.Batch(m.loadChat(), m.loadInbox())

	case tea.KeyMsg:
		m.flash = ""
		return m.handleKey(msg)
	}

	// Cursor blink and other input internals.
	var cmd tea.Cmd
	switch m.focus {
	case focusCompose:
		m.compose, cmd = m.compose.Update(msg)
	case focusFilter:
		m.filter, cmd = m.filter.Update(msg)
	}
	return m, cmd
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	var cmd tea.Cmd

	switch m.focus {
	case focusFilter:
		switch k.String() {
		case "esc":
			m.filter.Reset()
			m.filter.Blur()
			m.focus = focusInbox
			m.applyFilter()
		case "enter":
			m.filter.Blur()
			m.focus = focusInbox
		default:
			m.filter, cmd = m.filter.Update(k)
			m.selected = "" // jump to the first match
			m.applyFilter()
		}

	case focusCompose:
		switch k.String() {
		case "esc":
			m.compose.Blur()
			m.focus = focusChat
		case "enter":
			text := strings.TrimSpace(m.compose.Value())
			if text != "" {
				m.compose.Reset()
				cmd = m.send(text)
			}
		default:
			m.compose, cmd = m.compose.Update(k)
		}

	case focusChat:
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "esc", "h", "left", "tab":
			m.focus = focusInbox
		case "i", "enter":
			m.focus = focusCompose
			cmd = m.compose.Focus()
		case "g", "home":
			m.chatView.GotoTop()
		case "G", "end":
			m.chatView.GotoBottom()
		default:
			m.chatView, cmd = m.chatView.Update(k)
		}

	default:
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "ctrl+d", "pgdown":
			m.move(m.rows())
		case "ctrl+u", "pgup":
			m.move(-m.rows())
		case "g", "home":
			m.move(-len(m.visible))
		case "G", "end":
			m.move(len(m.visible))
		case "/":
			m.focus = focusFilter
			m.layout()
			cmd = m.filter.Focus()
		case "esc":
			if m.filter.Value() != "" {
				m.filter.Reset()
				m.applyFilter()
			}
		case "enter", "l", "right":
			cmd = m.openSelected(focusChat)
		case "i":
			cmd = m.openSelected(focusCompose)
		case "tab":
			if m.open != nil {
				m.focus = focusChat
			}
		}
	}
	return m, cmd
}

func (m *Model) openSelected(f focus) tea.Cmd {
	if m.cursor >= len(m.visible) {
		return nil
	}
	chat := m.visible[m.cursor].Chat
	if m.open == nil || chatKey(*m.open) != chatKey(chat) {
		m.open = &chat
		m.messages = nil
		m.chatView.SetContent("")
		m.compose.Reset()
	}
	m.focus = f
	if f == focusCompose {
		return tea.Batch(m.loadChat(), m.compose.Focus())
	}
	return m.loadChat()
}

func (m *Model) move(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(m.visible)-1)
	m.selected = chatKey(m.visible[m.cursor].Chat)
	m.scrollInbox()
}

func (m *Model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visible = nil
	for _, c := range m.inbox.Chats {
		if q == "" || strings.Contains(strings.ToLower(c.Title()), q) {
			m.visible = append(m.visible, c)
		}
	}
	m.cursor = 0
	for i, c := range m.visible {
		if chatKey(c.Chat) == m.selected {
			m.cursor = i
			break
		}
	}
	if len(m.visible) > 0 {
		m.selected = chatKey(m.visible[m.cursor].Chat)
	}
	m.scrollInbox()
}

func (m *Model) clearUnread(key string) {
	for i := range m.inbox.Chats {
		if chatKey(m.inbox.Chats[i].Chat) == key {
			m.inbox.Chats[i].Unread = 0
		}
	}
	for i := range m.visible {
		if chatKey(m.visible[i].Chat) == key {
			m.visible[i].Unread = 0
		}
	}
}

func (m *Model) scrollInbox() {
	rows := m.rows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.visible)-rows))
}

// layout resizes the inner components after a window or mode change.
func (m *Model) layout() {
	rw, ih := m.rightInnerWidth(), m.innerHeight()
	m.chatView.Width = rw
	m.chatView.Height = max(1, ih-3) // title, divider, composer
	m.compose.Width = max(1, rw-len(m.compose.Prompt)-1)
	m.filter.Width = max(1, m.leftInnerWidth()-len(m.filter.Prompt)-1)
	m.renderChat()
	m.scrollInbox()
}
