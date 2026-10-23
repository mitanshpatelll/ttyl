package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/dmz/internal/cellwidth"
	"github.com/mitanshpatelll/dmz/internal/core"
)

// Every width below goes through cellwidth, which knows how this terminal draws Gujarati,
// Hindi or emoji. Lines are assembled cell by cell so a wide name can't push a border.

var platformNames = map[core.PlatformID]string{core.WhatsApp: "WhatsApp", core.Telegram: "Telegram", core.Instagram: "Instagram"}

// Layout: one frame. Top border with the status, column headers, a rule, the inbox and chat
// columns, a rule, the key hints, the bottom border.
func (m Model) leftW() int  { return min(max(m.width*30/100, 30), 44) }
func (m Model) rightW() int { return max(20, m.width-m.leftW()-3) }
func (m Model) bodyH() int  { return max(3, m.height-6) }

func (m Model) filterShown() bool { return m.focus == focusFilter || m.filter.Value() != "" }

// rows is how many inbox entries fit: two lines each plus a spacer.
func (m Model) rows() int { return max(1, (m.bodyH()+1)/3) }

func border(s string) string { return lipgloss.NewStyle().Foreground(subtle).Render(s) }

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.inbox.Locked() {
		return m.viewLocked()
	}
	if m.help {
		return m.viewHelp()
	}
	w, lw, rw, bh := m.width, m.leftW(), m.rightW(), m.bodyH()
	left, right := m.inboxLines(lw, bh), m.chatLines(rw, bh)

	out := make([]string, 0, m.height)
	out = append(out, m.topBorder(w))
	out = append(out, border("│")+cw.Pad(m.inboxHeader(lw), lw)+border("│")+cw.Pad(m.chatHeader(rw), rw)+border("│"))
	out = append(out, border("├"+strings.Repeat("─", lw)+"┼"+strings.Repeat("─", rw)+"┤"))
	for i := range bh {
		out = append(out, border("│")+cw.Pad(left[i], lw)+border("│")+cw.Pad(right[i], rw)+border("│"))
	}
	out = append(out, border("├"+strings.Repeat("─", lw)+"┴"+strings.Repeat("─", rw)+"┤"))
	out = append(out, border("│")+cw.Pad(m.footer(w-2), w-2)+border("│"))
	out = append(out, border("╰"+strings.Repeat("─", w-2)+"╯"))
	return strings.Join(out, "\n")
}

// topBorder carries the app name, a dot per platform, and only unusual state on the right.
func (m Model) topBorder(w int) string {
	left := " " + accentStyle.Bold(true).Render("dmz")
	ids := make([]core.PlatformID, 0, len(m.inbox.Status))
	for id := range m.inbox.Status {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		left += "  " + statusText(platformTags[id], m.inbox.Status[id])
		if p := m.inbox.Sync[id]; p > 0 && p < 100 {
			left += dimStyle.Render(fmt.Sprintf(" syncing %d%%", p))
		}
	}
	left += " "

	var right []string
	if !m.inbox.Next.IsZero() {
		if m.inbox.Held > 0 {
			right = append(right, lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("%d held · next %s", m.inbox.Held, m.inbox.Next.Format("15:04"))))
		} else {
			right = append(right, dimStyle.Render("next drop "+m.inbox.Next.Format("15:04")))
		}
	}
	if m.inbox.Limit > 0 {
		remaining := m.inbox.Limit - m.inbox.Used
		style := dimStyle
		if remaining <= 5*time.Minute {
			style = lipgloss.NewStyle().Foreground(yellow)
		}
		right = append(right, style.Render(minutes(remaining)+" left"))
	}
	if m.inbox.Receipts {
		right = append(right, lipgloss.NewStyle().Foreground(yellow).Render("read receipts on"))
	}
	r := ""
	if len(right) > 0 {
		r = " " + strings.Join(right, border(" │ ")) + " "
	}
	fill := w - 4 - cw.String(left) - cw.String(r)
	if fill < 1 {
		r = ""
		left = cw.Truncate(left, max(0, w-5), "… ")
		fill = max(0, w-4-cw.String(left))
	}
	return border("╭─") + left + border(strings.Repeat("─", fill)) + r + border("─╮")
}

func statusText(name string, s core.StatusEvent) string {
	dot := func(c lipgloss.AdaptiveColor) string { return lipgloss.NewStyle().Foreground(c).Render("●") }
	switch s.State {
	case core.StateConnected:
		return dot(green) + " " + dimStyle.Render(name)
	case core.StateLoggedOut:
		return dot(red) + " " + lipgloss.NewStyle().Foreground(red).Render(name+" logged out")
	}
	detail := "connecting"
	if s.Detail != "" {
		detail = s.Detail
	}
	return dot(yellow) + " " + dimStyle.Render(name+" ") + lipgloss.NewStyle().Foreground(yellow).Render(detail)
}

