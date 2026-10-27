package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

// Every width below goes through cellwidth, which knows how this terminal draws Gujarati,
// Hindi or emoji. Lines are assembled cell by cell so a wide name can't push anything.

var platformNames = map[core.PlatformID]string{core.WhatsApp: "WhatsApp", core.Telegram: "Telegram", core.Instagram: "Instagram"}

// Layout, Discord style: a shaded sidebar on the left, the conversation on the right, no
// boxes. Row 0 is the top bar, row 1 headings and a rule, then the body, then key hints.
func (m Model) leftW() int  { return min(max(m.width*34/100, 34), 48) }
func (m Model) rightW() int { return max(20, m.width-m.leftW()) }
func (m Model) bodyH() int  { return max(3, m.height-3) } // minus top bar, headings, key hints

// rows is how many inbox entries fit: a top gap, then two lines and a spacer per chat.
func (m Model) rows() int { return max(1, m.bodyH()/3) }

// on paints a background under a style, so a whole shaded area stays shaded between segments.
func on(bg lipgloss.TerminalColor) func(lipgloss.Style) lipgloss.Style {
	return func(s lipgloss.Style) lipgloss.Style { return s.Background(bg) }
}

// fill pads a line to w cells with spaces in the given background.
func fill(line string, w int, bg lipgloss.TerminalColor) string {
	if lw := cw.String(line); lw < w {
		return line + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", w-lw))
	}
	return cw.Truncate(line, w, "")
}

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
	lw, rw, bh := m.leftW(), m.rightW(), m.bodyH()
	chatW, dw := m.chatW(), m.detailsW()
	left, right := m.inboxLines(lw, bh), m.chatLines(chatW, bh)
	panel, panelHead := make([]string, bh), ""
	if dw > 0 {
		panel, panelHead = m.detailsLines(dw, bh), m.detailsHeading(dw)
	}

	out := make([]string, 0, m.height)
	out = append(out, m.sidebarTop(lw)+cw.Pad(m.chatHeader(rw), rw))
	out = append(out, m.inboxHeader(lw)+dimStyle.Render(strings.Repeat("─", chatW))+panelHead)
	for i := range bh {
		out = append(out, left[i]+cw.Pad(right[i], chatW)+panel[i])
	}
	if m.focus == focusSearch {
		box := m.searchBox()
		out = overlay(m.backdrop(out), box, (m.width-m.searchW())/2, max(0, (m.height-len(box))/2))
	}
	out = append(out, cw.Pad(m.footer(m.width), m.width))
	return strings.Join(out, "\n")
}

// sidebarTop is the app name and a dot per platform, on the sidebar shade.
func (m Model) sidebarTop(w int) string {
	bg := on(sidebarBg)
	line := bg(lipgloss.NewStyle()).Render(" ") + bg(accentStyle.Bold(true)).Render("ttyl")
	ids := make([]core.PlatformID, 0, len(m.inbox.Status))
	for id := range m.inbox.Status {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		line += bg(lipgloss.NewStyle()).Render("  ")
		if s, ok := m.inbox.Sync[id]; ok {
			line += m.syncTag(id, s, sidebarBg)
		} else {
			line += statusText(id, m.inbox.Status[id], sidebarBg)
		}
	}
	return fill(line, w, sidebarBg)
}

// statusText is a platform in the top bar: its icon (or a dot) colored by connection state.
func statusText(id core.PlatformID, s core.StatusEvent, bg lipgloss.TerminalColor) string {
	style := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c).Background(bg) }
	dot, name := "●", platformTags[id]
	if useIcons {
		dot = platformIcons[id].glyph
	}
	switch s.State {
	case core.StateConnected:
		return style(green).Render(dot) + style(faint).Render(" "+name)
	case core.StateLoggedOut:
		return style(red).Render(dot + " " + name + " logged out")
	}
	detail := "connecting"
	if s.Detail != "" {
		detail = s.Detail
	}
	return style(yellow).Render(dot) + style(faint).Render(" "+name+" ") + style(yellow).Render(detail)
}

