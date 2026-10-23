package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitanshpatelll/dmz/internal/core"
)

var platformNames = map[core.PlatformID]string{core.WhatsApp: "WhatsApp", core.Telegram: "Telegram", core.Instagram: "Instagram"}

// Layout: one header line, two titled panels, one footer line.
func (m Model) leftWidth() int       { return min(max(m.width*32/100, 28), 46) }
func (m Model) leftInnerWidth() int  { return m.leftWidth() - 2 }
func (m Model) rightInnerWidth() int { return max(10, m.width-m.leftWidth()-2) }
func (m Model) innerHeight() int     { return max(4, m.height-4) }

func (m Model) filterShown() bool { return m.focus == focusFilter || m.filter.Value() != "" }

// rows is how many two-line inbox entries fit.
func (m Model) rows() int {
	h := m.innerHeight()
	if m.filterShown() {
		h--
	}
	return max(1, h/2)
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
	ih := m.innerHeight()
	inboxActive := m.focus == focusInbox || m.focus == focusFilter
	left := panel("Inbox", m.inboxSummary(), strings.Split(m.viewInbox(m.leftInnerWidth(), ih), "\n"), m.leftWidth(), ih+2, inboxActive, -1)
	right := m.viewChatPanel(ih)
	return lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), lipgloss.JoinHorizontal(lipgloss.Top, left, right), m.viewFooter())
}

// panel draws a rounded box of w×h cells with the title set into the top border. A divider
// index >= 0 turns that body line into a ├──┤ rule.
func panel(title, right string, body []string, w, h int, active bool, divider int) string {
	border := lipgloss.NewStyle().Foreground(subtle)
	titleStyle := boldStyle
	if active {
		border = border.Foreground(accent)
		titleStyle = titleStyle.Foreground(accent)
	}
	inner := w - 2
	t := " " + titleStyle.Render(title) + " "
	r := ""
	if right != "" {
		r = " " + right + " "
	}
	fill := w - 3 - lipgloss.Width(t) - lipgloss.Width(r)
	if fill < 1 {
		r = ""
		t = ansi.Truncate(t, max(0, w-4), "… ")
		fill = max(0, w-3-lipgloss.Width(t))
	}
	lines := []string{border.Render("╭─") + t + border.Render(strings.Repeat("─", fill)) + r + border.Render("╮")}
	for i := 0; i < h-2; i++ {
		if i == divider {
			lines = append(lines, border.Render("├"+strings.Repeat("─", inner)+"┤"))
			continue
		}
		line := ""
		if i < len(body) {
			line = body[i]
		}
		if lipgloss.Width(line) > inner {
			line = ansi.Truncate(line, inner, "")
		}
		lines = append(lines, border.Render("│")+line+strings.Repeat(" ", inner-lipgloss.Width(line))+border.Render("│"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

// viewHeader shows only what differs from normal: platform problems, held messages,
// the budget, receipts switched on.
func (m Model) viewHeader() string {
	left := logoStyle.Render("dmz")
	ids := make([]core.PlatformID, 0, len(m.inbox.Status))
	for id := range m.inbox.Status {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		left += "  " + statusText(platformNames[id], m.inbox.Status[id])
		if p := m.inbox.Sync[id]; p > 0 && p < 100 {
			left += dimStyle.Render(fmt.Sprintf(" syncing %d%%", p))
		}
	}

	var right []string
	if !m.inbox.Next.IsZero() {
		held := dimStyle.Render("next drop " + m.inbox.Next.Format("15:04"))
		if m.inbox.Held > 0 {
			held = lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("%d held", m.inbox.Held)) + dimStyle.Render(" · next "+m.inbox.Next.Format("15:04"))
		}
		right = append(right, held)
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
	return spread(" "+left, strings.Join(right, dimStyle.Render("  ·  "))+" ", m.width)
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

func (m Model) viewFooter() string {
	var hints string
	switch m.focus {
	case focusFilter:
		hints = keyHints("type", "to filter", "enter", "keep", "esc", "clear")
	case focusCompose:
		hints = keyHints("enter", "send", "esc", "stop typing")
	case focusChat:
		hints = keyHints("j/k", "select", "o", "open media", "i", "reply", "esc", "inbox", "?", "help")
	default:
		hints = keyHints("j/k", "move", "enter", "open", "i", "reply", "/", "search", "?", "help")
	}
	status := errorStyle.Render(m.flash)
	if m.flash == "" {
		status = accentStyle.Render(m.note)
	}
	return spread(" "+hints, status+" ", m.width)
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

// centered places a block in the middle of the screen, exactly width×height.
func (m Model) centered(block string) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

// spread puts left and right on one line of width w, truncating left if needed.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(0, w-lipgloss.Width(right)-1), "…")
		gap = max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

func shortTime(t, now time.Time) string {
	switch {
	case sameDay(t, now):
		return t.Format("15:04")
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