func minutes(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func (m Model) footer(w int) string {
	var hints string
	switch m.focus {
	case focusFilter:
		hints = keyHints("type", "to search", "enter", "keep", "esc", "clear")
	case focusCompose:
		hints = keyHints("enter", "send", "esc", "stop typing")
	case focusChat:
		hints = keyHints("j/k", "select", "o", "open media", "i", "reply", "esc", "inbox", "?", "help")
	default:
		hints = keyHints("j/k", "move", "enter", "chat", "i", "reply", "/", "search", "?", "help", "q", "quit")
	}
	status := errorStyle.Render(m.flash)
	if m.flash == "" {
		status = accentStyle.Render(m.note)
	}
	return spread(" "+hints, status+" ", w)
}

var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Inbox", [][2]string{{"j / k", "move"}, {"enter, l", "open chat"}, {"i", "open and reply"}, {"/", "search by name"}, {"g / G", "top / bottom"}, {"tab", "go to open chat"}}},
	{"Chat", [][2]string{{"j / k", "select message"}, {"o", "open attachment"}, {"i, enter", "reply"}, {"d / u", "half page down / up"}, {"g / G", "oldest / newest"}, {"esc, h", "back to inbox"}}},
	{"Typing", [][2]string{{"enter", "send"}, {"esc", "stop typing"}}},
	{"Anywhere", [][2]string{{"?", "this help"}, {"q", "quit (outside text input)"}, {"ctrl+c", "quit"}}},
}

func (m Model) viewHelp() string {
	var body []string
	for _, sec := range helpSections {
		body = append(body, "", " "+accentStyle.Bold(true).Render(sec.title))
		for _, k := range sec.keys {
			body = append(body, "   "+keyStyle.Render(fmt.Sprintf("%-10s", k[0]))+" "+dimStyle.Render(k[1]))
		}
	}
	body = append(body, "", " "+dimStyle.Render("any key closes · run dmz stats for your week"))
	return m.centered(panel("Keys", "", body, 54, len(body)+3, true, -1))
}

func (m Model) viewLocked() string {
	body := []string{
		"",
		"  " + boldStyle.Render("Today's dmz time is used up"),
		"",
		"  " + fmt.Sprintf("You spent %s here today, budget %s.", minutes(m.inbox.Used), minutes(m.inbox.Limit)),
		"  " + "Messages keep arriving; they'll be here tomorrow.",
		"",
		"  " + keyHints("q", "quit", "e", "5 more minutes (counted in dmz stats)"),
	}
	if m.extend.Focused() {
		body = append(body, "", "  "+m.extend.View())
	}
	if m.flash != "" {
		body = append(body, "", "  "+errorStyle.Render(m.flash))
	}
	return m.centered(panel("Time's up", "", body, 60, len(body)+3, true, -1))
}

// panel draws a rounded box of w×h cells with the title set into the top border. A divider
// index >= 0 turns that body line into a ├──┤ rule.
func panel(title, right string, body []string, w, h int, active bool, divider int) string {
	b := lipgloss.NewStyle().Foreground(subtle)
	titleStyle := boldStyle
	if active {
		b = b.Foreground(accent)
		titleStyle = titleStyle.Foreground(accent)
	}
	inner := w - 2
	t := " " + titleStyle.Render(title) + " "
	r := ""
	if right != "" {
		r = " " + right + " "
	}
	fill := w - 3 - cw.String(t) - cw.String(r)
	if fill < 1 {
		r = ""
		t = cw.Truncate(t, max(0, w-4), "… ")
		fill = max(0, w-3-cw.String(t))
	}
	lines := []string{b.Render("╭─") + t + b.Render(strings.Repeat("─", fill)) + r + b.Render("╮")}
	for i := 0; i < h-2; i++ {
		if i == divider {
			lines = append(lines, b.Render("├"+strings.Repeat("─", inner)+"┤"))
			continue
		}
		line := ""
		if i < len(body) {
			line = body[i]
		}
		lines = append(lines, b.Render("│")+cw.Pad(line, inner)+b.Render("│"))
	}
	lines = append(lines, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

// centered places a block in the middle of the screen, exactly width×height.
func (m Model) centered(block string) string {
	lines := strings.Split(block, "\n")
	blockW := 0
	for _, l := range lines {
		blockW = max(blockW, cw.String(l))
	}
	top := max(0, (m.height-len(lines))/2)
	out := make([]string, 0, m.height)
	for range top {
		out = append(out, strings.Repeat(" ", m.width))
	}
	for _, l := range lines {
		out = append(out, cw.Pad(cw.Center(cw.Pad(l, blockW), m.width), m.width))
	}
	for len(out) < m.height {
		out = append(out, strings.Repeat(" ", m.width))
	}
	return strings.Join(out[:m.height], "\n")
}

// spread puts left and right on one line of width w, truncating left if needed.
func spread(left, right string, w int) string {
	gap := w - cw.String(left) - cw.String(right)
	if gap < 1 {
		left = cw.Truncate(left, max(0, w-cw.String(right)-1), "…")
		gap = max(1, w-cw.String(left)-cw.String(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

func shortTime(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yest"
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	case t.Year() == now.Year():
		return t.Format("02 Jan")
	}
	return t.Format("02/01/06")
}

func dayLabel(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return t.Format("Mon, 02 Jan 2006")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
