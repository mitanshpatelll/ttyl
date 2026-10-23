package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/ttyl/internal/cellwidth"
	"github.com/mitanshpatelll/ttyl/internal/core"
)

var platformTags = map[core.PlatformID]string{
	core.WhatsApp:  "WA",
	core.Telegram:  "TG",
	core.Instagram: "IG",
}

var platformColors = map[core.PlatformID]lipgloss.AdaptiveColor{
	core.WhatsApp:  {Light: "#1A7F37", Dark: "#3FB950"},
	core.Telegram:  {Light: "#0969DA", Dark: "#58A6FF"},
	core.Instagram: {Light: "#BF3989", Dark: "#F778BA"},
}

func platformTag(p core.PlatformID) string {
	return lipgloss.NewStyle().Foreground(platformColors[p]).Render(platformTags[p])
}

// avatar is a colored block with initials, the way a chat app shows a profile picture.
func avatar(id, name string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(white).Background(personColor(id)).Render(" " + cw.Pad(initialsOf(name), 2) + " ")
}

// inboxHeader is the sidebar heading under the top bar, or the search box while searching.
func (m Model) inboxHeader(w int) string {
	bg := on(sidebarBg)
	pos := ""
	if len(m.visible) > 0 {
		pos = bg(dimStyle).Render(fmt.Sprintf("%d/%d ", m.cursor+1, len(m.visible)))
	}
	var left string
	if m.filterShown() {
		left = bg(accentStyle).Render(" / ") + m.filter.View()
	} else {
		style := bg(dimStyle.Bold(true))
		if m.focus == focusInbox {
			style = style.Foreground(accent)
		}
		left = bg(lipgloss.NewStyle()).Render(" ") + style.Render("MESSAGES")
	}
	gap := max(1, w-cw.String(left)-cw.String(pos))
	return fill(left+bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap))+pos, w, sidebarBg)
}

// inboxLines is the sidebar body: two lines per chat, exactly h lines, all on the sidebar shade.
func (m Model) inboxLines(w, h int) []string {
	var lines []string
	if len(m.visible) == 0 {
		bg := on(sidebarBg)
		msg := []string{bg(boldStyle).Render("Waiting for your chats"), "", bg(dimStyle).Render("The first sync after linking"), bg(dimStyle).Render("can take a few minutes.")}
		if m.filter.Value() != "" {
			msg = []string{bg(dimStyle).Render("No chat matches “" + m.filter.Value() + "”")}
		}
		for range (h - len(msg)) / 2 {
			lines = append(lines, "")
		}
		for _, l := range msg {
			pad := max(0, (w-cw.String(l))/2)
			lines = append(lines, bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", pad))+l)
		}
	}
	multi := m.multiPlatform()
	if len(m.visible) > 0 {
		lines = append(lines, "") // breathing room under the heading
	}
	for i := m.offset; i < len(m.visible) && len(lines)+2 <= h; i++ {
		lines = append(lines, m.inboxRow(m.visible[i], i == m.cursor, multi, w)...)
		lines = append(lines, "") // space between chats
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = fill(lines[i], w, sidebarBg)
	}
	return lines[:h]
}

// viewInbox is the sidebar as one string (used by tests).
func (m Model) viewInbox(w, h int) string { return strings.Join(m.inboxLines(w, h), "\n") }

// inboxRow: avatar, name and time; then the preview and the unread count. The selected row
// is a lighter shade with an accent edge.
func (m Model) inboxRow(c core.ChatSummary, selected, multi bool, w int) []string {
	shade := lipgloss.TerminalColor(sidebarBg)
	if selected {
		shade = selectedBg
	}
	bg := on(shade)
	plain := bg(lipgloss.NewStyle())
	edge := plain.Render(" ")
	if selected {
		edge = bg(accentStyle).Render("▌")
	}

	when := bg(dimStyle).Render(shortTime(c.Last.Timestamp, time.Now()))
	if multi {
		when = bg(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(platformTags[c.Platform]) + plain.Render(" ") + when
	}
	nameStyle := bg(lipgloss.NewStyle().Foreground(secondary))
	if c.Unread > 0 || selected {
		nameStyle = nameStyle.Foreground(text).Bold(true)
	}
	nameRoom := w - 7 - cw.String(when) - 3
	name := highlightMatch(cw.Truncate(c.Title(), max(1, nameRoom), "…"), m.filter.Value(), nameStyle)

	pill := ""
	if c.Unread > 0 {
		pill = badgeStyle.Render(fmt.Sprint(c.Unread))
	}
	previewStyle := bg(dimStyle)
	if c.Unread > 0 {
		previewStyle = bg(lipgloss.NewStyle().Foreground(secondary))
	}
	preview := previewStyle.Render(cw.Truncate(previewText(c), max(1, w-7-cw.String(pill)-3), "…"))

	// Name and preview start in the same column; time and unread count end in the same column.
	line := func(left, right string) string {
		gap := max(1, w-cw.String(left)-cw.String(right)-2)
		return left + plain.Render(strings.Repeat(" ", gap)) + right + plain.Render("  ")
	}
	return []string{
		line(edge+plain.Render(" ")+avatar(chatKey(c.Chat), c.Title())+plain.Render(" ")+name, when),
		line(edge+plain.Render("      ")+preview, pill),
	}
}

// highlightMatch underlines the first case-insensitive match of query in s.
func highlightMatch(s, query string, base lipgloss.Style) string {
	q := strings.ToLower(strings.TrimSpace(query))
	lower := strings.ToLower(s)
	i := strings.Index(lower, q)
	if q == "" || i < 0 || len(lower) != len(s) { // byte offsets only line up when lowering keeps lengths
		return base.Render(s)
	}
	return base.Render(s[:i]) + base.Underline(true).Foreground(accent).Render(s[i:i+len(q)]) + base.Render(s[i+len(q):])
}

func (m Model) multiPlatform() bool {
	for _, c := range m.inbox.Chats {
		if c.Platform != m.inbox.Chats[0].Platform {
			return true
		}
	}
	return false
}

func previewText(c core.ChatSummary) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(mediaTag(c.Last)+" "+c.Last.Body)), " ")
	switch {
	case c.Last.FromMe:
		return "You: " + text
	case c.IsGroup:
		return c.Last.Sender() + ": " + text
	}
	return text
}
