// Package tui is the Bubble Tea interface: inbox on the left, open chat on the right.
package tui

import (
	"context"
	"path/filepath"
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
	sentMsg struct {
		chatKey, text string
		err           error
	}
	mediaMsg struct {
		path string
		err  error
	}
	errMsg  struct{ err error }
	tickMsg struct{}
)

// sleepGap: a gap longer than this between ticks means the laptop slept; it is not counted as use.
const sleepGap = 2 * time.Minute

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
	limit    int   // how many messages of the open chat are loaded
	sel      int   // selected message; the newest one follows new arrivals
	starts   []int // first viewport line of each message, set by renderChat
	chatView viewport.Model

	filter  textinput.Model
	compose textinput.Model
	extend  textinput.Model // on the lock screen: type "extend" for more time

	focused    bool      // terminal has focus; only focused time counts toward the budget
	activeFrom time.Time // start of the focused time not yet recorded

	help bool // ? overlay

	flash string // last error, cleared on the next key press
	note  string // last info message, cleared on the next key press
}

func New(a *app.App) Model {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter chats"
	compose := textinput.New()
	compose.Prompt = "> "
	compose.Placeholder = "message"
	compose.CharLimit = 4096
	extend := textinput.New()
	extend.Prompt = "> "
	extend.Placeholder = `type "extend" for 5 more minutes`
	return Model{app: a, filter: filter, compose: compose, extend: extend, chatView: viewport.New(0, 0),
		focused: true, activeFrom: time.Now()}
}

// takeActive returns focused time since the last call.
func (m *Model) takeActive(now time.Time) time.Duration {
	d := now.Sub(m.activeFrom)
	m.activeFrom = now
	if !m.focused || d <= 0 || d > sleepGap {
		return 0
	}
	return d
}

func (m Model) recordActive(d time.Duration) tea.Cmd {
	if d <= 0 {
		return nil
	}
	a := m.app
	return func() tea.Msg {
		if err := a.RecordActive(time.Now(), d); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// FlushUsage records the last stretch of use; call it once after the program exits.
func (m Model) FlushUsage() error {
	if d := m.takeActive(time.Now()); d > 0 {
		return m.app.RecordActive(time.Now(), d)
	}
	return nil
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

// loadChat reloads the open chat, which also marks it read (and may send receipts), so it
// never runs behind the lock screen.
func (m Model) loadChat() tea.Cmd {
	if m.open == nil || m.inbox.Locked() {
		return nil
	}
	a, chat, limit := m.app, *m.open, m.limit
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msgs, err := a.Open(ctx, chat, time.Now(), limit)
		return messagesMsg{chatKey(chat), msgs, err}
	}
}

func (m Model) send(text string) tea.Cmd {
	a, chat := m.app, *m.open
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return sentMsg{chatKey(chat), text, a.Send(ctx, chat, text)}
	}
}

func (m Model) openMedia(msg core.Message) tea.Cmd {
	a := m.app
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		path, err := a.OpenMedia(ctx, msg)
		return mediaMsg{path, err}
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
		return m, tea.Batch(m.recordActive(m.takeActive(time.Now())), m.loadInbox(), m.loadChat(), tick())

	case tea.FocusMsg:
		m.focused, m.activeFrom = true, time.Now()
		return m, nil

	case tea.BlurMsg:
		cmd := m.recordActive(m.takeActive(time.Now()))
		m.focused = false
		return m, cmd

	case errMsg:
		m.flash = msg.err.Error()
		return m, nil

	case inboxMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
			return m, nil
		}
		wasLocked := m.inbox.Locked()
		m.inbox = msg.inbox
		m.applyFilter()
		if m.inbox.Locked() && !wasLocked {
			m.compose.Blur()
			m.filter.Blur()
		}
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
		// Keep the selected message selected; if it was the newest, follow new arrivals.
		keep := ""
		if m.sel < len(m.messages)-1 {
			keep = m.messages[m.sel].ID
		}
		m.messages = msg.msgs
		m.sel = len(m.messages) - 1
		for i, x := range m.messages {
			if x.ID == keep {
				m.sel = i
			}
		}
		m.renderChat()
		m.showSelected()
		m.clearUnread(msg.chatKey)
		return m, nil

	case sentMsg:
		if msg.err != nil {
			m.flash = "send failed: " + msg.err.Error()
			// Give the draft back, but only to the chat it was written for.
			if m.open != nil && chatKey(*m.open) == msg.chatKey && m.compose.Value() == "" {
				m.compose.SetValue(msg.text)
				m.compose.CursorEnd()
			}
		}
		return m, tea.Batch(m.loadChat(), m.loadInbox())

	case mediaMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else {
			m.note = "saved " + filepath.Base(msg.path)
		}
		return m, nil

	case tea.KeyMsg:
		m.flash, m.note = "", ""
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
	if m.extend.Focused() {
		m.extend, cmd = m.extend.Update(msg)
	}
	return m, cmd
}

