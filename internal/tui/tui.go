// Package tui is the Bubble Tea interface: inbox on the left, open chat on the right.
package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mitanshpatelll/ttyl/internal/app"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// RefreshMsg tells the UI that stored data changed and should be reloaded.
type RefreshMsg struct{}

type focus int

const (
	focusInbox  focus = iota
	focusSearch       // the search modal
	focusChat
	focusCompose
)

type (
	inboxMsg struct {
		inbox app.Inbox
		err   error
	}
	messagesMsg struct {
		chatKey  string
		msgs     []core.Message
		lastRead time.Time // read marker before this open: later incoming messages are new
		err      error
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
	offset   int    // first sidebar line shown
	selected string // chat under the cursor, kept across reloads
	showAll  bool   // every chat, not only unread ones

	open       *core.Chat
	messages   []core.Message
	limit      int       // how many messages of the open chat are loaded
	sel        int       // selected message; the newest one follows new arrivals
	starts     []int     // first viewport line of each message, set by renderChat
	newFrom    time.Time // the "new messages" line goes before the first incoming message after this
	newFromSet bool
	newLine    int // viewport line of that divider, -1 if none
	chatView   viewport.Model

	bubbles     map[string][]string // rendered bubbles, keyed by content and state
	bubbleWidth int
	chatContent []string // every line of the open chat; the viewport tracks the scroll position

	search   textinput.Model    // the search modal's query
	found    []core.ChatSummary // its results
	foundSel int
	back     focus // where esc in the search modal goes back to

	compose textinput.Model
	extend  textinput.Model // on the lock screen: type "extend" for more time

	focused    bool      // terminal has focus; only focused time counts toward the budget
	activeFrom time.Time // start of the focused time not yet recorded

	help    bool // ? overlay
	helpOff int  // first help line shown, when it doesn't all fit
	leader  bool // space was pressed: the next key is a command (e: details)

	showDetails bool        // details panel next to the chat
	det         app.Details // for the chat in detKey
	detKey      string

	spin     spinner.Model // catch-up loader
	spinning bool          // spinner ticks are running

	ready   bool      // startup is over and the chat list shows (see checkReady)
	started time.Time // when this UI started
	settled time.Time // since when every app has been connected and quiet
	checkAt time.Time // a readiness check is due then

	flash string // last error, cleared on the next key press
	note  string // last info message, cleared on the next key press
}

func New(a *app.App) Model {
	search := textinput.New()
	search.Prompt = ""
	search.TextStyle = lipgloss.NewStyle().Foreground(text)
	search.PlaceholderStyle = lipgloss.NewStyle().Foreground(faint)
	compose := textinput.New()
	compose.Prompt = ""
	compose.Placeholder = "Write a message…"
	compose.CharLimit = 4096
	compose.TextStyle = lipgloss.NewStyle().Foreground(text).Background(inputBg)
	compose.PlaceholderStyle = lipgloss.NewStyle().Foreground(faint).Background(inputBg)
	compose.Cursor.TextStyle = lipgloss.NewStyle().Background(inputBg)
	extend := textinput.New()
	extend.Prompt = "> "
	extend.Placeholder = `type "extend" for 5 more minutes`
	return Model{app: a, search: search, compose: compose, extend: extend, chatView: viewport.New(0, 0),
		spin: spinner.New(spinner.WithSpinner(loaderFrames)), focused: true, activeFrom: time.Now(), started: time.Now(),
		showDetails: a.Pref(detailsPref) != "off"}
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
		msgs, lastRead, err := a.Open(ctx, chat, time.Now(), limit)
		return messagesMsg{chatKey(chat), msgs, lastRead, err}
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
		return m, m.loadDetails()

	case RefreshMsg:
		return m, tea.Batch(m.loadInbox(), m.loadChat(), m.loadDetails())

	case tickMsg:
		return m, tea.Batch(m.recordActive(m.takeActive(time.Now())), m.loadInbox(), m.loadChat(), m.loadDetails(), tick())

	case detailsMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
		} else if m.open != nil && chatKey(*m.open) == msg.chatKey {
			m.det, m.detKey = msg.details, msg.chatKey
		}
		return m, nil

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

	case readyMsg:
		m.checkAt = time.Time{}
		return m, m.loadInbox()

	case spinner.TickMsg:
		if !m.loading() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case inboxMsg:
		if msg.err != nil {
			m.flash = msg.err.Error()
			return m, nil
		}
		wasLocked := m.inbox.Locked()
		m.inbox = msg.inbox
		m.applyFilter()
		if m.focus == focusSearch {
			m.findChats(true)
		}
		if m.inbox.Locked() && !wasLocked {
			m.compose.Blur()
			if m.focus == focusSearch {
				m.closeSearch()
			}
		}
		if m.open != nil {
			for _, c := range m.inbox.Chats {
				if chatKey(c.Chat) == chatKey(*m.open) {
					m.open.Name = c.Name
				}
			}
		}
		ready := m.checkReady(time.Now())
		return m, tea.Batch(ready, m.startSpinner())

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
		firstLoad := !m.newFromSet
		if firstLoad { // keep the "new messages" line where it was while the chat stays open
			m.newFrom, m.newFromSet = msg.lastRead, true
		}
		m.sel = len(m.messages) - 1
		for i, x := range m.messages {
			if x.ID == keep {
				m.sel = i
			}
		}
		m.renderChat()
		m.showSelected()
		if firstLoad && m.newLine >= 0 {
			// Opening a chat with unread messages starts at the first one, like the phone apps.
			for i, x := range m.messages {
				if !x.FromMe && x.Timestamp.After(m.newFrom) {
					m.sel = i
					break
				}
			}
			m.renderChat()
			m.chatView.SetYOffset(max(0, m.newLine-1))
		}
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
	case focusSearch:
		m.search, cmd = m.search.Update(msg)
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
	if !m.ready { // the startup loader: wait, look anyway, or leave
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "enter":
			m.ready = true
		}
		return m, nil
	}
	typing := m.focus == focusCompose || m.focus == focusSearch
	if m.help {
		// j/k scroll a help taller than the window; any other key closes it.
		if shown, total := m.helpRows(); shown < total {
			switch k.String() {
			case "j", "down":
				m.helpOff = min(m.helpOff+1, total-shown)
				return m, nil
			case "k", "up":
				m.helpOff = max(m.helpOff-1, 0)
				return m, nil
			}
		}
		m.help = false
		return m, nil
	}
	if k.String() == "?" && !typing {
		m.help, m.helpOff = true, 0
		return m, nil
	}
	var cmd tea.Cmd

	// space starts a two-key command outside text input: space e toggles the details panel.
	if !typing {
		if m.leader {
			m.leader = false
			if k.String() == "e" {
				return m, m.toggleDetails()
			}
			if k.String() == "esc" {
				return m, nil
			}
		} else if k.String() == " " {
			m.leader = true
			return m, nil
		}
	}

	switch m.focus {
	case focusSearch:
		return m.handleSearchKey(k)

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
		case "/":
			cmd = m.openSearch()
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
			cmd = m.openSearch()
		case "a":
			m.showAll = !m.showAll
			m.applyFilter()
			m.note = "showing unread chats only"
			if m.showAll {
				m.note = "showing all chats"
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

// setFocus switches panes; the chat re-renders because the selection marker only shows when
// it has focus. With one pane the chat and the list swap places, so the sizes change too.
func (m *Model) setFocus(f focus) {
	before := m.chatW()
	m.focus = f
	if m.chatW() != before {
		m.layout()
		return
	}
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
		m.newFrom, m.newFromSet = time.Time{}, false
		m.limit = app.HistoryPage
		m.chatView.SetContent("")
		m.compose.Reset()
		m.layout() // the details panel may take part of the chat area now
	}
	m.setFocus(f)
	if f == focusCompose {
		return tea.Batch(m.loadChat(), m.loadDetails(), m.compose.Focus())
	}
	return tea.Batch(m.loadChat(), m.loadDetails())
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

// applyFilter picks the chats the sidebar lists: unread ones (plus the open and the selected
// chat, so reading one doesn't pull it away), or every chat with showAll. They are grouped by
// platform. Search has its own list, in the modal.
func (m *Model) applyFilter() {
	keep := map[string]bool{m.selected: true}
	if m.open != nil {
		keep[chatKey(*m.open)] = true
	}
	m.visible = nil
	for _, c := range m.inbox.Chats {
		if !m.showAll && c.Unread == 0 && !keep[chatKey(c.Chat)] {
			continue
		}
		m.visible = append(m.visible, c)
	}
	slices.SortStableFunc(m.visible, func(a, b core.ChatSummary) int { return platformRank(a.Platform) - platformRank(b.Platform) })
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

// scrollInbox keeps the selected chat in view, with its section heading when it is the first.
func (m *Model) scrollInbox() {
	h := m.bodyH()
	items := m.sideItems()
	y, top, bottom := 0, -1, -1
	for i, it := range items {
		if it.kind == sideChat && it.chat == m.cursor {
			top, bottom = y, y+it.height()-1
			if i > 0 && items[i-1].kind == sideHeading {
				top -= items[i-1].height()
			}
		}
		y += it.height()
	}
	if top >= 0 {
		if bottom >= m.offset+h {
			m.offset = bottom - h + 1
		}
		if top < m.offset {
			m.offset = top
		}
	}
	m.offset = min(max(m.offset, 0), max(0, y-h))
}

// layout resizes the inner components after a window or mode change.
func (m *Model) layout() {
	w := m.chatW()
	m.chatView.Width = w
	m.chatView.Height = max(1, m.bodyH()-composerH)
	m.compose.Width = max(1, w-9)
	chip := 20 // room for the icon and the "WhatsApp only" chip
	m.search.Placeholder = "Who do you want to talk to?"
	if m.searchNarrow() {
		chip, m.search.Placeholder = 12, "Find a chat"
	}
	m.search.Width = max(4, m.searchW()-6-chip)
	m.renderChat()
	m.showSelected()
	m.scrollInbox()
}
