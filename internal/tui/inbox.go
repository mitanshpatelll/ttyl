package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitanshpatelll/dmz/internal/core"
)

// platformTags mark each inbox row once chats come from more than one platform.
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

// inboxSummary goes into the inbox border: unread count, or matches while searching.
func (m Model) inboxSummary() string {
	if m.filter.Value() != "" {
		return dimStyle.Render(fmt.Sprintf("%d of %d", len(m.visible), len(m.inbox.Chats)))
	}
	unread := 0
	for _, c := range m.inbox.Chats {
		if c.Unread > 0 {
			unread++
		}
	}
	if unread == 0 {
		return ""
	}
	return accentStyle.Render(fmt.Sprintf("%d unread", unread))
}

func (m Model) viewInbox(w, h int) string {
	var lines []string
	if m.filterShown() {
		lines = append(lines, accentStyle.Render(" / ")+m.filter.View())
	}
	if len(m.visible) == 0 {
		msg := []string{boldStyle.Render("Waiting for your chats"), "", dimStyle.Render("The first sync after linking"), dimStyle.Render("can take a few minutes.")}
		if m.filter.Value() != "" {
			msg = []string{dimStyle.Render("No chat matches"), dimStyle.Render("“" + m.filter.Value() + "”")}
		}
		block := lipgloss.Place(w, h-len(lines), lipgloss.Center, lipgloss.Center, strings.Join(msg, "\n"))
		return strings.Join(append(lines, block), "\n")
	}
	multi := m.multiPlatform()
	for i := m.offset; i < len(m.visible) && len(lines)+2 <= h; i++ {
		lines = append(lines, m.inboxRow(m.visible[i], i == m.cursor, multi, w)...)
	}
	return strings.Join(lines, "\n")
}

// inboxRow is two lines: avatar, name and time; then the preview and unread count.
// The selected row is highlighted across its full width.
func (m Model) inboxRow(c core.ChatSummary, selected, multi bool, w int) []string {
	on := func(s lipgloss.Style) lipgloss.Style {
		if selected {
			return s.Background(selectedBg)
		}
		return s
	}
	plain := on(lipgloss.NewStyle())
	bar := " "
	if selected {
		bar = on(accentStyle).Render("▌")
	}

	when := on(dimStyle).Render(shortTime(c.Last.Timestamp, time.Now()))
	if multi {
		when = on(lipgloss.NewStyle().Foreground(platformColors[c.Platform])).Render(platformTags[c.Platform]) + plain.Render(" ") + when
	}
	nameStyle := on(lipgloss.NewStyle().Foreground(text))
	if c.Unread > 0 {
		nameStyle = nameStyle.Bold(true)
	}
	if selected && (m.focus == focusInbox || m.focus == focusFilter) {
		nameStyle = nameStyle.Foreground(accent)
	}
	nameWidth := w - 1 - 3 - 1 - lipgloss.Width(when) - 2
	name := highlightMatch(ansi.Truncate(c.Title(), max(1, nameWidth), "…"), m.filter.Value(), nameStyle)

	unread := ""
	if c.Unread > 0 {
		unread = badgeStyle.Render(fmt.Sprint(c.Unread))
	}
	previewStyle := on(dimStyle)
	if c.Unread > 0 {
		previewStyle = on(lipgloss.NewStyle().Foreground(text))
	}
	preview := previewStyle.Render(ansi.Truncate(previewText(c), max(1, w-1-4-lipgloss.Width(unread)-2), "…"))

	line := func(left, right string) string {
		gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right)-1)
		return left + plain.Render(strings.Repeat(" ", gap)) + right + plain.Render(" ")
	}
	return []string{
		line(bar+avatar(chatKey(c.Chat), c.Title())+plain.Render(" ")+name, when),
		line(bar+plain.Render("    ")+preview, unread),
	}
}

// highlightMatch underlines the first case-insensitive match of query in s.
func highlightMatch(s, query string, base lipgloss.Style) string {
	q := strings.ToLower(strings.TrimSpace(query))
	i := strings.Index(strings.ToLower(s), q)
	if q == "" || i < 0 || len(strings.ToLower(s)) != len(s) { // byte offsets only line up when lowering keeps lengths
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
