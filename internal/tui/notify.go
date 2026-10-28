package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// Notifications. A new message in a chat you aren't looking at slides in as a toast in the
// top right corner; while the terminal is in the background it becomes a desktop
// notification instead. Messages held for a delivery window notify when they are delivered,
// not before. Nothing is said during the startup loader or a catch-up, and desktop
// notifications come at most every desktopGap, summing up what arrived in between.

var notifySettings = struct {
	send    func(title, body string) error // desktop; nil means none
	preview bool                           // show what the message says, not just who
	toasts  bool
}{preview: true, toasts: true}

// SetNotify sets how new messages are announced: send shows a desktop notification (nil for
// none), preview puts the message text in it, toasts shows them inside ttyl.
func SetNotify(send func(title, body string) error, preview, toasts bool) {
	notifySettings.send, notifySettings.preview, notifySettings.toasts = send, preview, toasts
}

const (
	toastFor   = 4 * time.Second
	desktopGap = 5 * time.Second
)

type (
	toastDoneMsg    struct{ id int }
	desktopFlushMsg struct{}
	notifiedMsg     struct{ err error }
)

// toast is the message shown in the corner, with how many others came with it.
type toast struct {
	id   int
	chat core.ChatSummary
	more int
}

type notices struct {
	seen        map[string]time.Time // per chat: the newest message already announced (or there at start)
	unread      map[string]int
	toast       *toast
	toastID     int
	lastDesktop time.Time
	pending     map[string]news // waiting for the next desktop notification
	flushDue    bool
}

// news is what is new in one chat: its summary and how many messages.
type news struct {
	chat core.ChatSummary
	n    int
}

// notice compares a fresh inbox with what was known and announces what is new.
func (m *Model) notice(now time.Time) tea.Cmd {
	nt := &m.notes
	first := nt.seen == nil
	if first {
		nt.seen, nt.unread, nt.pending = map[string]time.Time{}, map[string]int{}, map[string]news{}
	}
	var fresh []news
	for _, c := range m.inbox.Chats {
		k := chatKey(c.Chat)
		seen, prevUnread := nt.seen[k], nt.unread[k]
		nt.unread[k] = c.Unread
		if !c.Last.Timestamp.After(seen) {
			continue
		}
		nt.seen[k] = c.Last.Timestamp
		if first || c.Last.FromMe || c.Last.Deleted {
			continue
		}
		fresh = append(fresh, news{c, max(1, c.Unread-prevUnread)})
	}
	if len(fresh) == 0 || !m.ready || m.syncing() {
		return nil
	}
	if !m.focused {
		for _, f := range fresh {
			p := nt.pending[chatKey(f.chat.Chat)]
			nt.pending[chatKey(f.chat.Chat)] = news{f.chat, p.n + f.n}
		}
		return m.flushDesktop(now)
	}
	// In front of you: a toast, unless it is the chat on screen.
	var shown []news
	for _, f := range fresh {
		if m.open == nil || chatKey(*m.open) != chatKey(f.chat.Chat) || !m.chatShown() {
			shown = append(shown, f)
		}
	}
	if len(shown) == 0 || !notifySettings.toasts {
		return nil
	}
	latest := shown[0]
	for _, f := range shown {
		if f.chat.Last.Timestamp.After(latest.chat.Last.Timestamp) {
			latest = f
		}
	}
	nt.toastID++
	id := nt.toastID
	nt.toast = &toast{id: id, chat: latest.chat, more: len(shown) - 1}
	return tea.Tick(toastFor, func(time.Time) tea.Msg { return toastDoneMsg{id} })
}

// flushDesktop sends what is pending as one desktop notification, or waits until desktopGap
// has passed since the last one.
func (m *Model) flushDesktop(now time.Time) tea.Cmd {
	nt := &m.notes
	if len(nt.pending) == 0 || notifySettings.send == nil {
		nt.pending = map[string]news{}
		return nil
	}
	if wait := nt.lastDesktop.Add(desktopGap).Sub(now); wait > 0 {
		if nt.flushDue {
			return nil
		}
		nt.flushDue = true
		return tea.Tick(wait, func(time.Time) tea.Msg { return desktopFlushMsg{} })
	}
	title, body := desktopText(nt.pending)
	nt.pending, nt.lastDesktop = map[string]news{}, now
	send := notifySettings.send
	return func() tea.Msg { return notifiedMsg{send(title, body)} }
}

// desktopText words a notification: one chat names it and quotes the message, several are
// counted and named.
func desktopText(pending map[string]news) (title, body string) {
	var all []news
	total := 0
	for _, p := range pending {
		all = append(all, p)
		total += p.n
	}
	if len(all) == 1 {
		p := all[0]
		title = p.chat.Title() + " · " + platformNames[p.chat.Platform]
		switch {
		case notifySettings.preview && p.n > 1:
			body = fmt.Sprintf("%d new messages · %s", p.n, previewText(p.chat))
		case notifySettings.preview:
			body = previewText(p.chat)
		case p.n > 1:
			body = fmt.Sprintf("%d new messages", p.n)
		default:
			body = "New message"
		}
		return title, body
	}
	// Newest first, so the names that matter most come first.
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[j].chat.Last.Timestamp.After(all[i].chat.Last.Timestamp) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	var names []string
	for _, p := range all {
		names = append(names, p.chat.Title())
	}
	return fmt.Sprintf("%d new messages in %d chats", total, len(all)), strings.Join(names, ", ")
}

// toastBox is the toast card: avatar, name and app, then the message and how many more.
func (m Model) toastBox() []string {
	t := m.notes.toast
	w := min(46, m.width-2)
	if t == nil || w < 24 {
		return nil
	}
	c := t.chat
	bg := on(selectedBg)
	plain := bg(lipgloss.NewStyle())
	edge := bg(accentStyle).Render("▌")
	right := bg(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(platformIcons[c.Platform].sp()+platformTags[c.Platform]) + plain.Render(" ")
	name := bg(boldStyle).Render(cw.Truncate(c.Title(), max(4, w-14-cw.String(right)), "…"))
	top := spread(edge+plain.Render(" ")+avatar(chatKey(c.Chat), c.Title())+plain.Render(" ")+name, right, w)
	more := ""
	if t.more > 0 {
		more = bg(dimStyle).Render(fmt.Sprintf("+%d more ", t.more))
	}
	text := bg(lipgloss.NewStyle().Foreground(secondary)).Render(cw.Truncate(previewText(c), max(4, w-8-cw.String(more)), "…"))
	bottom := spread(edge+plain.Render("      ")+text, more, w)
	return []string{fill(top, w, selectedBg), fill(bottom, w, selectedBg)}
}