// statusRight is only what differs from normal: held messages, the budget, receipts on.
func (m Model) statusRight() string {
	var parts []string
	if !m.inbox.Next.IsZero() {
		if m.inbox.Held > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("%d held · next %s", m.inbox.Held, m.inbox.Next.Format("15:04"))))
		} else {
			parts = append(parts, dimStyle.Render("next drop "+m.inbox.Next.Format("15:04")))
		}
	}
	if m.inbox.Limit > 0 {
		remaining := m.inbox.Limit - m.inbox.Used
		style := dimStyle
		if remaining <= 5*time.Minute {
			style = lipgloss.NewStyle().Foreground(yellow)
		}
		parts = append(parts, style.Render(minutes(remaining)+" left"))
	}
	if m.inbox.Receipts {
		parts = append(parts, lipgloss.NewStyle().Foreground(yellow).Render("read receipts on"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, dimStyle.Render("  ·  ")) + " "
}

func minutes(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func (m Model) footer(w int) string {
	var hints string
	switch {
	case m.leader:
		hints = keyHints("space", "then", "e", "details panel", "esc", "cancel")
	case m.focus == focusSearch:
		hints = keyHints("type", "a name", "↑/↓", "choose", "enter", "open", "esc", "close")
	case m.focus == focusCompose:
		hints = keyHints("enter", "send", "esc", "stop typing")
	case m.focus == focusChat:
		hints = keyHints("j/k", "select", "o", "open media", "i", "reply", "/", "search", "space e", "details", "esc", "back", "?", "help")
	default:
		hints = keyHints("j/k", "move", "enter", "open", "/", "search", "a", "all chats", "space e", "details", "?", "help", "q", "quit")
	}
	status := errorStyle.Render(m.flash)
	if m.flash == "" {
		status = accentStyle.Render(m.note)
	}
	return spread(" "+hints, status+" ", w)
}

type helpSection struct {
	title string
	keys  [][2]string
}

// helpColumns are shown side by side so the whole list fits a 24-line terminal.
var helpColumns = [2][]helpSection{
	{
		{"Chat list", [][2]string{{"j / k", "move"}, {"enter, l", "open chat"}, {"i", "open and reply"}, {"a", "all chats / unread"}, {"g / G", "top / bottom"}, {"tab", "go to open chat"}}},
		{"Typing", [][2]string{{"enter", "send"}, {"esc", "stop typing"}}},
	},
	{
		{"Chat", [][2]string{{"j / k", "select message"}, {"o", "open attachment"}, {"i, enter", "reply"}, {"d / u", "half page down / up"}, {"g / G", "oldest / newest"}, {"esc, h", "back to the list"}}},
		{"Anywhere", [][2]string{{"/", "search every chat"}, {"w: i: t:", "…in one app only"}, {"space e", "details on / off"}, {"?", "this help"}, {"q", "quit (not while typing)"}, {"ctrl+c", "quit"}}},
	},
}

func (m Model) viewHelp() string {
	const colW = 36
	var cols [2][]string
	for i, sections := range helpColumns {
		for _, sec := range sections {
			cols[i] = append(cols[i], "", " "+accentStyle.Bold(true).Render(sec.title))
			for _, k := range sec.keys {
				cols[i] = append(cols[i], "   "+keyStyle.Render(fmt.Sprintf("%-9s", k[0]))+" "+dimStyle.Render(k[1]))
			}
		}
	}
	var body []string
	for i := range max(len(cols[0]), len(cols[1])) {
		var l, r string
		if i < len(cols[0]) {
			l = cols[0][i]
		}
		if i < len(cols[1]) {
			r = cols[1][i]
		}
		body = append(body, cw.Pad(l, colW)+r)
	}
	body = append(body, "", " "+dimStyle.Render("any key closes · run ttyl stats for your week"))
	return m.centered(panel("Keys", "", body, 2*colW+2, len(body)+3, true, -1))
}

func (m Model) viewLocked() string {
	body := []string{
		"",
		"  " + boldStyle.Render("Today's ttyl time is used up"),
		"",
		"  " + fmt.Sprintf("You spent %s here today, budget %s.", minutes(m.inbox.Used), minutes(m.inbox.Limit)),
		"  " + "Messages keep arriving; they'll be here tomorrow.",
		"",
		"  " + keyHints("q", "quit", "e", "5 more minutes (counted in ttyl stats)"),
	}
	if m.extend.Focused() {
		body = append(body, "", "  "+m.extend.View())
	}
	if m.flash != "" {
		body = append(body, "", "  "+errorStyle.Render(m.flash))
	}
	return m.centered(panel("Time's up", "", body, 60, len(body)+3, true, -1))
}

// panel draws a rounded box of w×h cells with the title set into the top border (popups only).
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
	gap := w - 3 - cw.String(t) - cw.String(r)
	if gap < 1 {
		r = ""
		t = cw.Truncate(t, max(0, w-4), "… ")
		gap = max(0, w-3-cw.String(t))
	}
	lines := []string{b.Render("╭─") + t + b.Render(strings.Repeat("─", gap)) + r + b.Render("╮")}
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

// stamp is the time in a message header: "Today at 17:15".
func stamp(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return "Today at " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday at " + t.Format("15:04")
	}
	return t.Format("02/01/2006 15:04")
}

func dayLabel(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return t.Format("Monday, 02 January 2006")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
