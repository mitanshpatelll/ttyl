package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	cw "github.com/mitanshpatelll/dmz/internal/cellwidth"
	"github.com/mitanshpatelll/dmz/internal/core"
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

// inboxHeader is "INBOX (n)" with the cursor position, or the search box while searching.
func (m Model) inboxHeader(w int) string {
	pos := ""
	if len(m.visible) > 0 {
		pos = dimStyle.Render(fmt.Sprintf("[%d/%d] ", m.cursor+1, len(m.visible)))
	}
	if m.filterShown() {
		return spread(accentStyle.Render(" / ")+m.filter.View(), pos, w)
	}
	style := boldStyle
	if m.focus == focusInbox {
		style = style.Foreground(accent)
	}
	return spread(" "+style.Render("INBOX")+dimStyle.Render(fmt.Sprintf(" (%d)", len(m.inbox.Chats))), pos, w)
}

// inboxLines is the inbox column: two lines per chat and a spacer, exactly h lines.
func (m Model) inboxLines(w, h int) []string {
	var lines []string
	if len(m.visible) == 0 {
		msg := []string{boldStyle.Render("Waiting for your chats"), "", dimStyle.Render("The first sync after linking"), dimStyle.Render("can take a few minutes.")}
		if m.filter.Value() != "" {
			msg = []string{dimStyle.Render("No chat matches"), dimStyle.Render("“" + m.filter.Value() + "”")}
		}
		for range (h - len(msg)) / 2 {
			lines = append(lines, "")
		}
		for _, l := range msg {
			lines = append(lines, cw.Center(l, w))
		}
	}
	multi := m.multiPlatform()
	for i := m.offset; i < len(m.visible) && len(lines)+2 <= h; i++ {
		lines = append(lines, m.inboxRow(m.visible[i], i == m.cursor, multi, w)...)
		lines = append(lines, "")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

// viewInbox is the inbox column as one string (used by tests).
func (m Model) viewInbox(w, h int) string { return strings.Join(m.inboxLines(w, h), "\n") }

// inboxRow: initials, name, platform and time; then the preview and the unread count.
// The selected row is highlighted across the whole column.
func (m Model) inboxRow(c core.ChatSummary, selected, multi bool, w int) []string {
	on := func(s lipgloss.Style) lipgloss.Style {
		if selected {
			return s.Background(selectedBg)
		}
		return s
	}
	plain := on(lipgloss.NewStyle())
	bar := plain.Render(" ")
	if selected {
		bar = on(accentStyle).Render("▌")
	}
	initials := on(lipgloss.NewStyle().Bold(true).Foreground(personColor(chatKey(c.Chat)))).Render(cw.Pad(initialsOf(c.Title()), 2))

	when := on(dimStyle).Render(shortTime(c.Last.Timestamp, time.Now()))
	if multi {
		when = on(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(platformTags[c.Platform]) + plain.Render("  ") + when
	}
	nameStyle := on(lipgloss.NewStyle().Foreground(text))
	if c.Unread > 0 {
		nameStyle = nameStyle.Bold(true)
	}
	if selected && (m.focus == focusInbox || m.focus == focusFilter) {
		nameStyle = nameStyle.Foreground(accent)
	}
	nameRoom := w - 5 - cw.String(when) - 2
	name := highlightMatch(cw.Truncate(c.Title(), max(1, nameRoom), "…"), m.filter.Value(), nameStyle)

	pill := ""
	if c.Unread > 0 {
		pill = badgeStyle.Render(fmt.Sprint(c.Unread))
	}
	previewStyle := on(dimStyle)
	if c.Unread > 0 {
		previewStyle = on(lipgloss.NewStyle().Foreground(text))
	}
	preview := previewStyle.Render(cw.Truncate(previewText(c), max(1, w-5-cw.String(pill)-2), "…"))

	line := func(left, right string) string {
		gap := max(1, w-cw.String(left)-cw.String(right)-1)
		return left + plain.Render(strings.Repeat(" ", gap)) + right + plain.Render(" ")
	}
	return []string{
		line(bar+plain.Render(" ")+initials+plain.Render(" ")+name, when),
		line(bar+plain.Render("    ")+preview, pill),
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