// handleLockedKey is the whole UI while the daily budget is used up: quit, or type "extend".
func (m Model) handleLockedKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.extend.Focused() {
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "e":
			return m, m.extend.Focus()
		}
		return m, nil
	}
	switch k.String() {
	case "esc":
		m.extend.Reset()
		m.extend.Blur()
	case "enter":
		ok := strings.TrimSpace(strings.ToLower(m.extend.Value())) == "extend"
		m.extend.Reset()
		m.extend.Blur()
		if !ok {
			m.flash = `type the word "extend" to unlock`
			return m, nil
		}
		a := m.app
		return m, func() tea.Msg {
			if err := a.Extend(time.Now()); err != nil {
				return errMsg{err}
			}
			in, err := a.Inbox(time.Now())
			return inboxMsg{in, err}
		}
	default:
		var cmd tea.Cmd
		m.extend, cmd = m.extend.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.inbox.Locked() {
		return m.handleLockedKey(k)
	}
	typing := m.focus == focusCompose || m.focus == focusFilter
	if m.help || (k.String() == "?" && !typing) {
		m.help = !m.help && k.String() == "?" // any key closes it
		return m, nil
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
			m.setFocus(focusChat)
		case "enter":
			text := strings.TrimSpace(m.compose.Value())
			if text != "" {
				m.compose.Reset()
				m.sel = len(m.messages) - 1 // follow the reply
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
			m.setFocus(focusInbox)
		case "i", "enter":
			m.setFocus(focusCompose)
			cmd = m.compose.Focus()
		case "j", "down":
			m.selectMessage(m.sel + 1)
		case "k", "up":
			if m.sel == 0 && len(m.messages) >= m.limit {
				m.limit += app.HistoryPage
				m.note = "loading older messages…"
				cmd = m.loadChat()
			}
			m.selectMessage(m.sel - 1)
		case "g", "home":
			m.selectMessage(0)
		case "G", "end":
			m.selectMessage(len(m.messages) - 1)
		case "o":
			switch {
			case m.sel < 0 || m.sel >= len(m.messages):
			case !m.messages[m.sel].HasMedia:
				m.note = "no attachment on this message"
			default:
				m.note = "downloading…"
				cmd = m.openMedia(m.messages[m.sel])
			}
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
				m.setFocus(focusChat)
			}
		}
	}
	return m, cmd
}

// setFocus switches panes; the chat re-renders because the selection marker only shows when it has focus.
func (m *Model) setFocus(f focus) {
	m.focus = f
	m.renderChat()
}

func (m *Model) openSelected(f focus) tea.Cmd {
	if m.cursor >= len(m.visible) {
		return nil
	}
	chat := m.visible[m.cursor].Chat
	if m.open == nil || chatKey(*m.open) != chatKey(chat) {
		m.open = &chat
		m.messages, m.starts, m.sel = nil, nil, -1
		m.limit = app.HistoryPage
		m.chatView.SetContent("")
		m.compose.Reset()
	}
	m.setFocus(f)
	if f == focusCompose {
		return tea.Batch(m.loadChat(), m.compose.Focus())
	}
	return m.loadChat()
}

func (m *Model) selectMessage(i int) {
	if len(m.messages) == 0 {
		return
	}
	m.sel = min(max(i, 0), len(m.messages)-1)
	m.renderChat()
	m.showSelected()
}

// showSelected scrolls the chat so the selected message is in view (its top, if it is taller than the view).
func (m *Model) showSelected() {
	if m.sel < 0 || m.sel >= len(m.starts) {
		m.chatView.GotoBottom()
		return
	}
	start, end := m.starts[m.sel], m.chatView.TotalLineCount()-1
	if m.sel+1 < len(m.starts) {
		end = m.starts[m.sel+1] - 1
	}
	if end >= m.chatView.YOffset+m.chatView.Height {
		m.chatView.SetYOffset(end - m.chatView.Height + 1)
	}
	if start < m.chatView.YOffset {
		m.chatView.SetYOffset(start)
	}
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
	m.showSelected()
	m.scrollInbox()
}
